# 工单 gRPC 统计链路验证报告

> 验证对象: dashboard-service `WorkOrderProvider` 调用 workorder-service gRPC `ListWorkOrders`, 聚合出大屏工单卡片(待处理数 / 完成率 / 今日新增 / 平均处理时长)
> 验证方式: 全链路代码走读 + 既有单元/集成测试实跑 + 口径核对
> 验证日期: 2026-09-27
> 状态: 链路正确, 统计口径对齐; 1 处测试覆盖缺口(服务端聚合无单测) + 2 处文档/口径需确认

## 1. 验证目标

确认大屏工单卡片的数据来源链路: `OverviewLogic` → `WorkOrder.Stat` → workorder-service gRPC `ListWorkOrders` → 服务端单次聚合 → 字段映射 → `WorkOrderCard`, 且返回的**待处理工单数、完成率等统计口径正确、单位换算无误、租户隔离不串数据**。

## 2. 链路架构与代码落点

| 环节 | 代码位置 | 说明 |
|------|----------|------|
| 聚合入口 | `dashboard/.../logic/dashboard/overviewlogic.go` `computeOverview` | 四路数据源并行(errgroup), 单路失败仅标记 `degraded`, 整体不返回 5xx |
| 工单数据源 | `dashboard/.../provider/workorder.go` `WorkOrder.Stat` | 单次 gRPC 调用, 只消费聚合字段(`PageSize=1`) |
| gRPC 调用 | `proto/workorder` `ListWorkOrders` | 入参 `TenantId / Status / Page / PageSize` |
| 服务端实现 | `workorder/.../rpcserver/server.go` `ListWorkOrders` | 单次 SQL 聚合算出全部指标 + 分页清单 |
| 状态枚举 | `workorder/.../state/fsm.go` | 0待派单 1处理中 2待验收 3已完成 4已关闭 |

## 3. 验证方法与结果

实跑测试(全绿):
```
go test ./app/dashboard-service/internal/provider/...        # 字段映射/单位换算/错误上浮
go test ./app/workorder-service/internal/rpcserver/...       # statusFilter 语义(问题1修复)
go test ./app/dashboard-service/internal/logic/dashboard/... # Overview 聚合/降级/超时/缓存
```
- `TestWorkOrderStat_Mapping`: 验证 `TodayCount→TodayTotal`、`PendingCount→Unfinished`、`CompletionRate/100→CompleteRate`、`AvgProcessMinutes*60→AvgHandleSec`, 并断言 `tenant_id` 透传、`PageSize=1`、仅调用 1 次 —— 通过
- `TestWorkOrderStat_ZeroValues`: 空数据下 `CompleteRate=0`(无除零)、`AvgHandleSec=nil`(非 0 秒) —— 通过
- `TestWorkOrderStat_ErrorPropagated`: 上游报错必须向上传(降级依据) —— 通过
- `TestStatusFilter`: `status<0=不限`、`0=待派单`、`3=已完成` 语义与 HTTP 对齐 —— 通过
- `TestOverview_*`: 全成功 / 单路失败降级 / 全失败 / 慢源超时 / 缓存命中 —— 通过

## 4. 各统计指标口径核对

| 指标(大屏) | M2 gRPC 字段 | 服务端计算口径 | dashboard 映射 | 单位换算 | 结论 |
|------|------|------|------|------|------|
| 今日新增 `today_total` | `TodayCount` | `SUM(created_at >= 今日0点)` | 直采 | 无 | ✓ |
| 待处理/未完成 `unfinished` | `PendingCount` | `SUM(status IN (0,1))` 待派单+处理中 | 直采 | 无 | ✓(术语见 F3) |
| 完成率 `complete_rate` | `CompletionRate` | `SUM(status IN (3,4)) / COUNT(*) * 100` | `÷100` | 0~100 → 0~1 | ✓(含已关闭见 F2) |
| 平均处理时长 `avg_handle_sec` | `AvgProcessMinutes` | `AVG(状态3/4 且 finished_at 非空 的 TIMESTAMPDIFF(分钟))` | `*60` | 分 → 秒, 仅 >0 时给值 | ✓ |
| (未使用) `Total` / `CompletedToday` | 服务端计算并返回 | `COUNT(*)` / `SUM(status IN(3,4) AND finished_at>=今日0点)` | dashboard 不取 | — | 不影响 |

**关键正确性结论:**
- **租户隔离**: dashboard 透传 `TenantId`, 服务端 `scoped()` 对聚合与清单均加 `tenant_id=?`; 不传会串园区数据 —— 已验证
- **单次聚合防截断**: 旧实现自拉列表数数会受 `pageSize` 截断导致偏小; 现服务端一次聚合直采 —— 已验证(测试断言仅 1 次调用)
- **错误上浮 + 降级**: 上游报错 `Stat` 返回 error → Overview 标记 `degraded` 且字段 null, 不抛 5xx —— 已验证
- **空数据零值**: `Total=0` 时完成率不除零; 无已完成工单时 `AvgHandleSec=nil`(而非 0 秒假数据) —— 已验证
- **时区**: `WORKORDER_MYSQL_DSN` 含 `loc=Local`(`.env.example` 与 k8s `gen.sh` 一致), `startOfDay` 用 Go 本地时区, 与 `created_at` 存储时区对齐 → `today_count` / `completed_today` 不受时区偏移影响 —— 已核对配置, 无隐患

## 5. 发现的问题 / 待确认项

### F1(中·测试覆盖缺口) 服务端聚合计算无单测
`rpcserver/server_test.go` 仅覆盖 `statusFilter`, **真正决定统计口径的 SQL 聚合(`status IN (0,1)` 计待处理、`status IN (3,4)` 计完成、完成率公式、平均时长 `TIMESTAMPDIFF`)没有任何测试**。本仓库未引入 sqlite 驱动, 且当前 Docker/MySQL 未起, 集成测试暂不能跑。建议:
- 数据库就绪后补一个 DB-backed 测试, 灌入已知状态分布的工单, 断言各聚合字段与完成率;
- 或将完成率/平均时长公式抽成纯函数(入参为原始计数)以便 DB-free 单测。

### F2(低·注释与代码不一致) 完成率口径
`server.go:119` 注释写"已完成(状态3)/总数", 实际代码 `stats.Done = SUM(status IN (?,?))` 含 **状态4 已关闭**(属审查问题9 与 HTTP 看板口径对齐的既定改动)。注释已过时。语义上"已关闭"可能包含"取消关闭", 会轻微抬高完成率 —— 需与产品确认"完成率"是否应含已关闭; 无论结论如何, 应修正该注释。

### F3(低·术语) "待处理" 的字段定义
`Unfinished = 待派单(0)+处理中(1)`, JSON 字段 `unfinished`, 注释"未完成"。用户诉求为"待处理工单数"。代码内部口径一致(其为"尚未完成"而非严格"待派单"), 但需确认大屏前端展示文案与此定义一致(避免把"处理中"也计入"待派单"列)。

### F4(信息) `Status: 0` 在 dashboard 调用中是 no-op
`WorkOrder.Stat` 传 `Status: 0`。因 `statusFilter` 仅作用于分页清单(聚合查询不套用), 该值对统计指标无影响, 仅为语义噪音。建议改为 `Status: -1`(显式"不限")表达意图, 以免后续维护者误解。

### F5(信息) 冗余字段
gRPC 返回 `Total`、`CompletedToday`, dashboard `WorkOrderStat` 未消费。无害, 保留供其它消费者。

## 6. 结论

工单 gRPC 统计链路**正确闭环**: 大屏工单卡片的待处理数、完成率、今日新增、平均处理时长均由 M2 服务端单次聚合直采, 经正确的单位换算(0~100→0~1、分→秒)与租户隔离后返回; 上游异常时优雅降级而非编造零值。

## 7. 已落地的修复(F1/F2/F4)

> 2026-09-27 第二轮: 用户确认推进代码可落地的三项。

- **F2（已修）** `app/workorder-service/internal/rpcserver/server.go:119` 注释由"已完成(状态3)/总数"改为"(已完成(状态3)+已关闭(状态4))/总数", 与第 95 行及 HTTP 看板口径一致。同时修正 dashboard `provider/workorder.go` 历史注释(旧实现自算完成率=已完成/总数, 与 M2 含已关闭终态口径不一致)。
- **F4（已修）** `app/dashboard-service/internal/provider/workorder.go` 调用 `ListWorkOrders` 的 `Status` 由 `0` 改为 `-1`, 显式表达"不限状态"意图(服务端 `statusFilter` 约定 `status<0` 为不限); 聚合不受 status 影响, 纯属意图声明, 行为无变化。
- **F1（部分补强）** 将完成率派生逻辑抽为纯函数 `deriveCompletionRate(done, total)`(含 `total<=0` 不除零保护), 并新增 `TestDeriveCompletionRate`(覆盖空园区/零完成/七成/全完成/含已关闭口径)。**注意**: 真正的 `SUM/COUNT/AVG` 聚合仍在 SQL 中, 该部分仍需要起 MySQL 后的集成测试覆盖(Docker 未起前无法跑)。

## 8. 仍待处理

- **F3（待产品确认）** `Unfinished` = 待派单(0)+处理中(1), 注释"未完成"; 用户诉求词为"待处理"。代码口径内部一致, 但需与产品/前端确认大屏展示文案与此定义一致(不要写成"待派单")。未改代码。
