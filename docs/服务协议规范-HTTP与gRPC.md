# OnePark 服务协议规范：HTTP 与 gRPC 的区分和升级指南

> 维护：陈建羽（M1 物联接入底座负责人）
> 适用：OnePark 智慧园区项目 6 人开发团队
> 版本：v1.0  2026-09-11

---

## 一、为什么写这份文档

团队搭框架阶段已建好 22 个 go module，但 **HTTP 和 gRPC 的使用边界** 还有疑问：

- 我的接口该用 HTTP 还是 gRPC？
- 为什么大部分服务是 HTTP，只有 shadow 用 gRPC？
- 我的 proto 文件为什么只占了个位？
- dashboard 要调我的服务，要不要补 gRPC？

**这份文档给出明确决策规则**，避免每个人重复纠结、避免后期返工。

---

## 二、当前架构现状

### 总览：20 个服务 + 1 网关

```
前端/大屏 → gateway/(HTTP:8080)
                │
                ├── HTTP ──→ app/ 17 个 HTTP 服务
                │              （device/workorder/alarm/...）
                │
                └── (预留 gRPC)
                              app/shadow-service  ← 唯一 gRPC 服务
```

### 各服务协议现状

| 服务 | 模块 | 现有协议 | proto 占位 | 实际被其他服务调用？ |
|------|------|---------|-----------|---------------------|
| device-service | M1 | HTTP | ✅ Ping | **是**（dashboard 查设备） |
| shadow-service | M1 | **gRPC** | ✅ 有业务 | **是**（device 调） |
| gateway-service | M1 | TCP/CoAP | — | 否（设备直连） |
| event-dispatcher | M1 | 后台进程 | — | 否（订阅 EMQX） |
| workorder-service | M2 | HTTP | ✅ Ping | **是**（dashboard 查工单数） |
| visitor-service | M2 | HTTP | ✅ Ping | 否 |
| parking-service | M2 | HTTP | ✅ Ping | 否 |
| notice-service | M2 | HTTP | ✅ Ping | 否 |
| alarm-service | M3 | HTTP | ✅ Ping | **是**（dashboard 查告警） |
| access-control-service | M3 | HTTP | ✅ Ping | 否 |
| video-service | M3 | HTTP | ✅ Ping | 否 |
| energy-data-service | M4 | HTTP | ✅ Ping | **是**（dashboard 查日报） |
| energy-analysis-service | M4 | HTTP | ✅ Ping | 否 |
| billing-service | M4 | HTTP | ✅ Ping | 否 |
| leasing-service | M5 | HTTP | ✅ Ping | 否 |
| dashboard-service | M5 | HTTP | ✅ Ping | 否（它调别人，不被调） |
| dispatch-service | M5 | HTTP | ✅ Ping | 否 |
| auth-service | M6 | HTTP | ✅ Ping | 否（网关直接 HTTP 调） |
| user-manage | M6 | HTTP | ✅ Ping | 否 |
| api-gateway | M6 | HTTP | ✅ Ping | 否（网关自身） |

**统计**：
- 纯 gRPC：1 个（shadow）
- 纯 HTTP：18 个
- 应升级成双协议：4 个（device / workorder / alarm / energy-data）

---

## 三、HTTP 与 gRPC 的 5 大区别

| 维度 | HTTP（`.api`） | gRPC（`.proto`） |
|------|---------------|-----------------|
| **契约文件** | go-zero 自创 DSL | Protobuf 标准格式 |
| **管什么** | 南北向：前端/网关 → 系统 | 东西向：服务 ↔ 服务 |
| **生成命令** | `make xxx-goctl` | `make genproto` + `make shadow-goctl` |
| **代码位置** | 服务自己 `internal/` | pb 在 `proto/`（独立 module），骨架在服务目录 |
| **调用方** | 前端/Postman | 后端服务（gRPC Client） |

### 示例对比

**HTTP 契约**（`app/device-service/device.api`）：
```go
syntax = "v1"

type RegisterReq {
    ProductKey string `json:"productKey"`
}

service device-api {
    @handler RegisterHandler
    post /api/device/register (RegisterReq) returns (RegisterResp)
}
```

**gRPC 契约**（`proto/device/device.proto`）：
```protobuf
syntax = "proto3";
package onepark.device;
option go_package = "onepark/proto/device;devicepb";

service DeviceService {
    rpc ListDevices(ListDevicesReq) returns (ListDevicesResp);
}
```

---

## 四、决策规则：什么时候用 HTTP，什么时候用 gRPC

### 🎯 唯一判断标准：这个接口的调用方是谁？

| 调用方 | 用什么协议 | 例子 |
|--------|-----------|------|
| **前端/大屏/Postman** | HTTP | 设备注册、工单创建、告警列表 |
| **别的后端服务** | gRPC | dashboard 调 device 查设备数、device 调 shadow 操作影子 |
| **设备（电表/水表）** | MQTT/TCP | 设备上报、网关接入 |

### 决策流程图

```
新接口需求来了
     │
     ▼
谁是调用方？
     │
     ├── 前端/管理员 ──→ HTTP（写 .api）
     │
     ├── 别的服务 ────→ gRPC（写 .proto）
     │
     └── 前端 + 别的服务都要 ──→ 双协议（.api + .proto 都写）
                                  例：device-service 的 ListDevices
                                  前端分页查 → HTTP
                                  dashboard 聚合 → gRPC
```

### 为什么不全用 gRPC？

go-zero 官方推荐"全 gRPC + 网关 HTTP"，但 OnePark 不这么做，**3 个原因**：

1. **工作量**：全 gRPC 每个服务要维护 `.api`（给网关）+ `.proto`（给别的服务调），双倍契约
2. **没必要**：14 个服务没人调，加了 gRPC 也没人用，纯属浪费
3. **开发期 HTTP 更友好**：Postman 直接测，前端联调不绕网关

---

## 五、各模块组员该怎么做

### M1（陈建羽）：device-service 要补 gRPC

**原因**：dashboard-service 要调你的 `ListDevices`、`GetDeviceStatus`

**要补的 proto 接口**：
```protobuf
service DeviceService {
    rpc Ping(common.Empty) returns (common.Empty);

    // 给 dashboard 聚合用
    rpc ListDevices(ListDevicesReq) returns (ListDevicesResp);
    rpc GetDevice(GetDeviceReq) returns (DeviceInfo);

    // 给别的服务查设备状态用
    rpc GetDeviceStatus(GetDeviceStatusReq) returns (DeviceStatus);
}
```

**操作步骤**（详见第六节）：
1. 改 `proto/device/device.proto`
2. `make genproto`
3. device-service 加 gRPC server 逻辑（参考 shadow-service）

### M2（成员3）：workorder-service 要补 gRPC

**原因**：dashboard 要调你的 `ListWorkOrders` 查工单数

**要补的 proto 接口**：
```protobuf
service WorkorderService {
    rpc Ping(common.Empty) returns (common.Empty);
    rpc ListWorkOrders(ListWorkOrdersReq) returns (ListWorkOrdersResp);
}
```

### M3（成员4）：alarm-service 要补 gRPC

**原因**：dashboard 要调你的 `GetActiveAlarms` 查活跃告警

**要补的 proto 接口**：
```protobuf
service AlarmService {
    rpc Ping(common.Empty) returns (common.Empty);
    rpc GetActiveAlarms(GetActiveAlarmsReq) returns (GetActiveAlarmsResp);
}
```

### M4（成员5）：energy-data-service 要补 gRPC

**原因**：dashboard 要调你的 `GetDailyReport` 查能耗日报

**要补的 proto 接口**：
```protobuf
service EnergyDataService {
    rpc Ping(common.Empty) returns (common.Empty);
    rpc GetDailyReport(GetDailyReportReq) returns (DailyReport);
}
```

### M5（成员6）：dashboard-service 是 gRPC 调用方

**你不补 proto 接口给被人调**，但你是 4 路 gRPC 并发调用方：

```go
// dashboard-service 的 logic 里
var wg sync.WaitGroup
wg.Add(4)

go func() { defer wg.Done(); deviceCli.ListDevices(ctx, req) }()    // 调 M1
go func() { defer wg.Done(); workorderCli.ListWorkOrders(ctx, req) }()  // 调 M2
go func() { defer wg.Done(); alarmCli.GetActiveAlarms(ctx, req) }()     // 调 M3
go func() { defer wg.Done(); energyCli.GetDailyReport(ctx, req) }()     // 调 M4

wg.Wait()  // 4 路并发聚合
```

**你要做**：
1. 在 servicecontext.go 里注入 4 个 gRPC Client
2. 等 M1/M2/M3/M4 把接口定好后再写

### M6（成员2）：auth-service 和网关

**auth-service 不补 gRPC**：网关直接 HTTP 调 `/auth/validate`，不需要 gRPC。

---

## 六、升级 gRPC 的标准操作步骤

以 device-service 补 `ListDevices` 为例：

### 第 1 步：改 proto 文件

打开 `proto/device/device.proto`：
```protobuf
syntax = "proto3";
package onepark.device;
option go_package = "onepark/proto/device;devicepb";

import "common/common.proto";

service DeviceService {
    rpc Ping(onepark.common.Empty) returns (onepark.common.Empty);

    // 新增
    rpc ListDevices(ListDevicesReq) returns (ListDevicesResp);
}

message ListDevicesReq {
    int32 page = 1;
    int32 size = 2;
    string product_key = 3;  // 可选过滤
}

message ListDevicesResp {
    int64 total = 1;
    repeated DeviceInfo list = 2;
}

message DeviceInfo {
    string device_id = 1;
    string device_name = 2;
    int32 status = 3;
    string created_at = 4;
}
```

### 第 2 步：生成 pb 文件

```bash
make genproto
```

生成物：`proto/device/device.pb.go` 和 `proto/device/device_grpc.pb.go`（已提交入库，组员不用装 protoc）

### 第 3 步：device-service 加 gRPC server

在 servicecontext.go 注入 DB：
```go
type ServiceContext struct {
    Config    config.Config
    DB        *gorm.DB
    Redis     *redis.Client
}
```

在 `internal/logic/` 加 `listdeviceslogic.go`（参考已有 logic 结构）。

在 `internal/server/` 加 `deviceserver.go`（参考 shadow-service 的 server/shadowserver.go）。

### 第 4 步：改 main（device.go）

参考 shadow-service 的 shadow.go，加 zrpc server：
```go
// device.go 里既有 rest 又有 zrpc
server := rest.MustNewServer(c.RestConf)
defer server.Stop()
handler.RegisterHandlers(server, ctx)

// 加 gRPC server
s := zrpc.MustNewServer(c.RpcServerConf)
defer s.Stop()
pb.RegisterDeviceServiceServer(s.Server(), server.NewDeviceServer(ctx))
s.Start()

server.Start()
```

### 第 5 步：etc/device-api.yaml 加 RPC 配置

```yaml
Name: device-api
Host: 0.0.0.0
Port: 8888

# 新增
Rpc:
  Name: device-rpc
  ListenOn: 127.0.0.1:9001   # gRPC 端口
```

config.go 加：
```go
type Config struct {
    rest.RestConf
    zrpc.RpcServerConf
}
```

### 第 6 步：编译启动测试

```bash
# 编译
cd app/device-service && go build ./...

# 启动（会同时起 HTTP:8888 和 gRPC:9001）
make run-device

# HTTP 测试
curl http://localhost:8888/api/devices

# gRPC 测试（用 grpcurl 或写 client）
grpcurl -plaintext -d '{"page":1,"size":10}' 127.0.0.1:9001 device.DeviceService/ListDevices
```

---

## 七、团队约定（必读）

### ✅ DO

1. **新接口先问"谁调"**：前端调写 `.api`，服务调写 `.proto`，两边都要写双协议
2. **proto 文件改完跑 `make genproto`**：重新生成 pb 后提交入库
3. **gRPC 服务用独立端口**：HTTP 和 gRPC 不能同端口，建议 HTTP 8xxx，gRPC 9xxx
4. **dashboard 调别人的服务用 gRPC Client**：在 servicecontext.go 注入
5. **pb 文件提交入库**：组员拉代码不用装 protoc 就能编译

### ❌ DON'T

1. **不要手写 routes.go / types.go**：goctl 每次重写，手改会被覆盖
2. **不要把 gRPC pb 放服务 internal/**：别的服务 import 不到，必须放 `proto/` 独立 module
3. **不要让 HTTP 服务直接 HTTP 调别的服务**：dashboard 不要用 `http.Client` 调 device 的 HTTP 接口，要调 gRPC
4. **不要给没人调的服务加 gRPC**：visitor/parking/notice 这些没人调，加 gRPC 是浪费
5. **不要跨服务 import 别人的 internal/**：Go 语言限制，只能 import `proto/` 里的 pb

---

## 八、快速对照表

| 你的服务... | 要做的事 |
|-------------|---------|
| 前端要调我 | 写 `.api`，跑 `make xxx-goctl` |
| 别的服务要调我 | 写 `.proto`，跑 `make genproto`，加 zrpc server |
| 前端 + 别的服务都要调我 | `.api` + `.proto` 都写（双协议） |
| 没人调我 | 只写 `.api`，gRPC 不用管 |
| 我要调别的服务 | 在 servicecontext.go 注入 gRPC Client |

---

## 九、问题反馈

- proto 文件位置：`proto/<服务名>/<服务名>.proto`
- proto 模块路径：`onepark/proto/<服务名>`
- 生成命令：`make genproto`（一条命令生成全部 14 个 proto）
- 单服务 gRPC 骨架：`make shadow-goctl`（目前只有 shadow 用，其他服务要加 Makefile 命令）

**遇到问题找陈建羽（M1）**，技术规范统一维护。
