package lease

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// ZoneListLogic 园区可租区域列表.
type ZoneListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewZoneListLogic 构造区域列表逻辑.
func NewZoneListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ZoneListLogic {
	return &ZoneListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ZoneList 分页查询可租区域.
func (l *ZoneListLogic) ZoneList(req *types.ZoneListReq) (*types.ZoneListResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > maxPageSize {
		req.PageSize = 10
	}

	var total int64
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseZone{}).
		Count(&total).Error; err != nil {
		l.Errorf("[lease] count zones failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询可租区域失败")
	}

	records := make([]model.LeaseZone, 0, req.PageSize)
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseZone{}).
		// 按区域编码升序: 编码本身带楼栋/楼层语义(A-3F-301), 升序即空间顺序
		Order("zone_code ASC").
		Offset(int((req.Page - 1) * req.PageSize)).
		Limit(int(req.PageSize)).
		Find(&records).Error; err != nil {
		l.Errorf("[lease] page zones failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询可租区域失败")
	}

	items := make([]types.Zone, 0, len(records))
	for i := range records {
		items = append(items, toZoneDTO(&records[i]))
	}

	return &types.ZoneListResp{Total: total, List: items}, nil
}
