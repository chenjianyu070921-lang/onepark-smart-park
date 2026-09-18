# gateway-service CoAP 协议接入方案与排期说明

> 负责人: 陈建羽(M1) · 日期: 2026-09-17 · 优先级: P1(本期排期, 非本期交付)
>
> 关联: `app/gateway-service`(TCP 长连接网关 :7000)、README 架构说明、
> `docs/服务协议规范-HTTP与gRPC.md` 中 gateway-service 的 TCP/CoAP 承诺。

## 1. 背景与现状

`gateway-service` 是 M1 面向设备的接入网关(注意与根目录 `gateway/` HTTP 对外入口区分)。
当前已落地 **TCP 长连接 + 按行分隔 JSON 帧** 一条接入链路:

```
设备建连 → auth 帧(bcrypt 校验 device_secret) → online 回写
        → telemetry / event / status 帧投递 Kafka(device-telemetry, 告警类追加 alarm-event)
        → ack 帧经 CommandLogModel.Finish 回写 command_log
```

- 代码: `internal/frame/protocol.go`(帧定义)、`internal/frame/handler.go`(会话/认证/投递/回执)。
- 设备密钥校验、在线状态回写、指令回执(CommandLogModel)均已在 b341f95 落地,
  CoAP 接入可直接复用, 不需要新建模型层。
- README 与协议规范对外承诺的 **CoAP(UDP)接入尚未实现**, 目前代码中仅有
  `config.go` / `gateway.go` 两处"后续引入 plgd-dev/go-coap"的注释。

## 2. 为什么要做 / 不做什么

**要解决的问题**: 门禁地磁等弱设备在 NAT 穿透、省电、固件栈受限场景下,
TCP 长连接保活成本高; CoAP(UDP, 二进制, 基于消息事务)更贴合这类设备。

**一期非目标(明确不做)**:

- 不替换 TCP 链路, TCP 与 CoAP 长期并存;
- 一期不做 DTLS(走园区内网/专线 + 短时 token, DTLS-PSK 放二期);
- CoAP 不承担服务端指令下行推送(UDP 无连接, 推送语义别扭),
  下行仍走 MQTT / TCP; CoAP-only 设备的下行(长轮询)在二期评估。

## 3. 技术方案

### 3.1 技术选型

- 库: `github.com/plgd-dev/go-coap/v3`(代码注释中已预留该选型), 支持 UDP/TCP/DTLS。
- 端口: UDP `5683`(明文 + 内网); 二期 DTLS `5684`。
- 部署: docker-compose 网关容器暴露 UDP 5683; 与 TCP :7000 同一进程监听。

### 3.2 资源模型(与 TCP 帧一一对应)

| CoAP | TCP 帧 | 说明 |
|---|---|---|
| `POST /auth` body=`{device_id, secret}` | auth | 换取短时 token, 校验逻辑复用现有 bcrypt |
| `POST /devices/{deviceId}/telemetry` | telemetry | body 为 metrics JSON |
| `POST /devices/{deviceId}/events` | event | body 携带 event_type + payload |
| `POST /devices/{deviceId}/status` | status | 上线/下线状态 |
| `POST /devices/{deviceId}/acks` | ack | 指令回执, 复用 `CommandLogModel.Finish` |

投递到 Kafka 的消息体**继续复用统一的 7 字段结构**
(`request_id/device_id/device_type/event_type/occurred_at/payload/source`),
`source` 取 `"coap-gateway"`, 与 `mqtt` / `tcp-gateway` / `http-fallback` 并列 ——
下游 device-service / dispatch-service / alarm-service 无需任何改动。

### 3.3 认证与在线状态(CoAP 无连接, 与 TCP 的差异点)

1. 设备先 `POST /auth`, 复用 `DeviceModel.FindByDeviceID` + bcrypt 校验;
2. 通过后签发随机 token, 存 Redis: `m1:coap:token:{token} → device_id`, TTL 与 TCP 读超时对齐(默认 120s);
3. 后续请求带 URI-Query `?token=xxx`; 每次上报滑动续期, 等价于 TCP 的心跳保活;
4. 在线状态: 上报即 `UpdateOnline(online)`; 超过 2×TTL 无报文由设备侧超时任务置离线
   (复用现有超时扫描节奏, 不新增常驻任务)。

### 3.4 QoS 与幂等

- 遥测/高频状态允许 `NON`(不要求 CoAP 层重传); 事件/告警/回执要求 `CON`(可观测到投递结果);
- 业务层幂等仍由 `request_id` 保证(HTTP 降级通道已有同口径实现), 不依赖 CoAP 消息 ID。

### 3.5 重构前置(动手前先做)

把 `frame/handler.go` 中与传输无关的三段逻辑抽成内部接口, TCP / CoAP 共用:

```
IngestService:
  Authenticate(deviceID, secret) (deviceID, error)
  Publish(deviceID, eventType, payload, source) error
  FinishCommand(requestID, status, payload) error
```

避免在 CoAP handler 里复制 bcrypt 校验、alarmEventTypes 分支与回执落库逻辑。

## 4. 排期说明

当前迭代(2026-09-17)优先级 P0 为 device 表补列、消息契约核对、gRPC↔dashboard 联调,
**CoAP 接入排入下一迭代 M1.1**, 不影响本期里程碑:

| 阶段 | 内容 | 粗估工时 |
|---|---|---|
| M1.1-a | 抽 IngestService, TCP handler 迁移复用(行为不变) | 0.5d |
| M1.1-b | go-coap UDP server、资源路由、token 签发/校验 | 1.5d |
| M1.1-c | 联调(真实模组或 libcoap 客户端)、docker-compose UDP 端口、监控日志 | 0.5d |
| M1.2(评估) | DTLS-PSK(5684)、CoAP-only 设备下行长轮询 `GET /commands` | 另排 |

排期约束: b 依赖 device-service Redis 实例可被网关访问(现配置已具备);
若 M2/M3 出现必须走 CoAP 的设备型号, 可将 M1.1 提前到当前迭代后半段。
