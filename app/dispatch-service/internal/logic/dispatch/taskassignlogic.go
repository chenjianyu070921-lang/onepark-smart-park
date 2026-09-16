package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/assign"
	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/app/dispatch-service/internal/ecode"
	"onepark/common/errorx"
)

// TaskAssignLogic 指派调度人员.
//
// assignee_id > 0: 手动指派指定人员
// assignee_id = 0: 自动指派 —— 从历史指派记录中选"就近 + 负载低"的人
type TaskAssignLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewTaskAssignLogic 构造指派逻辑.
func NewTaskAssignLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TaskAssignLogic {
	return &TaskAssignLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// errVersionConflict 事务内乐观锁冲突哨兵, 避免冲突被误当数据库错误.
var errVersionConflict = errors.New("task version conflict")

// TaskAssign 指派处理人, 并用乐观锁防并发改派.
func (l *TaskAssignLogic) TaskAssign(req *types.TaskAssignReq) (*types.TaskAssignResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	tenantID := ctxdata.GetTenantId(l.ctx)

	var task model.DispatchTask
	err := l.svcCtx.DB.WithContext(l.ctx).
		Where("id = ? AND tenant_id = ?", req.Id, tenantID).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(ecode.ErrTaskNotFound, "调度工单不存在")
	}
	if err != nil {
		l.Errorf("[dispatch] load task failed: %v", err)
		return nil, errorx.NewError(ecode.ErrTaskQueryFailed, "加载调度工单失败")
	}

	to, ok := state.Next(task.Status, state.ActionAssign)
	if !ok {
		return nil, errorx.NewError(ecode.ErrTaskStatusInvalid, "当前状态不允许指派")
	}

	assigneeId := req.AssigneeId
	assigneeName := l.lookupAssigneeName(assigneeId)

	if assigneeId == 0 {
		candidates, cerr := assign.LoadCandidates(l.ctx, l.svcCtx.DB)
		if cerr != nil {
			l.Errorf("[dispatch] load candidates failed: %v", cerr)
			return nil, errorx.NewError(ecode.ErrAssigneeLoadFailed, "加载候选处理人失败")
		}
		best, found := assign.Pick(task.ZoneCode, candidates)
		if !found {
			return nil, errorx.NewError(ecode.ErrNoAssignee, "暂无可用处理人, 请指定 assignee_id")
		}
		assigneeId, assigneeName = best.AssigneeId, best.AssigneeName
	}
	if assigneeId <= 0 {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "处理人不能为空")
	}

	// 乐观锁更新与审计流水同事务, 防止"更新成功但流水丢失"的审计断档.
	expireAt := time.Now().Add(assignExpireWindow)
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.DispatchTask{}).
			Where("id = ? AND tenant_id = ? AND version = ?", task.Id, tenantID, task.Version).
			Updates(map[string]interface{}{
				"assignee_id":      assigneeId,
				"assignee_name":    assigneeName,
				"status":           to,
				"assign_expire_at": expireAt,
				"version":          gorm.Expr("version+1"),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errVersionConflict
		}
		return tx.Create(&model.DispatchTaskLog{
			TenantID:   tenantID,
			TaskId:     task.Id,
			FromStatus: task.Status,
			ToStatus:   to,
			Action:     state.ActionAssign,
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error
	})
	if errors.Is(err, errVersionConflict) {
		return nil, errorx.NewError(ecode.ErrTaskConflict, "工单已被他人修改, 请刷新后重试")
	}
	if err != nil {
		l.Errorf("[dispatch] assign task failed: %v", err)
		return nil, errorx.NewError(ecode.ErrAssignFailed, "指派失败")
	}

	return &types.TaskAssignResp{
		Id:           task.Id,
		Status:       int32(to),
		AssigneeId:   assigneeId,
		AssigneeName: assigneeName,
	}, nil
}

// lookupAssigneeName 从历史指派记录中反查处理人姓名.
// M5 不持有人员主数据(属 M6 user 服务), 因此只能从本服务历史记录中取一个可用姓名;
// 查不到时返回空串, 不影响指派本身.
func (l *TaskAssignLogic) lookupAssigneeName(assigneeId int64) string {
	if assigneeId <= 0 {
		return ""
	}
	var name string
	err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchTask{}).
		// 无匹配行时 MAX 返回 NULL, 必须 COALESCE 成空串, 否则 Scan 到 string 会报错.
		Select("COALESCE(MAX(assignee_name), '')").
		Where("assignee_id = ? AND assignee_name <> ''", assigneeId).
		Scan(&name).Error
	if err != nil {
		l.Errorf("[dispatch] lookup assignee name failed: %v", err)
		return ""
	}
	return name
}
