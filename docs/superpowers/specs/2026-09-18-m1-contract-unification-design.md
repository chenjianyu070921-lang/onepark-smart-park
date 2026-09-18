# M1 契约统一与链路修复 设计文档

- 日期：2026-09-18
- 负责人：陈建羽（M1 物联接入底座）
- 状态：已确认（与组长逐项确认范围与方案）
- 范围：M1 及 common/（仅 M1 侧改动；M4 由成员5 依据本契约文档自行适配）

## 1. 背景与问题

全盘审计（2026-09-18）确认 M1 四个模块均为真实现，但存在四类问题：

1. **契约重复定义**：`Message`/`AlarmEvent`/告警 `event_type` 枚举在 event-dispatcher、device-service、shadow-service、gateway-service 四处重复定义，无编译期约束，已实际造成 M4 消费端按错误想象编写（`onepark.device.telemetry` vs 实际 `device-telemetry`，平铺字段 vs 实际 `payload.metrics` 嵌套），能源链路因此断裂。
2. **消息缺少租户/区域维度**：M1 消息不带 `tenant_id`/`zone_id`，M4 的区域计费与租户隔离无法实现（`energy_reading.tenant_id` 目前落 0）。
3. **shadow 双写入口语义分叉**：device-service 注册时在同一事务直写 shadow 表（无条件覆盖 + version+1），shadow-service gRPC 带乐观锁（冲突返回 success=false），且 shadow-service 零调用方。
4. **event-dispatcher 无死信队列**：坏消息仅记日志丢弃，Kafka 投递失败消息直接丢失。

## 2. 已确认的决策

| 决策点 | 结论 |
|---|---|
| 修复范围 | 契约统一 + 能源链路对齐 + shadow 孤岛治理 + event-dispatcher DLQ（不含 TDengine 结构化写入） |
| 改动边界 | 只改 M1 与 common/；M4 的 consumer.go 由成员5 按契约文档自行改造 |
| tenant_id/zone_id 补齐方式 | M1 生产时充入消息（契约自包含） |
| shadow 治理 | 统一语义 + 保留双入口（直写与 gRPC 走同一套共享模型与乐观锁语义） |
| 契约形态 | common/kafka 内 Go 结构体 + JSON 序列化（保持现有 topic 的 JSON 格式兼容） |
| topic 命名 | 现有名定稿：`device-telemetry`、`alarm-event`；M3/M4 侧别名（`onepark.device.telemetry`、`onepark.alarm.event`）标记废弃 |

## 3. 设计

### 3.1 统一契约包：common/kafka/contract.go

统一消息结构（`device-telemetry` 与 `alarm-event` 两个 topic 共用同一结构，与现有线上格式逐字段兼容——`alarm-event` 的实际载荷就是 Message 的再发布，workorder/dispatch 两个消费方均按此解析）：

```go
package kafka

// 设备遥测/事件统一契约
// topic: device-telemetry（全量）；alarm-event（仅 IsAlarmEvent 命中的告警类）
type DeviceTelemetry struct {
    RequestID  string          `json:"request_id"`
    TenantID   int64           `json:"tenant_id"`   // 新增：生产时充入（历史消息缺省为零值，向后兼容）
    DeviceID   string          `json:"device_id"`
    DeviceType string          `json:"device_type"`
    EventType  string          `json:"event_type"`
    ZoneID     string          `json:"zone_id"`     // 新增：生产时充入（能耗区域，未登记为空串）
    OccurredAt int64           `json:"occurred_at"` // Unix 秒（保持现有线上类型，不得改为 time.Time）
    Payload    json.RawMessage `json:"payload"`     // 业务负载，能耗类为 {"metrics":{"energy_total":..,"power":..}}
    Source     string          `json:"source"`      // tcp-gateway / mqtt / http
}

// event_type 枚举（现 device-service mq/telemetry.go 与 dispatch.go 各自维护的 online/offline/fault/status 等合一）
const (
    EventOnline   = "online"
    EventOffline  = "offline"
    EventFault    = "fault"
    EventStatus   = "status"
    EventTelemetry = "telemetry"
    EventGeneric  = "event"
)

// 告警类型枚举（原三处硬编码合一：dispatch.go / deviceeventlogic.go / gateway protocol.go）
const (
    AlarmIntrusion = "intrusion"
    AlarmFire      = "fire"
    AlarmSmoke     = "smoke"
    AlarmFault     = "fault"
    AlarmDoorForce = "door_force"
    AlarmOffline   = "offline_alert"
)

func IsAlarmEvent(eventType string) bool
```

注意：不引入 `ProductKey` 字段（event-dispatcher 虽从 MQTT topic 解析 productKey，但现行 Message 不携带，保持不变）。

改造点：
- event-dispatcher `internal/dispatch/dispatch.go`（Message 结构体 + 告警枚举）→ 引用契约；
- device-service `internal/mq/telemetry.go`（Message 结构体 + 告警枚举）→ 引用契约；
- device-service `internal/logic/deviceeventlogic.go`（告警枚举）→ 引用契约；
- gateway-service `internal/frame/protocol.go`（告警枚举）→ 引用契约；
- shadow-service `internal/model/shadow.go`（重复 Shadow 结构体）→ 引用 common/shadow（见 3.3）。

序列化保持 JSON。现有消费方（alarm/video/parking 等）按字段名取值，顶层字段不变则无感；新增字段对旧消费方是 JSON 多余字段，天然兼容。

### 3.2 生产时充入 tenant_id / zone_id

**数据模型**：
- 迁移 SQL `deploy/sql/m1_device_add_zone.sql`：device 表加 `zone_id VARCHAR(64) NOT NULL DEFAULT ''` + 索引（风格对齐 `m1_device_add_type_location.sql`）；
- GORM `Device` 模型（device-service `internal/model/device.go`）加 `ZoneID` 字段；`m1_mysql_tables.sql` 全量脚本同步补列；
- 设备注册接口：`zone_id` 可选入参，落库。

**三个生产端充入**：
| 生产端 | 充入方式 |
|---|---|
| device-service（HTTP 事件降级通道） | deviceeventlogic 已查过设备，直接填 TenantID/ZoneID |
| gateway-service（TCP 接入） | 认证时已查 Device 表做 bcrypt 校验，将 tenant_id/zone_id 存入会话对象，telemetry/event 帧上报时复用，不重复查库 |
| event-dispatcher（MQTT 接入） | 新增只读 MySQL 依赖（复用现有 `${MYSQL_DSN}` 环境变量），按 device_id 查档案；加 30 秒进程内 TTL 缓存（模式对齐 alarm-service 规则引擎快照）；查不到设备 → 消息进 DLQ |

### 3.3 shadow 统一语义（双入口保留）

- 新增 `common/shadow` 包：
  - 共享 GORM 模型 `Shadow`（表名、列、JSON 序列化与现状一致）；
  - 共享版本语义常量与条件更新 SQL 片段（version 匹配才更新，version+1；不匹配 → 零行受影响，调用方据此返回 success=false）。
- device-service `internal/model/shadow_model.go` 的 `SaveReported` 从"无条件覆盖 + version+1"改为**乐观锁条件更新**，与 shadow-service gRPC `UpdateDesired/UpdateReported` 语义完全一致；
- 注册时直写（version=0 初始）保留在同一事务，不受影响；
- shadow-service gRPC 继续作为对外读写入口；今日不引入新调用方（不在本次范围）。

### 3.4 event-dispatcher 死信队列与重试

- `common/kafka/topics.go` 新增 `TopicDispatcherDLQ = "event-dispatcher-dlq"`；
- 死信信封结构（同包定义）：
  ```go
  type DLQEnvelope struct {
      SourceTopic string    `json:"source_topic"` // MQTT topic 或目标 Kafka topic
      Reason      string    `json:"reason"`       // 解析失败/未知设备/投递重试耗尽
      Raw         string    `json:"raw"`          // 原始报文（截断至 4KB）
      FailedAt    time.Time `json:"failed_at"`
  }
  ```
- 坏消息（JSON 解析失败、必填字段缺失、未知设备）→ 投 DLQ，不再静默丢弃；
- Kafka 生产失败 → 3 次退避重试（100ms / 500ms / 2s，节奏与 alarm-service 一致），仍失败 → DLQ + 错误日志；
- DLQ 投递本身失败 → 仅记错误日志（最后防线，不再无限递归）。

### 3.5 交付给成员5（M4）的适配文档

产出 `docs/设备遥测消息契约v1.md`，内容：
1. topic 定稿表（`device-telemetry` / `alarm-event` / `event-dispatcher-dlq`）；
2. 字段表 + 各 event_type 真实 JSON 样例（联调时从 topic 实抓）；
3. M4 consumer.go 改造指引：topic 常量替换、`import "onepark/common/kafka"` 直接使用 `DeviceTelemetry`、能耗取数路径 `payload.metrics.energy_total`（累计电量）与 `payload.metrics.power`（瞬时功率）、`tenant_id`/`zone_id` 已在消息顶层无需自查档案；
4. 提醒：`energy_reading` 建表补 `power_kw`/`created_at` 列、tenant_id 回填逻辑在 M4 侧完成。

## 4. 错误处理

- 未知设备消息 → DLQ（可追溯），不静默丢弃；
- event-dispatcher 的 DB 短暂不可用 → TTL 缓存兜底；缓存也失效 → 消息进 DLQ（reason=DBUnavailable），服务不崩溃、不丢消息；
- gateway-service 会话充入失败（设备被删除后仍在线）→ 该帧消息按未知设备处理，拒绝投递并断开重认证。

## 5. 测试

- contract 包：JSON 序列化/反序列化往返单测（含旧格式消息向后兼容用例——无 tenant_id/zone_id 的历史消息可正常解码为零值）；
- event-dispatcher：坏消息入 DLQ、未知设备入 DLQ、生产失败重试后成功、重试耗尽入 DLQ 四用例（扩展现有 dispatch_test.go）；
- device-service：SaveReported 乐观锁冲突返回不覆盖（扩展现有 logic 单测）；注册带 zone_id 落库用例；
- 验收：`go build` 21 模块全绿、M1 全部单测通过、手工经 EMQX 投一条 MQTT 遥测，`kafka-console-consumer` 抓到带 tenant_id/zone_id 的完整消息。

## 6. 明确不做（YAGNI）

- 不改 Kafka 消息为 protobuf 序列化；
- 不改 topic 名（避免波及 alarm/video/parking 等现有消费方）；
- 不把 device-service 影子写入改为调 shadow-service gRPC（保留注册事务原子性）；
- 不做 TDengine meter_elec/meter_water 结构化写入（排期 9.21）；
- 不做 CoAP 接入；
- 不改 M4 代码（成员5 按文档自行适配）。
