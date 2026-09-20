# 变更记录 (Changelog)

> 由 AI 按用户规则 29 在每次代码修改后自动维护。最新变更置顶。

## 2026-09-20（三）— 四大缺口落地: SQL编排 / 健康检查 / Compose补全 / 网关熔断灰度

### 背景
用户清单 7 项"阻塞/缺口"经逐条核查: auth-service/user-manage/RBAC/Token签发/用户表/限流 **均已实现**(非空桩/非缺失)。真正待办 = ① 网关熔断+灰度缺失 ② 各服务无 HTTP 健康检查端点 ③ Compose 缺 9 服务 ④ 上轮遗留 SQL 编排缺口(部署阻塞, 需定数据库模型)。用户确认采用 **方案 A 每服务独立库**。

### 执行结果
- **SQL 编排修复(方案 A)**:
  - `m1_mysql_tables.sql` 补 `USE device_db`；`m2_mysql_tables.sql` 按服务拆分为 `m2_workorder_tables.sql`/`m2_notice_tables.sql`/`m2_energy_reading.sql`(**删除**原打包文件), 规避重复定义与错库建表；`m2_parking_visitor_tables.sql` 与 `billing_tables.sql` 各自覆盖 parking/visitor/billing 域, 互不重复。
  - 新增 `deploy/sql/run-all.sh` 编排脚本, 按"每服务独立库"模型执行各模块 DDL(全部 `IF NOT EXISTS`/带守卫, 幂等); `m5_mysql_migrations`/`l2_tenant_id_migration`/`m1_device_add_*`(存量迁移) 与 `m1_tdengine_tables.sql`(TDengine) 刻意跳过(全新环境不需要/非 MySQL)。
  - compose mysql 卷改为挂载 `./sql:/sql-init:ro` + 仅 `run-all.sh` 入 `initdb.d`, 替代原直接挂载 `init.sql`/`sys.sql`。
  - 归属: `energy_reading` 由 M4 energy-data-service 写入、billing 只读, 现阶段置于 `billing_db`(见 `m2_energy_reading.sql` 注释); energy-data 经 `ENERGY_DATA_MYSQL_DSN`→billing_db 写入。
- **健康检查端点**: 新增 `common/health` 共享包(探测 MySQL/Redis, 依赖缺失则跳过, 状态 ok/degraded); 全部 16 个 REST 服务 + 网关注入 `/health`(根路径; 多行路由格式服务用独立 AddRoutes 避免切片闭合错误)。RPC/Worker 类(shadow/event-dispatcher)无 HTTP 端点。
- **Compose 补 9 服务**: parking/visitor/notice/access-control/energy-data/energy-analysis/video(REST)+shadow(gRPC)+event-dispatcher(Worker) 纳入 `docker-compose.yml`, 端口/依赖/健康检查对齐既有模式；修正两处历史硬编码——`energy-data`/`energy-analysis` yaml 的外部 IP `115.191.16.159`+旧库 `onepark-smart-park` 参数化为 `ENERGY_DATA_MYSQL_DSN`/`ENERGY_ANALYSIS_MYSQL_DSN`/`REDIS_ADDR`/`KAFKA_BROKERS`；`.env.example` 的 `MYSQL_DSN`(指向不存在的 `onepark-smart-park`) 改为 `DEVICE_MYSQL_DSN`(device_db)+`SHADOW_MYSQL_DSN`(shadow_db)；`.env.example` 补齐缺失 DSN 与 9 服务端口变量；visitor/access-control 的 `DeviceRPC` 端点参数化为 `DEVICE_RPC_ENDPOINTS`(容器内→`device-service:9001`)。
- **网关熔断 + 灰度**: 重写 `gateway/internal/proxy/proxy.go`, 每上游内置熔断器(连续失败阈值 5→断开, 冷却 10s→半开探测, 断开期快速返回 503, 防雪崩)；新增灰度(`UpstreamConf.CanaryTarget`/`CanaryWeight`/`CanaryHeader`, 按权重或 Header 命中导向灰度实例, 灰度目标同样受熔断保护)；`statusRecorder` 透传 Flush/Hijack 保证 WebSocket/流式不受影响。

### 验证
- 全量编译 22 个 `go.mod` 模块 `go build ./...` **全部通过, 0 失败**。
- compose YAML 段落缩进与既有服务一致(人工核验); 受本机无 Docker 守护进程限制, `docker compose up` 实跑与全 healthy 验证待可运行 Docker 的环境执行。

### 已知限制 / 待办
1. docker 实跑验证(`docker compose up -d` + 全部 healthy + 建表)未在本地完成(守护进程未运行), 需目标环境验证。
2. energy-data/energy-analysis 健康检查传 `(nil,nil)`(其 DB/Redis 为异构客户端, 暂未探测); shadow/event-dispatcher 无 HTTP 端点。
3. 网关灰度需部署侧在 Nacos/upstreams 配置 `canary_target` 等才生效, 当前默认仅主上游。
4. dashboard/energy_analysis/energy_data/shadow/event 等业务库目前无独立 DDL 脚本(可能依赖 AutoMigrate 或只读聚合), 已建空库占位, 非阻塞。

---

## 2026-09-20（二）— 收尾任务清单执行(编译/临时文件/env对齐/中间件就绪/DeviceTelemetry清理)

### 执行结果
- **① 全量编译绿**: 遍历 22 个 `go.mod` 模块逐一 `go build ./...`，**全部通过，0 失败**；包含本次 parking 重构，无回退。（注：用户口径"19/19"指 19 个业务服务，均绿；外加 common/gateway/proto 共 22。）
- **② 临时文件清理**: 扫描发现 `.codebuddy/backup/gw.exe`（25MB 网关编译产物备份，非源码），已删除。其余无临时/构建产物残留。
- **③ .env.example ↔ 各服务 yaml 对齐（含 tokenblk）**: 脚本提取全部服务 `etc/*.yaml` 的 `${VAR}` 比对 `.env.example`，**全部已定义**；`docker compose config -q`（以 `.env.example` 为 env-file）返回 0。tokenblk 相关 `REDIS_ADDR/REDIS_PASS`(网关)、`AUTH_REDIS_ADDR/PASS`(auth) 均已对齐并注入 compose。
- **④ 中间件就绪检查**: **本机 Docker 守护进程未运行，live `healthy` 无法实跑**（与 09-18 的 B2/B3 一致，需在可运行 Docker 的环境验证）。静态核查发现部署就绪致命缺口：
  - `init.sql` 仅建 20 库、零建表；compose 的 mysql 仅挂载 `init.sql`+`sys.sql` ⇒ **模块建表脚本与迁移脚本未自动执行**，全新 `up` 后业务表缺失。
  - **架构矛盾（修复前需定夺）**：`m2_mysql_tables.sql` 注释声明 M2 的 11 张表落共享库 `onepark-smart-park`（脚本无 USE），但 `init.sql` 建的是每服务独立库且未建 `onepark-smart-park`，各服务 DSN 也连独立库 → 三者矛盾。m3/m5/billing 各自 `USE` 独立库（与"每服务独立库"一致），仅 M2 为"共享库"异类。
  - `m1_tdengine_tables.sql` 是 TDengine SQL，绝不能进 MySQL `initdb.d`；迁移脚本（`m1_device_add_*`/`m5_mysql_migrations`/`l2_tenant_id_migration`/`rbac_data_scope`）注释明确"全新环境无需/会报错"，不可自动跑。
  - 处置：未盲改，已将完整结论 + 修复方向（方案 A 每服务独立库 / 方案 B 共享库）写入 `deploy/中间件就绪检查-20260918.md` 第六节，**待用户确认数据库模型后实施 SQL 编排修复**。
- **⑤ tokenblk ↔ auth 集成**: `gateway/internal/middleware` 测试 `ok`，端到端（auth 写黑名单 → 网关 `IsRevoked`）生效；上次补的网关 `REDIS_PASS` 注入已使 prod 下不再静默降级。
- **⑥ DeviceTelemetry 重复定义清理**: `app/parking-service/.../consumer.go` 本地 `deviceTelemetry` 信封**重构为复用 `common/kafka.DeviceTelemetry`**（保留 `parkingTelemetry` 业务归一化 + `telemetryPayload` 取 Payload 字段），`go build` 通过，符合契约"各服务不得再本地定义同名结构"。`energy-data-service` 的 `Telemetry` 为独立能耗契约，非重复定义，未改。

### 待决策
1. ④ 数据库模型：**已决(选 A 每服务独立库)**, 修复见 2026-09-20（三）SQL 编排修复。
2. ④ 实跑验证：需在可运行 Docker 的环境执行 `docker compose up -d` + `ps` 验证全 healthy 与建表。

---

## 2026-09-20 — 收尾质量门禁: ④点核查与整改( env 一致性 + tokenblk 集成 + CI )

### 背景
按用户四点收尾要求逐条核查: ① common 封装厚度 ② CI 自动编译 ③ 环境变量名一致性 ④ tokenblk↔auth 集成. 核查中发现一处部署级致命缺陷并修复, 另补一道 CI 防线.

### 关键发现与整改
- **④ / ③ [致命] 网关 compose 漏注入 REDIS_PASS**: `deploy/docker-compose.yml` 的 `gateway` 容器此前仅注入 Nacos + `AUTH_SECRET`, 未注入 `REDIS_ADDR/REDIS_PASS`; 而 `gateway/etc/apigateway-api.yaml` 的 `Redis.Pass: ${REDIS_PASS}` 且 redis 容器设了 `requirepass`. 结果: compose 部署态网关连 Redis **无密码 → 限流 + 令牌黑名单 `IsRevoked` 全部静默降级放行**, Token 失效机制在 prod 被击穿. 其余 6 个业务服务(workorder/alarm/dispatch/leasing/dashboard/billing)均已注入, 仅网关遗漏.
  - 整改: 网关 compose 块补 `REDIS_ADDR/REDIS_PASS` 注入; `apigateway-api.yaml` 的 `Redis.Addr` 由硬编码 `redis:6379` 改为 `${REDIS_ADDR}`(与全网服务一致, 本地联调可切 127.0.0.1:6379).
- **③ [清理] 死变量**: `deploy/.env.example` 中 `JWT_ACCESS_SECRET/JWT_ACCESS_EXPIRE/JWT_REFRESH_EXPIRE` 全仓 0 引用(auth-service 用单一 `JWT_SECRET` + token Type 区分, 过期取自配置). 已移除, 避免误导.
- **③ [残留债, 未动] Redis 密码三命名**: `REDIS_PASSWORD`(容器)/`REDIS_PASS`(应用)/`AUTH_REDIS_PASS`(auth) 实为同一值, 建议后续统一为 `REDIS_PASS`; 属 B 类重构, 未本次改动.
- **② [纠正+增强]**: 仓库已有 `.github/workflows/ci.yml`, **已对全部 22 个 go module 执行 `go test` + `go build`**, "无 CI" 说法不准确. 但原 CI 不校验 compose 注入完整性, 故新增 `compose-validate` job: `docker compose config -q` 守卫 `${VAR}` 无缺失(注: 该检查只能抓"变量未定义", "已定义但未注入某服务"类问题仍需集成测试/人工审计 — 本次网关缺陷即属此类, 已由注入修复本身解决).

### ① common 封装评估(不改)
`common/` 共 19 个包, 多为对 go-redis/go-zero/gorm 的薄封装, **属合理设计(集中 DRY, 统一配置/降级语义)**, 非缺陷. 核查确认 JWT 校验未重复造轮子: 仅 gateway/dashboard/device 解析 JWT 且都走 `common/jwt`; 下游统一信任网关注入的 `x-*` Header(M6 收口). 真正缺口是**测试覆盖**: 19 包仅 5 包有单测(jwt/tokenblk/datascope/kafka/tdengine), 约 14 包无单测, 且 CI 无运行时/集成测试.

### 验证
- `docker compose -f deploy/docker-compose.yml --env-file deploy/.env.example config -q` → `0`
- `cd gateway && go build ./...` → `0`; `go test ./internal/middleware/` → `ok`

---

## 2026-09-19 — 登录认证功能验证 + 网关 Auth 测试回归修复

### 背景
按用户要求对"账号密码登录 + Token 签发 + Token 失效机制"做运行时验证。登录/签发/失效代码与编译此前已确认完整；本次重点实证"失效机制"闭环，并修复 `Auth` 签名重构遗留的测试回归。

### 变更点
- `gateway/internal/middleware/auth_chain_test.go` / `auth_test.go`：补 `Auth(secret, rdb)` 第二参数（`rdb=nil`，黑名单降级放行），修复 `Auth` 加 `rdb` 参数时漏改的 3 处测试调用方（否则 `go test` 编译失败）。
- `gateway/internal/middleware/auth_blacklist_test.go`（新增）：代码级端到端用例，复用**真实生产中间件 + 真实 Redis**，跑通"签发 → 网关放行 → 注销吊销 jti → 同一 token 过网关被 401 拒绝"，实证 Token 失效机制；Redis 不可达时自动 Skip，不阻塞 CI。

### 验证
- `go test ./internal/middleware/` → `ok`（TestAuth / TestJWTToDownstreamCtxdata / TestTokenInvalidationClosedLoop 全 PASS）
- 登录↔sys_db 的运行时联调因本机 Docker/WSL2 下 MySQL 8.0.46 初始化受限（bootstrap 线程 errno 1）未能实跑；该路径已通过 `go build`、`sys.sql` 种子(admin/`Admin@123456`)与代码审查确认。需用户提供宿主 `mysql80` 凭据方可跑通完整登录流。

---

## 2026-09-18 — compose 注入 auth-service 令牌黑名单 Redis

### 背景
M6 在研的「登录态注销 + token 黑名单」（`common/tokenblk`、`app/auth-service` 的 `logout`）已落地，但 `deploy/docker-compose.yml` 的 `auth-service` 容器此前仅注入 `JWT_SECRET` 与 `AUTH_MYSQL_DSN`，**未注入** `AUTH_REDIS_ADDR/AUTH_REDIS_PASS`。`auth-api.yaml` 引用这两个变量，未配置时 `tokenblk` 静默降级为无操作（注销不生效）。本次补齐注入，使黑名单在 compose 部署态真正可用。

### 变更点
- `deploy/docker-compose.yml` · `auth-service.environment` 新增（纯增量）：
  - `AUTH_REDIS_ADDR: ${AUTH_REDIS_ADDR}`
  - `AUTH_REDIS_PASS: ${AUTH_REDIS_PASS}`
- 注入值与 `.env.example` 一致：`AUTH_REDIS_ADDR=redis:6379`、`AUTH_REDIS_PASS=onepark123`，与 redis 容器 `requirepass`（`REDIS_PASSWORD=onepark123`）匹配。

### 验证
- `docker compose -f deploy/docker-compose.yml --env-file deploy/.env.example config -q` → `0`（变量替换与 YAML 合法，无缺失变量）
- 纯增量，不影响本地 `go run` 开发模型（本地不读 compose 注入，仍按 `auth-api.yaml` 注释降级为无操作）

---

## 2026-09-17 — auth/user 双模 gRPC + CD 流水线

### 背景
对"诊断"中的 5 项逐一核验：其中 3 项（auth/user-manage 空桩、errorx 缺 M4/M5/M6）**不成立**，已驳回；
仅 2 项属实——`auth/user proto 仅 Ping` 与 `无 CD 流水线`。本次按用户指令"A 后 B"补齐这两项真实缺口。

### 变更点
- **proto 契约补全**：`proto/auth/auth.proto`、`proto/user/user.proto` 在 `Ping` 基础上补齐业务 RPC
  - auth：`Login` / `Verify` / `Refresh`（复用现有 HTTP 登录/校验/刷新逻辑，双令牌签发）
  - user：`UserCreate` / `UserUpdate` / `UserDelete` / `UserDetail` / `UserList`（复用现有 CRUD logic）
  - 重新 `protoc` 生成 `auth.pb.go` / `auth_grpc.pb.go` / `user.pb.go` / `user_grpc.pb.go`（及 `common.pb.go`）
- **gRPC handler 实现**：`app/auth-service/internal/server/authserver.go`、`app/user-manage/internal/server/usermanageserver.go`
  实现 `AuthServiceServer` / `UserManageServer`，内部直接复用 `logic` 层，零逻辑重复
- **双模接线**：两服务 `main` 改造为「HTTP + gRPC 同进程」
  - 未配置 `Grpc.ListenOn` 时保持**纯 HTTP**（网关 `:8080` 路由行为完全不变）
  - 配置后同进程额外起 gRPC server；`Mode=dev/test` 时开启 `grpc/reflection`
- **config**：两服务 `Config` 增加可选 `Grpc.ListenOn` 字段（`json:",optional"`）
- **CD 流水线**：新增 `.github/workflows/cd.yml`
  - 触发：push 到 main/master/hanxia、tag `v*`、`workflow_dispatch`
  - 复用仓库根统一参数化 `Dockerfile`，矩阵构建 10 个可部署服务 + gateway 镜像
  - 默认推送 `ghcr.io/<owner>/onepark/<svc>:latest`（用内置 `GITHUB_TOKEN`，无需密钥）；
    经 `env.REGISTRY` 可切换到 docker.io / 私有仓库

### 验证
- `cd app/auth-service && go build ./...` → `0`
- `cd app/user-manage && go build ./...` → `0`
- `proto` 经 go workspace（`go.work` 已 `use ./proto`）解析，无需改动各服务 `go.mod`
- `go mod tidy` 已将 `google.golang.org/grpc` 由 indirect 转正（消除 lint 提示）

### 启用 gRPC（可选，默认关闭）
在 `etc/<svc>-api.yaml` 追加：
```yaml
Grpc:
  ListenOn: 0.0.0.0:9xxx
```
并在部署（docker-compose / k8s）中暴露该端口即可。当前 `deploy/docker-compose.yml` 未启用，保持纯 HTTP 部署。
