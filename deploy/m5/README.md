# M5 服务容器化片段（运营招商 + 指挥调度）

把 M5 的 **4 个进程**接入 `deploy/docker-compose.yml` 的中间件环境。

## 边界声明（先读这个）

**本目录只负责 M5。** 仓库目前的状况是：

- `deploy/docker-compose.yml` **只有中间件**（mysql / redis / tdengine / kafka / emqx / nacos / minio / es），
  **一个应用服务都没有**；
- 全仓**只有本目录一个 Dockerfile**（M1~M4、M6 的服务都还是宿主机 `go run`）。

所以计划书验收标准里的「**Docker Compose 一键启动全平台**」**尚未达成** ——
那需要各模块都出自己的片段后合并，由平台负责人收口。本目录是把 M5 这一块先交出来，不替代那件事。

同理，M5 依赖的 M1~M4 gRPC 上游目前也在宿主机直跑，容器内通过 `host.docker.internal` 访问
（见 `etc/dashboard-api.yaml` 的注释）。它们一旦进入同一 compose 网络，这里应改成服务名。

## 目录内容

| 文件 | 说明 |
|---|---|
| `Dockerfile` | 4 个入口共用的多阶段构建（`--build-arg SERVICE=<main 包所在目录>`） |
| `docker-compose.m5.yml` | override 片段，定义 4 个服务 |
| `etc/*.yaml` | **容器内**配置（连接目标用服务名，与本地开发版的 `app/*/etc/*.yaml` 不同） |

## 用法

```bash
# 1) 起中间件（若已在跑可跳过）
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d

# 2) 构建 + 起 M5 的 4 个进程
#    ⚠️ 必须显式列出服务名 —— 不带服务名会连中间件一起创建（其中有拉不动的镜像会直接报错）
docker compose -f deploy/docker-compose.yml -f deploy/m5/docker-compose.m5.yml \
               --env-file deploy/.env up -d --build \
               leasing-service leasing-grpc dashboard-service dispatch-service

# 3) 看状态（4 个都应为 healthy）
docker ps --format '{{.Names}}  {{.Status}}' | grep onepark-

# 4) 停掉（只停 M5，不动中间件）
docker compose -f deploy/docker-compose.yml -f deploy/m5/docker-compose.m5.yml \
               --env-file deploy/.env rm -sf \
               leasing-service leasing-grpc dashboard-service dispatch-service
```

### 端口

| 服务 | 容器名 | 端口 | 环境变量可覆盖 |
|---|---|---|---|
| leasing-service (HTTP) | `onepark-leasing-api` | 8051 | `M5_LEASING_HTTP_PORT` |
| leasing-service (gRPC) | `onepark-leasing-grpc` | 9051 | `M5_LEASING_GRPC_PORT` |
| dashboard-service | `onepark-dashboard-api` | 8052 | `M5_DASHBOARD_HTTP_PORT` |
| dispatch-service | `onepark-dispatch-api` | 8053 | `M5_DISPATCH_HTTP_PORT` |

### 验证（真实响应，非仅端口通）

```bash
curl -s http://127.0.0.1:8051/api/lease/zones        # 招商园区列表
curl -s http://127.0.0.1:8051/api/lease/contracts    # 合同列表
curl -s http://127.0.0.1:8053/api/dispatches         # 调度工单列表
curl -s http://127.0.0.1:8053/api/dispatch/staffs    # 调度人员池
curl -s http://127.0.0.1:8052/api/dashboard/overview # 大屏聚合
```

> 注意路径：租赁前缀是 `/api/lease`（不是 `/api/leasing`）；调度列表是 `/api/dispatches`（复数），
> 写成 `/api/dispatch/tasks` 会命中 `/dispatch/:id` 并把 `tasks` 当 id 转数字，报 400。

## 为什么是「override 片段」而不是直接改 `deploy/docker-compose.yml`

`deploy/` 是全平台共用目录。直接往主文件里加 M5 的服务，会和队友的改动**改同一个文件**，
冲突概率高且难以回溯责任。片段形式可以独立评审、独立合入。

## 三个已经踩过的坑（改这个文件前务必看）

1. **相对路径以「基础文件所在目录」为基准**，不是以本文件所在的 `deploy/m5/` 为基准。
   所以 `docker-compose.m5.yml` 里写的是 `context: ..`（→ 仓库根）和 `./m5/etc/*.yaml`
   （→ `deploy/m5/etc/*.yaml`）。按直觉写成 `../..` / `./etc/...` 会被解析到仓库**上一级**，
   构建与挂载静默失败。已用 `docker compose config` 实测确认过。

2. **不能复用 `deploy/.env` 里的 `REDIS_ADDR`**（它的值是 `127.0.0.1:6380`，是**给宿主机直跑的服务用的**，
   因为宿主机 6379 被本地 redis 占用）。
   compose 的 `${VAR:-默认}` **默认值只在变量未设置时生效**，而 `.env` 里它是设了的 →
   容器会去连自己的 `127.0.0.1:6380` 并启动失败。故这里用 M5 专属变量名
   `M5_REDIS_ADDR`（默认正是容器需要的 `redis:6379`）。

3. **`SERVICE` 必须指向 main 包所在目录**。leasing 的 gRPC 入口 main 包在
   `app/leasing-service/grpcserver/`，所以要传 `SERVICE=leasing-service/grpcserver`。
   传成 `leasing-service` 会编出 **HTTP 入口的二进制**，拿它读 gRPC 配置会报
   `field "Port" is not set`（`rest.RestConf` 要 `Port`，`RpcServerConf` 不要）并无限重启。

## 当前限制

- **健康检查是 TCP 探测，不是业务探测**：服务尚无 `/healthz`（清单 A7，待 M6 的 `common/checks`）。
  换成 HTTP 探测后更有意义 —— TCP 通不代表业务可用。
- **Kafka 消费者在容器配置里保持关闭**：开启前需确认 topic 授权、消费组不重名、
  broker 是否会在消费不存在的 topic 时隐式创建（属向共享设施写入）。
- **`JWT_SECRET` 默认空 = 鉴权放行**：与本地开发一致；要塞值请通过 `M5_JWT_SECRET` 注入。
- **配置以只读卷挂载**（不烘进镜像）：改配置不需要重建镜像，但也意味着**镜像不自包含**。
