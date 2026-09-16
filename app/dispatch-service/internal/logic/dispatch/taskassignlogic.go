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

// TaskAssign 指派处理人, 并用乐观锁防并发改派.
func (l *TaskAssignLogic) TaskAssign(req *types.TaskAssignReq) (*types.TaskAssignResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	var task model.DispatchTask
	err := l.svcCtx.DB.WithContext(l.ctx).First(&task, req.Id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(errorx.ErrNotFound, "调度工单不存在")
	}
	if err != nil {
		l.Errorf("[dispatch] load task failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "加载调度工单失败")
	}

	to, ok := state.Next(task.Status, state.ActionAssign)
	if !ok {
		return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许指派")
	}

	assigneeId := req.AssigneeId
	assigneeName := l.lookupAssigneeName(assigneeId)

	if assigneeId == 0 {
		candidates, cerr := assign.LoadCandidates(l.ctx, l.svcCtx.DB)
		if cerr != nil {
			l.Errorf("[dispatch] load candidates failed: %v", cerr)
			return nil, errorx.NewError(errorx.ErrInternal, "加载候选处理人失败")
		}
		best, found := assign.Pick(task.ZoneCode, candidates)
		if !found {
			return nil, errorx.NewError(errorx.ErrBadRequest, "暂无可用处理人, 请指定 assignee_id")
		}
		assigneeId, assigneeName = best.AssigneeId, best.AssigneeName
	}
	if assigneeId <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "处理人不能为空")
	}

	expireAt := time.Now().Add(assignExpireWindow)
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchTask{}).
		Where("id = ? AND version = ?", task.Id, task.Version).
		Updates(map[string]interface{}{
			"assignee_id":      assigneeId,
			"assignee_name":    assigneeName,
			"status":           to,
			"assign_expire_at": expireAt,
			"version":          gorm.Expr("version+1"),
		})
	if res.Error != nil {
		l.Errorf("[dispatch] assign task failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrInternal, "指派失败")
	}
	if res.RowsAffected == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "工单已被他人修改, 请刷新后重试")
	}

	l.writeLog(task.Id, task.Status, to, state.ActionAssign, ctxdata.GetUserId(l.ctx))

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

// writeLog 写状态流转审计; 失败不影响主流程, 但必须留痕.
func (l *TaskAssignLogic) writeLog(taskId int64, from, to int8, action string, operatorId int64) {
	if err := l.svcCtx.DB.WithContext(l.ctx).Create(&model.DispatchTaskLog{
		TaskId:     taskId,
		FromStatus: from,
		ToStatus:   to,
		Action:     action,
		OperatorId: operatorId,
	}).Error; err != nil {
		l.Errorf("[dispatch] write task log failed: taskId=%d, err=%v", taskId, err)
	}
}
