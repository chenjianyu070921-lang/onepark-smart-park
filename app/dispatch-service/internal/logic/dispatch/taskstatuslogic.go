package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// TaskStatusLogic 状态回写: start 开始处理 / finish 完成 / close 关闭.
type TaskStatusLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewTaskStatusLogic 构造状态回写逻辑.
func NewTaskStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TaskStatusLogic {
	return &TaskStatusLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// TaskStatus 执行状态流转, 非法转移由状态机拒绝.
func (l *TaskStatusLogic) TaskStatus(req *types.TaskStatusReq) (*types.TaskStatusResp, error) {
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

	action := req.Action
	to, ok := state.Next(task.Status, action)
	if !ok {
		return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许该操作")
	}

	updates := map[string]interface{}{
		"status":  to,
		"version": gorm.Expr("version+1"),
	}
	// 终态写入完成时间.
	if to == model.StatusCompleted || to == model.StatusClosed {
		updates["finished_at"] = time.Now()
	}

	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchTask{}).
		Where("id = ? AND version = ?", task.Id, task.Version).
		Updates(updates)
	if res.Error != nil {
		l.Errorf("[dispatch] update task status failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrInternal, "更新工单状态失败")
	}
	if res.RowsAffected == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "工单已被他人修改, 请刷新后重试")
	}

	if err := l.svcCtx.DB.WithContext(l.ctx).Create(&model.DispatchTaskLog{
		TaskId:     task.Id,
		FromStatus: task.Status,
		ToStatus:   to,
		Action:     action,
		Remark:     req.Remark,
		OperatorId: ctxdata.GetUserId(l.ctx),
	}).Error; err != nil {
		l.Errorf("[dispatch] write task log failed: taskId=%d, err=%v", task.Id, err)
	}

	return &types.TaskStatusResp{Id: task.Id, Status: int32(to)}, nil
}
