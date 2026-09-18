package cron

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
)

// TestRunBillOnce_GenerateAndIdempotent 验证月度出账(组长计划书 周三 P1「每月自动生成租金账单」):
//   - 「生效中」合同能生成上一自然月账单, 金额取合同月租金
//   - 「已到期」合同不出账(只对生效中出账)
//   - 重复执行不产生第二张 —— 幂等由 uk_contract_period 唯一索引兜底
//
// 断言只针对本用例造的合同, 不断言全局条数:
// 开发库里还躺着联调数据, 断言全局计数属于脆测试(超时重派那边就踩过一次)。
func TestRunBillOnce_GenerateAndIdempotent(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()
	svcCtx := &svc.ServiceContext{DB: db, Redis: rdb}

	suffix := time.Now().UnixNano() % 1_000_000_000
	period := time.Now().AddDate(0, -1, 0).Format("2006-01")

	mk := func(tag string, status int8, rent string) *model.LeaseContract {
		d, err := decimal.NewFromString(rent)
		if err != nil {
			t.Fatalf("非法金额 %q: %v", rent, err)
		}
		return &model.LeaseContract{
			ContractNo:  fmt.Sprintf("LC-BILL-%s-%d", tag, suffix),
			TenantId:    9_100_000_000 + suffix%1000,
			TenantName:  "Bill Cron Test Co",
			ZoneCode:    "Z-2F-201",
			AreaSqm:     100,
			MonthlyRent: d,
			Deposit:     d,
			StartDate:   time.Now().AddDate(-1, 0, 0),
			EndDate:     time.Now().AddDate(1, 0, 0),
			Status:      status,
		}
	}

	active := mk("ACT", model.StatusActive, "12345.67")
	expired := mk("EXP", model.StatusExpired, "999.99")
	for _, c := range []*model.LeaseContract{active, expired} {
		if err := db.WithContext(ctx).Create(c).Error; err != nil {
			t.Fatalf("准备合同失败: %v", err)
		}
	}

	ids := []int64{active.Id, expired.Id}
	t.Cleanup(func() {
		bg := context.Background()
		db.WithContext(bg).Where("contract_id IN ?", ids).Delete(&model.LeaseBill{})
		db.WithContext(bg).Where("id IN ?", ids).Delete(&model.LeaseContract{})
		// 释放账期锁, 否则同一台机器紧接着重跑会被锁挡住(表现为"正在生成中")
		_ = rdb.Del(bg, fmt.Sprintf("m5:lease:bill:lock:%s", period)).Err()
	})

	// ---------- 第一次: 生成 ----------
	if _, err := RunBillOnce(ctx, svcCtx); err != nil {
		t.Fatalf("RunBillOnce 失败: %v", err)
	}

	var bill model.LeaseBill
	if err := db.WithContext(ctx).
		Where("contract_id = ? AND billing_period = ?", active.Id, period).
		First(&bill).Error; err != nil {
		t.Fatalf("生效中合同应生成账单, 实际查询失败: %v", err)
	}
	if !bill.Amount.Equal(active.MonthlyRent) {
		t.Errorf("账单金额 = %s, 期望 %s(应取合同月租金原值)",
			bill.Amount.String(), active.MonthlyRent.String())
	}
	if bill.Status != model.BillStatusUnpaid {
		t.Errorf("新账单状态 = %d, 期望 %d(未缴)", bill.Status, model.BillStatusUnpaid)
	}
	if bill.TenantId != active.TenantId {
		t.Errorf("账单租户 = %d, 期望 %d", bill.TenantId, active.TenantId)
	}

	// 「已到期」合同不应出账
	var cnt int64
	if err := db.WithContext(ctx).Model(&model.LeaseBill{}).
		Where("contract_id = ? AND billing_period = ?", expired.Id, period).
		Count(&cnt).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if cnt != 0 {
		t.Errorf("「已到期」合同不应出账, 实际生成 %d 张", cnt)
	}

	// ---------- 第二次: 幂等 ----------
	if _, err := RunBillOnce(ctx, svcCtx); err != nil {
		t.Fatalf("第二次 RunBillOnce 失败: %v", err)
	}

	if err := db.WithContext(ctx).Model(&model.LeaseBill{}).
		Where("contract_id = ? AND billing_period = ?", active.Id, period).
		Count(&cnt).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if cnt != 1 {
		t.Errorf("重复执行后账单数 = %d, 期望 1(uk_contract_period 幂等)", cnt)
	}
}

// TestRunBillOnce_NoDB 未配库时应明确报错, 不能静默当成"没有合同需要出账".
func TestRunBillOnce_NoDB(t *testing.T) {
	if _, err := RunBillOnce(context.Background(), &svc.ServiceContext{}); err == nil {
		t.Error("DB 未初始化时应报错")
	}
}
