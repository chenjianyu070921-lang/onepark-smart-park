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
// assignee_id = 0: 自动指派 —— 按「技能 > 就近 > 负载均衡」打分选人(见 internal/assign)
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
		best, found, perr := l.pickAutoAssignee(&task)
		if perr != nil {
			return nil, perr
		}
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

// pickAutoAssignee 自动选定处理人: 按「技能 > 就近 > 负载」打分(见 internal/assign).
//
// 候选池优先用 dispatch_staff(带技能标签), 人员池为空时回退到历史指派记录(不带技能)--
// 保证技能池尚未录入时自动指派仍可用, 老行为不退化。
//
// 指派决策必须可解释: 打分依据会写进日志, 便于事后追溯"为什么派给了他"。
func (l *TaskAssignLogic) pickAutoAssignee(task *model.DispatchTask) (assign.Candidate, bool, error) {
	candidates, err := assign.LoadStaffCandidates(l.ctx, l.svcCtx.DB)
	if err != nil {
		l.Errorf("[dispatch] load staff candidates failed: %v", err)
		return assign.Candidate{}, false, errorx.NewError(errorx.ErrInternal, "加载候选处理人失败")
	}

	pool := "staff"
	if len(candidates) == 0 {
		pool = "history"
		candidates, err = assign.LoadCandidates(l.ctx, l.svcCtx.DB)
		if err != nil {
			l.Errorf("[dispatch] load history candidates failed: %v", err)
			return assign.Candidate{}, false, errorx.NewError(errorx.ErrInternal, "加载候选处理人失败")
		}
	}

	best, found := assign.Pick(task.ZoneCode, task.RequiredSkill, candidates)
	if !found {
		return assign.Candidate{}, false, nil
	}

	bd := assign.Explain(task.ZoneCode, task.RequiredSkill, best)
	l.Infof("[dispatch] 自动指派: taskId=%d, pool=%s, requiredSkill=%q -> assignee=%d(%s), "+
		"技能命中=%v, 距离=%d, 负载=%d, 得分=%d, 候选数=%d",
		task.Id, pool, task.RequiredSkill, best.AssigneeId, best.AssigneeName,
		bd.SkillMatched, bd.Distance, bd.Load, bd.Score, len(candidates))

	return best, true, nil
}

// lookupAssigneeName 反查处理人姓名: 先查人员池(权威来源), 再退回历史指派记录.
// 人员池尚未自持之前, 人名只能从历史工单里取; 现在人员池是首选。
// 查不到时返回空串, 不影响指派本身.
func (l *TaskAssignLogic) lookupAssigneeName(assigneeId int64) string {
	if assigneeId <= 0 {
		return ""
	}

	var name string
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchStaff{}).
		Select("COALESCE(name, '')").
		Where("staff_id = ?", assigneeId).
		Scan(&name).Error; err != nil {
		l.Errorf("[dispatch] lookup staff name failed: %v", err)
	}
	if name != "" {
		return name
	}

	// 无匹配行时 MAX 返回 NULL, 必须 COALESCE 成空串, 否则 Scan 到 string 会报错.
	err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchTask{}).
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
