package dispatch

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/errorx"
)

// TaskListLogic 调度工单分页列表.
type TaskListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewTaskListLogic 构造调度工单列表逻辑.
func NewTaskListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TaskListLogic {
	return &TaskListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// TaskList 按状态/优先级分页查询.
func (l *TaskListLogic) TaskList(req *types.TaskListReq) (*types.TaskListResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > maxPageSize {
		req.PageSize = 10
	}

	// 闭包构造条件, 避免 Count 与 Find 共用被 GORM 改写的 Statement.
	scope := func() *gorm.DB {
		db := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchTask{})
		if req.Status != 0 {
			db = db.Where("status = ?", req.Status)
		}
		if req.Priority != 0 {
			db = db.Where("priority = ?", req.Priority)
		}
		return db
	}

	var total int64
	if err := scope().Count(&total).Error; err != nil {
		l.Errorf("[dispatch] count tasks failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询调度工单列表失败")
	}

	// 紧急优先: 先按 priority 升序(1 紧急), 再按创建时间倒序.
	records := make([]model.DispatchTask, 0, req.PageSize)
	if err := scope().
		Order("priority ASC, id DESC").
		Offset(int((req.Page - 1) * req.PageSize)).
		Limit(int(req.PageSize)).
		Find(&records).Error; err != nil {
		l.Errorf("[dispatch] page tasks failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询调度工单列表失败")
	}

	items := make([]types.DispatchTask, 0, len(records))
	for i := range records {
		items = append(items, toTaskDTO(&records[i]))
	}

	return &types.TaskListResp{Total: total, List: items}, nil
}
