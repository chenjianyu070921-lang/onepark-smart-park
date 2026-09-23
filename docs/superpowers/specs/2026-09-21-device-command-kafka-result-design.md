# device-service 指令下发 Kafka 回执与重试设计

日期：2026-09-21
状态：已获用户批准

## 目标

修改 device-service 指令下发链路：

1. EMQX 下行命令具备明确的超时控制。
2. 指令执行结果通过 Kafka 回传并确认。
3. 超时与设备明确返回 failed 均自动重试。
4. 重试耗尽后进入稳定终态，避免指令悬挂或重复执行。

## 非目标

- 不改变现有设备认证与遥测上报协议。
- 不引入新的 MQ 中间件，继续使用 Kafka。
- 不实现人工重放界面；后续可通过运维工具或管理接口补充。

## 总体链路

```text
device-service
  -> command_log 落库
  -> MQTT publish onepark/cmd/{deviceId}/down
  -> 等待设备执行结果

设备/EMQX:
  onepark/device/{productKey}/{deviceId}/cmd-result
  -> event-dispatcher
  -> Kafka topic device-command-result

TCP 设备:
  gateway-service ack 帧
  -> Kafka topic device-command-result

device-service:
  Kafka consumer(device-command-result)
  -> 幂等更新 command_log
  -> success 终态 / failed 触发重试或终态
```

## Kafka 契约

新增权威契约 `common/kafka.CommandResult`：

```go
type CommandResult struct {
    RequestID  string          `json:"request_id"`
    DeviceID   string          `json:"device_id"`
    Status     string          `json:"status"` // success / failed
    Response   json.RawMessage `json:"response"`
    OccurredAt int64           `json:"occurred_at"`
    Source     string          `json:"source"` // mqtt / tcp-gateway
}
```

新增常量：

```go
TopicDeviceCommandResult = "device-command-result"
GroupDeviceCommandResult = "device-service-command-result"
```

Kafka key 使用 `device_id`，保证同一设备回执的分区顺序。

## 生产端设计

### EMQX 回执

设备发布到：

```text
onepark/device/{productKey}/{deviceId}/cmd-result
```

`event-dispatcher` 在现有 `onepark/device/#` 订阅中识别 `kind=cmd-result`，解析设备回执并转换为 `CommandResult` 后投递 `device-command-result`。未知设备、解析失败、投递重试耗尽沿用现有 DLQ 策略。

### TCP ACK

`gateway-service` 收到 `ack` 帧后不再直接写 MySQL `command_log`，改为构造 `CommandResult` 并发布到 `device-command-result`。网关侧移除指令回执直写 DB 的依赖路径。

## command_log 状态与重试模型

现有状态：

```text
0 pending    待发送
1 sent       已下发/等待回执
2 success    执行成功
3 failed     执行失败
4 timeout    超时
```

不新增临时状态。`sent` 同时表示“已下发，等待回执”，也可表示“已收到 failed，等待后台重发”。

### success 回执

仅当当前状态为 `pending` 或 `sent` 时允许更新：

```text
status=success
response=回执内容
executed_at=回执时间
```

已进入 `success/failed/timeout` 的记录不会被重复回执覆盖。

### failed 回执

- 未达到最大重试次数：
  - 保存失败 response；
  - 状态保持 `sent`；
  - `executed_at` 写入回执时间；
  - `timeout_at` 提前到当前时间，让重试任务尽快处理。
- 已达到最大重试次数：
  - `status=failed`；
  - 保存失败 response 与 `executed_at`。
- 后续重试成功时，允许从 `sent` 推进为 `success`。

### 超时

定时任务继续扫描：

```text
status IN (pending, sent)
AND timeout_at < NOW()
```

若 `retry_count < CommandMaxResend`，执行 MQTT 重发；否则：

- 未收到明确 failed：置 `timeout`；
- 已收到 failed 且重试耗尽：置 `failed`。

为区分“未回执超时”和“明确失败终态”，失败回执会保存 response；重试耗尽时若 response 表示设备失败，则终态为 `failed`，否则为 `timeout`。

## retry_count 持久化

现有 Redis 计数仅作为临时实现，存在丢 key 后重复重发风险。新增数据库字段：

```sql
ALTER TABLE command_log
ADD COLUMN retry_count INT NOT NULL DEFAULT 0;
```

`retry_count` 表示已经重发次数，不含首次下发。

重发前使用条件更新原子抢占：

```sql
UPDATE command_log
SET retry_count = retry_count + 1,
    status = sent,
    timeout_at = NOW() + CommandTimeoutSec
WHERE request_id = ?
  AND status IN (pending, sent)
  AND retry_count < CommandMaxResend;
```

只有更新成功后才执行 MQTT publish。多个 device-service 实例并发扫描时，同一指令同一次重试只会被一个实例抢占。

## MQTT 下行失败

首次或后续 MQTT publish 失败时不直接置终态：

1. API 对外返回“下发失败”；
2. `command_log` 保存错误 response；
3. `timeout_at` 设置为当前时间；
4. 后台任务在 EMQX 恢复后继续重试，直到达到上限。

## 消费幂等与安全

device-service 回执 consumer：

- JSON 解析失败：记录日志并提交位移，避免毒消息阻塞分区；
- 缺少 `request_id/device_id/status`：记录日志并提交位移；
- 未知 `request_id`：记录日志并提交位移；
- `device_id` 与 command_log 不匹配：记录日志并提交位移；
- 非法 status：记录日志并提交位移；
- DB 更新失败：返回 error，由 Kafka consumer 按既有退避策略重试。

## 配置

沿用现有配置：

```yaml
CommandTimeoutSec: 30
TimeoutScanIntervalSec: 15
TimeoutScanBatchLimit: 200
CommandMaxResend: 2
```

`CommandMaxResend=0` 表示不重发；负数按默认 2 处理。

## 测试计划

1. success 回执只允许覆盖 pending/sent，不覆盖终态。
2. failed 回执未达上限时保存 response、提前 timeout_at，并保持 sent。
3. failed 回执达到上限后置 failed。
4. 超时任务多实例并发时，同一指令同一次重试只被一个实例抢占。
5. MQTT publish 失败后不直接终态，后台可继续重试。
6. event-dispatcher 正确识别 cmd-result 并投递新 topic。
7. TCP ACK 改为 Kafka 后不再直写 DB。
8. 非法回执、未知 request_id、device_id 不匹配时跳过且不阻塞消费。
9. 全量运行 device-service、event-dispatcher、gateway-service、common 相关 Go 测试。

## 风险与权衡

- `command_log.status=sent` 在失败等待重试期间不是纯“已发送”语义，但通过 response、retry_count、timeout_at 能完整表达状态，避免扩大状态枚举影响 API 兼容性。
- Kafka 分区顺序只保证同设备内顺序，不同设备之间无需顺序。
- 数据库字段需要迁移；部署时必须先执行 SQL 再发布新代码。
- event-dispatcher 与 gateway-service 同时作为生产端，契约统一下沉 common/kafka，避免双端结构漂移。
