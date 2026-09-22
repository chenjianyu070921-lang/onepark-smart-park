# 代码讲解卡 · 网关熔断器（gateway/internal/proxy/proxy.go）

> 入仓留痕：code-explain-gate 通过后产出，供日 check 抽查与答辩。
> 关联改动：网关熔断阈值支持「按路由维度」配置（per-route threshold）。

## 1. 机制总览

网关对每个上游前缀路由（`route`）持有一个独立熔断器（`breaker`），实现经典三态机：
`closed → open → half-open → closed`。作用是上游持续失败时快速失败（`503 M6-E-0007`），
给下游喘息时间，避免雪崩。

## 2. 核心数据结构（proxy.go）

- `breaker`（proxy.go:48-57）：`state`(int32, 原子) / `threshold`(int32, 失败阈值) /
  `cooldown`(int64 ns, 熔断冷却) / `failCount`(int32) / `openedAt`(int64 ns)。
- `route`（proxy.go:102-110）：`prefix` + 主上游 `proxy` + 可选灰度 `canary` +
  共享的 `breaker`。
- `Gateway`（proxy.go:150-156）：`routes atomic.Pointer[[]route]`（支持 Nacos 热更新原子替换）、
  `breakerThreshold` / `breakerCooldown`（已收敛的全局默认值）。

## 3. 状态机驱动（三个方法）

- `newBreaker(threshold, cooldown)`（proxy.go:60-66）：仅用参数构造单实例，
  **不关心路由归属**，故按路由维度扩展阈值时此函数无需改。
- `allow(now)`（proxy.go:70-82）：
  - closed → 放行；
  - open → 仅当 `now-openedAt >= cooldown` 时 CAS 到 half-open（放一个探测），否则拒；
  - half-open → 拒（探测在飞）。
- `record(success, now)`（proxy.go:85-100）：
  - 成功 → 复位 failCount、回 closed；
  - 失败 → half-open 时重开；否则 `failCount++ >= threshold` 时打开并记 openedAt。

## 4. 默认值收敛（NewGateway，proxy.go:158-179）

`threshold <= 0` 回落 `breakerThreshold` 常量；`cooldown <= 0` 回落 `breakerCooldown`。
**兜底目的**：防止运维误配 `0` 创建非法实例——
- `threshold=0` → `failCount++ >= 0` 恒真 → **第一次失败即打开**（过敏，误杀正常抖动）；
- `cooldown=0` → `now-openedAt >= 0` 恒真 → open 后每次 allow 立刻半开 → **Open/HalfOpen 高频震荡**，下游无喘息、保护失效。

## 5. 热更新原子性（Reload，proxy.go:182-222）

先在**局部** `routes` 构建全量新表，全程 `return err`（proxy.go:187/205，URL 解析失败）
均发生在 `g.routes.Store`（proxy.go:220）**之前**。只有整轮校验通过才原子替换指针。
→ **all-or-nothing**：中途 err 则丢弃半成品，旧路由表完全保留，不会部分生效。
**调用方责任**：热更新入口必须消费 `Reload` 的 err 并告警，否则新配置静默失效、无报错提示。

## 6. 本次改动决策（per-route threshold，已于 2026-09-22 落地）

- **不动** `newBreaker`（仅构造单实例，复用即可）。
- **加字段**：`config.UpstreamConf` 增加 `BreakerThreshold int`（yaml `Upstreams[].breakerThreshold`，
  如 `/api/billing` 写 `3`）。
- **Reload 收敛**（proxy.go:198 处）：
  `th := g.breakerThreshold; if up.BreakerThreshold > 0 { th = up.BreakerThreshold };
  newBreaker(th, g.breakerCooldown)`（cooldown 仍全局）。
- **fallback 一致**：路由填 0/不填 → 回落全局默认，与 `NewGateway` 的 `<=0` 收敛同源。
- **波及面**：`config.go`（字段+yaml）、`Reload`（构造逻辑）、各路由 `rt.breaker` 实例自动持有新阈值；
  无需额外映射表。

## 7. 边界与降级小结

| 场景 | 行为 | 守门代码 |
|------|------|----------|
| `threshold=0` | 一击即开，误杀抖动 | `NewGateway` `<=0` 兜底 + per-route `>0` 判断 |
| `cooldown=0` | Open/HalfOpen 高频震荡 | 同上 |
| Nacos 推非法 `target` | Reload err，旧表保留 | `Reload` 先建局部表、最后才 Store |
| 调用方吞 err | 静默失效，旧表继续服务 | 热更新入口须告警 |
