# M3 — Redis ZSET 滑动窗口 + TTL 冷却去重（实现级定案 v1.0）

> 2026-09-14 · 与文档 06（去重层次）、文档 07（三类规则）配套，本文聚焦**可落地的 key/脚本/TTL/降级**
> Redis API 已实证（go-zero v1.10.3）：`SetnxExCtx`、`ZaddFloatCtx`、`ZremrangebyscoreCtx`、`ZcardCtx`、`ExpireCtx`、`EvalCtx`/`ScriptRunCtx`

---

## 1. 三张键总表

| 用途 | Key | 类型 | 写入方式 | TTL | 命中含义 |
|---|---|---|---|---|---|
| L1 消息幂等 | `alarm:dedup:{requestId}` | String | `SetnxEx` | **86400s（24h）** | false → 已处理，跳过 |
| L2 业务冷却 | `alarm:cooldown:{deviceId}:{eventType}:{ruleId}` | String | `SetnxEx` | **cooldown_sec（默认 60）** | false → 抑制，不生成告警 |
| 时间窗口 | `alarm:window:{ruleId}:{deviceId}` | ZSet | Lua（见 §3） | **window_sec + 60** | count ≥ 阈值 → 触发 |

命名统一 `alarm:` 前缀 + 冒号分段；`{ruleId}` 参与 L2 是因为不同规则对同一事件可能要求不同冷却。

### 1.1 TTL 取值依据

| TTL | 值 | 理由 |
|---|---|---|
| dedup 24h | 覆盖 Kafka 消息保留期内的重放/重试窗口；过长浪费内存，过短重放即重复 | 与 `retention.ms`（默认 168h）折中：24h 内重复投递是异常，超过 24h 的重放视为新事件并接受告警（人工可判重复） |
| cooldown 60s | 抑制设备抖动（门磁反复触发、传感器毛刺） | P1-3 待产品按事件类型配置化 |
| window + 60 | 窗口本身 + 缓冲，防冷设备 key 残留 | 每次写入都 `EXPIRE` 续期 |

---

## 2. L2 冷却与窗口的协作（易错点）

顺序固定：

```
规则命中 → L2 冷却 SetnxEx
  ├─ false（冷却中）→ 抑制，不生成告警、不写库
  └─ true（首次）→ 生成告警 + 写库
```

**time_window 规则的额外动作**：窗口触发（计数达阈值）后 **DEL 窗口 key**，再依赖 L2 冷却抑制后续。
→ 否则窗口满了之后每来一条都触发，持续刷屏。

---

## 3. 滑动窗口 Lua 脚本（原子）

```lua
-- KEYS[1] = alarm:window:{ruleId}:{deviceId}
-- ARGV[1] = now_ms   ARGV[2] = window_ms   ARGV[3] = member(requestId)
-- ARGV[4] = threshold   ARGV[5] = ttl_sec
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, ARGV[1] - ARGV[2])
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[3])
local cnt = redis.call('ZCARD', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[5])
if cnt >= tonumber(ARGV[4]) then
  redis.call('DEL', KEYS[1])
  return cnt
end
return 0
```

- 返回 `0` = 未触发；返回 `>0` = 触发（值为窗口内计数）
- 用 `ScriptRunCtx`（go-zero `redis.NewScript`）加载一次后按 SHA 执行，避免每次传全脚本
- member 用 `requestId`：天然去重，同一消息重复投递不会重复计数（配合 L1 双保险）

### 3.1 为什么滑动窗口而非固定窗口

固定窗口在边界处最多产生 **2× 误差**（2 次在窗口末尾 + 2 次在下一窗口开头 = 计为 2 次，实际 1 个窗口内 4 次）。安防场景（"5 分钟内 3 次闯入"）漏报代价高 → 选滑动窗口。

### 3.2 时间基准

- **score 用 Redis 服务器时间**（`ARGV[1]` 由 Lua 内 `redis.call('TIME')` 取更准，避免应用时钟漂移；若由应用传入则用应用本地时间）
  - 推荐在脚本内用 `redis.call('TIME')` 取 `now_ms`，多实例时间基准统一
- **乱序/迟到事件**：事件时间戳早于 `now - window` 会被 `ZREMRANGEBYSCORE` 立即清除 → 迟到严重的事件不计入窗口（可接受；设备时钟异常应作为独立告警）

---

## 4. 并发与原子性

| 场景 | 保证 |
|---|---|
| 同设备并发事件 | 单 key Lua 脚本原子执行，计数准确 |
| 多实例（alarm-service 多副本） | 均落到同一 Redis，Lua 原子 → 全局一致 |
| 组合规则多条件（多 key） | 条件标记用独立 `SetnxEx`；若要求"全部条件同时判定"的原子性，改为把同一规则的 keys 放进一个 Lua 批量处理 |
| Redis 主从切换 | **不保证**（异步复制可能丢 Setnx 结果）→ 由 L3 MySQL 唯一索引兜底 |

---

## 5. Redis 故障降级策略

| 组件 | 故障时行为 | 理由 |
|---|---|---|
| L1 dedup | **返回 error → 消息进 DLQ**（不静默放行） | 放行会导致重复告警风暴；进 DLQ 可重放且被监控发现 |
| L2 cooldown | 同上 | 同上 |
| 窗口计数 | 同上 | 计数不准会导致漏报/误报 |

> 统一原则：**去重组件不可用 = 处理失败**，不降级为"跳过去重"。宁可积压（有监控告警）也不产生错误告警。

---

## 6. 容量与清理

- key 规模：窗口 key ≈ 活跃设备数 × 命中规则数；cooldown key 同上。园区量级（千级设备、百级规则）无压力
- 全部 key 均设 TTL，**无定时清理任务**（避免额外组件）
- 规则删除/禁用：主动 `DEL alarm:window:{ruleId}:*` 需 SCAN（不建议）；依赖 TTL 自然过期即可

---

## 7. 监控指标（Day 8 一并埋）

| 指标 | 说明 |
|---|---|
| `dedup_hit_rate` | L1 命中率异常升高 → 上游重复投递或重放 |
| `cooldown_suppress_count` | L2 抑制数 → 设备抖动或冷却设置过长 |
| `window_trigger_count` | 窗口触发次数 → 规则合理性 |
| Redis `Eval` 耗时 P99 | Lua 脚本性能 |

---

## 8. 测试要点

| 场景 | 断言 |
|---|---|
| 窗口边界 | 第 N-1 次不触发，第 N 次触发；窗口外事件被清除后不计数 |
| 触发后 | key 已 DEL，计数重置 |
| 冷却期内 | 同设备同规则不产生第二条告警 |
| 冷却过期 | 可再次告警 |
| 同 requestId 重复 | 窗口内只计一次（member 去重） |
| Redis 不可用 | 返回 error，消息进 DLQ，不产生重复告警 |
