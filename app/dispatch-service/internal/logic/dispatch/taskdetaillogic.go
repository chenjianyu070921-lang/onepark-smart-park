package dispatch

import (
	"context"
	"errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/app/dispatch-service/internal/ecode"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// TaskDetailLogic 查询调度工单详情.
type TaskDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewTaskDetailLogic 构造调度工单详情逻辑.
func NewTaskDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TaskDetailLogic {
	return &TaskDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// TaskDetail 按主键查询调度工单.
func (l *TaskDetailLogic) TaskDetail(req *types.TaskDetailReq) (*types.TaskDetailResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	var task model.DispatchTask
	// 按 id+租户加载, 防止越权读取其它园区工单(RBAC 行级隔离).
	err := l.svcCtx.DB.WithContext(l.ctx).
		Where("id = ? AND tenant_id = ?", req.Id, ctxdata.GetTenantId(l.ctx)).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(ecode.ErrTaskNotFound, "调度工单不存在")
	}
	if err != nil {
		l.Errorf("[dispatch] query task failed: %v", err)
		return nil, errorx.NewError(ecode.ErrTaskQueryFailed, "查询调度工单失败")
	}

	// 状态流转时间线: **按 task_id 取, 不再叠租户条件**。
	//
	// 为什么不再叠租户: 上面的任务查询已经过租户校验(id + tenant_id), 日志必然属于该任务,
	// 不存在越权读取; 而 dispatch_task_log.tenant_id 此前只有「状态回写」一处写入
	// (2026-09-27 已把建单/派单/重派/告警自动建单四处补齐), 历史行的 tenant_id 是 0 ——
	// 叠租户条件会把建单、派单、重派这些格子**整段抹掉**, 时间线看起来像"缺了一半"。
	var logs []model.DispatchTaskLog
	if err := l.svcCtx.DB.WithContext(l.ctx).
		Where("task_id = ?", task.Id).
		Order("id ASC"). // 按发生顺序; 时间线允许有缺口, 不允许乱序
		Find(&logs).Error; err != nil {
		l.Errorf("[dispatch] query task logs failed: %v", err)
		return nil, errorx.NewError(ecode.ErrTaskQueryFailed, "查询工单流转记录失败")
	}

	items := make([]types.TaskLog, 0, len(logs))
	for i := range logs {
		items = append(items, toTaskLogDTO(&logs[i]))
	}

	return &types.TaskDetailResp{Task: toTaskDTO(&task), Logs: items}, nil
}
