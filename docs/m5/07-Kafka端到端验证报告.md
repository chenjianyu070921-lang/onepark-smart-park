# M5 Kafka 端到端验证报告

> 对应未实现清单 **A10**（"Kafka 端到端未验证"）。本报告是**实跑结果**，不是设计说明。
> 复现方式见文末「一键复现」。

| 项 | 值 |
|---|---|
| 执行日期 | 2026-09-21 |
| 结论 | ✅ **闭环** —— 主线 + 加练 A/B/C 四组断言全绿，脚本退出码 **0** |
| 验证方式 | `deploy/test/m5_kafka_e2e.ps1` 一键执行 |
| 断言结果 | **PASS=45 / FAIL=0** |
| broker | **本地** broker（`apache/kafka:3.7.0`，KRaft 单节点，宿主机 19092），**未连组长共享 broker** |

---

## 1. 为什么用本地 broker

`deploy/.env` 里配的是组长提供的共享 broker（`115.191.16.159:9092`），M5 的消费者默认关闭。
之前把 A10 记成"等组长授权 topic"，但那只挡住**连那个 broker** ——
「M5 的消费者能不能正确消费」这件事**不需要**共享 broker 就能验证完。

本地 broker 上随便建 topic，验证完即弃，不向共享基础设施写入任何东西。

---

## 2. 验证环境

| 组件 | 说明 |
|---|---|
| Kafka | `docker.m.daocloud.io/apache/kafka:3.7.0`，容器 `onepark-kafka-local`，**双监听器**（见 §5 坑 1） |
| topic | `alarm-event`（3 分区，1 副本）—— 与 `KafkaConf.Topic` 默认值一致 |
| 消费组 | `m5-dispatch-e2e-local`（专用组名，与线上组 `m5-dispatch-dev-hjy` 隔离） |
| MySQL / Redis | 沿用 `deploy/docker-compose.yml` 已在跑的 `onepark-mysql` / `onepark-redis` |
| 服务 | `dispatch-service` 两实例（`go run`，临时配置在 `%TEMP%`，**未改仓库 `etc/`**） |
| 临时配置差异 | `Kafka.Enabled=true`、`Brokers=127.0.0.1:19092`、`Reassign.IntervalSec=5`（便于验证重派）、`Kafka.DefaultTenantId=0` |

⭐ **`DefaultTenantId` 必须与查询口径一致**：消费者按它落库，而详情/流转接口按请求租户过滤，
两者不一致会表现为**"工单建出来了但查不到"**（配置注释里专门警告过这一点）。开发库存量工单是 tenant 0，故统一取 0。

---

## 3. 主线：投告警 → 自动建单

投递一条 `event_type=fire` 的告警（`deploy/test/alarm_producer.ps1`），消费者落库结果：

| 断言 | 实测 |
|---|---|
| 工单条数（`uk_alarm_id` 幂等） | **1** |
| 初始状态 | **1 = 待指派** |
| 来源 | **2 = 告警自动创建**（而非人工） |
| 区域 | **`A-1F-101`** —— 正确取自 `payload.zone_code` |
| 审计流水 | **1 条 `create`，`from_status→to_status` = `0→1`** |

消费者日志（结构化 JSON，含 caller 便于定位）：

```json
{"caller":"consumer/handler.go:88","content":"[consumer] 告警自动建单成功: taskNo=... alarmId=req-e2e-main-... zone=\"A-1F-101\" priority=1","level":"info"}
```

---

## 4. 加练 A：完整业务闭环（演示用的那条故事线）

```
投告警 → 自动建单 → 自动派单(技能+就近+负载) → 超时未接单 → cron 重派
      → 达上限释放回池 → 人工指派 → start → finish → close
```

**审计动作序列（真实查询结果）**：

```
create,assign,assign,release,assign,start,finish,close
```

逐项断言：

| 环节 | 断言 | 实测 |
|---|---|---|
| 自动派单 | 返回处理人 | `assignee_id=2002`，`name=Li-Far-Fire`（技能/就近/负载命中） |
| 派单后状态 | = 已指派 | **2** |
| 超时重派 | `reassign_count >= 1` | 通过（cron 扫描周期 5s，4s 内完成） |
| 重派审计 | `assign` 至少 2 条 | 通过（首次派单 + 重派） |
| 达上限释放 | 状态回到待指派 | **1**，且有 `release` 审计 |
| 人工指派 | 指派给指定人员 | `staff=1` |
| 状态流转 | `start` / `finish` / `close` | 三步全成功 |
| 终态 | = 已关闭 | **5** |
| 审计完整性 | ≥ 6 条贯穿全程 | 通过（实际 8 条） |
| 完成时间 | `finished_at` 已写入 | 通过 |

> **每一步都有审计** —— 这正是"能讲 3 分钟"的那条完整故事线，`dispatch_task_log` 全程可追溯。

---

## 5. 加练 B：多实例并发消费（单实例测不出来的）

起**两个** `dispatch-service` 实例（**同消费组**、不同 HTTP 端口），把**同一个 `request_id`** 的消息连投 2 条：

| 断言 | 实测 |
|---|---|
| 工单数 | **1** —— `uk_alarm_id` 幂等在真实并发下成立 |
| 审计 `create` 条数 | **1** —— 没有出现"一条消息写两条审计" |
| 日志出现「幂等跳过」 | ✅ —— 证明**第二条确实被消费到并撞了唯一键**，而非"只有一条被投进去" |

> 最后一条断言是关键：只看"只落 1 单"无法区分
> 「幂等生效」与「消息根本没被消费两次」。日志证据把这两种情况分开了。

---

## 6. 加练 C：毒消息不阻塞分区

按顺序投 3 条：**非法 JSON** → **缺 `request_id`** → **正常消息**。

| 断言 | 实测 |
|---|---|
| 第 3 条正常消息**仍被处理** | ✅（关键断言：分区没被前两条卡死） |
| 坏消息被记录并跳过 | ✅ 日志：`[consumer] 丢弃非法告警消息: 解析告警消息失败: invalid character ...` / `告警消息缺少 request_id, 无法做幂等去重` |

> 这里体现的是**两级**毒消息防护：
> `AlarmHandler` 对**格式非法**的消息立即返回 nil（记日志跳过，不重试）；
> `common/kafka` 对 **handler 反复失败**的消息退避重试 5 次后提交位移跳过。
> 两级都**提交位移**，这才保证了分区不被卡住。

---

## 7. ⭐ 过程中踩到的坑（都已修，写进 README 避免重踩）

| # | 现象 | 根因 | 处置 |
|---|---|---|---|
| 1 | 容器内 CLI 报 `Connection to node 1 (/127.0.0.1:19092) could not be established` | **单监听器**：broker 把宿主机可达地址告诉了容器内客户端 | 改**双监听器**：`INTERNAL://localhost:9092` + `EXTERNAL://127.0.0.1:19092`（前者给同容器 CLI，后者给宿主机服务） |
| 2 | 容器内 CLI 报 `No resolvable bootstrap urls` | INTERNAL 广告成了容器名，容器内解析不了自己 | INTERNAL 广告改为 `localhost:9092` |
| 3 | 脚本在 `redis-cli` 那行**直接终止** | PS 的 `$ErrorActionPreference='Stop'` 把**原生命令写 stderr 的警告**当终止错误（`2>$null` 挡不住） | 密码改走环境变量（`MYSQL_PWD` / `REDISCLI_AUTH`），从源头消除警告 |
| 4 | 日志断言连续两次假失败 | ① go-zero 的 `logx` 走 **stderr**，只读 `.log` 会漏；② 服务运行中**占着重定向文件**，`Get-Content` 静默返回空 | 断言移到**停服务之后**，并用 `FileShare.ReadWrite` 打开 |

> 第 4 条是"测试自己骗自己"的典型：行为其实全对（DB 断言早过了），
> 却因为**读日志的方式**报失败。**假失败和真失败一样消耗信任** —— 修的是取证手段，不是被测行为。

---

## 8. 未覆盖 / 限制（如实披露）

- ❌ **未连组长共享 broker**：`Brokers=115.191.16.159:9092` 上的实际消费仍未验证，
  依赖 topic 授权与共享 broker 的网络可达性 —— 这部分**不在 M5 可控范围**。
  但代码路径与本地 broker 上**完全一致**（同一个 `common/kafka`、同一个 handler）。
- ⚠️ **未做长时间稳定性验证**：只验证了功能闭环，没跑"长时间消费 + 断连重连"。
- ⚠️ **`common/kafka.NewConsumer` 固定 `StartOffset=FirstOffset`** —— 新消费组会从**最早位移**开始消费。
  本地反复验证时靠专用组名隔离；**上线前必须先确认共享 broker 上该 topic 的积压量**，
  否则首次开启会把历史消息一次性全部建单。`common/` 是跨模块共用目录，M5 未改动。
- ⚠️ **接口对 `release`/`assign` 等动作的租户过滤**依赖请求头，脚本未注入 `x-tenant-id`，与 `DefaultTenantId=0` 保持一致口径。

---

## 9. 一键复现

```powershell
# 1) 起本地 broker（含建 topic 与两个必踩的坑说明）
#    见 deploy/kafka/README.md
# 2) 一键跑完「起服务 → 投消息 → 断言 → 清理」
powershell -ExecutionPolicy Bypass -File .\deploy\test\m5_kafka_e2e.ps1
# 退出码 0 = 全部断言通过; 服务日志留在 %TEMP%\m5-kafka-e2e\
```

产出物清单：

| 文件 | 作用 |
|---|---|
| `deploy/test/m5_kafka_e2e.ps1` | 一键验证脚本（主线 + A/B/C + 清理，退出码即结论） |
| `app/dispatch-service/tools/alarmproducer/` | 告警消息生产工具（M1/M3 没起也能造数据；支持 `-raw` 造毒消息、`-request-id` 造幂等场景） |
| `deploy/test/alarm_producer.ps1` | 生产工具的包装脚本（保持计划书里的调用方式） |
| `deploy/kafka/README.md` | 本地 broker 起停 / 建 topic / 清场 / 排障 |
