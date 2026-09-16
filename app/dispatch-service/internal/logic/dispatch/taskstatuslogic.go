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
	"onepark/app/dispatch-service/internal/ecode"
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

	action := req.Action
	to, ok := state.Next(task.Status, action)
	if !ok {
		return nil, errorx.NewError(ecode.ErrTaskStatusInvalid, "当前状态不允许该操作")
	}

	updates := map[string]interface{}{
		"status":  to,
		"version": gorm.Expr("version+1"),
	}
	// 终态写入完成时间.
	if to == model.StatusCompleted || to == model.StatusClosed {
		updates["finished_at"] = time.Now()
	}

	// 乐观锁更新与审计流水同事务, 防止审计断档.
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.DispatchTask{}).
			Where("id = ? AND tenant_id = ? AND version = ?", task.Id, tenantID, task.Version).
			Updates(updates)
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
			Action:     action,
			Remark:     req.Remark,
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error
	})
	if errors.Is(err, errVersionConflict) {
		return nil, errorx.NewError(ecode.ErrTaskConflict, "工单已被他人修改, 请刷新后重试")
	}
	if err != nil {
		l.Errorf("[dispatch] update task status failed: %v", err)
		return nil, errorx.NewError(ecode.ErrTaskUpdateFailed, "更新工单状态失败")
	}

	return &types.TaskStatusResp{Id: task.Id, Status: int32(to)}, nil
}
