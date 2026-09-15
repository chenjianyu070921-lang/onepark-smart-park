package dispatch

import (
	"context"
	"errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
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
	err := l.svcCtx.DB.WithContext(l.ctx).First(&task, req.Id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(errorx.ErrNotFound, "调度工单不存在")
	}
	if err != nil {
		l.Errorf("[dispatch] query task failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询调度工单失败")
	}

	return &types.TaskDetailResp{Task: toTaskDTO(&task)}, nil
}
