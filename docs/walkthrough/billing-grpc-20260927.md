# 讲解卡 · billing-service 双模 gRPC 补齐业务 rpc（消除死契约）

> 模块: app/billing-service（M6 公共基础服务+DevOps / 计费）
> 日期: 2026-09-27
> 关联提交: feat(M6): billing-service 双模 gRPC 补齐业务 rpc + 鉴权收口方案

## 1. 需求背景（为什么改）

审计发现 `proto/billing/billing.proto` 仅有 `Ping`（死契约，注释明示"业务接口由各服务负责人补充"），且 `billing-service` 完全没有 gRPC server——全仓 19 个服务里少数几个既无业务 proto rpc、也无 gRPC 进程的服务之一。本次按双模（HTTP+gRPC 同进程）范式补齐 `GetBillRules / GetBills / CalculateBill` 三个业务 rpc，复用现有 HTTP logic，消除死契约，使计费能力可被网关/内部服务经 gRPC 调用。

## 2. 业务与技术难点

- 难点 1：billing 的 logic 通过 `tenantOf(ctx)` 从 `ctxdata` 取租户做数据隔离；HTTP 入口由 `middleware.IdentityFromHeader` 注入，而 gRPC 入口没有等价注入，直接调用 logic 会报"缺租户"。
- 难点 2：proto 消息需与 `internal/types` 既有结构对齐，且不引入重复嵌套 message（`RuleConfig` 含 `*float64` 等复杂结构）。
- 难点 3：保持与 auth-service 已落地的双模 gRPC 范式一致（配置 `Grpc.ListenOn` 为空则纯 HTTP），不影响现有网关行为。

## 3. 解决方案

### 3.1 proto 补业务 rpc（billing/billing.proto）
新增 `GetBillRules / GetBills / CalculateBill` 三个 rpc 及对应 message；`config` 字段以 JSON 字符串承载（避免重复定义 `RuleConfig` 嵌套 message），`detail` 用基础类型 `BillDetailItem`。`protoc --go_out/--go-grpc_out` 重新生成 `billing.pb.go` / `billing_grpc.pb.go`。

### 3.2 双模 gRPC server（internal/server/billingserver.go）
- `BillingServer` 嵌入 `UnimplementedBillingServiceServer`，实现三个业务方法，内部直接复用 `logic.NewXxxLogic(...).Xxx(&types.XxxRequest{...})`，与 HTTP handler 共用同一套计费实现（单一职责、零重复）。
- `withIdentity(ctx)`：从 gRPC `metadata` 读 `x-tenant-id / x-user-id / x-role-ids` 注入 `ctxdata`，对齐 HTTP `IdentityFromHeader`；**租户缺失时回退到 `Config.DefaultTenantId`**，保证内部 gRPC 调用在缺少租户头时仍可用（与 M4 未回填 `tenant_id` 的现状兼容）。

### 3.3 启动与配置
- `internal/config/config.go` 增加 `Grpc { ListenOn }` 可选字段。
- `billing.go` 改为双模：读取 `c.Grpc.ListenOn`，为空则纯 HTTP（网关行为不变）；否则 goroutine 起 HTTP + 同进程起 gRPC（`billingpb.RegisterBillingServiceServer`），dev/test 模式注册 `reflection` 便于 `grpcurl` 联调。
- `etc/billing-api.yaml` 增加 `Grpc.ListenOn: ${BILLING_GRPC_LISTEN}`；`deploy/.env.example`、`deploy/docker-compose.yml` 增加 `BILLING_GRPC_LISTEN` 与 18063 端口映射。

## 4. 优化成果

- `billing.proto` 从死契约变为含 3 个业务 rpc 的可用 gRPC 契约；`billing-service` 具备双模 gRPC 能力。
- 业务实现零重复：gRPC 与 HTTP 共享 `logic` 层。
- 租户隔离在 gRPC 入口被正确注入，数据与 HTTP 一致；缺省回退 `DefaultTenantId` 避免内部调用失败。
- 向后兼容：不配置 `BILLING_GRPC_LISTEN` 时行为与改动前完全一致（纯 HTTP）。

## 5. 测试与质量保障

- `go build ./...`：billing-service、proto 模块均通过（已验证）。
- 联调建议（需 MySQL+Redis 起）：`grpcurl -plaintext -d '{"zoneId":"A栋"}' localhost:18063 onepark.billing.BillingService/GetBillRules` 验证规则列表；`CalculateBill` 需该区域有用量与启用规则。
- 防御：logic 层对 `DB==nil`、参数缺失、无用量/无规则已有明确错误返回，gRPC 直接透传。

## 6. 面试官/老师追问预测

1. **为什么 gRPC 方法不直接查库，而是复用 HTTP logic？**
   计费核心（选规则/算钱/幂等落库）已在 logic 层实现且经 HTTP 验证；gRPC 仅作另一入口，复用可避免两套实现不一致与重复代码，符合 DRY 与单一职责。

2. **gRPC 没传租户头会怎样？**
   `withIdentity` 回退到 `Config.DefaultTenantId`（默认 1，与 M4 未回填 `tenant_id` 的现状对齐），保证内部调用可用；真实多租户场景由调用方（网关/内部服务）经 metadata 注入 `x-tenant-id`。

3. **proto 的 config 为什么用 JSON 字符串而不是嵌套 message？**
   `RuleConfig` 含 `*float64`（不封顶档）、`[]TierItem` 等结构，若再定义一套 proto 嵌套 message 会与 `types.RuleConfig` 重复且易漂移；JSON 字符串复用现有结构、序列化简单，接口消费方按需解析即可。

4. **不配 `BILLING_GRPC_LISTEN` 会影响现有 HTTP 吗？**
   不影响。`ListenOn` 为空时走纯 HTTP 分支，与改动前完全一致。
