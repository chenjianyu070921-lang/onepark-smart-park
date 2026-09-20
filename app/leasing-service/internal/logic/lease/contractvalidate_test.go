package lease

import (
	"context"
	"testing"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/gormx"
)

// 本文件覆盖合同主干的**入参校验分支**与**状态流转**。
//
// 为什么值得逐条测: 这些校验与状态判定**早就写好了**, 但此前 0 用例覆盖 ——
// 既拉低覆盖率, 更危险的是「有人误删其中一条校验也不会有测试报警」。
// 每条用例只改一个字段, 失败时能直接定位是哪条校验没了。

// validCreateReq 一份合法的创建请求.
//
// ⚠️ 关于 AutoRenew / RenewNoticeDays: HTTP 层由 goctl 注入 `default=-1`(表示"本次不改动"),
// 而直接以 Go 调用 logic 时零值是 0, 会被当成"要把它改成 0"。
// 所以构造 update 请求时必须**显式给 -1**, 否则测不出真实行为。
func validCreateReq() *types.ContractCreateReq {
	return &types.ContractCreateReq{
		TenantId:        1,
		TenantName:      "Validation Test Co",
		ZoneCode:        "Z-VAL-1F",
		AreaSqm:         100,
		MonthlyRent:     "8000.00",
		Deposit:         "16000.00",
		StartDate:       "2026-01-01",
		EndDate:         "2026-12-31",
		AutoRenew:       0,
		RenewNoticeDays: 30,
	}
}

// TestContractCreate_Validation 创建合同: 逐条命中校验分支.
func TestContractCreate_Validation(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	ctx := context.Background()

	cases := []struct {
		name   string
		mutate func(*types.ContractCreateReq)
	}{
		{"月租金非数字", func(r *types.ContractCreateReq) { r.MonthlyRent = "abc" }},
		{"月租金为负", func(r *types.ContractCreateReq) { r.MonthlyRent = "-1" }},
		{"月租金为空", func(r *types.ContractCreateReq) { r.MonthlyRent = "" }},
		{"押金非数字", func(r *types.ContractCreateReq) { r.Deposit = "abc" }},
		{"押金为负", func(r *types.ContractCreateReq) { r.Deposit = "-0.01" }},
		{"起租日格式非法", func(r *types.ContractCreateReq) { r.StartDate = "2026/01/01" }},
		{"终止日格式非法", func(r *types.ContractCreateReq) { r.EndDate = "2026-13-01" }},
		{"终止日等于起租日", func(r *types.ContractCreateReq) { r.EndDate = r.StartDate }},
		{"终止日早于起租日", func(r *types.ContractCreateReq) { r.EndDate = "2025-12-31" }},
		{"区域编码为空", func(r *types.ContractCreateReq) { r.ZoneCode = "" }},
		{"auto_renew 非法值", func(r *types.ContractCreateReq) { r.AutoRenew = 7 }},
		{"提醒天数超上限", func(r *types.ContractCreateReq) { r.RenewNoticeDays = 366 }},
		{"提醒天数为负", func(r *types.ContractCreateReq) { r.RenewNoticeDays = -1 }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := validCreateReq()
			c.mutate(req)
			if _, err := NewContractCreateLogic(ctx, svcCtx).ContractCreate(req); err == nil {
				t.Errorf("%s: 应被拒绝, 但通过了", c.name)
			}
		})
	}
}

// TestContractCreate_OptionalDeposit 押金是可选字段: 省略时按 0 处理, 不能报错.
func TestContractCreate_OptionalDeposit(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()

	req := validCreateReq()
	req.Deposit = "" // 省略押金
	resp, err := NewContractCreateLogic(ctx, svcCtx).ContractCreate(req)
	if err != nil {
		t.Fatalf("省略押金应允许, 实际报错: %v", err)
	}
	t.Cleanup(func() { cleanupContract(context.Background(), db, resp.Id) })

	var got model.LeaseContract
	if err := db.WithContext(ctx).First(&got, resp.Id).Error; err != nil {
		t.Fatalf("回读合同失败: %v", err)
	}
	if !got.Deposit.IsZero() {
		t.Errorf("省略押金时押金应为 0, 实际 %s", got.Deposit.String())
	}
	if got.Status != model.StatusPending {
		t.Errorf("新合同状态 = %d, 期望待生效", got.Status)
	}
}

// createTestContract 造一份「待生效」合同供后续流转用例使用, 并注册清理.
func createTestContract(t *testing.T, ctx context.Context, db *svc.ServiceContext) *types.ContractCreateResp {
	t.Helper()
	resp, err := NewContractCreateLogic(ctx, db).ContractCreate(validCreateReq())
	if err != nil {
		t.Fatalf("准备合同失败: %v", err)
	}
	return resp
}

// cleanupContract 删除合同及其审计流水, 避免污染开发库.
func cleanupContract(ctx context.Context, db *gormx.DB, id int64) {
	db.WithContext(ctx).Where("contract_id = ?", id).Delete(&model.LeaseContractStatusLog{})
	db.WithContext(ctx).Delete(&model.LeaseContract{}, id)
}

// TestContractUpdate_Validation 更新合同: 逐条命中校验分支.
func TestContractUpdate_Validation(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()

	created := createTestContract(t, ctx, svcCtx)
	t.Cleanup(func() { cleanupContract(ctx, db, created.Id) })
	logic := NewContractUpdateLogic(ctx, svcCtx)

	// 不带任何可变更字段的 update(AutoRenew/RenewNoticeDays 传 -1 表示"不改动")
	blank := &types.ContractUpdateReq{Id: created.Id, Action: "update", AutoRenew: -1, RenewNoticeDays: -1}

	cases := []struct {
		name string
		req  *types.ContractUpdateReq
	}{
		{"不支持的 action", &types.ContractUpdateReq{Id: created.Id, Action: "archive"}},
		{"action 为空", &types.ContractUpdateReq{Id: created.Id, Action: ""}},
		{"update 未提供任何字段", blank},
		{"update 租金非法", &types.ContractUpdateReq{
			Id: created.Id, Action: "update", MonthlyRent: "abc", AutoRenew: -1, RenewNoticeDays: -1}},
		{"update auto_renew 非法", &types.ContractUpdateReq{
			Id: created.Id, Action: "update", AutoRenew: 5, RenewNoticeDays: -1}},
		{"update 提醒天数越界", &types.ContractUpdateReq{
			Id: created.Id, Action: "update", AutoRenew: -1, RenewNoticeDays: 999}},
		{"renew 未给新终止日", &types.ContractUpdateReq{Id: created.Id, Action: "renew"}},
		{"renew 新终止日格式非法", &types.ContractUpdateReq{
			Id: created.Id, Action: "renew", NewEndDate: "2027/01/01"}},
		{"renew 新终止日不晚于原终止日", &types.ContractUpdateReq{
			Id: created.Id, Action: "renew", NewEndDate: "2026-12-31"}},
		{"renew 租金非法", &types.ContractUpdateReq{
			Id: created.Id, Action: "renew", NewEndDate: "2027-12-31", MonthlyRent: "-5"}},
		{"待生效合同不能 expire", &types.ContractUpdateReq{Id: created.Id, Action: "expire"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := logic.ContractUpdate(c.req); err == nil {
				t.Errorf("%s: 应被拒绝, 但通过了", c.name)
			}
		})
	}
}

// TestContractUpdate_NotFound 不存在的合同.
func TestContractUpdate_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	if _, err := NewContractUpdateLogic(context.Background(), svcCtx).
		ContractUpdate(&types.ContractUpdateReq{Id: 999_999_999, Action: "terminate"}); err == nil {
		t.Error("不存在的合同应报错")
	}
}

// TestContractLifecycle 主干流转: 创建 -> 生效 -> 续签 -> 终止 -> 终态拒绝, 并核对审计.
func TestContractLifecycle(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()

	created := createTestContract(t, ctx, svcCtx)
	t.Cleanup(func() { cleanupContract(ctx, db, created.Id) })
	logic := NewContractUpdateLogic(ctx, svcCtx)

	// 1) 生效
	resp, err := logic.ContractUpdate(&types.ContractUpdateReq{Id: created.Id, Action: "activate"})
	if err != nil {
		t.Fatalf("生效失败: %v", err)
	}
	if resp.Status != int32(model.StatusActive) {
		t.Errorf("生效后状态 = %d, 期望生效中", resp.Status)
	}

	// 2) 重复生效应被拒
	if _, err := logic.ContractUpdate(&types.ContractUpdateReq{Id: created.Id, Action: "activate"}); err == nil {
		t.Error("已生效合同重复生效应被拒绝")
	}

	// 3) 续签: 顺延终止日 + 调租金
	resp, err = logic.ContractUpdate(&types.ContractUpdateReq{
		Id: created.Id, Action: "renew", NewEndDate: "2027-12-31", MonthlyRent: "9000.00",
	})
	if err != nil {
		t.Fatalf("续签失败: %v", err)
	}
	if resp.Status != int32(model.StatusActive) {
		t.Errorf("续签后状态 = %d, 期望保持生效中", resp.Status)
	}
	var afterRenew model.LeaseContract
	if err := db.WithContext(ctx).First(&afterRenew, created.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if afterRenew.EndDate.Format(dateLayout) != "2027-12-31" {
		t.Errorf("续签后终止日 = %s, 期望 2027-12-31", afterRenew.EndDate.Format(dateLayout))
	}
	if afterRenew.MonthlyRent.StringFixed(2) != "9000.00" {
		t.Errorf("续签后月租金 = %s, 期望 9000.00", afterRenew.MonthlyRent.StringFixed(2))
	}

	// 4) 终止
	resp, err = logic.ContractUpdate(&types.ContractUpdateReq{
		Id: created.Id, Action: "terminate", Reason: "租户提前退租",
	})
	if err != nil {
		t.Fatalf("终止失败: %v", err)
	}
	if resp.Status != int32(model.StatusTerminated) {
		t.Errorf("终止后状态 = %d, 期望已终止", resp.Status)
	}

	// 5) 终态: 不可再流转, 也不可再变更
	for _, action := range []string{"activate", "renew", "expire", "terminate", "update"} {
		req := &types.ContractUpdateReq{Id: created.Id, Action: action}
		if action == "update" {
			req.MonthlyRent = "1.00"
			req.AutoRenew = -1
			req.RenewNoticeDays = -1
		}
		if _, err := logic.ContractUpdate(req); err == nil {
			t.Errorf("已终止合同不应允许 action=%s", action)
		}
	}

	// 6) 审计流水: 创建/生效/续签/终止 各一条
	var logs []model.LeaseContractStatusLog
	if err := db.WithContext(ctx).
		Where("contract_id = ?", created.Id).
		Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("读取审计流水失败: %v", err)
	}
	if len(logs) != 4 {
		t.Errorf("审计流水条数 = %d, 期望 4(创建/生效/续签/终止)", len(logs))
	}
	for i, want := range []string{"create", "activate", "renew", "terminate"} {
		if i < len(logs) && logs[i].Action != want {
			t.Errorf("第 %d 条流水 action = %q, 期望 %q", i+1, logs[i].Action, want)
		}
	}

	// 7) 无效引用: 终态合同的 update 不应改动任何字段
	if len(logs) > 0 {
		var latest model.LeaseContract
		if err := db.WithContext(ctx).First(&latest, created.Id).Error; err != nil {
			t.Fatalf("回读失败: %v", err)
		}
		if latest.Status != model.StatusTerminated {
			t.Errorf("终态被意外改动: status=%d", latest.Status)
		}
	}
}
