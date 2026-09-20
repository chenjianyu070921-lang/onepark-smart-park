package svc

import (
	"encoding/json"
	"testing"
	"time"

	"onepark/app/parking-service/internal/model"
)

// 验证停车计费算法(全链路验收口径):
// 月卡/VIP 免费; 临时车 ≤15 分钟免费, 之后 5 元/小时向上取整.
func TestCalcFee(t *testing.T) {
	entry := time.Date(2026, 9, 18, 9, 0, 0, 0, time.Local)
	at := func(min int) *time.Time {
		tm := entry.Add(time.Duration(min) * time.Minute)
		return &tm
	}

	cases := []struct {
		name  string
		entry *time.Time
		exit  *time.Time
		vType int8
		want  float64
	}{
		{"月卡免费", &entry, at(120), model.VehicleTypeMonthly, 0},
		{"VIP免费", &entry, at(120), model.VehicleTypeVIP, 0},
		{"时间缺失兜底0", nil, nil, model.VehicleTypeTemp, 0},
		{"临时车15分钟内免费", &entry, at(15), model.VehicleTypeTemp, 0},
		{"临时车16分钟按1小时", &entry, at(16), model.VehicleTypeTemp, 5},
		{"临时车61分钟按2小时", &entry, at(61), model.VehicleTypeTemp, 10},
		{"临时车120分钟整2小时", &entry, at(120), model.VehicleTypeTemp, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CalcFee(c.entry, c.exit, c.vType); got != c.want {
				t.Errorf("CalcFee(%v) = %.2f, want %.2f", c.name, got, c.want)
			}
		})
	}
}

// 验证月卡识别降级口径: DB 未初始化时按临时车处理, 不阻断入场.
func TestResolveVehicleType_Degrade(t *testing.T) {
	if got := ResolveVehicleType(nil, 1, "苏A99999", time.Now()); got != model.VehicleTypeTemp {
		t.Errorf("DB nil 时应降级为临时车, got %d", got)
	}
}

// 验证遥测报文归一化(地磁→停车全链路入口, 评审 P0 验证项):
// 标准信封(event_type + payload 业务字段) 与 旧扁平格式(event/plate_no/timestamp 顶层) 双兼容.
func TestNormalizeTelemetry(t *testing.T) {
	t.Run("标准信封+payload业务字段", func(t *testing.T) {
		raw := &deviceTelemetry{
			RequestID:  "req-1",
			DeviceID:   "geom-01",
			EventType:  "entry",
			OccurredAt: 1758200000,
			Source:     "mqtt",
			Payload:    json.RawMessage(`{"tenant_id":1,"plate_no":"苏A12345","vehicle_type":2}`),
		}
		got := normalizeTelemetry(raw)
		if got.Event != "entry" || got.TenantID != 1 || got.PlateNo != "苏A12345" || got.VehicleType != 2 {
			t.Errorf("标准信封归一化错误: %+v", got)
		}
		if got.Timestamp != 1758200000 || got.DeviceID != "geom-01" || got.RequestID != "req-1" {
			t.Errorf("信封字段透传错误: %+v", got)
		}
	})
	t.Run("旧扁平格式回退", func(t *testing.T) {
		raw := &deviceTelemetry{
			Event:       "exit",
			DeviceID:    "geom-02",
			PlateNo:     "BJ12345",
			VehicleType: 2,
			TenantID:    1,
			Timestamp:   1758203600,
		}
		got := normalizeTelemetry(raw)
		if got.Event != "exit" || got.PlateNo != "BJ12345" || got.Timestamp != 1758203600 {
			t.Errorf("旧扁平格式回退错误: %+v", got)
		}
	})
	t.Run("信封顶层字段优先于旧字段", func(t *testing.T) {
		raw := &deviceTelemetry{
			EventType:  "entry",
			OccurredAt: 1758200000,
			Event:      "exit",     // 旧字段, 应被 event_type 覆盖
			Timestamp:  1111111111, // 旧字段, 应被 occurred_at 覆盖
		}
		got := normalizeTelemetry(raw)
		if got.Event != "entry" || got.Timestamp != 1758200000 {
			t.Errorf("字段优先级错误: %+v", got)
		}
	})
}
