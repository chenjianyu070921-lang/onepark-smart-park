# 鉴权收口方案

## 背景

当前项目有 19 个业务服务，鉴权状态不统一：
- 部分服务走网关 gRPC 调 auth-service Verify
- 部分服务本地校验 JWT
- 部分服务没接鉴权，裸奔

目标：统一鉴权收口到网关，业务服务不再各自校验 JWT，只信任网关注入的身份上下文。

## 现状盘点

### 已有能力
- auth-service：Login/Verify/Refresh/Logout 完整，JWT 签发+校验+tokenblk 黑名单
- 网关：已有 AUTH_GRPC_ADDR 调 auth-service Verify，RBAC 调 user-manage CheckPermission
- common/jwt：JWT 解析工具
- common/ctxdata：身份上下文传递工具
- common/middleware：中间件框架

### 接线清单
| 服务 | HTTP 端口 | gRPC 端口 | 当前鉴权状态 | 需补 |
|---|---|---|---|---|
| device-service | 8001 | 9001 | 无鉴权裸奔 | 网关接 JWT 校验 |
| shadow-service | - | 9002 | 无鉴权裸奔 | 内部 gRPC 信任网关 |
| event-dispatcher | - | - | 无 HTTP 入口 | 不需要 |
| gateway-service | 7000(TCP) | - | 设备侧认证（bcrypt） | 不需要 |
| workorder-service | 8002 | 9091 | 部分接口有鉴权 | 全量接网关 |
| visitor-service | 8083 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| parking-service | 8084 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| notice-service | 8085 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| alarm-service | 8009 | 9009 | 无鉴权裸奔 | 网关接 JWT 校验 |
| access-control-service | 8010 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| video-service | 8011 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| energy-data-service | 8061 | 9061 | 无鉴权裸奔 | 网关接 JWT 校验 |
| energy-analysis-service | 8062 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| billing-service | 8063 | 18063 | 无鉴权裸奔 | 网关接 JWT 校验 |
| leasing-service | 8051 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| dashboard-service | 8052 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| dispatch-service | 8053 | - | 无鉴权裸奔 | 网关接 JWT 校验 |
| auth-service | 8088 | 18088 | 自己就是鉴权服务 | 不需要 |
| user-manage | 8086 | 18089 | 无鉴权裸奔 | 网关接 JWT 校验 |
| gateway | 8080 | - | 统一入口 | 已有 AUTH_GRPC_ADDR |

## 收口方案

### 架构
```
客户端 → 网关(8080) → JWT 校验(调 auth-service:18088 Verify) → RBAC 鉴权(调 user-manage:18089 CheckPermission) → 注入身份头(X-User-ID/X-Tenant-ID/X-Role-IDs) → 转发到业务服务
```

### 核心原则
1. **网关是唯一鉴权入口**：所有 HTTP 请求必须经过网关，业务服务不直接对外暴露
2. **业务服务信任网关注入的身份头**：不再自己解析 JWT，直接从 header 取 User-ID/Tenant-ID
3. **gRPC 内部调用**：同机房内部 gRPC 调用信任上游传入的身份 metadata，不重复校验 JWT

### 网关侧改动
1. 所有路由统一加 JWT 校验中间件（已有，补全路由覆盖）
2. 校验通过后，从 JWT claims 提取 User-ID/Tenant-ID/Role-IDs，注入到转发请求的 header
3. RBAC 鉴权：调 user-manage CheckPermission，按路由匹配权限点

### 业务服务侧改动
1. 删除本地 JWT 校验逻辑（如果有）
2. 从 request header 读取 X-User-ID/X-Tenant-ID/X-Role-IDs
3. 用 common/ctxdata 工具注入到 context，后续业务逻辑从 context 取身份

### 分阶段实施
**P0（今日）**：网关 JWT 校验中间件接线，所有 HTTP 路由必须经过网关
**P1（本周）**：业务服务删除本地 JWT 校验，改从 header 读身份
**P2（下周）**：RBAC 鉴权中间件接线，按路由匹配权限点

## 配置基线
- JWT_SECRET：网关 AUTH_SECRET 与 auth-service JWT_SECRET 必须一致
- AUTH_GRPC_ADDR：网关调 auth-service Verify 的地址
- USER_GRPC_ADDR：网关调 user-manage CheckPermission 的地址
- GATEWAY_MODE：dev/test 关闭鉴权便于联调，pre/prod 强制开启

## 验收标准
1. 未带 Token 的请求访问任何业务接口，网关返回 401 Unauthorized
2. 带合法 Token 的请求，业务服务能从 header 正确读到 User-ID/Tenant-ID
3. 注销后 Token 立即失效，网关返回 401
4. 权限不足的用户访问受限接口，网关返回 403 Forbidden
