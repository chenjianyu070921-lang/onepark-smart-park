# 学习档案（累计）

> 由 code-explain-gate 维护，记录每次闸门问答原话、学情诊断与技术提升清单。

---

## 会话 2026-09-22 · 网关熔断 per-route threshold

### 一、问答沟通原话（逐题）

- **Q1 触发条件/动作**：用户答——上游故障达到阈值触发，closed/open/half-open，阈值用连续失败次数；上游 5xx/超时记入失败，成功复位；达到阈值进入 open，返回 503，冷却后探测。→ PASS
- **Q2 状态机/状态/转换**：用户答——closed→open（失败达阈值），open→half-open（冷却后放一个探测），half-open→closed（探测成功）/→open（探测失败）。→ PASS
- **Q3 recover/panic**：用户答——panic 不在 breaker 内 recover，交给框架；panic 不会触发熔断计数、可能中断请求。→ PASS
- **Q4 修改题（改动点/波及面）**：用户答——newBreaker 入参是 threshold/cooldown，只构造单实例、不关心路由归属，函数本身不用改。→ PASS（提示后补充：UpstreamConf 加 BreakerThreshold 字段、Reload 收敛、各路由 breaker 实例自动持有）
- **Q5 边界（threshold=0 / cooldown=0 分别）**：用户答——threshold=0 → `failCount++>=0` 第一次失败即触发熔断；cooldown=0 → `now-openedAt>=0` 每次 allow 立刻半开放行探测，熔断 Open/HalfOpen 高频震荡、保护失效。→ PASS
- **Q6 降级/回滚（原子性+取舍）**：用户答——Reload 中途 err 属整体更新失败，保留旧路由、新配置不生效、不部分生效（先建局部副本、全量校验后 atomic 交换、失败丢弃半成品）；调用方不处理 err → 沿用旧路由静默失效无提示；all-or-nothing 代价=单条非法阻塞全部更新，好处=路由整体一致不混杂脏状态。→ PASS

### 二、学情诊断[study-profile.md](study-profile.md)

#### 主要问题（薄弱点）
1. **设计倾向过度改动**：第一反应想改 `newBreaker` 签名承载路由维度，需引导到「配置驱动 + 复用构造函数」的最小改动思路。
2. **边界分析偏抽象**：先给原则级答案（“防止非法实例”），需追到具体代码行（`record` 的 `failCount++>=threshold`、`allow` 的 `now-openedAt>=cooldown`）才能说出“一击即开 / 高频震荡”的运行时后果。
3. **取舍维度缺位**：对 all-or-nothing 的一致性收益敏感，但初始未主动权衡“阻塞单点 / 静默失效”的运维代价。

#### 技术提升清单
1. 中间件/框架类扩展优先走「配置维度 + 构造收敛」，而非改通用构造函数签名。
2. 每个 `<=0`/`nil` 兜底都要能答出：若没这行，哪个比较式恒真、导致什么故障。
3. 热更新/回调入口返回的 error 必须被上游消费并告警，否则“不报错 ≠ 成功”。
4. 降级设计要同时讲清「一致性收益」与「可用性代价」（all-or-nothing vs 部分生效）。

#### 学习建议
- 改中间件前先画「配置 → 构造 → 运行时」三步链路，定位最小改动点。
- 写任何阈值/超时兜底时，反问自己“若删掉这行会出什么事故”。
- 任何返回 error 的热路径，确认调用方是否消费并告警，避免静默失效。

### 三、累计进度
- [x] 网关熔断器三态机与状态转换（Q1-Q2）
- [x] panic 与熔断计数的边界（Q3）
- [x] per-route 阈值的改动点判断（Q4）
- [x] 阈值/冷却 `<=0` 的运行时失效模式（Q5）
- [x] Reload 热更新原子性与静默失效取舍（Q6）
