# M3 — Kafka 消费组 + RequestId 幂等去重 + 死信队列 设计（v1.0，待评审）

> 2026-09-14 · 适用 alarm-service · 实现日：Day 6（依赖 Day 3-5 骨架与规则引擎）
> 依赖：《docs/m3/01-M3技术方案.md》§4、P0-2（M1 事件结构）

---

## 1. 客户端选型（已实证，非凭记忆）

| 候选 | 实证结论 | 结论 |
|---|---|---|
| go-zero 内置 `core/mq/kq` | ❌ **go-zero v1.10.3 的 `core/` 下无 `mq` 包**（已列目录确认），该封装已移除 | 排除 |
| `github.com/IBM/sarama` | ✅ **v1.43.1 已在 module graph**（go-zero 间接依赖），module cache 已存在；`ConsumerGroup` 接口完整（Consume/Setup/Cleanup/ConsumeClaim） | **推荐** |
| `segmentio/kafka-go` | 不在本地依赖树，需新下载；API 简洁、纯 Go | 备选 |

**决策：sarama v1.43.1**（零新增下载、事实标准、消费组能力完整）。
**风险**：当前为 indirect，需 `go get github.com/IBM/sarama@v1.43.1` 提升为 direct；若 GOPROXY 受限则改用已缓存版本——**Day 6 首动作必须先跑通 `go build`**。

> 修正：昨日草案中「go-zero `kq` vs kafka-go」的选型前提已证伪（go-zero 无 kq）。

---

## 2. Topic 与消费组

| 项 | 值 | 说明 |
|---|---|---|
| 消费 topic | `device-telemetry` | M1 生产 → M3 消费（2026-09-18 M1 契约定稿，`common/kafka/topics.go` TopicDeviceTelemetry；本文初稿的 `onepark.device.event` 已废弃） |
| 消费组 | `alarm-service` | 仅 alarm-service 使用（access/video 不消费 Kafka） |
| 分区数 | 6（单 broker，`--replication-factor 1`） | 分区数 = 最大消费并发度 |
| 生产 topic | `onepark.alarm.event` | M3 → M5（告警确认/解决） |
| DLQ | MySQL `alarm_dlq` 表（见 §5） | broker 侧不建 DLQ topic |
| 分区键 | `deviceId` | 同设备事件保序 |
| 初始 offset | `FirstOffset` | 首次启动不回放历史；需回放改 `OffsetOldest` |
| 提交方式 | **手动提交**（处理成功才 Commit） | 处理成功才提交 → at-least-once |

**语义**：Kafka 侧 at-least-once + 消费端幂等（§4）= 端到端有效 exactly-once。

### 2.1 并发模型

- sarama 每个 partition 起一个 `ConsumeClaim` goroutine，**分区内串行**（保序 + offset 简单）
- 并发度 = 分区数（6）；扩容靠加分区，**不在分区内引入 worker pool**（会破坏 offset 提交顺序）

### 2.2 关键配置（`sarama.NewConfig()`）

| 配置 | 值 | 理由 |
|---|---|---|
| `Consumer.Offsets.AutoCommit.Enable` | **false**（手动提交） | 处理成功才提交 |
| `Consumer.Return.Errors` | true | 监听 `Errors()` 通道并落日志 |
| `Consumer.Group.Session.Timeout` | 10s | 与 rebalance 配合 |
| `Consumer.Group.Heartbeat.Interval` | 3s | |
| `Consumer.Group.Rebalance.Timeout` | 60s（默认） | **重试退避总时长须 < 此值** |
| `Producer.RequiredAcks` | `WaitForAll` | 生产侧不丢 |
| `Producer.Idempotent` | true（配 `Net.MaxOpenRequests=1`） | Kafka 3.x 幂等生产者 |

### 2.3 生命周期

用 `service.ServiceGroup` 把消费者与 REST server 一起托管：停服时先退出 `Consume` 循环再 `Close()` consumer group，避免 rebalance 风暴。

---

## 3. 消费主流程

```
ConsumeClaim(msg)
  ├─ 解析 DeviceEvent                失败 → 不可重试 → DLQ
  ├─【L1】requestId 幂等检查          命中 → MarkMessage + 跳过（不算失败）
  ├─ 规则引擎 Evaluate（见文档 07）
  ├─【L2】业务冷却检查                命中 → 抑制，不生成告警
  ├─ 告警入库【L3 唯一索引兜底】+ ES 双写 + WS 推送
  ├─ MarkMessage + Commit
  └─ 可重试错误 → 本地退避重试 3 次 → 仍失败 → DLQ → MarkMessage（防毒丸卡分区）
```

**毒丸消息（poison pill）**：进 DLQ 后**仍提交 offset**，否则单条坏消息卡死整个分区。

---

## 4. 幂等去重（三层）

| 层 | 目标 | 实现 | TTL |
|---|---|---|---|
| **L1 消息级** | 同一条消息只处理一次（重投/rebalance 重放） | `SetnxEx("alarm:dedup:{requestId}", "1", 86400)`；返回 false = 已处理，跳过 | 24h |
| **L2 业务级** | 抑制设备抖动重复告警（不同 requestId，同一物理事件） | `SetnxEx("alarm:cooldown:{deviceId}:{eventType}:{ruleId}", "1", 60)` | 60s（按规则可配，P1-3） |
| **L3 存储级** | Redis 失效/过期时的最终防线 | MySQL `alarm` 表唯一索引 `uk_request_id(request_id)`，冲突忽略 | 永久 |

> L1 防「同一消息重复处理」，L2 防「同一现象重复告警」——**目的不同，不可互相替代**。

### 4.1 RequestId 来源（⚠️ P0-2 阻塞）

- **首选**：消息体 `request_id`（需 M1 在 `DeviceEvent` 补齐）
- **降级**：缺失时用 `sha1(deviceId|eventType|eventTime|payload)` 作指纹，打 WARN 日志 + 计数指标（推动 P0-2 闭环）
- **禁止**用 `{partition}-{offset}` 作幂等键：重放时 offset 变化 → 重复处理

### 4.2 顺序

L1 先于业务处理（最廉价）；L2 在规则命中后、写库前（避免无谓写库）。

---

## 5. 死信队列（DLQ）

### 5.1 错误分类

| 类型 | 例子 | 处理 |
|---|---|---|
| **可重试** | Redis/MySQL 超时、ES 写失败、网络抖动 | 本地退避重试 3 次（100ms / 500ms / 2s，总计 < 3s ≪ rebalance timeout 60s） |
| **不可重试** | JSON 反序列化失败、必填字段缺失、未知 eventType | 直接进 DLQ |

### 5.2 落点方案（推荐 A+B）

| 方案 | 优点 | 缺点 |
|---|---|---|
| A. Kafka `onepark.device.event.dlq` | 标准做法、可被重放工具统一消费 | 难查询、难按条件重放 |
| B. MySQL `alarm_dlq` 台账表 | 可查询、可界面重放、可统计 | 多一条写路径 |
| C. 仅日志 | 零成本 | **不可重放，不推荐** |

**推荐 A+B**：Kafka topic 作传输/重放通道，MySQL 台账作可观测与运营入口。
**Day 6 若只做一件 → 先做 B**（M3 有管理后台，界面重放最实用），A 同批或次日补。

### 5.3 `alarm_dlq` 表结构（草案）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| topic | varchar(64) | 源 topic |
| partition_no | int | |
| msg_offset | bigint | 定位原始消息 |
| request_id | varchar(64) | 索引，关联幂等键 |
| device_id | varchar(64) | 索引，便于按设备排查 |
| event_type | varchar(64) | |
| payload | json | 原始报文 |
| error_msg | varchar(512) | 失败原因 |
| retry_count | int | 已重试次数 |
| status | tinyint | 0-待处理 / 1-已重放 / 2-已丢弃 |
| created_at / updated_at | datetime | |

索引：`idx_status_created(status, created_at)`、`idx_device(device_id)`。

### 5.4 重放

- 后台接口 `POST /api/alarm/dlq/:id/replay`：读取台账 → 重新投递主 topic（或直接同步调用处理函数）
- 重放走同一 L1 幂等键 → 重复重放安全
- 成功重放后 status=1；人工判定无效数据置 2

---

## 6. 生产侧：告警事件外发（M3 → M5）

- Topic `onepark.alarm.event`，key = `alarmId`（或 deviceId），`SyncProducer` + `WaitForAll` + 幂等生产者
- **Outbox 兜底（推荐）**：告警状态变更与事件外发不在同一事务（跨 MySQL/Kafka）→ 先写 `alarm_event_outbox(status=0)`，再由定时补偿任务发送，成功后置 1。避免「告警已解决但 M5 没收到」
- 至少一次投递，M5 侧需按 `alarmId + action` 幂等消费

---

## 7. 可观测（Day 6 一并埋点）

| 指标 | 用途 |
|---|---|
| 消费 lag（sarama `Consumer.Group` + `GetOffset`） | 积压告警 |
| DLQ 写入速率 / 累计未处理数 | 死信监控 |
| L1 命中率、L2 抑制率 | 去重效果与规则合理性 |
| 单条处理耗时 P99 | 容量规划 |

---

## 8. 待确认决策（确认后进入实现）

| # | 决策点 | 推荐 | 需确认 |
|---|---|---|---|
| D1 | Kafka 客户端 | sarama v1.43.1 | M3 负责人（若无网络则改 kafka-go） |
| D2 | DLQ 落点 | A+B（先 B） | M3 负责人 |
| D3 | 分区数 | 6 | 架构（单 broker 环境够用） |
| D4 | `request_id` 字段 | 由 M1 补 | **M1 负责人（P0-2）** |
| D5 | 冷却默认 60s | 保持 60s，按规则可配 | 产品（P1-3） |
