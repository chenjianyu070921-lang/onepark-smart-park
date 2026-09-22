package logic

import (
	"testing"
	"time"

	"onepark/app/parking-service/internal/model"
)

// TestMonthlyCardTimeValid 月卡有效期校验: 生效止必须晚于生效起(对齐 createmonthlycardlogic.go 业务约束).
func TestMonthlyCardTimeValid(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if !monthlyCardTimeValid(base, base.Add(time.Hour)) {
		t.Error("止晚于起应有效")
	}
	if monthlyCardTimeValid(base, base) {
		t.Error("起止相等应无效")
	}
	if monthlyCardTimeValid(base, base.Add(-time.Hour)) {
		t.Error("止早于起应无效")
	}
}

// TestMonthlyCardResp 月卡模型转响应: 字段与时间戳映射契约(对齐 createmonthlycardlogic.go monthlyCardResp).
func TestMonthlyCardResp(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local)
	card := &model.MonthlyCard{PlateNo: "苏A12345", StartTime: start, EndTime: end, Status: model.MonthlyCardStatusActive}
	resp := monthlyCardResp(card)
	if resp.PlateNo != "苏A12345" || resp.Status != model.MonthlyCardStatusActive {
		t.Errorf("月卡响应字段映射错误: %+v", resp)
	}
	if resp.StartTime != start.Unix() || resp.EndTime != end.Unix() {
		t.Errorf("月卡响应时间戳映射错误: start=%d end=%d", resp.StartTime, resp.EndTime)
	}
}
