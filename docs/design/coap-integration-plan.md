# CoAP 接入落地方案（gateway-service / M1 多协议网关）

> 状态：设计文档（**P0/P1/P2/P3/P4 已实现落地**，P5 测试/P6 灰度上线待执行，见 §12）
> 关联 TODO：`app/gateway-service/gateway.go:25`、`app/gateway-service/internal/config/config.go:4`
> 原文标注：*"CoAP 接入后续引入 plgd-dev/go-coap，与 TCP 共用同一套帧语义"*
> 作者：M6（DevOps/公共基础）按 member2 走查结论产出，供 M1 排期实施

---

## 1. 背景与目标

gateway-service 当前仅实现 **TCP 长连接**接入（`app/gateway-service/gateway.go` 起 `net.Listen("tcp", ...)` + `frame.NewHandler` 逐连接会话）。物联场景中大量轻量设备（NB-IoT、LwM2M、受限节点）使用 **CoAP**（RFC 7252，UDP，端口 5683）而非 TCP，缺 CoAP 会导致该类设备无法接入。

**目标**：在 gateway-service 新增 CoAP（UDP）接入通道，**复用现有 TCP 帧语义**（同一套 `auth`/`telemetry`/`event`/`status`/`ack` 帧、同一套设备认证与 Kafka 投递），使双协议接入行为一致、维护单一。

**非目标（本期明确不做）**：
- 不新建 topic / 不改动 `DeviceTelemetry` 契约（仅 `Source` 字段区分 `coap-gateway`）
- 不改动 `device` / `command_log` 表结构（M1 多租户列已存在）
- 不实现 LwM2M 对象模型全量（仅做"CoAP 传输 + 既有帧语义"适配，LwM2M 业务建模留待 M1 单独排期）

---

## 2. 现状盘点（TCP 帧语义，需被 CoAP 复用）

来源：`app/gateway-service/internal/frame/{protocol.go,handler.go}`、`internal/model/model.go`、`common/kafka/*`

### 2.1 帧格式（上行单行 JSON，`\n` 结尾，TCP 下 `maxBytes=8192`）
```json
{ "type":"auth|telemetry|event|status|ack|ping",
  "device_id":"...", "secret":"...", "request_id":"...",
  "device_type":"...", "event_type":"...",
  "metrics":{...}, "payload":{...}, "status":"...", "occurred_at":0 }
```

### 2.2 处理流程（handler.go）
| 阶段 | 行为 |
|---|---|
| 建连 | 必须在 `AuthTimeoutSec`(默认10s) 内发 `auth` 帧，否则断连 |
| 认证 `auth` | `DeviceModel.FindByDeviceID` 查设备 → `bcrypt.CompareHashAndPassword(device_secret, secret)`；成功后置 `authed=true`，捕获 `tenantID`/`zoneID` 并 `UpdateOnline(online)` |
| 上行 `telemetry` | `metrics` 非空 → `publish` 投 `device-telemetry` |
| 上行 `event` | `event_type` 非空 → `publish` 投 `device-telemetry`；`IsAlarmEvent` 命中再投 `alarm-event` |
| 上行 `status` | `status` 非空 → `publish` 投 `device-telemetry` |
| 回执 `ack` | `request_id` 非空 → `finishCommand` 回写 `command_log`（终态不覆盖） |
| 心跳 `ping` | 直接回 `ok`，用于空闲保活 |

### 2.3 消息契约（common/kafka/contract.go）
`Message = kafka.DeviceTelemetry`，投递字段：`RequestID/TenantID/DeviceID/DeviceType/EventType/ZoneID/OccurredAt/Payload/Source`。
- `Source` 当前恒为 `"tcp-gateway"`，CoAP 侧改为 `"coap-gateway"` 以区分来源。
- 分区 key = `device_id`（`Producer.Publish(ctx, topic, []byte(deviceID), value)`）。

### 2.4 下行（指令）
**现状缺口**：当前 TCP 网关**仅有 ack 上行回写**，`command_log.Finish` 由设备 ack 触发；网关侧**没有订阅命令 topic 向设备主动下发**的通道（TCP 与 CoAP 均无）。指令下行属独立增强，见 §7。

---

## 3. 协议选型

- **库**：`github.com/plgd-dev/go-coap/v3`（Go 生态主流 CoAP 实现，支持 UDP/TCP/TLS/DTLS、block-wise、Observe）。作为新依赖引入（注意 `go.sum` 间接引入 `pion/dtls`）。
- **传输**：UDP，默认端口 **5683**（plaintext/NoSec 用于 dev/test）；生产建议 **5684 + DTLS**（见 §5）。
- **资源模型**：推荐 **单端点 `/frame`**（POST 携带与 TCP 完全相同的 JSON `Frame`），服务端解析后调用共享帧处理逻辑，最大化复用、与 TCP 行为 1:1 对齐。备选方案 B（idiomatic 多资源 `/auth` `/telemetry` `/event` `/status` `/ack`）亦可，但不带来额外收益，增加维护面。

---

## 4. 核心设计：帧逻辑 transport-agnostic 化（关键重构）

现状 `frame.Handler` 直接耦合 `net.Conn`（`Serve` 内 `Read`/`write(conn,...)`），TCP 与 CoAP 不能直接复用。需抽一层 **`Session`**（与传输无关），承载 `svcCtx / deviceID / tenantID / zoneID / authed` 及 `HandleFrame` 状态机；TCP、CoAP 各自只负责"字节 ↔ Frame"的编解码与连接生命周期。

示意（非实现代码）：
```
// frame/session.go
type Session struct {
    svcCtx   *svc.ServiceContext
    deviceID string
    tenantID int64
    zoneID   string
    authed   bool
}
// HandleFrame 处理单帧，返回响应与是否保持会话(供 TCP 长连接判断断连)
func (s *Session) HandleFrame(ctx context.Context, f *Frame) (Response, bool)

// TCP 适配: 保留原有 bufio 读帧 + 写回
type TCPHandler struct { sess *Session; conn net.Conn }
// CoAP 适配: 每请求转 Frame -> sess.HandleFrame -> 响应写回
type CoAPHandler struct { sess *Session }
```
- 重构后 `handler.go` 的 `auth/publish/finishCommand` 迁移进 `Session`；`frame/protocol_test.go` 改为对 `Session.HandleFrame` 单测，TCP/CoAP 共用。
- 影响面：仅 `frame` 包内部 + `gateway.go` 适配；`model`/`kafka` 不动。

### 4.1 CoAP 无状态性与 `Session.authed` 管理（设计缺口补充）

TCP 是长连接：`auth` 一次置 `authed=true`，后续帧复用该连接状态。CoAP 运行在 UDP 上、每个请求独立、无"连接"概念，**没有可供 `authed` 状态挂载的会话载体**。需在 CoAP 适配层显式决策认证状态来源：

- **选项 A（每请求全量认证）**：每个 CoAP 请求都带 `device_id+secret`，每次查库 + bcrypt。实现最简单（与 TCP `auth` 帧逻辑完全一致），但每次上报都付一次 bcrypt 代价，高并发设备下性能差。
- **选项 B（临时 token）**：`auth` 一次后网关签发短期 token，后续请求带 token（用 CoAP `Token` Option 或 body 字段）。性能好，但要引入 token 签发/校验/过期/撤销管理，新增状态面。
- **选项 C（DTLS 身份复用，推荐）**：DTLS（PSK/RawPublicKey）握手阶段已通过 `device_secret` 确认设备身份，应用层不再重复 bcrypt——`authed` 直接由 DTLS 会话身份推导（CoAP 适配层握手完成后即可拿到 `device_id`，构造 `Session` 时即置 `authed=true`、填充 `tenant_id/zone_id`）。无状态、零额外状态管理、零重复认证。

**方案决策：选 C 为主路径，A 为 NoSec 兜底。**
- 安全模式（DTLS/PSK）：应用层跳过 `auth` 帧，`authed` 由 DTLS 身份推导；`device_id` 取自 DTLS PSK 标识（==`device_id`），`tenant_id/zone_id` 仍按 `device_id` 查档填充（仅一次，缓存于请求生命周期）。
- NoSec 模式（dev/test，5683）：无 DTLS 身份，回落选项 A，每个请求必须带 `device_id+secret` 走既有 bcrypt 认证（与 TCP 行为一致，便于无证书联调）。

> 由此 `Session` 在 CoAP 下是**每请求实例**（TCP 下是每连接实例）。TCP 经 `Session.HandleFrame`（首帧 `auth` 一次），CoAP 经 `Session.HandleStateless`（每请求先认证再分发），两者最终都收敛到同一 `dispatch` 业务逻辑，`authed` 字段保留、仅赋值时机不同。

### 4.2 实现落地说明（与 §4.1 方案的偏差）

代码已落地，但对 §4.1「方案 C 为主路径」做了务实修正：

- **DTLS 模式仍保留每请求 bcrypt 认证（即选项 A 同时用于 NoSec 与 DTLS）**。根因：pion DTLS (`github.com/pion/dtls/v3`) 的 PSK 服务端配置为**单一共享密钥**（`Config.PSK(hint)` 不接收客户端身份），无法把 DTLS 握手身份绑定到具体 `device_id`。若仅依赖 DTLS 身份就跳过应用层认证，任意持有共享 PSK 的设备可伪称他人 `device_id`，存在越权风险。因此 DTLS 仅提供**通道加密**，设备身份仍由既有 `bcrypt(device_secret)` 逐请求确认（安全的双因子）。
- **方案 C（DTLS 握手即认证、应用层跳过）暂缓**：待 pion 支持按身份 PSK（或改用证书/RawPublicKey）后，可将 `HandleStateless` 在 DTLS 模式下改为信任 `device_id`、跳过 bcrypt，届时性能改善（§4.1 选项 C 收益）即可兑现。
- **`auth` 帧类型在 CoAP 下仍可用**：客户端既可发独立 `auth` 帧，也可在业务帧（`telemetry`/`event`/...）中直接带 `device_id+secret`，由 `HandleStateless` 统一先认证后分发。

---

## 5. 安全模型（DTLS）

| 环境 | 模式 | 认证决策（对应 §4.1） |
|---|---|---|
| dev/test | NoSec（5683） | 无 DTLS 身份 → 每请求带 `device_id+secret` 走 bcrypt（选项 A，与 TCP 一致） |
| pre/prod | DTLS（5684） | PSK/RawPublicKey 握手确认设备身份 → 应用层跳过 `auth`，`authed` 由 DTLS 身份推导（选项 C），不重复 bcrypt |

- DTLS 依赖 `pion/dtls`（go-coap v3 间接依赖）。PSK 标识复用 `device_id`、密钥复用 `device_secret`，不新建密钥体系；NoSec 模式为保证无证书可读联调，仍保留 bcrypt 认证路径。
- 配置开关：`CoAP.DTLSEnabled` / `CoAP.CertFile` / `CoAP.KeyFile`（证书模式）或 `PSK` 模式（默认复用 `device_secret`）。

---

## 6. 配置扩展

`internal/config/config.go` 新增（不破坏现有字段）：
```go
CoAP struct {
    Enabled   bool   `json:",env=COAP_ENABLED,default=false"`
    Host      string `json:",env=COAP_HOST,default=0.0.0.0"`
    Port      int    `json:",env=COAP_PORT,default=5683"`
    DTLSEnabled bool `json:",env=COAP_DTLS_ENABLED,default=false"`
    DTLSPort  int    `json:",env=COAP_DTLS_PORT,default=5684"`
}
```
- `gateway.yaml` 加 `CoAP` 段；`deploy/docker-compose.yml` 暴露 `5683/5684` 并注入 `COAP_*`；`deploy/.env.example` 记录变量。
- 默认 `Enabled=false`：**灰度可控，回滚=关开关，零 DB 依赖，零回归风险**。

---

## 7. 指令下行（可选增强，建议单独排期）

当前网关无"命令 → 设备"下行通道。CoAP 的 **Observe** 机制天然适合下行：
- 设备 `Observe /commands`；网关订阅 `command` topic（需 M1 新增 topic 或复用 `command_log` 待下发行），有指令时 `Notify` 设备。
- 该能力 TCP 侧同样缺失，建议作为独立任务，TCP/CoAP **同时**补齐，避免协议能力分裂。本期 CoAP 方案**仅保证上行接入与 TCP 对等**，下行增强不阻塞本期。

---

## 8. 逐步骤任务分解

| 阶段 | 任务 | 产出 | 风险/依赖 |
|---|---|---|---|
| P0 | 重构 `frame.Handler` → transport-agnostic `Session`（`HandleFrame` 状态机）；`handler.go` 的 `write(conn,...)` 改为 `HandleFrame` 返回 `Response`，TCP 适配层负责序列化+写回；CoAP 适配层每请求构造 `Session` | `frame/session.go` + 改造 `handler.go`/`gateway.go` | 仅 frame 包改动；268 行文件改动量小；补 `Session` 单测 |
| P1 | 引入 `plgd-dev/go-coap/v3`，新增 `internal/coap/server.go`：UDP 监听、请求→`Frame`→`Session.HandleFrame`→响应；按 §4.1 落 DTLS 身份复用（C）/NoSec 每请求认证（A） | CoAP 上行可用 | 新依赖（先 `go get` 验证与 Go 1.26 兼容，含 `pion/dtls` 间接依赖）；UDP 端口暴露 |
| P2 | 配置扩展（§6）+ compose/`.env.example` 暴露端口 | 可灰度开关 | DevOps |
| P3 | DTLS（PSK 复用 device_secret）或 NoSec 开关 | 生产安全 | `pion/dtls` 间接依赖 |
| P4 | `Source` 字段置 `coap-gateway`；告警双投 `alarm-event` 回归校验 | 来源可观测 | 无 |
| P5 | 测试：扩展 `protocol_test.go` 覆盖 `Session`；coap 集成测试（coap client 模拟设备 auth+telemetry+ack） | 质量门禁 | — |
| P6 | 上线：compose 暴露 5683/5684；监控 CoAP 连接/帧计数；回滚=`CoAP.Enabled=false` | 可灰度 | 防火墙/NAT |

---

## 9. 风险与缓解

| 风险 | 缓解 |
|---|---|
| UDP 端口暴露面扩大 | `Enabled` 默认关；生产仅 5684+DTLS；防火墙限源 |
| DTLS/PSK 引入新依赖与密钥管理 | 复用既有 `device_secret`，不新建密钥体系 |
| CoAP 单包 ≤1024B，大遥测需 block-wise | go-coap 支持 block-wise；`maxBytes` 约束沿用 |
| NAT/运营商防火墙阻断 UDP 长会话 | Observe 保活 + 短轮询兜底（可选） |
| 双协议行为漂移 | 共用 `Session` 单一状态机，单测覆盖；`Source` 区分来源便于对账 |
| 运维成本（双监听） | 灰度开关 + 统一 metrics |

---

## 10. 验收标准（Definition of Done）

1. `CoAP.Enabled=true` 时 UDP 5683 可接收设备 `auth`+`telemetry`/`event`/`status`/`ack`，行为与 TCP 一致（同 `Session`）。
2. 上报消息进入 `device-telemetry`，告警类进入 `alarm-event`，`tenant_id`/`zone_id` 正确充入。
3. `ack` 正确回写 `command_log` 终态。
4. `CoAP.Enabled=false` 时服务不监听 CoAP 端口，TCP 行为零回归。
5. `protocol_test.go` 覆盖 `Session.HandleFrame` 全帧类型；CoAP 集成测试通过。
6. 生产建议路径（5684+DTLS）经联调验证。

---

## 11. 待 M1 确认事项

1. CoAP 端口与防火墙策略（5683 明文是否对公网开放？建议仅内网/5684）。
2. 指令下行（§7）是否随本期一并做？建议独立排期。
3. LwM2M 对象模型是否本期需要？本方案仅做传输适配，不含 LwM2M 业务语义。
4. `go-coap/v3` 具体版本锁定（需 M1 评估与现有 Go 版本兼容）。

---

## 12. 实现落地情况（代码已提交，未部署）

按本方案已落地 **P0/P1/P2/P3/P4**，编译 + `go vet` + 既有单测全绿（Go 1.26.5 / go-coap v3.5.4 / pion dtls v3.1.2）：

| 阶段 | 落地内容 | 文件 |
|---|---|---|
| P0 | `frame.Handler` 抽为 transport-agnostic `Session`（auth/publish/finishCommand/dispatch）；TCP 瘦身为编解码适配层；抽 `HandleStateless` 解决 CoAP 无连接下"每请求认证" | `internal/frame/session.go`(新) `internal/frame/handler.go`(改) |
| P1 | 引入 `plgd-dev/go-coap/v3`，新增 CoAP 服务端：NoSec UDP(5683) + DTLS(5684) 双传输，handler 共享，复用 `Session` 业务语义 | `internal/coap/server.go`(新) `go.mod`/`go.sum`(+go-coap/pion-dtls) |
| P2 | Config 增加 `CoAP` 段（Enabled/Host/Port/DTLSEnabled/DTLSPort/DTLSPSK，默认关）；gateway.yaml + .env.example 补变量 | `internal/config/config.go` `etc/gateway.yaml` `deploy/.env.example` |
| P3 | DTLS 服务端（PSK + AES-128-CCM-8），`DTLSPSK` 为空拒绝启动（安全兜底） | `internal/coap/server.go` |
| P4 | 上行消息 `Source` 置 `coap-gateway`（TCP 仍 `tcp-gateway`），下游可按来源对账 | `internal/frame/session.go` |
| — | gateway 启动时按 `CoAP.Enabled` 接入（默认关，零回归） | `gateway.go` |

**未落地 / 待办**：
- **P5 测试**：CoAP 集成测试需 Kafka+MySQL 测试桩（与现有仓库仅纯函数单测的约定一致，TCP 侧亦无集成测试），暂缓；建议 M1 后续补 CoAP 客户端联调用例。
- **P6 灰度上线**：compose 未部署 gateway-service（既有设计为本地/K8s 运行，见 `.env.example` 注释），故未加 compose 服务块；上线时 K8s 清单注入 `COAP_*` 并开放 5683/5684。
- **方案 C 暂缓**：DTLS 模式因 pion 单 PSK 限制仍保留每请求 bcrypt（见 §4.2）；待 pion 支持按身份 PSK 或改证书后再优化。
- **指令下行（§7）**：CoAP Observe 下行仍未做（TCP 侧同样缺失），属独立增强。
