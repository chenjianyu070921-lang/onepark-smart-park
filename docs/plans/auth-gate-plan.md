# M6 鉴权收口方案（Auth Gate Plan）

> 模块: M6 公共基础服务 + DevOps（auth / common / gateway / proto）
> 日期: 2026-09-27
> 关联: 全仓审计遗留台账「最高优先级卡点——用户侧鉴权收口缺口」

## 1. 现状与风险

- `common/middleware.JWT`（HS256 校验）**已实现但全仓 19 个服务零接线**（`middleware.JWT(` 命中 0）。
- 所有 HTTP 服务仅用 `middleware.IdentityFromHeader` **信任网关注入的身份头**（`x-user-id / x-tenant-id / x-role-ids / x-data-scope`）。
- 本仓存在两个 "gateway"：
  - `gateway/`（apigateway，HTTP :8080）：**用户侧 API 网关**，已具备 `AUTH_SECRET` + `AUTH_GRPC_ADDR` 委托 `auth-service` gRPC `Verify` 的校验能力，`GATEWAY_MODE=pre/prod` 强制开鉴权。
  - `app/gateway-service`：**设备侧 TCP/CoAP 网关**（端口 7000），做设备 bcrypt 认证，**无用户侧 JWT 中间件**（leasing.go 注释引用的 `gateway/internal/middleware.Auth` 在本仓不存在）。
- `auth-service` 双模 gRPC `Verify`（18088）已就绪；`tokenblk` 注销黑名单（Redis）已实现且 auth 已接入，但 `user-manage` 未注入 Redis（注销黑名单未打通）。

**核心风险**：业务服务直连（绕过 apigateway）时可伪造身份头，越权访问；当前仅依赖"所有流量必须经 apigateway"这一部署约束，缺少服务自身的纵深防御。

## 2. 目标架构（两层收口）

1. **边界层（apigateway，必须）**：`GATEWAY_MODE=pre/prod` 下强制 JWT 校验，校验通过后将身份写入上述 Header 注入下游；非法/过期令牌直接拒绝。dev/test 模式关闭便于无 token 联调。
2. **服务层（纵深防御，逐步）**：在关键业务服务 `server.Use(cmw.JWT(c.JwtSecret))` 二次校验 JWT，避免"绕过网关直连"的越权。secret 为空时 `JWT` 中间件**透传**（兼容本地联调），故可灰度、零风险回滚。

## 3. 落地步骤

### Phase 1（P1，本周内，配置+网关侧）
- 确认 `apigateway` 在 `pre/prod` 下已委托 `auth-service.Verify` 校验并注入 Header（现有 `AUTH_GRPC_ADDR`/`AUTH_SECRET` 已接）；补齐单测覆盖"无 token/假 token 被拒"。
- 明确文档：**生产流量必须走 apigateway**，禁止业务服务端口对外暴露（compose/k8s 网络策略配合）。

### Phase 2（P2，纵深防御，核心服务先行）
对 `device / shadow / alarm / dashboard / access-control / visitor` 等核心/高敏感服务：
- `config` 增加 `JwtSecret string \`json:",optional"\``（值来自 `JWT_SECRET`，与 auth 同源）。
- `main.go` 在 `IdentityFromHeader` **之前**加 `server.Use(cmw.JWT(c.JwtSecret))`；`JWT` 校验通过后也把 claims 写入 `ctxdata`（与 `IdentityFromHeader` 互补，避免重复解析）。
- 灰度顺序：核心服务 → 其余服务；secret 留空环境自动透传，不阻断。

### Phase 3（P2，全量 + 注销闭环）
- 全 19 服务接 `JWT` 中间件（需各模块负责人配合，属跨全员改动）。
- `user-manage` 注入 Redis 并与 `auth-service` 共享 `tokenblk` 黑名单，打通登出吊销闭环。
- 设备侧 `gateway-service` 维持既有 bcrypt 设备认证，与用户侧 JWT 解耦（二者身份体系不同，不在本方案混合）。

## 4. 配置与依赖

| 项 | 说明 |
|---|---|
| `JWT_SECRET` | 全局统一密钥；`auth-service` 签发、`apigateway` 与下游 `JWT` 中间件校验必须使用同一值 |
| `GATEWAY_MODE` | dev/test 关鉴权；pre/prod 强制开 |
| `cmw.JWT` | secret 空→透传；非空→HS256 校验，失败拒 401 |
| `cmw.IdentityFromHeader` | 信任网关注入头并写入 `ctxdata`（保留） |

## 5. 风险与回滚

- **误拒联调流量**：`JWT` 中间件 secret 空即透传，本地/联调环境默认不阻断；仅 prod 配密钥才强制，回滚=去掉 `server.Use(cmw.JWT(...))` 或清空 secret。
- **密钥不一致**：`apigateway` 与下游若用不同 `JWT_SECRET` 会全员 401；通过 `.env.example` 统一 `JWT_SECRET` 约束，CI 校验一致。
- **范围**：Phase 2/3 触及全服务 `main.go`+`config`，需各模块 owner 协同，统一 PR 模板与讲解卡（代码讲解闸门）。

## 6. 验收

- [ ] apigateway 在 pre/prod 对无 token/假 token 返回 401；合法 token 透传身份头。
- [ ] 至少一个核心服务（如 device）接 `JWT` 中间件后，绕过网关直连带伪造头被 401。
- [ ] 带合法 token 绕过网关直连仍可用（JWT 自校验通过）。
- [ ] user-manage 登出走 tokenblk 黑名单，auth 侧 `Verify` 对吊销 token 返回无效。
