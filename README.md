# onepark-smart-park

基于中移物联 OnePark APaaS 底座二次开发的智慧园区后端系统，采用 Go + go-zero 框架实现。平台集成综合安防、智慧通行、能源楼宇管控、物业巡检、招商运营、可视化指挥大屏六大业务模块；支持 IoT 设备事件接入、AI 告警消费、RBAC 权限管理、消息异步处理，实现园区人、车、设备、能耗统一管控。6 人团队协作开发，采用 Monorepo 架构。

## 技术栈

- 框架：go-zero（API + RPC + Gateway）、gRPC + Protobuf
- 存储：MySQL 8（GORM）、Redis 7、TDengine 3、Elasticsearch 8、MinIO
- 消息：Kafka 3（KRaft 模式）、EMQX 5（MQTT Broker）
- 注册配置：Nacos 2
- 容器：Docker Compose
- Go：1.25 + go.work 多 module

## 目录结构

```
onepark-smart-park/
├── gateway/                # 对外入口（API 网关 :8080，JWT/限流/路由）
├── app/                    # 19 个内部业务服务（每服务独立 go module）
│   ├── device-service/     # M1 物联接入（HTTP+gRPC）
│   ├── gateway-service/    # M1 设备侧 TCP/CoAP 网关（不是对外入口）
│   ├── shadow-service/     # M1 设备影子（gRPC）
│   ├── event-dispatcher/   # M1 后台进程（MQTT 订阅→Kafka 分流）
│   ├── workorder-service/  # M2 ... visitor / parking / notice
│   ├── alarm-service/      # M3 ... access-control / video
│   ├── energy-data-service/# M4 ... energy-analysis / billing
│   ├── leasing-service/    # M5 ... dashboard / dispatch
│   └── auth-service/       # M6 ... user-manage
├── proto/                  # gRPC 契约独立 module（onepark/proto）：.proto + 生成的 .pb.go，服务间共享
├── common/                 # 共享代码包（errorx/response/ctxdata/middleware/checks）
├── deploy/                 # docker-compose.yml + .env.example + sql/init.sql
└── go.work                 # workspace 编排 22 个 module（19 业务 + gateway + proto + common）
```

gRPC 契约规则：`.pb.go` 统一生成在 `proto/<svc>/`（独立 module），服务端和调用方都 import `onepark/proto/<svc>`，业务服务之间不互相依赖 module。改 proto 后执行 `make genproto`。

> 注意区分：根目录 `gateway/` 是对前端/管理端的 **HTTP 对外入口**；`app/gateway-service/` 是 M1 面向设备的 **TCP/CoAP 接入网关**，两者不同。

## 快速开始

### 1. 安装工具

```bash
make install-tools   # goctl + protoc-gen-go + protoc-gen-go-grpc
# protoc 编译器需自行安装（Windows: scoop install protobuf）
```

### 2. 启动中间件

```bash
cp deploy/.env.example deploy/.env
make up              # 启动 MySQL/Redis/TDengine/Kafka/EMQX/Nacos/MinIO/ES
```

首次启动 MySQL 会自动执行 `deploy/sql/init.sql`，创建 20 个业务库及 RBAC 五表。

### 3. 生成 proto 代码

```bash
make genproto        # .pb.go 生成到独立 module proto/<svc>/，提交入库
```

### 4. 启动服务

```bash
# workspace 模式，无需 vendor，直接运行
cd app/device-service
go run device.go -f etc/device-api.yaml
```

## 公共约定

- 统一响应：`{code, msg, data}`，成功 code=`"0"`，见 `common/response`
- 错误码格式：`{Module}-{Level}-{Code}`，如 `M1-E-1001`，见 `common/errorx`
- 每服务独立库，禁止跨库 JOIN
- 配置走环境变量占位 `${VAR}`，参考 `deploy/.env.example`
- 全链路 RequestId 由网关注入，见 `common/ctxdata`、`common/middleware`

## 端口分配

| 服务 | HTTP | gRPC | 服务 | HTTP | gRPC |
|------|------|------|------|------|------|
| device | 8001 | 9001 | energy-data | 8012 | 9012 |
| shadow | — | 9002 | energy-analysis | 8013 | 9013 |
| workorder | 8091 | 9091 | billing | 8014 | 9014 |
| visitor | 8092 | — | leasing | 8015 | 9015 |
| parking | 8093 | — | dashboard | 8016 | 9016 |
| notice | 8094 | — | dispatch | 8017 | 9017 |
| alarm | 8009 | 9009 | auth | 8018 | 9018 |
| access-control | 8010 | 9010 | user-manage | 8019 | 9019 |
| video | 8011 | 9011 | gateway（对外入口） | 8080 | — |

gateway-service TCP 7000；event-dispatcher 无端口（后台进程）。
