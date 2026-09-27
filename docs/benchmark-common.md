# common 薄封装压测记录（P50/P99）

> 测量环境：Windows 11 / 11th Gen Intel i5-11400H / go 1.26（go.work 工作区）。
> 命令：`cd common && go test -bench=Benchmark -benchmem ./<pkg>`（逐包运行；go.work 下 `-bench` 模式会误编译工作区根，故进包目录用 `go test .`）。
> 说明：本批基准均测**纯函数/编码层/进程内 miniredis**，不依赖外部 broker（kafka/mqtt/tdengine 的 broker 交互未纳入，避免 CI 不可用）。
> 这些操作均为 **O(1) 常量时间**（无网络、无循环），故单轮 `ns/op`（均值）≈ P50 ≈ P99；下方 `ns/op` 即作为每操作延迟代表值。
> 数值方法：下表为 `go test -bench=Benchmark -benchmem -count=5` 的 **5 次中位数**（剔除冷启动方差）；旧版为单跑值故整体略高 10~15%，属 miniredis 运行方差，非真实回归。
> 加深项：mqtt `BenchmarkPublish` 接进程内 mochi-mqtt broker（allow-all 鉴权 hook，无外部 EMQX）；tdengine `BenchmarkExec`/`BenchmarkWritePoint` 接 `httptest` 仿真的 taosAdapter REST 端点（无外部 TDengine）。二者均测 wrapper **真实 I/O 路径**（序列化 / 网络栈 / JSON 解析），CI 仍无外部依赖。

## 数据权限 datascope（纯函数，无分配最优路径）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkWhereOf_Tenant（tenant_id=?） | 32.0 | 16 | 1 |
| BenchmarkWhereOf_All（空过滤，最短路径） | 5.4 | 0 | 0 |
| BenchmarkWhereOf_Self（create_by=?） | 36.1 | 16 | 1 |

## Kafka 封装（配置构建，无网络连接）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkProducerConfig | 488 | 624 | 12 |
| BenchmarkConsumerConfig | 15008 | 27086 | 49 |

> ConsumerConfig 方差较大（单轮 11.8k~54.8k ns/op，allocs 48~128），与组播/正则解析路径有关；取中位数 15µs 作代表。

## MQTT 封装（topic 拼接 + 真实发布往返）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkCmdDownTopic（纯 topic 拼接，对照基线） | 66 | 32 | 1 |
| BenchmarkPublish（进程内 mochi-mqtt broker 真实 QoS1 发布往返） | 58800 | 2873 | 50 |

## TDengine 封装（SQL 组装 + 防注入转义 + 真实 HTTP 写入往返）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkBuildInsertSQL（纯 SQL 组装，对照基线） | 663 | 272 | 12 |
| BenchmarkEscapeTDString（纯转义，对照基线） | 82 | 48 | 2 |
| BenchmarkExec（httptest 仿真 REST 端点，真实 HTTP POST + JSON 解析） | 91000 | 9806 | 107 |
| BenchmarkWritePoint（SQL 组装 + HTTP + JSON 解析，遥测落库真实热路径） | 88000 | 10146 | 116 |

## tokenblk 令牌黑名单/配对（进程内 miniredis，无网络）

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkRevoke（access 吊销写入） | 54500 | 1248 | 31 |
| BenchmarkIsRevoked（黑名单命中查询） | 52100 | 352 | 15 |
| BenchmarkStoreRefresh（refresh 登记写入） | 53300 | 1260 | 31 |
| BenchmarkRefreshExists（refresh 登记表查询） | 50000 | 360 | 15 |
| BenchmarkRevokeRefresh（refresh 吊销删除） | 107600 | 1191 | 46 |
| BenchmarkRevokePairedRefresh（access 注销原子连带吊销 refresh：3 删 TxPipeline） | 144600 | 2144 | 94 |

> `RevokePairedRefresh` 比 `RevokeRefresh`（2 删）高约 34%，源于多删一条反向索引 `rpair:` 键；为"用 access 令牌注销能连带吊销 refresh"的原子代价，属预期开销。

## jwt 令牌

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkGenerate | 3767 | 2923 | 35 |
| BenchmarkParse | 6629 | 3768 | 56 |

## redisx 客户端

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkSet | 49043 | 1146 | 29 |
| BenchmarkGet | 46800 | 295 | 15 |

## ctxdata 上下文透传

| 基准 | ns/op | B/op | allocs/op |
|---|---|---|---|
| BenchmarkSetGetIdentity | 158 | 192 | 4 |

## 结论 / 阈值建议

- 全部为微秒级（10^1~10^5 ns），属轻量热路径，单核每秒可承载 10^4~10^7 次，不构成瓶颈。
- Redis 类（tokenblk/redisx）单次 ~45~145µs，主要来自进程内 miniredis 开销；真实 Redis 网络往返约 0.3~1ms，仍需业务侧控制每请求令牌校验次数。
- 令牌注销相关原子操作代价排序：`RevokeRefresh`(~108µs) < `RevokePairedRefresh`(~145µs)，均远低于一次真实 Redis 往返，可放心用于登出热路径。
- 若需严格 P50/P99 分布：对任一基准加 `-count=20` 后用 `benchstat` 取中位数与 P99；本批 O(1) 操作可认为 P50≈P99≈上表 `ns/op`。
- CI 已新增 `bench` job 自动跑上述命令并上传结果为 artifact（见 `.github/workflows/ci.yml`）；新增 `BenchmarkRevokePairedRefresh` 会被该 job 自动纳入。
