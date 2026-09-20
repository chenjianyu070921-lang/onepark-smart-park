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

	return &types.TaskDetailResp{Task: toTaskDTO(&task)}, nil
}
