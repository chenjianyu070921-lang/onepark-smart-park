# 2026-09-16 工作日志

## [10:00] - 重构: 统一身份中间件 + 打通租户上下文管道 (M6 卡点 L1)

- **文件**: 删除 `common/middleware/tenant.go`；修改 `workorder/visitor/parking/notice`(Tenant→IdentityFromHeader)；补挂 `video/dashboard/alarm/access-control/leasing/dispatch/device/billing/energy-data/energy-analysis`
- **决策**: 合并功能重复的两个中间件（Tenant 与 IdentityFromHeader），统一保留更全的 `IdentityFromHeader`(多支持 x-data-scope)；仅 `user-manage` 原已挂 IdentityFromHeader，`tenant.go` 实际被 4 个服务用 `cmw.Tenant` 调用（非死代码，先前误判）；`shadow`(纯 gRPC)/`event-dispatcher`(非 HTTP) 排除
- **验证**: `go build ./common/... ./gateway/... ./app/...` 全部模块 EXIT=0
- **未做**: x-data-scope 注入、datascope.FromCtx 数据隔离落地（属 L2）；energy-analysis 网关前缀冲突（卡点②残留）；TenantId=0（阶段2 已知限制）

## [11:30] - [Bug修复]: 全仓 23 个 Go 模块 go mod tidy 依赖缺失批量修复

- **文件**: go.work 内全部模块的 go.mod / go.sum（alarm-service、access-control-service 此前已单独修复）
- **决策**: 统一用 `go mod tidy` 自动补齐 indirect 依赖，不手工维护版本号
- **验证**: 全部 23 模块 `go mod tidy` exit=0 且 `go build ./...` exit=0，无 "not in your go.mod" 报错

## [14:30] - [代码修复]: dispatch/workorder 审查问题整改（P0+P1）

- **文件**: `common/kafka/kafka.go`（消费循环重试上限+指数退避+毒消息跳过）；`dispatch-service`: runner 断线重连、handler 建单事务化+uk_alarm_id/uk_task_no 精确分流、model/logic 全链路接入 tenant_id、state 增 expire 动作、新增 `internal/cron/expire.go` 超时重派扫描（乐观锁防多实例重复）；`workorder-service`: create/assign/update 流水事务化（原 `_ =` 吞错）、入参校验（model.ValidType/ValidPriority、AssigneeID>0）、列表 pageSize 上限 200、gRPC 统计错误检查+state 常量+移除 itoa/statusFilter
- **决策**: 消费端租户取 KafkaConf.DefaultTenantId（默认 1，对齐 L2 回填口径）；毒消息超限策略=记录后提交位移跳过（防分区卡死）；超时重派逐单乐观锁抢占替代分布式锁；事件发布移到事务提交后
- **未做**(P2 待确认): Outbox 本地消息表、两服务状态机统一、Redis 依赖清理、LoadCandidates 索引与语义、单号序列化
- **验证**: dispatch/workorder/common/alarm/parking `go test -count=1` 全部 exit=0；全仓 23 模块 build+vet TOTAL_FAIL=0
