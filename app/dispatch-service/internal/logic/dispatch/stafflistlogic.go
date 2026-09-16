// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

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

type StaffListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewStaffListLogic 构造人员列表逻辑.
func NewStaffListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *StaffListLogic {
	return &StaffListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// StaffList 分页查询人员池; OnDuty = -1 表示不限.
//
// 排序刻意用「在岗优先 + staff_id 升序」: 前端先看到能派单的人, 且顺序稳定便于翻页。
func (l *StaffListLogic) StaffList(req *types.StaffListReq) (*types.StaffListResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	page, pageSize := clampTaskPage(req.Page, req.PageSize)

	// 同一组条件做 Count 与 Find, 避免两处条件写歪
	where := func(q *gorm.DB) *gorm.DB {
		if req.OnDuty == 0 || req.OnDuty == 1 {
			return q.Where("on_duty = ?", int8(req.OnDuty))
		}
		return q
	}

	var total int64
	if err := where(l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchStaff{})).Count(&total).Error; err != nil {
		l.Errorf("[dispatch] count staff failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询人员失败")
	}

	var staff []model.DispatchStaff
	if err := where(l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchStaff{})).
		Order("on_duty DESC, staff_id ASC").
		Limit(int(pageSize)).Offset(int((page - 1) * pageSize)).
		Find(&staff).Error; err != nil {
		l.Errorf("[dispatch] list staff failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询人员失败")
	}

	list := make([]types.StaffItem, 0, len(staff))
	for i := range staff {
		list = append(list, toStaffDTO(&staff[i]))
	}

	return &types.StaffListResp{Total: total, List: list}, nil
}
