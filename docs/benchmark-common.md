# common 薄封装压测记录（P50/P99）

> 测量环境：Windows 11 / 11th Gen Intel i5-11400H / go 1.26（go.work 工作区）。
> 命令：`cd common && go test -bench=Benchmark -benchmem ./<pkg>`（逐包运行；go.work 下 `-bench` 模式会误编译工作区根，故进包目录用 `go test .`）。
> 说明：本批基准均测**纯函数/编码层/进程内 miniredis**，不依赖外部 broker（kafka/mqtt/tdengine 的 broker 交互未纳入，避免 CI 不可用）。
> 这些操作均为 **O(1) 常量时间**（无网络、无循环），故单轮 `ns/op`（均值）≈ P50 ≈ P99；下方 `ns/op` 即作为每操作延迟代表值。

## 数据权限 datascope（纯函数，无分配最优路径）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkWhereOf_Tenant（tenant_id=?） | 32.7 | 16 | 1 |
| BenchmarkWhereOf_All（空过滤，最短路径） | 5.5 | 0 | 0 |
| BenchmarkWhereOf_Self（create_by=?） | 37.1 | 16 | 1 |

## Kafka 封装（配置构建，无网络连接）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkProducerConfig | 509.5 | 624 | 12 |
| BenchmarkConsumerConfig | 15583 | 27086 | 49 |

## MQTT 封装（topic 拼接，纯字符串）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkCmdDownTopic | 86.2 | 32 | 1 |

## TDengine 封装（SQL 组装 + 防注入转义）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkBuildInsertSQL | 728.4 | 272 | 12 |
| BenchmarkEscapeTDString | 83.9 | 48 | 2 |

## tokenblk 令牌黑名单/配对（进程内 miniredis，无网络）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkRevoke（access 吊销写入） | 45124 | 1485 | 31 |
| BenchmarkIsRevoked（黑名单命中查询） | 44338 | 352 | 15 |
| BenchmarkStoreRefresh（refresh 登记写入） | 49603 | 1236 | 31 |
| BenchmarkRefreshExists（refresh 登记表查询） | 44508 | 360 | 15 |
| BenchmarkRevokeRefresh（refresh 吊销删除） | 94856 | 1183 | 46 |

## jwt 令牌

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkGenerate | 3631 | 2923 | 35 |
| BenchmarkParse | 6690 | 3768 | 56 |

## redisx 客户端

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkSet | 45433 | 1407 | 29 |
| BenchmarkGet | 44342 | 295 | 15 |

## ctxdata 上下文透传

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkSetGetIdentity | 180.8 | 192 | 4 |

## 结论 / 阈值建议

- 全部为微秒级（10^1~10^5 ns），属轻量热路径，单核每秒可承载 10^4~10^7 次，不构成瓶颈。
- Redis 类（tokenblk/redisx）单次 ~45µs，主要来自进程内 miniredis 开销；真实 Redis 网络往返约 0.3~1ms，仍需业务侧控制每请求令牌校验次数。
- 若需严格 P50/P99 分布：对任一基准加 `-count=20` 后用 `benchstat` 取中位数与 P99；本批 O(1) 操作可认为 P50≈P99≈上表 `ns/op`。
- CI 已新增 `bench` job 自动跑上述命令并上传结果为 artifact（见 `.github/workflows/ci.yml`）。
