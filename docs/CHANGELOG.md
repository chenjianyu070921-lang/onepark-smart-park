# 变更记录 (Changelog)

> 由 AI 按用户规则 29 在每次代码修改后自动维护。最新变更置顶。

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
