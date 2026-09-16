package lease

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// OccupancyLogic 入驻率统计.
type OccupancyLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewOccupancyLogic 构造入驻率逻辑.
func NewOccupancyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *OccupancyLogic {
	return &OccupancyLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Occupancy 计算 已租面积 / 可租总面积.
func (l *OccupancyLogic) Occupancy(req *types.OccupancyReq) (*types.OccupancyResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	// 可租总面积来自 lease_zone 配置表.
	var totalArea float64
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseZone{}).
		Select("COALESCE(SUM(total_area_sqm), 0)").
		Scan(&totalArea).Error; err != nil {
		l.Errorf("[lease] sum zone area failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "统计入驻率失败")
	}

	// 已租面积只统计「生效中」的合同.
	scope := func() *gorm.DB {
		db := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseContract{}).
			Where("status = ?", model.StatusActive)
		if req.TenantId != 0 {
			db = db.Where("tenant_id = ?", req.TenantId)
		}
		return db
	}

	var leasedArea float64
	if err := scope().Select("COALESCE(SUM(area_sqm), 0)").Scan(&leasedArea).Error; err != nil {
		l.Errorf("[lease] sum leased area failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "统计入驻率失败")
	}

	// 总面积为 0 时返回 0 而非 NaN, 避免前端显示异常.
	rate := 0.0
	if totalArea > 0 {
		rate = leasedArea / totalArea
	}

	return &types.OccupancyResp{
		TotalAreaSqm:  totalArea,
		LeasedAreaSqm: leasedArea,
		OccupancyRate: rate,
	}, nil
}
