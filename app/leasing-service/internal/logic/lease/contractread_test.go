package lease

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
)

// 本文件补齐合同**读路径**的单测: 详情 / 列表 / 入驻率 / DTO 映射。
//
// 此前这四条读路径 **0 用例覆盖** —— 而它们正是第 1 页的主干接口。
// 读路径最要命的两件事都在这: ①租户隔离(读不到别人园区的数据) ②金额定长(末尾零不能丢)。

// seedContract 造一份合同并注册清理; 租户取 ctx 里的(与读路径同口径).
func seedContract(t *testing.T, ctx context.Context, svcCtx *svc.ServiceContext,
	status int8, areaSqm float64) *model.LeaseContract {
	t.Helper()

	suffix := time.Now().UnixNano() % 1_000_000_000
	c := &model.LeaseContract{
		ContractNo:  fmt.Sprintf("LC-READ-%d-%d", suffix, status),
		TenantId:    ctxdata.GetTenantId(ctx),
		TenantName:  "读路径测试",
		ZoneCode:    "Z-READ-1F",
		AreaSqm:     areaSqm,
		MonthlyRent: decimal.RequireFromString("1000.50"),
		Deposit:     decimal.RequireFromString("2001.00"),
		StartDate:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		EndDate:     time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local),
		Status:      status,
	}
	if err := svcCtx.DB.Create(c).Error; err != nil {
		t.Fatalf("造合同失败: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		svcCtx.DB.WithContext(bg).Where("contract_id = ?", c.Id).Delete(&model.LeaseContractStatusLog{})
		svcCtx.DB.WithContext(bg).Delete(&model.LeaseContract{}, c.Id)
	})
	return c
}

// otherTenantCtx 另一个园区的身份, 用于验证"读不到别人的".
func otherTenantCtx(ctx context.Context) context.Context {
	return ctxdata.SetTenantId(context.Background(), ctxdata.GetTenantId(ctx)+1)
}

// TestToContractDTO_MoneyKeepsTrailingZeros 金额必须按列声明的精度**定长**输出.
//
// DECIMAL(12,2) 里存的是 10000.50, 若用 decimal.String() 读出来是 "10000.5" ——
// 数值没错, 但前端会显示成「月租 ¥10000.5」。金额的规范表示要与列精度一致。
func TestToContractDTO_MoneyKeepsTrailingZeros(t *testing.T) {
	dto := toContractDTO(&model.LeaseContract{
		Id: 7, ContractNo: "LC-DTO-1", TenantId: 1, TenantName: "DTO 测试",
		ZoneCode: "Z-1F", AreaSqm: 100.5,
		MonthlyRent:     decimal.RequireFromString("10000.50"),
		Deposit:         decimal.RequireFromString("0"),
		StartDate:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		EndDate:         time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local),
		Status:          model.StatusActive,
		AutoRenew:       model.AutoRenewOn,
		RenewNoticeDays: 30,
	})

	if dto.MonthlyRent != "10000.50" {
		t.Errorf("月租 = %q, 期望 %q(末尾零不能丢)", dto.MonthlyRent, "10000.50")
	}
	if dto.Deposit != "0.00" {
		t.Errorf("押金 = %q, 期望 %q(零也要补足两位)", dto.Deposit, "0.00")
	}
	if dto.StartDate != "2026-01-01" || dto.EndDate != "2026-12-31" {
		t.Errorf("日期格式 = %q ~ %q, 期望 yyyy-MM-dd", dto.StartDate, dto.EndDate)
	}
	if dto.Id != 7 || dto.ContractNo != "LC-DTO-1" || dto.ZoneCode != "Z-1F" {
		t.Errorf("基础字段映射错误: %+v", dto)
	}
	if dto.Status != int32(model.StatusActive) || dto.AutoRenew != int32(model.AutoRenewOn) {
		t.Errorf("状态/续约字段映射错误: status=%d autoRenew=%d", dto.Status, dto.AutoRenew)
	}
	if dto.RenewNoticeDays != 30 || dto.AreaSqm != 100.5 {
		t.Errorf("面积/提醒天数映射错误: area=%v notice=%d", dto.AreaSqm, dto.RenewNoticeDays)
	}
}

// TestContractDetail_TenantScoped 合同详情: 本租户可读, 跨租户读不到, 不存在则报错.
func TestContractDetail_TenantScoped(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	ctx := testCtx()

	c := seedContract(t, ctx, svcCtx, model.StatusActive, 123.45)

	// 1) 本租户: 查得到, 且金额定长
	got, err := NewContractDetailLogic(ctx, svcCtx).ContractDetail(&types.ContractDetailReq{Id: c.Id})
	if err != nil {
		t.Fatalf("本租户应查得到: %v", err)
	}
	if got.Contract.Id != c.Id {
		t.Errorf("id = %d, 期望 %d", got.Contract.Id, c.Id)
	}
	if got.Contract.AreaSqm != 123.45 {
		t.Errorf("面积 = %v, 期望 123.45", got.Contract.AreaSqm)
	}
	if got.Contract.MonthlyRent != "1000.50" {
		t.Errorf("月租 = %q, 期望 %q", got.Contract.MonthlyRent, "1000.50")
	}

	// 2) 跨租户: 必须查不到(而不是把别人的数据返回给你)
	if _, err := NewContractDetailLogic(otherTenantCtx(ctx), svcCtx).
		ContractDetail(&types.ContractDetailReq{Id: c.Id}); err == nil {
		t.Error("跨租户不应查到别人的合同")
	}

	// 3) 不存在
	if _, err := NewContractDetailLogic(ctx, svcCtx).
		ContractDetail(&types.ContractDetailReq{Id: 999_999_999}); err == nil {
		t.Error("不存在的合同应报错")
	}

	// 4) 未配库: 明确失败而不是返回空合同
	if _, err := NewContractDetailLogic(ctx, &svc.ServiceContext{}).
		ContractDetail(&types.ContractDetailReq{Id: c.Id}); err == nil {
		t.Error("DB 未初始化时应明确失败")
	}
}

// TestContractList_PaginationTenantAndStatus 列表: 分页兜底 + 状态过滤 + 租户隔离.
func TestContractList_PaginationTenantAndStatus(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	ctx := testCtx()
	tenantID := ctxdata.GetTenantId(ctx)

	active := seedContract(t, ctx, svcCtx, model.StatusActive, 100)
	seedContract(t, ctx, svcCtx, model.StatusTerminated, 200)

	// 1) 分页参数兜底: Page=0 / PageSize=0 不应报错(内部兜底成 1 / 10)
	list, err := NewContractListLogic(ctx, svcCtx).
		ContractList(&types.ContractListReq{Page: 0, PageSize: 0})
	if err != nil {
		t.Fatalf("ContractList 失败: %v", err)
	}
	if list.Total < 2 {
		t.Errorf("本租户合同数 = %d, 期望 >= 2", list.Total)
	}
	// 列表里**不能混入其它租户**的数据
	for _, c := range list.List {
		if c.TenantId != tenantID {
			t.Errorf("列表混入其它租户的合同: tenant=%d, 期望 %d", c.TenantId, tenantID)
		}
	}

	// 2) 状态过滤: 结果里只能有该状态, 且必须包含刚建的生效中合同
	onlyActive, err := NewContractListLogic(ctx, svcCtx).ContractList(
		&types.ContractListReq{Status: int32(model.StatusActive), Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("按状态查询失败: %v", err)
	}
	found := false
	for _, c := range onlyActive.List {
		if c.Status != int32(model.StatusActive) {
			t.Errorf("按状态过滤后混入其它状态: %d", c.Status)
		}
		if c.Id == active.Id {
			found = true
		}
	}
	if !found {
		t.Error("按生效中过滤后应包含刚建的生效中合同")
	}

	// 3) 跨租户: 看不到本租户的合同
	other, err := NewContractListLogic(otherTenantCtx(ctx), svcCtx).
		ContractList(&types.ContractListReq{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("跨租户查询失败: %v", err)
	}
	for _, c := range other.List {
		if c.Id == active.Id {
			t.Error("跨租户不应看到本租户的合同")
		}
	}
}

// TestOccupancy_FormulaAndTenantIsolation 入驻率: 分子按租户统计, 分母是全局可租面积.
//
// 两条容易错的边界:
//   - 分子只算「生效中」合同(已到期/已终止不计);
//   - 可租总面积为 0 时返回 0 **而不是 NaN**(前端会被 NaN 直接搞崩)。
func TestOccupancy_FormulaAndTenantIsolation(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := testCtx()

	// 分母来自 lease_zone(全局主数据, 不按租户隔离)。用独立编码, 不碰真实园区数据。
	zoneCode := fmt.Sprintf("Z-OCC-%d", time.Now().UnixNano()%1_000_000)
	if _, err := NewZoneUpsertLogic(ctx, svcCtx).ZoneUpsert(&types.ZoneUpsertReq{
		ZoneCode: zoneCode, ZoneName: "入驻率测试区", TotalAreaSqm: 1000,
	}); err != nil {
		t.Fatalf("准备园区失败: %v", err)
	}
	t.Cleanup(func() {
		db.WithContext(context.Background()).Where("zone_code = ?", zoneCode).Delete(&model.LeaseZone{})
	})

	const myArea = 500.0
	seedContract(t, ctx, svcCtx, model.StatusActive, myArea) // 计入分子
	seedContract(t, ctx, svcCtx, model.StatusExpired, 777)   // 不计入分子

	occ, err := NewOccupancyLogic(ctx, svcCtx).Occupancy(&types.OccupancyReq{})
	if err != nil {
		t.Fatalf("Occupancy 失败: %v", err)
	}
	if occ.TotalAreaSqm < 1000 {
		t.Errorf("可租总面积 = %v, 期望 >= 1000(含刚建的 1000)", occ.TotalAreaSqm)
	}
	if occ.LeasedAreaSqm < myArea {
		t.Errorf("已租面积 = %v, 期望 >= %v", occ.LeasedAreaSqm, myArea)
	}
	// 口径恒等式: 入驻率 == 已租 / 可租
	want := occ.LeasedAreaSqm / occ.TotalAreaSqm
	if diff := occ.OccupancyRate - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("入驻率 = %v, 期望 %v(已租/可租)", occ.OccupancyRate, want)
	}
	if occ.OccupancyRate < 0 || occ.OccupancyRate > 1 {
		t.Errorf("入驻率 = %v, 应在 0~1 之间(不是百分数, 也不是 NaN)", occ.OccupancyRate)
	}

	// 换一个租户: 分子必须是 0(看不到别人的已租面积); 分母是全局主数据, 不受影响
	otherOcc, err := NewOccupancyLogic(otherTenantCtx(ctx), svcCtx).Occupancy(&types.OccupancyReq{})
	if err != nil {
		t.Fatalf("跨租户 Occupancy 失败: %v", err)
	}
	if otherOcc.LeasedAreaSqm != 0 {
		t.Errorf("跨租户已租面积 = %v, 期望 0(分子必须按租户隔离)", otherOcc.LeasedAreaSqm)
	}
	if otherOcc.OccupancyRate != 0 {
		t.Errorf("跨租户入驻率 = %v, 期望 0", otherOcc.OccupancyRate)
	}
	if otherOcc.TotalAreaSqm < 1000 {
		t.Errorf("跨租户可租总面积 = %v, 期望 >= 1000(分母是全局主数据)", otherOcc.TotalAreaSqm)
	}
}
