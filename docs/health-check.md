# 健康检查接入规范 (P2)

统一为各 go-zero REST 服务接入存活/就绪探针，供 K8s/Docker 探针与运维排障使用。
复用 `common/health` 组件，探测 MySQL / Redis / Kafka 等依赖连通性。

## 1. 端点语义

| 端点 | 类型 | 探测内容 | 用途 |
|---|---|---|---|
| `/api/healthz` | Liveness | 不探任何外部依赖，进程在即健康 | K8s `livenessProbe`：假死即重启 Pod |
| `/api/readyz` | Readiness | 探测 MySQL / Redis 及自定义依赖 | K8s `readinessProbe`：未就绪摘流量，但不杀 Pod |
| `/health` | 兼容旧探针 | MySQL + Redis（不区分存活/就绪） | 运维兼容，网关沿用此端点 |

> 设计约定：任一**已配置**依赖探测失败，整体状态置为 `degraded`，但 HTTP 仍返回 `200`，
> 避免依赖抖动导致容器被误杀；依赖级不可用由 `components` 明细暴露（见响应体）。

### 响应体

```json
{
  "status": "ok",
  "timestamp": "2026-09-21T12:00:00+08:00",
  "components": [
    {"name": "mysql", "ok": true},
    {"name": "redis", "ok": true}
  ]
}
```

- `status`: `ok` | `degraded`
- `components`: 仅包含**已配置**的依赖（如服务未启用 Redis，则不出现 redis 项）

## 2. common/health API

```go
// 存活探针: 不依赖外部组件
func Liveness() http.HandlerFunc

// 就绪探针: 探测 MySQL(db) / Redis(rdb)，及自定义依赖 probes(如 Kafka)
// db / rdb 为 nil 时跳过对应探测
func Readiness(db *gormx.DB, rdb *redisx.Client, probes ...Probe) http.HandlerFunc

// 兼容旧用法（= Readiness(db, rdb)）
func Handler(db *gormx.DB, rdb *redisx.Client) http.HandlerFunc

// 自定义依赖探测：返回 (依赖名, 是否健康, 明细)
type Probe func(ctx context.Context) (name string, ok bool, detail string)
```

## 3. 服务接入方式（统一模板）

在 `main` 中 `handler.RegisterHandlers(server, ctx)` 之后、`server.Start()` 之前注册：

```go
import (
    "net/http"
    "onepark/common/health"
)

server.AddRoutes([]rest.Route{
    {Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
    {Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(ctx.DB, ctx.Redis)},
})
```

- 仅用 MySQL 的服务（如 user-manage）：`health.Readiness(ctx.DB, nil)`
- 网关无本地 DB：`health.Readiness(nil, ctx.Redis)`

## 4. Kafka 探针示例（Probe 机制）

`common/health` 不强制依赖 Kafka 客户端，统一通过 `Probe` 注入。以下以 TCP 拨测 broker 为例
（各服务 `ServiceContext` 已持有 `Config.Kafka.Brokers`）：

```go
import (
    "context"
    "fmt"
    "net"
    "strings"
    "time"
)

kafkaProbe := func(ctx context.Context) (string, bool, string) {
    for _, b := range strings.Split(svcCtx.Config.Kafka.Brokers, ",") {
        conn, err := net.DialTimeout("tcp", b, 2*time.Second)
        if err != nil {
            return "kafka", false, fmt.Sprintf("broker %s unreachable: %v", b, err)
        }
        _ = conn.Close()
    }
    return "kafka", true, ""
}

server.AddRoutes([]rest.Route{
    {Method: http.MethodGet, Path: "/api/readyz",
     Handler: health.Readiness(svcCtx.DB, svcCtx.Redis, kafkaProbe)},
})
```

## 5. 已接入的 5 个核心服务

| 服务 | 文件 | 依赖探测 |
|---|---|---|
| user-manage | `app/user-manage/usermanage.go` | MySQL |
| auth-service | `app/auth-service/auth.go` | MySQL + Redis（令牌黑名单） |
| workorder-service | `app/workorder-service/workorder.go` | MySQL + Redis（Kafka 旁路兜底） |
| alarm-service | `app/alarm-service/alarm.go` | MySQL + Redis |
| parking-service | `app/parking-service/parking.go` | MySQL + Redis（Kafka 旁路兜底） |
| 网关 gateway | `gateway/apigateway.go` | Redis（`/health` + `/api/healthz` + `/api/readyz`） |

> 注：`device-service` 使用原始 `*gorm.DB` 与 go-zero `*redis.Redis`（类型与其他服务不同），
> 接入时同样可用 `Readiness`，但需传入其 ServiceContext 的对应字段（或为本服务加一个
> `Ping(ctx)` 适配器后作为 `Probe` 注册）。

## 6. K8s 探针配置示例

```yaml
livenessProbe:
  httpGet:
    path: /api/healthz
    port: 8086
  initialDelaySeconds: 5
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /api/readyz
    port: 8086
  initialDelaySeconds: 10
  periodSeconds: 10
```

## 7. 本地验证

```bash
# 存活
curl -s http://127.0.0.1:8086/api/healthz
# 就绪（依赖正常则 status=ok）
curl -s http://127.0.0.1:8086/api/readyz
```
