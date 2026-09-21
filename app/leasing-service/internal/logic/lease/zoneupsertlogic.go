package lease

import (
	"context"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm/clause"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// zoneAreaMax 单个区域可租面积上限(平方米).
//
// 设上限是为了拦住明显的录入错误: 把总面积多写几个零, 入驻率会立刻变成接近 0,
// 而且这种错误在页面上"看起来是对的"(数字很大但没报错)。
const zoneAreaMax = 10_000_000

// ZoneUpsertLogic 园区可租区域的新增/更新.
//
// 为什么需要它: 入驻率 = 生效中合同面积 / 可租总面积, 而分母来自 lease_zone 表 ——
// 但此前这张表**只能手工插库**, "园区有多少可租面积"这个招商基础数据进不了系统。
type ZoneUpsertLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewZoneUpsertLogic 构造区域维护逻辑.
func NewZoneUpsertLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ZoneUpsertLogic {
	return &ZoneUpsertLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ZoneUpsert 按 region/楼层编码新增或更新可租面积.
//
// upsert 而非"先查后插": 后者在并发下会两个请求都读到"不存在"然后一起插,
// 第二个撞唯一键报错。这里交给数据库的 uk_zone_code 与 ON DUPLICATE KEY UPDATE
// 一条语句完成, 天然并发安全。
func (l *ZoneUpsertLogic) ZoneUpsert(req *types.ZoneUpsertReq) (*types.ZoneUpsertResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	code := strings.TrimSpace(req.ZoneCode)
	if code == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "区域编码不能为空")
	}
	if req.TotalAreaSqm <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "可租总面积必须大于 0")
	}
	if req.TotalAreaSqm > zoneAreaMax {
		return nil, errorx.NewError(errorx.ErrBadRequest, "可租总面积超出合理范围")
	}

	zone := model.LeaseZone{
		ZoneCode:     code,
		ZoneName:     strings.TrimSpace(req.ZoneName),
		TotalAreaSqm: req.TotalAreaSqm,
	}
	if err := l.svcCtx.DB.WithContext(l.ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "zone_code"}},
			DoUpdates: clause.AssignmentColumns([]string{"zone_name", "total_area_sqm"}),
		}).
		Create(&zone).Error; err != nil {
		l.Errorf("[lease] upsert zone failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "保存可租区域失败")
	}

	// 走的是 UPDATE 分支时 GORM 不会回填主键, 统一再查一次拿准 Id
	if err := l.svcCtx.DB.WithContext(l.ctx).
		Where("zone_code = ?", code).First(&zone).Error; err != nil {
		l.Errorf("[lease] reload zone failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "读取可租区域失败")
	}

	l.Infof("[lease] zone upserted: zoneCode=%s, totalAreaSqm=%v", zone.ZoneCode, zone.TotalAreaSqm)
	return &types.ZoneUpsertResp{Id: zone.Id, ZoneCode: zone.ZoneCode}, nil
}
