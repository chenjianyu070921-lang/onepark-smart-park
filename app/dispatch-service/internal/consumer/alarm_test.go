package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/model"
	"onepark/common/gormx"
)

// ---------- 纯函数部分: 无外部依赖, 任何环境都应通过 ----------

func TestDecodeAlarm(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"正常消息", `{"request_id":"req-1","device_id":"dev-1","event_type":"fire"}`, false},
		{"缺少 request_id", `{"device_id":"dev-1","event_type":"fire"}`, true},
		{"缺少 device_id", `{"request_id":"req-1","event_type":"fire"}`, true},
		{"非法 JSON", `{`, true},
		{"空消息", ``, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeAlarm([]byte(tt.value)); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, 期望是否出错 = %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildTaskDraft(t *testing.T) {
	tests := []struct {
		name         string
		evt          AlarmEvent
		wantTitle    string
		wantPriority int8
		wantZone     string
	}{
		{
			name:         "火灾 -> 紧急",
			evt:          AlarmEvent{RequestID: "r1", DeviceID: "d1", EventType: "fire"},
			wantTitle:    "火灾告警",
			wantPriority: model.PriorityUrgent,
			wantZone:     "",
		},
		{
			name:         "入侵 -> 高优先级",
			evt:          AlarmEvent{RequestID: "r2", DeviceID: "d2", EventType: "intrusion"},
			wantTitle:    "非法入侵告警",
			wantPriority: model.PriorityHigh,
		},
		{
			name:         "设备故障 -> 普通",
			evt:          AlarmEvent{RequestID: "r3", DeviceID: "d3", EventType: "fault"},
			wantTitle:    "设备故障告警",
			wantPriority: model.PriorityNormal,
		},
		{
			name:         "未知类型不丢弃, 按普通处理",
			evt:          AlarmEvent{RequestID: "r4", DeviceID: "d4", EventType: "strange"},
			wantTitle:    "设备告警(strange)",
			wantPriority: model.PriorityNormal,
		},
		{
			name: "payload 带 zone_code",
			evt: AlarmEvent{
				RequestID: "r5", DeviceID: "d5", EventType: "smoke",
				Payload: json.RawMessage(`{"zone_code":"A-3F-301"}`),
			},
			wantTitle:    "烟雾告警",
			wantPriority: model.PriorityUrgent,
			wantZone:     "A-3F-301",
		},
		{
			name: "payload 只有 location 时退化使用",
			evt: AlarmEvent{
				RequestID: "r6", DeviceID: "d6", EventType: "fault",
				Payload: json.RawMessage(`{"location":"A-5F-502"}`),
			},
			wantTitle:    "设备故障告警",
			wantPriority: model.PriorityNormal,
			wantZone:     "A-5F-502",
		},
		{
			name: "payload 无位置字段 -> 留空不臆造",
			evt: AlarmEvent{
				RequestID: "r7", DeviceID: "d7", EventType: "fault",
				Payload: json.RawMessage(`{"temp":42}`),
			},
			wantTitle:    "设备故障告警",
			wantPriority: model.PriorityNormal,
			wantZone:     "",
		},
		{
			name: "payload 非法 JSON -> 留空",
			evt: AlarmEvent{
				RequestID: "r8", DeviceID: "d8", EventType: "fault",
				Payload: json.RawMessage(`{`),
			},
			wantTitle:    "设备故障告警",
			wantPriority: model.PriorityNormal,
			wantZone:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildTaskDraft(tt.evt)
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, 期望 %q", got.Title, tt.wantTitle)
			}
			if got.Priority != tt.wantPriority {
				t.Errorf("Priority = %d, 期望 %d", got.Priority, tt.wantPriority)
			}
			if got.ZoneCode != tt.wantZone {
				t.Errorf("ZoneCode = %q, 期望 %q", got.ZoneCode, tt.wantZone)
			}
			if got.AlarmID != tt.evt.RequestID {
				t.Errorf("AlarmID = %q, 期望等于 request_id %q", got.AlarmID, tt.evt.RequestID)
			}
		})
	}
}

// ---------- 依赖本地 MySQL 的部分: 拿不到配置则跳过 ----------

// openTestDB 复用服务自身的 etc/dispatch-api.yaml 连接本地开发库。
// 这样做的好处: 不额外引入测试配置, 且与运行期连的是同一个库。
func openTestDB(t *testing.T) *gormx.DB {
	t.Helper()

	for _, p := range []string{"../../../etc/dispatch-api.yaml", "../../etc/dispatch-api.yaml"} {
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
		if err := sqlDB.Ping(); err != nil {
			continue
		}
		return db
	}

	t.Skip("跳过: 未找到可用的 etc/dispatch-api.yaml, 或本地数据库不可用")
	return nil
}

// TestHandle_Idempotent 验证同一告警重复投递只落一张工单。
// 幂等靠的是 uk_alarm_id 唯一索引, 而不是应用层的"先查后插"(那有并发竞态)。
func TestHandle_Idempotent(t *testing.T) {
	db := openTestDB(t)
	// 用一个非 0、且不等于 DB 默认值(0)的兜底园区 —— 才能证明租户是"配置注入"进来的,
	// 而不是恰好被数据库默认值填上的。
	const fallbackTenant = 42
	h := NewAlarmHandler(db, fallbackTenant)
	ctx := context.Background()

	alarmID := fmt.Sprintf("req-test-%d", time.Now().UnixNano())
	value := []byte(fmt.Sprintf(
		`{"request_id":%q,"device_id":"dev-test","event_type":"fire","payload":{"zone_code":"A-3F-301"}}`,
		alarmID,
	))

	t.Cleanup(func() {
		db.WithContext(ctx).Where("alarm_id = ?", alarmID).Delete(&model.DispatchTask{})
		db.WithContext(ctx).Where("remark LIKE ?", "%"+alarmID+"%").Delete(&model.DispatchTaskLog{})
	})

	if err := h.Handle(ctx, value); err != nil {
		t.Fatalf("首次处理失败: %v", err)
	}
	if err := h.Handle(ctx, value); err != nil {
		t.Fatalf("重复处理不应报错(应幂等跳过): %v", err)
	}
	if err := h.Handle(ctx, value); err != nil {
		t.Fatalf("第三次处理不应报错: %v", err)
	}

	var count int64
	if err := db.WithContext(ctx).Model(&model.DispatchTask{}).
		Where("alarm_id = ?", alarmID).Count(&count).Error; err != nil {
		t.Fatalf("统计工单数失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一告警落库工单数 = %d, 期望 1", count)
	}

	// 校验建单结果符合告警语义
	var task model.DispatchTask
	if err := db.WithContext(ctx).Where("alarm_id = ?", alarmID).First(&task).Error; err != nil {
		t.Fatalf("读取工单失败: %v", err)
	}
	if task.Source != model.SourceAlarm {
		t.Errorf("Source = %d, 期望 %d(告警自动创建)", task.Source, model.SourceAlarm)
	}
	// 自动建单必须落到 config.DefaultTenantId 指定的兜底园区(告警消息不带租户)。
	// 不写租户的话恒为 0 —— 网关注入非 0 租户时, 这张单会**建出来就查不到**。
	if task.TenantID != fallbackTenant {
		t.Errorf("TenantID = %d, 期望 %d(来自 config.DefaultTenantId 兜底)",
			task.TenantID, fallbackTenant)
	}
	if task.Priority != model.PriorityUrgent {
		t.Errorf("Priority = %d, 期望 %d(火灾为紧急)", task.Priority, model.PriorityUrgent)
	}
	if task.Status != model.StatusPendingAssign {
		t.Errorf("Status = %d, 期望 %d(待指派)", task.Status, model.StatusPendingAssign)
	}
	if task.ZoneCode != "A-3F-301" {
		t.Errorf("ZoneCode = %q, 期望 A-3F-301", task.ZoneCode)
	}
}

// TestHandle_BadMessageNotBlocking 坏消息必须"记录后跳过":
// 若返回 error, 位移不提交, 整条分区会被这一条毒消息卡死。
func TestHandle_BadMessageNotBlocking(t *testing.T) {
	db := openTestDB(t)
	h := NewAlarmHandler(db, 1)

	for _, bad := range []string{`{`, `{"device_id":"d1"}`, ``} {
		if err := h.Handle(context.Background(), []byte(bad)); err != nil {
			t.Fatalf("坏消息 %q 应记录后跳过(返回 nil), 实际: %v", bad, err)
		}
	}
}
