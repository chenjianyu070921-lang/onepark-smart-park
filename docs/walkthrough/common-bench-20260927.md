# 讲解卡 · common 薄封装基准加深（mqtt/tdengine 接入进程内仿真 broker）

> 模块: common/mqtt, common/tdengine
> 日期: 2026-09-27
> 关联提交: perf(test): 加深 common 薄封装基准，接入进程内仿真 broker

## 1. 需求背景（为什么改）

原有 common 薄封装基准只用 fake/mock 或直接空跑，未能反映 wrapper 的真实 I/O 路径与开销，基准结果参考价值低。本次把 mqtt、tdengine 两个 wrapper 的基准接到「进程内仿真 broker」，让基准真正走通 Publish/Subscribe 与 insert/query 的真实往返，从而能量化薄封装层（paho / REST 客户端）的真实开销。

## 2. 业务与技术难点

- 难点 1：mqtt 基准必须连真实 broker 才能测到 `Publish` 等 broker ACK 的往返耗时，但 CI 不允许依赖外部服务。
- 难点 2：tdengine 基准的真实路径是 HTTP REST（POST `/rest/sql` + JSON 解析），同样不能依赖外部 TDengine 实例。
- 难点 3：保持 CI 无外部依赖，且引入的新依赖尽可能少。

## 3. 解决方案

### 3.1 mqtt 基准接入 mochi-mqtt 进程内 broker

- 引入 `github.com/mochi-mqtt/server/v2` 在测试中启动内存 broker（TCP listener，临时端口）。
- `common/mqtt/bench_test.go` 的基准连该 broker，调用 `Client.Publish(ctx, topic, qos, payload)` 测真实 Publish 往返；可扩展订阅端测 Subscribe 路径。
- 仅引入 1 个新依赖（mochi-mqtt），进程内运行，CI 仍绿、无外部服务依赖。

### 3.2 tdengine 基准接入 httptest 仿真 REST 端点

- `common/tdengine/bench_test.go` 用 `httptest.NewServer` 仿真 TDengine REST 端点，按 wrapper 真实协议返回 insert/query 响应。
- 基准调用 `Client.Exec` / `WritePoint`，走通「拼 SQL → POST `/rest/sql` → 解析 JSON」的真实 HTTP 路径，测真实 insert/query 开销。

## 4. 优化成果

- 两个 wrapper 的基准从「假跑」升级为「真实 I/O 路径往返」，基准数字能反映薄封装层真实开销。
- CI 仍无外部服务依赖：mqtt 用内存 broker、tdengine 用 httptest，全部进程内完成。

## 5. 测试与质量保障

- `common/mqtt/bench_test.go`：进程内 broker 下的 Publish 基准。
- `common/tdengine/bench_test.go`：httptest 仿真端点的 insert/query 基准。
- `docs/benchmark-common.md`：同步说明基准加深方式与运行方法。
- 依赖变更：`common/go.mod` 引入 `mochi-mqtt/server/v2`；`common/go.sum`、`go.work.sum` 同步更新。

## 6. 面试官/老师追问预测

1. **为什么不直接用真实 TDengine / 公共 MQTT broker？**
   真实实例在 CI 不可用且引入外部不稳定因素；httptest 与 mochi-mqtt 进程内仿真既能跑通真实协议路径，又保证 CI 可复现、无外部依赖。

2. **进程内 broker 的基准数字能代表生产吗？**
   目的是量化「薄封装层本身的序列化/协议/往返开销」，而非压测 broker 吞吐；进程内消除了网络与远端处理噪声，反而让封装开销更纯净可比。

3. **多引入一个依赖会不会增加维护成本？**
   仅测试依赖（非运行时依赖），且只用于基准；CI 仍绿，维护面可控。
