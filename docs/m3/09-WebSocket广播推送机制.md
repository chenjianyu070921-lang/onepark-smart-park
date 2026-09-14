# M3 — WebSocket 告警广播推送机制（定案 v1.0）

> 2026-09-14 · 适用 alarm-service `internal/ws/` · 实现日：Day 11-12
> 依赖：P0-4（网关 WS Upgrade 转发）、M6 鉴权

---

## 1. 库选型（实证）

| 候选 | 实证 | 结论 |
|---|---|---|
| `gorilla/websocket` | ✅ **v1.5.4-0.20250319132907 已在 module cache**；go-zero v1.10.3 **无** WS 封装（递归 grep 源码无命中） | **推荐** |
| `coder/websocket` | ❌ 不在本地缓存，需联网下载 | 备选 |

**决策：gorilla/websocket**。虽已归档（无新特性/CVE 响应），但：
1. 零下载、go-zero 生态事实标准；
2. 全部用法收敛在 `internal/ws` 包内，对外只暴露 `Hub.Broadcast()`，未来替换只改一处。

---

## 2. 架构：Hub + Client 双泵

```
            ┌──────────── Hub ────────────┐
            │ register / unregister /     │
            │ broadcast chan []byte       │
            └──┬───────────────────┬──────┘
               │                   │
        ┌──────▼──────┐     ┌──────▼──────┐
        │  Client A   │ ... │  Client N   │  每个 Client 两个 goroutine
        │ readPump    │     │ readPump    │  读：pong/关闭检测
        │ writePump   │     │ writePump   │  写：send chan（缓冲 256）+ 心跳
        └─────────────┘     └─────────────┘
```

- **broadcast chan** 缓冲 256；Hub 单 goroutine 分发（避免锁竞争）
- **client.send** 缓冲 256；写满视为慢消费者 → 关闭连接（背压保护，防内存堆积）
- Client 字段：`conn`、`send chan []byte`、`userId`、`connId`、`lastPong`

### 2.1 心跳与踢除

| 参数 | 值 |
|---|---|
| 服务端 ping 间隔 | 30s（`WriteControl(PingMessage)`） |
| 读超时（pong 等待） | 60s（`SetReadDeadline`） |
| 写超时 | 10s |
| 单消息大小上限 | 4KB（`SetReadLimit`，防滥用） |
| 客户端重连 | 指数退避 1s→30s，上限；前端自行实现 |

---

## 3. 挂载方式（go-zero）

WS 需 Upgrade，不能用 go-zero 的 REST handler（其响应已被包装）。用已实证的 `AddRoutes` 挂原生 `http.HandlerFunc`：

```go
server.AddRoutes([]rest.Route{
    {Method: http.MethodGet, Path: "/ws/alarm", Handler: wsHandler},
})
```

**代价**：go-zero 的 `Use` 中间件**不作用于 AddRoutes 注册的路由** → 以下能力需在 handler 内自行实现：

| 能力 | 处理 |
|---|---|
| 鉴权 | WS 握手无法自定义 header（浏览器限制）→ 用 query `?token=xxx`，handler 内校验 JWT |
| RequestId | handler 内生成/透传，写入日志 |
| 跨域 | 握手阶段校验 `Origin` 白名单 |
| CORS 中间件 | 对 Upgrade 请求不适用，无需 |

---

## 4. ⚠️ 多实例广播（必须决策的架构点）

alarm-service 多副本时，WS 连接分散在不同实例 → 产生告警的实例无法直接广播到连接在别的实例上的客户端。

| 方案 | 做法 | 权衡 |
|---|---|---|
| A. 单实例部署 WS | 不做跨实例 | ❌ 与多副本/滚动发布冲突，发布即断连 |
| B. **Redis Pub/Sub**（推荐） | 广播时 `PUBLISH alarm:ws:broadcast {payload}`；每实例订阅并转发给本地 Client | 零新组件（Redis 已有）；Pub/Sub 无持久化，断线期间消息丢失（告警场景可接受，前端重连后拉活跃告警列表补偿） |
| C. Kafka 广播 topic | 每实例独立 consumer group 消费 | 可持久化、可重放；组件重（为本功能引 Kafka 消费组，但 M3 已有 Kafka） |

**推荐 B（Redis Pub/Sub）**，理由：轻量、满足实时性；丢消息由「重连后拉 `/api/alarm/active`」补偿。
若后续要求「断线不丢推送」再升级到 C。

---

## 5. 消息协议

```json
{
  "type": "alarm.created" | "alarm.ack" | "alarm.resolved",
  "data": { "alarmId": "...", "deviceId": "...", "level": 3, "content": "...", "eventType": "...", "areaId": "..." },
  "ts": 1757...,
  "requestId": "..."
}
```

- 广播时机：告警入库后（异步，不阻塞消费主流程）、告警确认/解决后
- 广播失败（无在线连接）不视为错误

---

## 6. 依赖与风险

| # | 事项 | 状态 |
|---|---|---|
| R1 | **网关 WS Upgrade 转发**（P0-4） | 阻塞对外暴露；网关需支持 `Connection: Upgrade` |
| R2 | 鉴权方式（query token） | 需 M6 确认 JWT 校验方式（本地验签 or 调 auth） |
| R3 | 多实例广播选 B | 需架构确认 |
| R4 | 断线补偿由前端拉活跃列表 | 需前端确认 |
| R5 | gorilla 归档风险 | 已用 `internal/ws` 隔离，可替换 |

---

## 7. 测试要点

- 连接建立/正常关闭/异常断网（服务端能踢除僵尸连接）
- 心跳超时踢除；慢消费者踢除
- 广播 1→N；无连接时广播不报错
- 鉴权失败握手拒绝（401）
- 多实例（起两个进程 + Redis Pub/Sub）验证跨实例可达
