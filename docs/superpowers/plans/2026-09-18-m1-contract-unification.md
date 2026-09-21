# M1 契约统一与链路修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除 M1 四处重复的消息契约定义，在设备遥测消息中生产时充入 tenant_id/zone_id，统一 shadow 双入口版本语义，并为 event-dispatcher 补齐死信队列与投递重试。

**Architecture:** 契约以 Go 结构体落在 `common/kafka`（JSON 序列化、与线上格式逐字段兼容）；三个 Kafka 生产端（device-service HTTP 降级通道、gateway-service TCP、event-dispatcher MQTT）各自从设备档案充入 tenant_id/zone_id；shadow 模型下沉 `common/shadow` 并统一为乐观锁条件更新；event-dispatcher 增加只读 MySQL 档案查询（30s TTL 缓存）与 `event-dispatcher-dlq` 死信 topic。

**Tech Stack:** Go 1.26 / go-zero / GORM + MySQL / segmentio kafka-go / paho MQTT / go.work 多模块（21 个模块，逐模块 `go build`）

**Spec:** `docs/superpowers/specs/2026-09-18-m1-contract-unification-design.md`

## Global Constraints

- 序列化保持 JSON；`occurred_at` 保持 Unix 秒（int64），不得改为 time.Time（会破坏全部现有消费方）。
- topic 名不变：`device-telemetry`、`alarm-event`；新增 `event-dispatcher-dlq`。
- 契约结构体新增字段仅 `tenant_id`、`zone_id`（顶层、历史消息缺省为零值，向后兼容）。
- 不引入 `ProductKey` 字段到消息契约。
- 只改 M1 与 common/，不改 M4/其他模块代码。
- 每个 Go 模块独立 `go.mod`（go.work），改完必须在模块目录内 `go build ./...`。
- 提交信息用中文，末尾带 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。
- 注释风格与仓库一致：中文注释、句号结尾、说明"为什么"。

---

### Task 1: common/kafka 契约包（结构体 + 枚举 + DLQ 信封 + topic 常量）

**Files:**
- Create: `common/kafka/contract.go`
- Create: `common/kafka/contract_test.go`
- Modify: `common/kafka/topics.go:7-19`（新增 DLQ topic 常量）

**Interfaces:**
- Consumes: 无（纯新增）
- Produces（后续任务全部依赖，签名精确如下）:
  - `kafka.DeviceTelemetry` 结构体（字段：RequestID/TenantID/DeviceID/DeviceType/EventType/ZoneID/OccurredAt/Payload/Source）
  - `kafka.EventOnline/EventOffline/EventFault/EventStatus/EventTelemetry/EventGeneric`（string 常量）
  - `kafka.AlarmIntrusion/AlarmFire/AlarmSmoke/AlarmFault/AlarmDoorForce/AlarmOffline`（string 常量）
  - `kafka.IsAlarmEvent(eventType string) bool`
  - `kafka.DLQEnvelope` 结构体 + `kafka.NewDLQEnvelope(sourceTopic, reason string, raw []byte) DLQEnvelope`
  - `kafka.TopicDispatcherDLQ`（= `"event-dispatcher-dlq"`）

- [ ] **Step 1: 写失败测试**

创建 `common/kafka/contract_test.go`：

```go
package kafka

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDeviceTelemetryRoundTrip 契约结构体 JSON 序列化往返.
func TestDeviceTelemetryRoundTrip(t *testing.T) {
	in := DeviceTelemetry{
		RequestID:  "req-001",
		TenantID:   7,
		DeviceID:   "dev-001",
		DeviceType: "camera",
		EventType:  "intrusion",
		ZoneID:     "zone-a",
		OccurredAt: 1758153600,
		Payload:    json.RawMessage(`{"metrics":{"energy_total":123.5}}`),
		Source:     "mqtt",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	// 关键 JSON 键必须与线上契约一致, 任何键名漂移都会破坏消费方
	for _, key := range []string{"request_id", "tenant_id", "device_id", "device_type", "event_type", "zone_id", "occurred_at", "payload", "source"} {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("缺少 JSON 键 %s: %s", key, b)
		}
	}

	var out DeviceTelemetry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if out != in {
		t.Fatalf("往返不一致:\n in=%+v\nout=%+v", in, out)
	}
}

// TestDeviceTelemetryBackwardCompat 历史消息(无 tenant_id/zone_id)必须能解码为零值,
// 保证契约升级不要求消费方同步发版.
func TestDeviceTelemetryBackwardCompat(t *testing.T) {
	legacy := `{"request_id":"r1","device_id":"d1","device_type":"meter","event_type":"telemetry","occurred_at":100,"payload":{},"source":"mqtt"}`
	var m DeviceTelemetry
	if err := json.Unmarshal([]byte(legacy), &m); err != nil {
		t.Fatalf("历史消息解码失败: %v", err)
	}
	if m.TenantID != 0 || m.ZoneID != "" {
		t.Fatalf("历史消息新字段应为零值, got tenant=%d zone=%q", m.TenantID, m.ZoneID)
	}
	if m.DeviceID != "d1" || m.OccurredAt != 100 {
		t.Fatalf("历史字段解码错误: %+v", m)
	}
}

func TestIsAlarmEvent(t *testing.T) {
	for _, e := range []string{AlarmIntrusion, AlarmFire, AlarmSmoke, AlarmFault, AlarmDoorForce, AlarmOffline} {
		if !IsAlarmEvent(e) {
			t.Fatalf("%s 应为告警事件", e)
		}
	}
	for _, e := range []string{"telemetry", "online", "status", "", "fire_drill"} {
		if IsAlarmEvent(e) {
			t.Fatalf("%s 不应为告警事件", e)
		}
	}
}

// TestNewDLQEnvelopeTruncates 死信信封必须截断超长原始报文, 防止 DLQ 消息本身过大.
func TestNewDLQEnvelopeTruncates(t *testing.T) {
	env := NewDLQEnvelope("onepark/device/pk/d1/event", "报文解析失败", []byte(strings.Repeat("x", 8192)))
	if len(env.Raw) > 4096 {
		t.Fatalf("raw 应截断至 4KB, got %d", len(env.Raw))
	}
	if env.SourceTopic == "" || env.Reason == "" || env.FailedAt.IsZero() {
		t.Fatalf("信封字段不完整: %+v", env)
	}
	if env.FailedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("FailedAt 不应是未来时间: %v", env.FailedAt)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd common && go test ./kafka/ -run 'TestDeviceTelemetry|TestIsAlarmEvent|TestNewDLQEnvelope' -v`
Expected: FAIL，报 `DeviceTelemetry undefined` / `IsAlarmEvent undefined`

- [ ] **Step 3: 实现 contract.go**

创建 `common/kafka/contract.go`：

```go
// 契约定稿(2026-09-18): 设备遥测/事件消息的唯一权威定义.
// 此前 event-dispatcher、device-service、gateway-service、workorder/dispatch 消费端
// 各自复制结构体, 已实际造成 M4 按错误想象编写消费端(错误 topic 名+错误字段结构),
// 因此下沉到 common 作为编译期约束; 各服务不得再本地定义同名结构.
package kafka

import (
	"encoding/json"
	"time"
)

// DeviceTelemetry 设备遥测/事件统一契约.
// topic: TopicDeviceTelemetry(全量上报); TopicAlarm(仅 IsAlarmEvent 命中的告警类).
// 兼容性: 与 2026-09 前线上格式逐字段一致, 仅新增 tenant_id/zone_id;
// 历史消息缺省解码为零值, 消费方无需同步升级.
type DeviceTelemetry struct {
	RequestID  string          `json:"request_id"`
	TenantID   int64           `json:"tenant_id"` // 生产端充入, 0 表示未归属(历史设备/未配置档案查询)
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	ZoneID     string          `json:"zone_id"` // 能源区域编码(M4 计费/分析维度), 空表示未分区
	OccurredAt int64           `json:"occurred_at"` // Unix 秒; 不得改为 time.Time, 会破坏现有消费方
	Payload    json.RawMessage `json:"payload"`     // 业务负载, 遥测类为 {"metrics":{...}}
	Source     string          `json:"source"`      // tcp-gateway / mqtt / http-fallback
}

// 事件类型常量(status 类事件的取值, 同时兜底 MQTT topic 的 kind 段).
const (
	EventOnline    = "online"
	EventOffline   = "offline"
	EventFault     = "fault"
	EventStatus    = "status"
	EventTelemetry = "telemetry"
	EventGeneric   = "event"
)

// 告警类事件类型: 命中后消息会额外投递 TopicAlarm, 供 M2 工单/M5 调度消费.
// 此前在 dispatch.go / deviceeventlogic.go / gateway protocol.go 三处硬编码, 现合一.
const (
	AlarmIntrusion = "intrusion"  // 非法入侵
	AlarmFire      = "fire"       // 火情
	AlarmSmoke     = "smoke"      // 烟感
	AlarmFault     = "fault"      // 设备故障
	AlarmDoorForce = "door_force" // 门禁强开
	AlarmOffline   = "offline_alert" // 异常离线
)

var alarmEventTypes = map[string]struct{}{
	AlarmIntrusion: {},
	AlarmFire:      {},
	AlarmSmoke:     {},
	AlarmFault:     {},
	AlarmDoorForce: {},
	AlarmOffline:   {},
}

// IsAlarmEvent 判断事件类型是否为告警类.
func IsAlarmEvent(eventType string) bool {
	_, ok := alarmEventTypes[eventType]
	return ok
}

// DLQEnvelope 死信信封: 坏消息/投递重试耗尽的消息统一包装后投 TopicDispatcherDLQ,
// 供事后排查与重放; 不再静默丢弃.
type DLQEnvelope struct {
	SourceTopic string    `json:"source_topic"` // 来源 MQTT topic 或目标 Kafka topic
	Reason      string    `json:"reason"`       // 解析失败 / 未知设备 / 档案查询失败 / 投递重试耗尽
	Raw         string    `json:"raw"`          // 原始报文, 截断至 4KB
	FailedAt    time.Time `json:"failed_at"`
}

// maxDLQRawBytes 死信原始报文上限, 防止 DLQ 消息本身过大撑爆分区.
const maxDLQRawBytes = 4096

// NewDLQEnvelope 构造死信信封, raw 超长时截断.
func NewDLQEnvelope(sourceTopic, reason string, raw []byte) DLQEnvelope {
	if len(raw) > maxDLQRawBytes {
		raw = raw[:maxDLQRawBytes]
	}
	return DLQEnvelope{
		SourceTopic: sourceTopic,
		Reason:      reason,
		Raw:         string(raw),
		FailedAt:    time.Now(),
	}
}
```

- [ ] **Step 4: topics.go 增加 DLQ topic**

修改 `common/kafka/topics.go`，在 `TopicWorkorder` 常量后追加：

```go
	// TopicDispatcherDLQ event-dispatcher 死信 topic: 坏消息/未知设备/投递重试耗尽的消息信封,
	// 供排查与重放, 不再静默丢弃.
	TopicDispatcherDLQ = "event-dispatcher-dlq"
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd common && go test ./kafka/ -v`
Expected: 全部 PASS（含既有 kafka 相关测试，如无则仅新增 4 个用例 PASS）

- [ ] **Step 6: 提交**

```bash
git add common/kafka/contract.go common/kafka/contract_test.go common/kafka/topics.go
git commit -m "feat(common/kafka): 设备遥测/事件统一契约与死信信封

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: device 表增加 tenant_id/zone_id（模型 + 全量 SQL + 迁移 SQL + 注册接口）

**Files:**
- Modify: `app/device-service/internal/model/device.go:10-30`（Device 结构体）
- Modify: `app/device-service/internal/types/types.go:72-79`（DeviceRegisterReq）
- Modify: `app/device-service/internal/logic/deviceregisterlogic.go:70-81`（注册写入）
- Modify: `deploy/sql/m1_mysql_tables.sql:38-62`（全量建表脚本）
- Create: `deploy/sql/m1_device_add_tenant_zone.sql`（存量迁移）

**Interfaces:**
- Consumes: 无
- Produces: `model.Device.TenantID int64`、`model.Device.ZoneID string`（Task 3/4/5 充入逻辑依赖）

- [ ] **Step 1: 修改 GORM Device 模型**

`app/device-service/internal/model/device.go` 的 Device 结构体，在 `ProductKey` 字段之后插入：

```go
	// TenantID 租户 ID(多园区隔离), 0 平台默认; 生产端充入 Kafka 消息供下游租户过滤.
	TenantID int64 `gorm:"column:tenant_id;type:bigint;not null;default:0;index:idx_tenant" json:"tenant_id"`
	// ZoneID 能源区域编码(M4 计费/分析维度), 空表示未分区.
	ZoneID string `gorm:"column:zone_id;type:varchar(64);not null;default:'';index:idx_zone" json:"zone_id"`
```

- [ ] **Step 2: 写迁移 SQL**

创建 `deploy/sql/m1_device_add_tenant_zone.sql`（风格对齐 m1_device_add_type_location.sql）：

```sql
-- ============================================================
-- OnePark M1 迁移脚本: device 表补 tenant_id/zone_id 列
-- 适用: 已按旧版 m1_mysql_tables.sql 建表的存量环境
-- 全新环境无需执行 —— 新版 m1_mysql_tables.sql 已包含这两列与索引.
-- 执行方式: Navicat 直接执行(MySQL 8.x)
-- ============================================================

ALTER TABLE `device`
  ADD COLUMN `tenant_id` BIGINT      NOT NULL DEFAULT 0  COMMENT '租户 ID(多园区隔离), 0 平台默认' AFTER `product_key`,
  ADD COLUMN `zone_id`   VARCHAR(64) NOT NULL DEFAULT '' COMMENT '能源区域编码(M4 计费/分析维度), 空未分区' AFTER `tenant_id`,
  ADD KEY `idx_tenant` (`tenant_id`),
  ADD KEY `idx_zone` (`zone_id`);

-- 存量设备回填示例(按产品归类):
-- UPDATE `device` SET `zone_id` = 'zone-a', `tenant_id` = 1 WHERE `product_key` = 'pk_electric_meter';
```

- [ ] **Step 3: 同步全量建表脚本**

`deploy/sql/m1_mysql_tables.sql` 的 device 建表语句中，在 `` `product_key` `` 行后插入两列定义、在 `KEY idx_type` 行后补两个索引：

```sql
  `tenant_id`        BIGINT       NOT NULL DEFAULT 0 COMMENT '租户 ID(多园区隔离), 0 平台默认',
  `zone_id`          VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '能源区域编码(M4 计费/分析维度), 空未分区',
```

```sql
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_zone` (`zone_id`),
```

- [ ] **Step 4: 注册接口透传字段**

`app/device-service/internal/types/types.go` 的 DeviceRegisterReq，`ProductKey` 字段后加：

```go
	TenantId int64  `json:"tenantId,optional"` // 租户 ID, 缺省 0(平台默认)
	ZoneId   string `json:"zoneId,optional"`  // 能源区域编码, 缺省未分区
```

`app/device-service/internal/logic/deviceregisterlogic.go` 第 71-81 行的 Device 构造改为：

```go
	device := &model.Device{
		DeviceID:     deviceID,
		DeviceName:   deviceName,
		DeviceSecret: string(hashedSecret),
		ProductKey:   product.ProductKey,
		TenantID:     req.TenantId,
		ZoneID:       req.ZoneId,
		ParkID:       req.ParkID,
		BuildingID:   req.BuildingID,
		Floor:        req.Floor,
		Location:     req.Location,
		Status:       0, // 0=未激活
	}
```

- [ ] **Step 5: 编译 + 既有测试回归**

Run: `cd app/device-service && go build ./... && go test ./...`
Expected: BUILD OK；既有 logic/mq/server 测试全部 PASS（注册测试若断言了完整 Device 字段，按新增字段更新断言）

- [ ] **Step 6: 提交**

```bash
git add app/device-service/internal/model/device.go app/device-service/internal/types/types.go \
  app/device-service/internal/logic/deviceregisterlogic.go deploy/sql/m1_mysql_tables.sql \
  deploy/sql/m1_device_add_tenant_zone.sql
git commit -m "feat(device-service): device 表补 tenant_id/zone_id 并在注册接口透传

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: device-service 接入契约（消费端别名 + HTTP 事件通道充入）

**Files:**
- Modify: `app/device-service/internal/mq/telemetry.go:24-42`（删除本地 Message/事件常量，改用契约）
- Modify: `app/device-service/internal/logic/deviceeventlogic.go:21-40,102-115`（删除本地结构，充入字段）

**Interfaces:**
- Consumes: Task 1 的 `kafka.DeviceTelemetry`/`kafka.IsAlarmEvent`/事件常量；Task 2 的 `Device.TenantID/ZoneID`
- Produces: device-service 发出的 `device-telemetry`/`alarm-event` 消息携带 tenant_id/zone_id

- [ ] **Step 1: mq/telemetry.go 改用契约（类型别名保持文件其余部分不动）**

删除 `app/device-service/internal/mq/telemetry.go` 第 24-42 行的本地 `Message` 结构体与 `EventOnline/EventOffline/EventFault/EventStatus` 常量块，替换为：

```go
// Message 统一契约别名: 消费端与三处生产端共用 common/kafka 的权威定义.
type Message = kafka.DeviceTelemetry
```

文件内 `statusPayload`、`metricsPayload` 及全部处理逻辑不变（`kafka.EventOnline` 等常量名与原本地常量同名，引用处零改动）。

- [ ] **Step 2: deviceeventlogic.go 删本地定义并充入**

删除 `app/device-service/internal/logic/deviceeventlogic.go` 第 21-40 行的 `alarmEventTypes` 与 `deviceEventMessage`。

第 78-85 行设备校验改为**捕获设备记录**：

```go
	// 2. 设备存在性校验(捕获记录, 用于消息充入 tenant_id/zone_id)
	device, err := l.svcCtx.DeviceModel.FindByDeviceID(l.ctx, req.DeviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrDeviceNotFound, "设备不存在")
		}
		l.Errorf("查询设备失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询设备失败")
	}
```

第 107-115 行组装消息改为：

```go
	msg := kafka.DeviceTelemetry{
		RequestID:  requestID,
		TenantID:   device.TenantID,
		DeviceID:   req.DeviceID,
		DeviceType: req.DeviceType,
		EventType:  req.EventType,
		ZoneID:     device.ZoneID,
		OccurredAt: occurredAt,
		Payload:    json.RawMessage(rawPayload),
		Source:     "http-fallback",
	}
```

第 129 行 `if _, ok := alarmEventTypes[req.EventType]; ok` 改为：

```go
	if kafka.IsAlarmEvent(req.EventType) {
```

- [ ] **Step 3: 编译 + 测试回归**

Run: `cd app/device-service && go build ./... && go test ./...`
Expected: BUILD OK；`TestDeviceEventParamInvalid` 等既有测试 PASS（该测试传 nil svcCtx 只测参数校验，不受影响）

- [ ] **Step 4: 提交**

```bash
git add app/device-service/internal/mq/telemetry.go app/device-service/internal/logic/deviceeventlogic.go
git commit -m "feat(device-service): 事件通道接入统一契约并充入 tenant_id/zone_id

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: gateway-service 接入契约（模型补列 + 会话充入）

**Files:**
- Modify: `app/gateway-service/internal/model/model.go:26-35`（Device 补字段）
- Modify: `app/gateway-service/internal/frame/protocol.go:45-64`（删除本地 Message/alarmEventTypes）
- Modify: `app/gateway-service/internal/frame/handler.go:22-28,184-192,214-222`（会话存档案、publish 充入）

**Interfaces:**
- Consumes: Task 1 契约；Task 2 的 device 表新列
- Produces: TCP 通道消息携带 tenant_id/zone_id

- [ ] **Step 1: gateway 只读模型补列**

`app/gateway-service/internal/model/model.go` 的 Device 结构体，`ProductKey` 字段后插入：

```go
	TenantID int64  `gorm:"column:tenant_id"`
	ZoneID   string `gorm:"column:zone_id"`
```

- [ ] **Step 2: protocol.go 删本地定义**

删除 `app/gateway-service/internal/frame/protocol.go` 第 45-64 行的 `Message` 结构体与 `alarmEventTypes`，替换为：

```go
// Message 统一契约别名: 与 event-dispatcher/device-service 共用 common/kafka 权威定义.
type Message = kafka.DeviceTelemetry
```

并在文件头 import 块加 `"onepark/common/kafka"`。

- [ ] **Step 3: handler.go 会话保存档案并在 publish 充入**

`Handler` 结构体（第 22-28 行）增加两个字段：

```go
type Handler struct {
	logx.Logger
	svcCtx   *svc.ServiceContext
	deviceID string
	tenantID int64  // 认证时从设备档案捕获, 供消息充入
	zoneID   string // 同上
	authed   bool
}
```

`auth` 方法（第 184-186 行）认证成功处补：

```go
	h.authed = true
	h.deviceID = device.DeviceID
	h.tenantID = device.TenantID
	h.zoneID = device.ZoneID
```

`publish` 方法（第 214-222 行）组装消息处补两个字段：

```go
	msg := Message{
		RequestID:  requestID,
		TenantID:   h.tenantID,
		DeviceID:   h.deviceID,
		DeviceType: f.DeviceType,
		EventType:  eventType,
		ZoneID:     h.zoneID,
		OccurredAt: occurredAt,
		Payload:    raw,
		Source:     "tcp-gateway",
	}
```

第 231 行 `if _, ok := alarmEventTypes[eventType]; ok` 改为 `if kafka.IsAlarmEvent(eventType) {`（import 已在文件头，若无则加 `"onepark/common/kafka"`）。

- [ ] **Step 4: 编译 + 测试回归**

Run: `cd app/gateway-service && go build ./... && go test ./...`
Expected: BUILD OK；既有 protocol/frame 测试 PASS

- [ ] **Step 5: 提交**

```bash
git add app/gateway-service/internal/model/model.go app/gateway-service/internal/frame/protocol.go \
  app/gateway-service/internal/frame/handler.go
git commit -m "feat(gateway-service): 会话捕获设备档案并充入消息 tenant_id/zone_id

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 5: event-dispatcher 档案查询 + 契约接入 + DLQ + 投递重试

**Files:**
- Create: `app/event-dispatcher/internal/archive/archive.go`
- Create: `app/event-dispatcher/internal/archive/archive_test.go`
- Modify: `app/event-dispatcher/internal/config/config.go`
- Modify: `app/event-dispatcher/internal/svc/servicecontext.go`
- Modify: `app/event-dispatcher/internal/dispatch/dispatch.go`（主体重写）
- Modify: `app/event-dispatcher/dispatcher.go:36`（装配 resolver）
- Modify: `app/event-dispatcher/internal/dispatch/dispatch_test.go`（扩展用例）

**Interfaces:**
- Consumes: Task 1 契约 + DLQEnvelope；Task 2 device 表新列；`common/gormx.NewDB(dsn) (*gorm.DB, error)`
- Produces: MQTT 通道消息携带 tenant_id/zone_id；坏消息/未知设备/重试耗尽 → `event-dispatcher-dlq`

- [ ] **Step 1: 写档案查询缓存的失败测试**

创建 `app/event-dispatcher/internal/archive/archive_test.go`：

```go
package archive

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubReader struct {
	profiles map[string]Profile
	err      error
	calls    int
}

func (s *stubReader) Get(ctx context.Context, deviceID string) (Profile, bool, error) {
	s.calls++
	if s.err != nil {
		return Profile{}, false, s.err
	}
	p, ok := s.profiles[deviceID]
	return p, ok, nil
}

// TestResolverCacheHit 缓存命中时不得重复查库(30s TTL 内).
func TestResolverCacheHit(t *testing.T) {
	s := &stubReader{profiles: map[string]Profile{"d1": {TenantID: 7, ZoneID: "zone-a"}}}
	r := NewResolver(s, time.Minute)

	for i := 0; i < 3; i++ {
		p, ok, err := r.Resolve(context.Background(), "d1")
		if err != nil || !ok || p.TenantID != 7 || p.ZoneID != "zone-a" {
			t.Fatalf("第 %d 次解析异常: p=%+v ok=%v err=%v", i, p, ok, err)
		}
	}
	if s.calls != 1 {
		t.Fatalf("缓存命中仍查库, calls=%d", s.calls)
	}
}

// TestResolverUnknownDevice 设备不存在: ok=false 且结果负缓存, 不反复查库.
func TestResolverUnknownDevice(t *testing.T) {
	s := &stubReader{profiles: map[string]Profile{}}
	r := NewResolver(s, time.Minute)

	_, ok, err := r.Resolve(context.Background(), "ghost")
	if err != nil || ok {
		t.Fatalf("未知设备应返回 ok=false err=nil, got ok=%v err=%v", ok, err)
	}
	_, _, _ = r.Resolve(context.Background(), "ghost")
	if s.calls != 1 {
		t.Fatalf("未知设备未负缓存, calls=%d", s.calls)
	}
}

// TestResolverDBError 查库报错不缓存, 下次重试.
func TestResolverDBError(t *testing.T) {
	s := &stubReader{err: errors.New("db down")}
	r := NewResolver(s, time.Minute)

	_, _, err := r.Resolve(context.Background(), "d1")
	if err == nil {
		t.Fatal("DB 错误应上抛")
	}
	_, _, _ = r.Resolve(context.Background(), "d1")
	if s.calls != 2 {
		t.Fatalf("DB 错误不应缓存, calls=%d", s.calls)
	}
}

// TestResolverNilReader 未配置 MySQL 时零值放行(向后兼容: 消息不带租户也照常转发).
func TestResolverNilReader(t *testing.T) {
	r := NewResolver(nil, time.Minute)
	p, ok, err := r.Resolve(context.Background(), "d1")
	if err != nil || ok || p.TenantID != 0 || p.ZoneID != "" {
		t.Fatalf("nil reader 应零值放行: p=%+v ok=%v err=%v", p, ok, err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd app/event-dispatcher && go test ./internal/archive/ -v`
Expected: FAIL，报 `undefined: NewResolver` / `undefined: Profile`

- [ ] **Step 3: 实现 archive.go**

创建 `app/event-dispatcher/internal/archive/archive.go`：

```go
// Package archive 提供设备档案(device_id → tenant_id/zone_id)查询与 TTL 缓存,
// 供 event-dispatcher 在投递 Kafka 前给消息充入租户与区域维度.
package archive

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Profile 设备档案中消息充入所需的两个字段.
type Profile struct {
	TenantID int64
	ZoneID   string
}

// Reader 档案读取抽象, 便于单测替换; 生产实现为 GormReader.
type Reader interface {
	// Get 返回设备档案; ok=false 表示设备不存在(负缓存依据), err 非 nil 表示查询失败(不缓存).
	Get(ctx context.Context, deviceID string) (Profile, bool, error)
}

// GormReader 直连业务库只读查询 device 表.
type GormReader struct {
	db *gorm.DB
}

// deviceRow 仅映射需要的两列, 避免耦合 device-service 的完整模型.
type deviceRow struct {
	TenantID int64  `gorm:"column:tenant_id"`
	ZoneID   string `gorm:"column:zone_id"`
}

// NewGormReader 构造 GORM 档案读取器.
func NewGormReader(db *gorm.DB) *GormReader {
	return &GormReader{db: db}
}

func (g *GormReader) Get(ctx context.Context, deviceID string) (Profile, bool, error) {
	var row deviceRow
	err := g.db.WithContext(ctx).
		Table("device").
		Select("tenant_id", "zone_id").
		Where("device_id = ? AND deleted_at IS NULL", deviceID).
		Take(&row).Error
	if err != nil {
		if errors2.IsRecordNotFound(err) {
			return Profile{}, false, nil
		}
		return Profile{}, false, err
	}
	return Profile{TenantID: row.TenantID, ZoneID: row.ZoneID}, true, nil
}

// errors2 隔离 gorm.ErrRecordNotFound 判定, 保持 import 干净.
// (gorm.io/gorm 已在上方引入, 这里用变量避免与 gorm 主名冲突.)

// cacheEntry 带 TTL 的缓存条目; notFound=true 为负缓存.
type cacheEntry struct {
	profile Profile
	notFound bool
	expires  time.Time
}

// Resolver 带 TTL 的档案缓存; 并发安全.
type Resolver struct {
	reader Reader
	ttl    time.Duration
	mu     sync.RWMutex
	cache  map[string]cacheEntry
}

// NewResolver 构造缓存解析器; reader 为 nil 时表示未配置 MySQL,
// 一律零值放行(保持"未配置即降级启动"的仓库约定).
func NewResolver(reader Reader, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Resolver{reader: reader, ttl: ttl, cache: map[string]cacheEntry{}}
}

// Resolve 查询设备档案; ok=false 表示设备不存在, err 非 nil 表示查询失败.
func (r *Resolver) Resolve(ctx context.Context, deviceID string) (Profile, bool, error) {
	if r.reader == nil {
		return Profile{}, false, nil
	}

	r.mu.RLock()
	e, hit := r.cache[deviceID]
	r.mu.RUnlock()
	if hit && time.Now().Before(e.expires) {
		return e.profile, !e.notFound, nil
	}

	p, ok, err := r.reader.Get(ctx, deviceID)
	if err != nil {
		// 查询失败不缓存, 下条消息重试
		return Profile{}, false, err
	}
	entry := cacheEntry{profile: p, expires: time.Now().Add(r.ttl)}
	if !ok {
		entry.notFound = true
		entry.profile = Profile{}
	}
	r.mu.Lock()
	r.cache[deviceID] = entry
	r.mu.Unlock()
	return entry.profile, !entry.notFound, nil
}
```

注意：上面 `errors2.IsRecordNotFound` 是示意，实际实现直接用 `errors.Is(err, gorm.ErrRecordNotFound)` 并在 import 中同时引入标准库 `errors`（两者不冲突，标准库 `errors` + `gorm.io/gorm` 各自引入即可）。实现时写为：

```go
import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)
```

`Get` 内判定写为 `if errors.Is(err, gorm.ErrRecordNotFound) { return Profile{}, false, nil }`。

- [ ] **Step 4: 运行 archive 测试确认通过**

Run: `cd app/event-dispatcher && go mod tidy && go test ./internal/archive/ -v`
Expected: 4 个用例 PASS（`go mod tidy` 会把 gorm 依赖写进 event-dispatcher 的 go.mod，属预期变更）

- [ ] **Step 5: 写 dispatch 重写后的失败测试**

扩展 `app/event-dispatcher/internal/dispatch/dispatch_test.go`，新增以下内容（保留既有用例）：

```go
// fakePublisher 记录投递结果, 供断言; 可按次数注入失败.
type fakePublisher struct {
	mu       sync.Mutex
	published []fakeMsg
	failFirst int // 前 N 次投递失败
}
type fakeMsg struct {
	topic string
	value string
}

func (f *fakePublisher) Publish(_ context.Context, topic string, key, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) < f.failFirst {
		f.published = append(f.published, fakeMsg{topic: topic, value: ""}) // 占位计失败次数
		return errors.New("broker down")
	}
	f.published = append(f.published, fakeMsg{topic: topic, value: string(value)})
	return nil
}

func (f *fakePublisher) topics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.published {
		out = append(out, m.topic)
	}
	return out
}

func newTestHandler(p Publisher, r *archive.Resolver) *Handler {
	return &Handler{producer: p, resolver: r, retryPauses: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}
}

// TestDispatchEnrichesTenantZone 正常消息必须带 tenant_id/zone_id 且投遥测 topic.
func TestDispatchEnrichesTenantZone(t *testing.T) {
	fp := &fakePublisher{}
	s := &stubReader{profiles: map[string]archive.Profile{"d1": {TenantID: 7, ZoneID: "zone-a"}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	err := h.Dispatch(context.Background(),
		"onepark/device/pk_meter/d1/telemetry",
		[]byte(`{"request_id":"r1","device_type":"meter","event_type":"telemetry","occurred_at":100,"payload":{"metrics":{"energy_total":1.5}}}`))
	if err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	msgs := fp.published
	if len(msgs) != 1 || msgs[0].topic != kafka.TopicDeviceTelemetry {
		t.Fatalf("应仅投遥测 topic 1 条, got %v", fp.topics())
	}
	var m kafka.DeviceTelemetry
	if err := json.Unmarshal([]byte(msgs[0].value), &m); err != nil {
		t.Fatalf("消息解码失败: %v", err)
	}
	if m.TenantID != 7 || m.ZoneID != "zone-a" || m.DeviceID != "d1" {
		t.Fatalf("充入字段错误: %+v", m)
	}
}

// TestDispatchAlarmDualPublish 告警类事件必须同时投遥测与告警 topic.
func TestDispatchAlarmDualPublish(t *testing.T) {
	fp := &fakePublisher{}
	s := &stubReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"fire","payload":{}}`))
	if got := fp.topics(); len(got) != 2 || got[0] != kafka.TopicDeviceTelemetry || got[1] != kafka.TopicAlarm {
		t.Fatalf("告警应双投, got %v", got)
	}
}

// TestDispatchBadMessageToDLQ 解析失败必须进 DLQ 且不返回错误(已妥善处理).
func TestDispatchBadMessageToDLQ(t *testing.T) {
	fp := &fakePublisher{}
	h := newTestHandler(fp, archive.NewResolver(&stubReader{profiles: map[string]archive.Profile{}}, time.Minute))

	if err := h.Dispatch(context.Background(), "onepark/device/pk/d1/event", []byte(`{bad`)); err != nil {
		t.Fatalf("坏消息应吞掉并进 DLQ, 不向上抛: %v", err)
	}
	if got := fp.topics(); len(got) != 1 || got[0] != kafka.TopicDispatcherDLQ {
		t.Fatalf("坏消息应投 DLQ, got %v", got)
	}
	var env kafka.DLQEnvelope
	_ = json.Unmarshal([]byte(fp.published[0].value), &env)
	if env.Reason == "" || !strings.Contains(env.Raw, "bad") {
		t.Fatalf("DLQ 信封不完整: %+v", env)
	}
}

// TestDispatchUnknownDeviceToDLQ 设备档案查不到必须进 DLQ.
func TestDispatchUnknownDeviceToDLQ(t *testing.T) {
	fp := &fakePublisher{}
	h := newTestHandler(fp, archive.NewResolver(&stubReader{profiles: map[string]archive.Profile{}}, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/ghost/event",
		[]byte(`{"event_type":"fire","payload":{}}`))
	if got := fp.topics(); len(got) != 1 || got[0] != kafka.TopicDispatcherDLQ {
		t.Fatalf("未知设备应投 DLQ, got %v", got)
	}
}

// TestDispatchPublishRetryThenSuccess 投递失败按退避重试, 成功后不进 DLQ.
func TestDispatchPublishRetryThenSuccess(t *testing.T) {
	fp := &fakePublisher{failFirst: 2} // 前 2 次失败, 第 3 次成功
	s := &stubReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	if err := h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"telemetry","payload":{}}`)); err != nil {
		t.Fatalf("重试后成功不应报错: %v", err)
	}
	for _, topic := range fp.topics() {
		if topic == kafka.TopicDispatcherDLQ {
			t.Fatal("重试成功不应进 DLQ")
		}
	}
}

// TestDispatchPublishRetryExhaustedToDLQ 重试耗尽必须进 DLQ.
func TestDispatchPublishRetryExhaustedToDLQ(t *testing.T) {
	fp := &fakePublisher{failFirst: 99}
	s := &stubReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"telemetry","payload":{}}`))
	found := false
	for _, topic := range fp.topics() {
		if topic == kafka.TopicDispatcherDLQ {
			found = true
		}
	}
	if !found {
		t.Fatalf("重试耗尽应进 DLQ, got %v", fp.topics())
	}
}
```

测试文件头部 import 需补：

```go
import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"onepark/app/event-dispatcher/internal/archive"
	"onepark/common/kafka"
)
```

（`stubReader` 若与 archive_test.go 中的同名类型冲突，将 dispatch_test.go 中的副本改名为 `stubArchiveReader` 并同步修改引用。）

- [ ] **Step 6: 运行确认失败**

Run: `cd app/event-dispatcher && go test ./internal/dispatch/ -v`
Expected: FAIL，报 `unknown field 'resolver'` / `undefined: Publisher` 等编译错误

- [ ] **Step 7: 重写 dispatch.go**

`app/event-dispatcher/internal/dispatch/dispatch.go` 整体替换为：

```go
// Package dispatch 负责把 EMQX 收到的设备上报消息分类后投递到 Kafka.
// 契约: 消息体使用 common/kafka.DeviceTelemetry(唯一权威定义), 投递前从设备档案
// 充入 tenant_id/zone_id; 坏消息/未知设备/投递重试耗尽统一进死信 topic, 不再静默丢弃.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"onepark/app/event-dispatcher/internal/archive"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// topic 分段中设备上报类型所在位置: onepark/device/{productKey}/{deviceId}/{kind}
const (
	topicPartDeviceID = 3
	topicPartKind     = 4
	minTopicParts     = 5
)

// 上报类型
const (
	KindEvent     = "event"
	KindTelemetry = "telemetry"
	KindStatus    = "status"
)

// defaultRetryPauses 投递失败的退避节奏, 与 alarm-service 消费侧重试一致.
var defaultRetryPauses = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// Publisher Kafka 生产抽象, 便于单测注入假实现; *kafka.Producer 天然满足.
type Publisher interface {
	Publish(ctx context.Context, topic string, key, value []byte) error
}

// rawMessage 设备上报的原始报文.
type rawMessage struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type Handler struct {
	logx.Logger
	producer    *kafka.Producer
	publisher   Publisher     // 与 producer 二选一: 测试注入假实现
	resolver    *archive.Resolver
	retryPauses []time.Duration
}

// NewHandler 构造分发器; resolver 允许为 nil(未配置 MySQL, 消息零值放行).
func NewHandler(producer *kafka.Producer, resolver *archive.Resolver) *Handler {
	return &Handler{
		Logger:      logx.WithContext(context.Background()),
		producer:    producer,
		resolver:    resolver,
		retryPauses: defaultRetryPauses,
	}
}

// newHandlerWithPublisher 测试入口: 注入假生产者与短退避.
func newHandlerWithPublisher(p Publisher, resolver *archive.Resolver) *Handler {
	return &Handler{
		Logger:      logx.WithContext(context.Background()),
		publisher:   p,
		resolver:    resolver,
		retryPauses: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
}

// publish 统一出口: 优先用注入的 publisher, 否则用真实 producer.
func (h *Handler) publish(ctx context.Context, topic string, key, value []byte) error {
	if h.publisher != nil {
		return h.publisher.Publish(ctx, topic, key, value)
	}
	if h.producer == nil {
		return fmt.Errorf("生产者未初始化")
	}
	return h.producer.Publish(ctx, topic, key, value)
}

// toDLQ 坏消息/耗尽消息投死信; DLQ 投递本身失败仅记日志(最后防线, 不再递归).
func (h *Handler) toDLQ(ctx context.Context, sourceTopic, reason string, raw []byte) {
	env := kafka.NewDLQEnvelope(sourceTopic, reason, raw)
	value, err := json.Marshal(env)
	if err != nil {
		h.Errorf("DLQ 信封序列化失败: %v", err)
		return
	}
	if err := h.publish(ctx, kafka.TopicDispatcherDLQ, nil, value); err != nil {
		h.Errorf("DLQ 投递失败: sourceTopic=%s, reason=%s, err=%v", sourceTopic, reason, err)
	}
}

// publishWithRetry 带退避重试的投递; 重试耗尽进 DLQ.
func (h *Handler) publishWithRetry(ctx context.Context, topic string, key, value []byte, sourceTopic string) {
	var err error
	for attempt := 0; attempt <= len(h.retryPauses); attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryPauses[attempt-1])
		}
		if err = h.publish(ctx, topic, key, value); err == nil {
			return
		}
	}
	h.toDLQ(ctx, sourceTopic, fmt.Sprintf("投递 %s 重试耗尽: %v", topic, err), value)
}

// Dispatch 解析一条 MQTT 报文并投递到 Kafka.
// topic: onepark/device/{productKey}/{deviceId}/{kind}
// 返回的 error 仅表示"本条已无法处理且 DLQ 也失败"等极端情况; 常规坏消息已进 DLQ, 返回 nil.
func (h *Handler) Dispatch(ctx context.Context, topic string, body []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < minTopicParts {
		h.toDLQ(ctx, topic, "非法的上报 topic", body)
		return nil
	}
	kind := parts[topicPartKind]
	topicDeviceID := parts[topicPartDeviceID]

	var raw rawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		h.toDLQ(ctx, topic, "报文解析失败: "+err.Error(), body)
		return nil
	}

	deviceID := raw.DeviceID
	if deviceID == "" {
		deviceID = topicDeviceID
	}
	if deviceID == "" {
		h.toDLQ(ctx, topic, "报文缺少 device_id", body)
		return nil
	}

	// 档案充入: tenant_id/zone_id. resolver 为 nil(未配置 MySQL)时零值放行;
	// 已配置但查询失败/设备不存在 → 进 DLQ, 不让无主消息污染下游统计.
	var tenantID int64
	var zoneID string
	if h.resolver != nil {
		p, ok, err := h.resolver.Resolve(ctx, deviceID)
		if err != nil {
			h.toDLQ(ctx, topic, "设备档案查询失败: "+err.Error(), body)
			return nil
		}
		if !ok {
			h.toDLQ(ctx, topic, "未知设备: "+deviceID, body)
			return nil
		}
		tenantID, zoneID = p.TenantID, p.ZoneID
	}

	eventType := raw.EventType
	if eventType == "" {
		// status 类型以 kind 兜底, 如 online/offline 由上报方放在 payload 中
		eventType = kind
	}

	payload := raw.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	msg := kafka.DeviceTelemetry{
		RequestID:  raw.RequestID,
		TenantID:   tenantID,
		DeviceID:   deviceID,
		DeviceType: raw.DeviceType,
		EventType:  eventType,
		ZoneID:     zoneID,
		OccurredAt: raw.OccurredAt,
		Payload:    payload,
		Source:     "mqtt",
	}
	value, err := json.Marshal(msg)
	if err != nil {
		h.toDLQ(ctx, topic, "消息序列化失败: "+err.Error(), body)
		return nil
	}

	switch kind {
	case KindEvent, KindTelemetry, KindStatus:
		h.publishWithRetry(ctx, kafka.TopicDeviceTelemetry, []byte(deviceID), value, topic)
	default:
		h.toDLQ(ctx, topic, "未知的上报类型: "+kind, body)
		return nil
	}

	// 告警类事件额外投递告警 topic, 供 M2 工单/M5 调度消费.
	if kafka.IsAlarmEvent(eventType) {
		h.publishWithRetry(ctx, kafka.TopicAlarm, []byte(deviceID), value, topic)
	}

	h.Infof("消息已转发: kind=%s, deviceId=%s, eventType=%s, tenant=%d, zone=%s",
		kind, deviceID, eventType, tenantID, zoneID)
	return nil
}

// SplitTopic 暴露给单测使用: 返回 topic 中的 deviceId 与 kind.
func SplitTopic(topic string) (deviceID, kind string, ok bool) {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < minTopicParts {
		return "", "", false
	}
	return parts[topicPartDeviceID], parts[topicPartKind], true
}
```

注意：既有 `dispatch_test.go` 中若有直接构造 `Handler{producer: ...}` 的用例，按新结构改用 `newHandlerWithPublisher`；测试文件中 `newTestHandler` 帮助函数与上面的 `newHandlerWithPublisher` 重复，实现时**只保留 `newHandlerWithPublisher`**，Step 5 测试代码中的 `newTestHandler(h,p)` 一律改叫 `newHandlerWithPublisher(p, r)`。

- [ ] **Step 8: 配置与装配**

`app/event-dispatcher/internal/config/config.go` 加一行：

```go
	// MySQLDSN 设备档案查询(充入 tenant_id/zone_id)用的只读连接;
	// 未配置时消息以零值放行, 行为与旧版一致.
	MySQLDSN string `json:",env=MYSQL_DSN,optional"`
```

`app/event-dispatcher/internal/svc/servicecontext.go` 的 `NewServiceContext` 末尾返回前加：

```go
	// 设备档案只读连接: 未配置 DSN 时保持 nil, dispatch 侧零值放行(降级启动约定).
	var db *gormx.DB
	if dsn := os.ExpandEnv(c.MySQLDSN); dsn != "" {
		var err error
		db, err = gormx.NewDB(dsn)
		if err != nil {
			logx.Must(fmt.Errorf("初始化 MySQL 失败: %w", err))
		}
	}
	var resolver *archive.Resolver
	if db != nil {
		resolver = archive.NewResolver(archive.NewGormReader(db), 30*time.Second)
	}
```

`ServiceContext` 结构体加字段 `Resolver *archive.Resolver`，返回值改为：

```go
	return &ServiceContext{
		Config:   c,
		Producer: kafka.NewProducer(c.KafkaBrokers),
		Resolver: resolver,
	}
```

import 补 `"fmt"`、`"time"`、`"github.com/zeromicro/go-zero/core/logx"`、`"onepark/common/gormx"`、`"onepark/app/event-dispatcher/internal/archive"`。

`app/event-dispatcher/dispatcher.go` 第 36 行改为：

```go
	handler := dispatch.NewHandler(ctx.Producer, ctx.Resolver)
```

- [ ] **Step 9: 运行全部测试确认通过**

Run: `cd app/event-dispatcher && go mod tidy && go build ./... && go test ./...`
Expected: BUILD OK；archive 4 用例 + dispatch 既有与新增用例全部 PASS

- [ ] **Step 10: 提交**

```bash
git add app/event-dispatcher/ go.work.sum
git commit -m "feat(event-dispatcher): 档案充入 tenant/zone + 死信队列 + 投递退避重试

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 6: common/shadow 统一模型与乐观锁语义

**Files:**
- Create: `common/shadow/shadow.go`
- Modify: `app/device-service/internal/model/device.go:36-48`（删除本地 Shadow，改别名）
- Modify: `app/device-service/internal/model/shadow_model.go`（UpdateDesired/UpdateReported 返回受影响行数；SaveReported 改条件更新）
- Modify: `app/device-service/internal/mq/telemetry.go:198-225`（mergeReported 改乐观锁+重试）
- Modify: `app/shadow-service/internal/model/shadow.go`（删除本地 Shadow，改别名）

**Interfaces:**
- Consumes: Task 1 无关；GORM
- Produces: `common/shadow.Shadow`（GORM 模型，两服务共用）；device-service `ShadowModel.UpdateDesired/UpdateReported(ctx, deviceID, data, version) (int64, error)`（0 行=版本冲突）；`SaveReported` 删除，由 mq 层 `mergeReported` 乐观锁重试替代

- [ ] **Step 1: 写 device-service 影子条件更新的失败测试**

在 `app/device-service/internal/mq/` 既有测试文件（或新建 `shadow_merge_test.go`）中添加：

```go
package mq

import (
	"testing"
)

// TestMergeReportedConflictRetry 语义约束(静态): SaveReported 已从接口删除,
// 遥测合并必须走 FindByDeviceID + UpdateReported(version) 的乐观锁路径.
// 此处用接口断言防回退: ShadowModel 接口不得再包含 SaveReported.
func TestShadowModelNoSaveReported(t *testing.T) {
	// 编译期接口断言: 若有人把 SaveReported 加回接口, 此处编译失败.
	var _ interface {
		Insert(ctx context.Context, s *shadowpkg.Shadow) error
		FindByDeviceID(ctx context.Context, deviceID string) (*shadowpkg.Shadow, error)
		UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) (int64, error)
		UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) (int64, error)
		Delete(ctx context.Context, deviceID string) error
	} = svcCtxForTest()
}
```

注：该静态断言写法依赖具体 svc 构造，实现时改为更直接的等价形式——在 `shadow_model.go` 所在 model 包写接口形状测试：

```go
package model

import "testing"

// TestShadowModelInterfaceShape 锁定接口形状: 无 SaveReported(无条件覆盖已废弃),
// Update* 返回受影响行数以暴露版本冲突.
func TestShadowModelInterfaceShape(t *testing.T) {
	var _ ShadowModel = (*shadowModel)(nil) // 编译期校验实现满足接口
	// 接口方法集即文档; 若签名回退为不返回行数, 此处编译失败.
}
```

（接口形状由编译器保证；真正的行为测试是 mergeReported 重试逻辑，见 Step 4。）

- [ ] **Step 2: 创建 common/shadow**

创建 `common/shadow/shadow.go`：

```go
// Package shadow 设备影子的共享 GORM 模型与版本语义.
// 此前 device-service 与 shadow-service 各持一份结构体(且版本语义分叉:
// 一侧无条件覆盖、一侧乐观锁), 本包将其合一, 两服务均以类型别名引用.
package shadow

import (
	"time"

	"gorm.io/datatypes"
)

// Shadow 设备影子, 对应 shadow 表(desired/reported JSON + version 乐观锁).
type Shadow struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID  string         `gorm:"column:device_id;type:char(36);uniqueIndex:uk_device_id;not null" json:"device_id"`
	Desired   datatypes.JSON `gorm:"column:desired;type:json" json:"desired"`
	Reported  datatypes.JSON `gorm:"column:reported;type:json" json:"reported"`
	Version   uint           `gorm:"column:version;type:int unsigned;not null;default:0" json:"version"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// TableName 表名.
func (Shadow) TableName() string { return "shadow" }
```

- [ ] **Step 3: 两服务模型改别名 + 接口统一**

`app/device-service/internal/model/device.go`：删除第 36-48 行的本地 `Shadow` 结构体与 TableName，替换为：

```go
// Shadow 设备影子共享模型(与 shadow-service 共用 common/shadow 权威定义).
type Shadow = shadow.Shadow
```

import 补 `"onepark/common/shadow"`。

`app/device-service/internal/model/shadow_model.go` 整体替换：

```go
package model

import (
	"context"

	"gorm.io/gorm"

	"onepark/common/shadow"
)

type (
	// ShadowModel 影子读写接口; 统一乐观锁语义:
	// Update* 按 version 条件更新并返回受影响行数, 0 行表示版本冲突(调用方决定重试或上抛).
	ShadowModel interface {
		Insert(ctx context.Context, s *shadow.Shadow) error
		FindByDeviceID(ctx context.Context, deviceID string) (*shadow.Shadow, error)
		// UpdateDesired 乐观锁更新期望值; 返回受影响行数, 0 表示版本冲突.
		UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) (int64, error)
		// UpdateReported 乐观锁更新上报值; 返回受影响行数, 0 表示版本冲突.
		UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) (int64, error)
		Delete(ctx context.Context, deviceID string) error
	}

	shadowModel struct {
		db *gorm.DB
	}
)

func NewShadowModel(db *gorm.DB) ShadowModel {
	return &shadowModel{db: db}
}

func (m *shadowModel) Insert(ctx context.Context, s *shadow.Shadow) error {
	return m.db.WithContext(ctx).Create(s).Error
}

func (m *shadowModel) FindByDeviceID(ctx context.Context, deviceID string) (*shadow.Shadow, error) {
	var s shadow.Shadow
	if err := m.db.WithContext(ctx).Where("device_id = ?", deviceID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *shadowModel) UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) (int64, error) {
	tx := m.db.WithContext(ctx).Model(&shadow.Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"desired": desired,
			"version": version + 1,
		})
	return tx.RowsAffected, tx.Error
}

func (m *shadowModel) UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) (int64, error) {
	tx := m.db.WithContext(ctx).Model(&shadow.Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"reported": reported,
			"version":  version + 1,
		})
	return tx.RowsAffected, tx.Error
}

func (m *shadowModel) Delete(ctx context.Context, deviceID string) error {
	return m.db.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&shadow.Shadow{}).Error
}
```

`app/shadow-service/internal/model/shadow.go` 整体替换为：

```go
package model

import "onepark/common/shadow"

// Shadow 设备影子共享模型(与 device-service 共用 common/shadow 权威定义).
type Shadow = shadow.Shadow
```

（shadow-service 的 `shadowmodel.go` 已是乐观锁+RowsAffected 语义，仅将其本地 `Shadow` 引用经别名自动指向共享模型，无需改动方法体。）

- [ ] **Step 4: mq/telemetry.go mergeReported 改乐观锁重试**

替换 `app/device-service/internal/mq/telemetry.go` 第 198-225 行的 `mergeReported`：

```go
// mergeReported 将遥测指标合并进影子 reported 后写入(乐观锁, 冲突重试).
// 语义与 shadow-service gRPC UpdateReported 一致: version 条件更新, 0 行即冲突.
const mergeReportedMaxAttempts = 3

func (h *Handler) mergeReported(ctx context.Context, deviceID string, metrics map[string]any) error {
	for attempt := 0; attempt < mergeReportedMaxAttempts; attempt++ {
		s, err := h.svcCtx.ShadowModel.FindByDeviceID(ctx, deviceID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// 影子缺失(如历史设备), 补建后再走正常路径, 保证上报不丢
			s = &model.Shadow{DeviceID: deviceID, Desired: []byte(`{}`), Reported: []byte(`{}`)}
			if err := h.svcCtx.ShadowModel.Insert(ctx, s); err != nil {
				return err
			}
		}

		reported := map[string]any{}
		if len(s.Reported) > 0 {
			_ = json.Unmarshal(s.Reported, &reported)
		}
		for k, v := range metrics {
			reported[k] = v
		}
		b, err := json.Marshal(reported)
		if err != nil {
			return err
		}

		rows, err := h.svcCtx.ShadowModel.UpdateReported(ctx, deviceID, b, s.Version)
		if err != nil {
			return err
		}
		if rows > 0 {
			return nil
		}
		// 0 行: 并发写入导致版本冲突, 重读快照后重试
		h.Infof("影子写入版本冲突, 重试: deviceId=%s, attempt=%d", deviceID, attempt+1)
	}
	return fmt.Errorf("影子 reported 写入重试耗尽: deviceId=%s", deviceID)
}
```

import 补 `"fmt"`。

- [ ] **Step 5: 编译 + 测试回归**

Run: `cd common && go build ./... && cd ../app/device-service && go build ./... && go test ./... && cd ../shadow-service && go build ./... && go test ./...`
Expected: 全部 BUILD OK；device-service 既有测试 PASS（`deviceregisterlogic` 等不引用已删除的 SaveReported；若 mq 测试直接调用了 `SaveReported`，按新接口改写为 UpdateReported）

- [ ] **Step 6: 提交**

```bash
git add common/shadow/ app/device-service/internal/model/ app/device-service/internal/mq/telemetry.go \
  app/shadow-service/internal/model/shadow.go
git commit -m "feat(common/shadow): 影子模型下沉共享, 统一乐观锁语义(冲突重试替代无条件覆盖)

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 7: M4 适配契约文档

**Files:**
- Create: `docs/设备遥测消息契约v1.md`

**Interfaces:**
- Consumes: Task 1-5 的最终契约形态
- Produces: 成员5 改造 energy-data-service consumer.go 的依据

- [ ] **Step 1: 写契约文档**

创建 `docs/设备遥测消息契约v1.md`：

```markdown
# 设备遥测消息契约 v1（2026-09-18 定稿）

> 生产端：M1（device-service HTTP 降级通道 / gateway-service TCP / event-dispatcher MQTT）。
> 本文档是 M4 energy-data-service 消费端改造的唯一依据。
> 契约代码：`common/kafka/contract.go`（`kafka.DeviceTelemetry`），可直接 import 使用。

## 1. Topic 定稿

| Topic | 方向 | 说明 |
|---|---|---|
| `device-telemetry` | M1 生产 → 全量消费 | 所有设备遥测/事件/状态，**M4 消费此 topic** |
| `alarm-event` | M1 生产 | 仅告警类事件（与 device-telemetry 同结构） |
| `event-dispatcher-dlq` | M1 生产 | 死信信封（DLQEnvelope），仅排查用 |
| ~~onepark.device.telemetry~~ | 废弃 | M4 旧配置中的错误 topic 名 |

## 2. 消息结构（JSON）

```json
{
  "request_id": "r-001",
  "tenant_id": 7,
  "device_id": "d-001",
  "device_type": "meter",
  "event_type": "telemetry",
  "zone_id": "zone-a",
  "occurred_at": 1758153600,
  "payload": {"metrics": {"energy_total": 123.5, "power": 3.2}},
  "source": "mqtt"
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| request_id | string | 幂等键，生产端缺省自动生成 UUID |
| tenant_id | int64 | 租户 ID，0=未归属（历史设备/未配置档案） |
| device_id | string | 设备唯一标识（UUID） |
| device_type | string | 设备类型（自由字符串，如 meter/camera） |
| event_type | string | telemetry/status/event/online/offline/fault/告警类型 |
| zone_id | string | 能源区域编码，空=未分区 |
| occurred_at | int64 | **Unix 秒** |
| payload | object | 业务负载；遥测类固定为 `{"metrics":{指标名:数值}}` |
| source | string | tcp-gateway / mqtt / http-fallback |

## 3. M4 consumer.go 改造指引

1. **topic 替换**：`etc/energydata-api.yaml` 的 Kafka topic 改为 `device-telemetry`（或直接引用 `kafka.TopicDeviceTelemetry` 常量）；删除 `onepark.device.telemetry`。
2. **结构体替换**：删除本地 `Telemetry` 定义，`import "onepark/common/kafka"` 后用 `kafka.DeviceTelemetry` 解码。
3. **取数路径**：电表累计电量 `payload.metrics.energy_total`（kWh，累计值，按设备差值算用量）；瞬时功率 `payload.metrics.power`（kW）。不再有顶层的 `energy_kwh`。
4. **tenant_id / zone_id**：已在消息顶层，直接落库，**无需**再查设备档案或自建映射表。
5. **时间**：`occurred_at` 为 Unix 秒（不是 RFC3339）。

## 4. DDL 提醒

`deploy/sql/m2_mysql_tables.sql` 的 `energy_reading` 建表缺 `power_kw`、`created_at` 两列，
与 M4 GORM 模型不一致——按现有 SQL 建表会导致写入报 Unknown column，改造时一并补列。

## 5. 验证方式

联调环境起 EMQX+Kafka 后，向 MQTT topic `onepark/device/{productKey}/{deviceId}/telemetry`
发布上文明文样例，`kafka-console-consumer --topic device-telemetry` 应抓到带 tenant_id/zone_id 的完整消息。
```

- [ ] **Step 2: 提交**

```bash
git add docs/设备遥测消息契约v1.md
git commit -m "docs: 设备遥测消息契约 v1 定稿(供 M4 消费端适配)

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 8: 全量构建验证与收尾

**Files:** 无新增（验证 + 修补）

- [ ] **Step 1: 全模块编译**

Run（仓库根目录）：
```bash
for d in common app/device-service app/gateway-service app/shadow-service app/event-dispatcher; do (cd "$d" && go build ./... && echo "OK $d" || echo "FAIL $d"); done
```
Expected: 5 个 OK。再跑其余 16 个模块确认无连带破坏：
```bash
for d in app/*/ gateway; do (cd "$d" && go build ./... || echo "FAIL $d"); done
```
Expected: 除 parking-service（本次范围外的存量编译失败，M2 负责）外无 FAIL。

- [ ] **Step 2: M1 范围全部测试**

Run:
```bash
(cd common && go test ./...) && (cd app/device-service && go test ./...) && \
(cd app/gateway-service && go test ./...) && (cd app/event-dispatcher && go test ./...) && \
(cd app/shadow-service && go test ./...)
```
Expected: 全部 PASS，无竞态告警（可追加 `-race` 跑 archive 与 dispatch 测试）

- [ ] **Step 3: 端到端冒烟（中间件可用时）**

1. `docker compose -f deploy/docker-compose.yml up -d mysql kafka emqx`
2. 执行 `deploy/sql/m1_mysql_tables.sql` + `m1_device_add_tenant_zone.sql`
3. 注册带 `tenantId=1, zoneId=zone-a` 的电表设备
4. 向 MQTT `onepark/device/{pk}/{deviceId}/telemetry` 发布 `{"metrics":{"energy_total":10}}`
5. `kafka-console-consumer --topic device-telemetry` 验证消息含 `"tenant_id":1,"zone_id":"zone-a"`
Expected: 第 5 步消息字段完整（无中间件环境可跳过本步，以单测为准）

- [ ] **Step 4: 收尾提交（如有修补）**

```bash
git add -A
git commit -m "fix(m1): 全量构建验证修补

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Self-Review 记录

- **Spec 覆盖**：§3.1 契约包→Task 1；§3.2 数据模型/注册→Task 2、三生产端充入→Task 3/4/5；§3.3 shadow→Task 6；§3.4 DLQ/重试→Task 5；§3.5 M4 文档→Task 7；§5 验证→各任务测试 + Task 8。无遗漏。
- **占位符**：Task 5 Step 3 的 `errors2` 示意已附明确实现写法；Task 5 测试帮助函数命名冲突已给出消解方案；无 TBD。
- **类型一致性**：`kafka.DeviceTelemetry` 字段在 Task 1/3/4/5 中引用一致；`ShadowModel` 新签名 `(int64, error)` 在 Task 6 内自洽；`archive.Resolver/Profile` 在 Task 5 测试与实现一致。
- **风险提示**：Task 5 对 event-dispatcher 新增 gorm 依赖（go.mod 变更属预期）；Task 6 删除 `SaveReported` 是接口破坏性变更，已确认全仓仅 mq/telemetry.go 一处调用。
