package rpcserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	leasingpb "onepark/proto/leasing"
)

// TestGetOccupancy_TenantScoped 入驻率必须按**调用方声明的租户**统计。
//
// 这是 2026-09-27 修的 Bug 的断言。修前的实际行为：
// `GetOccupancy` 只把 `req.TenantId` 塞进 DTO（而 DTO 那个字段下层从来不读），
// **没有把它注入 ctx** —— 而下层 `Occupancy()` 是按 **ctx 里的租户**做行级隔离的
// （`Where status = ? AND tenant_id = ?`，见 occupancylogic.go）。
// gRPC 不经过网关中间件、ctx 里没有租户，于是恒定按 tenant=0 统计：
// **对任何非 0 租户，已租面积永远返回 0（入驻率恒 0）**，而调用方无从察觉。
//
// 为什么原有用例一直绿：`TestGetOccupancy_EndToEnd` 只断言"0<=入驻率<=1"与
// "已租不超可租"—— tenant=0 时这两条不变量全都成立。**不变量测试挡不住这类 Bug**，
// 必须用"造数据 + 校验具体数值"的方式断言。
func TestGetOccupancy_TenantScoped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// 纳秒级唯一租户: 避免与库里既有数据/并行测试互相干扰
	suffix := time.Now().UnixNano() % 1_000_000_000
	targetTenant := int64(9_300_000_000) + suffix%1000
	otherTenant := targetTenant + 1

	seed := func(tenant int64, area float64, tag string) {
		t.Helper()
		c := &model.LeaseContract{
			ContractNo:      fmt.Sprintf("LC-OCC-%d-%s", tenant, tag),
			TenantId:        tenant,
			TenantName:      "入驻率租户隔离测试",
			ZoneCode:        "A-8F-801",
			AreaSqm:         area,
			MonthlyRent:     mustDecimal(t, "1000.00"),
			Deposit:         mustDecimal(t, "1000.00"),
			StartDate:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
			EndDate:         time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local),
			Status:          model.StatusActive,
			AutoRenew:       model.AutoRenewOff,
			RenewNoticeDays: 30,
		}
		if err := db.WithContext(ctx).Create(c).Error; err != nil {
			t.Fatalf("准备合同失败: %v", err)
		}
		t.Cleanup(func() {
			db.WithContext(context.Background()).Delete(&model.LeaseContract{}, c.Id)
		})
	}
	seed(targetTenant, 100, "t")
	seed(otherTenant, 200, "o")

	cli := startTestGRPC(t, &svc.ServiceContext{DB: db})
	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1) 声明目标租户 -> 必须统计到它的 100, 且不能把别的租户算进来(否则会是 300)
	resp, err := cli.GetOccupancy(callCtx, &leasingpb.GetOccupancyReq{TenantId: targetTenant})
	if err != nil {
		t.Fatalf("GetOccupancy 失败: %v", err)
	}
	if got := resp.GetLeasedAreaSqm(); got < 99.99 || got > 100.01 {
		t.Errorf("租户 %d 的已租面积 = %v, 期望 100 —— 修复前这里恒为 0(租户没注入 ctx)",
			targetTenant, got)
	}

	// 2) 换成另一个租户 -> 必须只统计它的 200(证明没串租户)
	resp2, err := cli.GetOccupancy(callCtx, &leasingpb.GetOccupancyReq{TenantId: otherTenant})
	if err != nil {
		t.Fatalf("GetOccupancy(第二租户) 失败: %v", err)
	}
	if got := resp2.GetLeasedAreaSqm(); got < 199.99 || got > 200.01 {
		t.Errorf("租户 %d 的已租面积 = %v, 期望 200(不能把其它租户的合同算进来)",
			otherTenant, got)
	}
}
