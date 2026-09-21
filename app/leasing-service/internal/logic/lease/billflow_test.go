package lease

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/zeromicro/go-zero/core/conf"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/gormx"
)

// 本文件覆盖「招商账单闭环」与「园区可租面积维护」—— 此前这两块没有任何测试
// (logic 包一个 _test.go 都没有), 也缺少查询/缴费接口。

// openTestDB 复用服务自身 etc/leasing-api.yaml 的 DSN 连本地 MySQL; 不可用则跳过.
func openTestDB(t *testing.T) *gormx.DB {
	t.Helper()

	for _, p := range []string{"../../etc/leasing-api.yaml", "../../../etc/leasing-api.yaml"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		var c config.Config
		if err := conf.Load(p, &c); err != nil {
			continue
		}
		if c.MySQL.DataSource == "" {
			continue
		}
		db, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = sqlDB.PingContext(ctx)
		cancel()
		if err != nil {
			continue
		}
		return db
	}

	t.Skip("跳过: 未找到可用的 etc/leasing-api.yaml, 或本地 MySQL 不可用")
	return nil
}

// testPeriod 用一个**未来的独特账期**做隔离。
//
// 为什么不复用当前月份: BillSummary 是对整个账期做聚合, 而开发库里躺着其它联调
// 数据 —— 断言"金额恰好等于 X"会随环境波动。用只属于本用例的账期, 断言才能精确。
const testPeriod = "2099-01"

// TestBillFlow 账单闭环: 查询 -> 汇总 -> 缴费(幂等) -> 撤销.
func TestBillFlow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svcCtx := &svc.ServiceContext{DB: db}

	suffix := time.Now().UnixNano() % 1_000_000_000
	rent, _ := decimal.NewFromString("10000.50")

	contract := &model.LeaseContract{
		ContractNo:  fmt.Sprintf("LC-BILLFLOW-%d", suffix),
		TenantId:    9_500_000_000 + suffix%1000,
		TenantName:  "BillFlow Test Co",
		ZoneCode:    "Z-9F-901",
		AreaSqm:     100,
		MonthlyRent: rent,
		Deposit:     rent,
		StartDate:   time.Now().AddDate(-1, 0, 0),
		EndDate:     time.Now().AddDate(1, 0, 0),
		Status:      model.StatusActive,
	}
	if err := db.WithContext(ctx).Create(contract).Error; err != nil {
		t.Fatalf("准备合同失败: %v", err)
	}

	bill := &model.LeaseBill{
		BillNo:        fmt.Sprintf("BL-BILLFLOW-%d", suffix),
		ContractId:    contract.Id,
		TenantId:      contract.TenantId,
		BillingPeriod: testPeriod,
		Amount:        rent,
		Status:        model.BillStatusUnpaid,
	}
	if err := db.WithContext(ctx).Create(bill).Error; err != nil {
		t.Fatalf("准备账单失败: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		db.WithContext(bg).Where("contract_id = ?", contract.Id).Delete(&model.LeaseBill{})
		db.WithContext(bg).Delete(&model.LeaseContract{}, contract.Id)
	})

	// ---------- 1. 查询列表 ----------
	list, err := NewBillListLogic(ctx, svcCtx).
		BillList(&types.BillListReq{ContractId: contract.Id, Period: testPeriod, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("BillList 失败: %v", err)
	}
	if list.Total != 1 || len(list.List) != 1 {
		t.Fatalf("账单列表 total=%d len=%d, 期望各为 1", list.Total, len(list.List))
	}
	if got := list.List[0].Amount; got != "10000.50" {
		t.Errorf("账单金额 = %q, 期望 %q(定长 2 位, 末尾零不能丢)", got, "10000.50")
	}
	if list.List[0].Status != int32(model.BillStatusUnpaid) {
		t.Errorf("初始状态 = %d, 期望未缴", list.List[0].Status)
	}

	// 账期格式非法必须报错, 而不是静默返回空列表
	if _, err := NewBillListLogic(ctx, svcCtx).
		BillList(&types.BillListReq{Period: "2026-13"}); err == nil {
		t.Error("非法账期应被拒绝")
	}

	// ---------- 2. 汇总 ----------
	sum, err := NewBillSummaryLogic(ctx, svcCtx).
		BillSummary(&types.BillSummaryReq{Period: testPeriod})
	if err != nil {
		t.Fatalf("BillSummary 失败: %v", err)
	}
	// 该账期只有本用例这一张账单, 因此可以精确断言
	if sum.BillCount != 1 || sum.UnpaidCount != 1 || sum.PaidCount != 0 {
		t.Errorf("汇总笔数异常: total=%d unpaid=%d paid=%d", sum.BillCount, sum.UnpaidCount, sum.PaidCount)
	}
	if sum.UnpaidAmount != "10000.50" || sum.TotalAmount != "10000.50" {
		t.Errorf("汇总金额异常: unpaid=%q total=%q", sum.UnpaidAmount, sum.TotalAmount)
	}
	if sum.PaidAmount != "0.00" {
		t.Errorf("已收金额 = %q, 期望 0.00", sum.PaidAmount)
	}

	// ---------- 3. 缴费(含幂等) ----------
	paid, err := NewBillStatusLogic(ctx, svcCtx).
		BillStatus(&types.BillStatusReq{Id: bill.Id, Action: "pay"})
	if err != nil {
		t.Fatalf("缴费失败: %v", err)
	}
	if paid.Status != int32(model.BillStatusPaid) {
		t.Errorf("缴费后状态 = %d, 期望已缴", paid.Status)
	}

	// 重复缴费: 幂等, 不报错也不重复写
	again, err := NewBillStatusLogic(ctx, svcCtx).
		BillStatus(&types.BillStatusReq{Id: bill.Id, Action: "pay"})
	if err != nil {
		t.Errorf("重复缴费应幂等成功, 实际报错: %v", err)
	}
	if again != nil && again.Status != int32(model.BillStatusPaid) {
		t.Errorf("重复缴费后状态 = %d, 期望仍为已缴", again.Status)
	}

	// 缴费后汇总: 已收金额应等于账单金额
	sum2, err := NewBillSummaryLogic(ctx, svcCtx).BillSummary(&types.BillSummaryReq{Period: testPeriod})
	if err != nil {
		t.Fatalf("二次汇总失败: %v", err)
	}
	if sum2.PaidAmount != "10000.50" || sum2.UnpaidAmount != "0.00" {
		t.Errorf("缴费后汇总异常: paid=%q unpaid=%q", sum2.PaidAmount, sum2.UnpaidAmount)
	}

	// ---------- 4. 撤销缴费 ----------
	unpaid, err := NewBillStatusLogic(ctx, svcCtx).
		BillStatus(&types.BillStatusReq{Id: bill.Id, Action: "unpay"})
	if err != nil {
		t.Fatalf("撤销缴费失败: %v", err)
	}
	if unpaid.Status != int32(model.BillStatusUnpaid) {
		t.Errorf("撤销后状态 = %d, 期望未缴", unpaid.Status)
	}

	// 非法动作必须被拒
	if _, err := NewBillStatusLogic(ctx, svcCtx).
		BillStatus(&types.BillStatusReq{Id: bill.Id, Action: "refund"}); err == nil {
		t.Error("非法 action 应被拒绝")
	}
	// 不存在的账单
	if _, err := NewBillStatusLogic(ctx, svcCtx).
		BillStatus(&types.BillStatusReq{Id: 999_999_999, Action: "pay"}); err == nil {
		t.Error("不存在的账单应报错")
	}
}

// TestZoneUpsertAndList 园区可租面积的新增/更新与列表.
//
// 这张表此前只能手工插库 —— 而它是入驻率的分母, 属于招商基础数据。
func TestZoneUpsertAndList(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svcCtx := &svc.ServiceContext{DB: db}

	zoneCode := fmt.Sprintf("Z-UT-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() {
		db.WithContext(context.Background()).Where("zone_code = ?", zoneCode).Delete(&model.LeaseZone{})
	})

	// ---------- 新增 ----------
	created, err := NewZoneUpsertLogic(ctx, svcCtx).ZoneUpsert(&types.ZoneUpsertReq{
		ZoneCode: zoneCode, ZoneName: "单元测试区", TotalAreaSqm: 1000,
	})
	if err != nil {
		t.Fatalf("新增区域失败: %v", err)
	}
	if created.Id <= 0 {
		t.Errorf("应回填主键, 实际 id=%d", created.Id)
	}

	// ---------- 更新(而不是插出第二条) ----------
	updated, err := NewZoneUpsertLogic(ctx, svcCtx).ZoneUpsert(&types.ZoneUpsertReq{
		ZoneCode: zoneCode, ZoneName: "单元测试区(改)", TotalAreaSqm: 2500,
	})
	if err != nil {
		t.Fatalf("更新区域失败: %v", err)
	}
	if updated.Id != created.Id {
		t.Errorf("upsert 应更新同一行: id %d -> %d", created.Id, updated.Id)
	}

	var cnt int64
	if err := db.WithContext(ctx).Model(&model.LeaseZone{}).
		Where("zone_code = ?", zoneCode).Count(&cnt).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if cnt != 1 {
		t.Errorf("同编码区域条数 = %d, 期望 1(uk_zone_code upsert)", cnt)
	}

	// ---------- 列表能查到, 且面积是更新后的值 ----------
	list, err := NewZoneListLogic(ctx, svcCtx).ZoneList(&types.ZoneListReq{Page: 1, PageSize: 200})
	if err != nil {
		t.Fatalf("区域列表失败: %v", err)
	}
	var found *types.Zone
	for i := range list.List {
		if list.List[i].ZoneCode == zoneCode {
			found = &list.List[i]
			break
		}
	}
	if found == nil {
		t.Fatal("列表中找不到刚写入的区域")
	}
	if found.TotalAreaSqm != 2500 {
		t.Errorf("面积 = %v, 期望 2500(更新后的值)", found.TotalAreaSqm)
	}

	// ---------- 入参校验 ----------
	for name, req := range map[string]*types.ZoneUpsertReq{
		"区域编码为空":   {ZoneCode: "  ", TotalAreaSqm: 100},
		"面积为零":     {ZoneCode: zoneCode, TotalAreaSqm: 0},
		"面积为负":     {ZoneCode: zoneCode, TotalAreaSqm: -1},
		"面积超出合理范围": {ZoneCode: zoneCode, TotalAreaSqm: zoneAreaMax + 1},
	} {
		if _, err := NewZoneUpsertLogic(ctx, svcCtx).ZoneUpsert(req); err == nil {
			t.Errorf("%s 应被拒绝", name)
		}
	}
}
