# 讲解卡：auth-service 令牌黑名单与白名单配对修复

> 对应提交：黑名单和缓存进行修复
> 涉及：`common/tokenblk/pair.go`（新增）、`app/auth-service/internal/logic/{login,logout,refresh}.go`、`common/tokenblk/*_test.go`

## 一、需求背景

用户鉴权采用双 Token 机制：access（短期）+ refresh（7 天）。公共基础功能要求：
1. 注销后令牌立即失效（黑名单）；
2. 刷新时轮换 refresh（旧令牌作废，白名单登记）；
3. 会话可真正结束——用 access 注销时，配套 refresh 也必须失效。

## 二、排查出的真实缺陷

### 缺陷 1：白名单写入时机错误（刷新轮换顺序）
- **现象**：刷新时先吊销旧 refresh、后登记新 refresh。
- **根因**：若"登记新"这一步 Redis 写入失败，旧令牌已吊销而新令牌未入库，但响应仍把新令牌返回客户端——客户端拿到一个永远无法使用的 refresh，会话被静默弄丢。
- **修复**（`refresh.go:66-76`）：调整顺序为**先登记新、后吊销旧**。写入失败时旧令牌仍可用、客户端可重试，不丢会话；两步均降级为无操作（Redis 不可用时不阻断登录/刷新）。

### 缺陷 2：access 注销未联动吊销配套 refresh
- **现象**：用 access 令牌注销只把 access 加入黑名单，7 天有效期的 refresh 仍可换发新 access，注销未真正生效。
- **修复**：
  - 新增 `common/tokenblk/pair.go`：`LinkPair` 在登录/刷新时记录 `auth:pair:{accessJti} → refreshJti` 映射（TTL = refresh 有效期）；
  - `logout.go:57-61`：注销 access 时查配对表，连带吊销配套 refresh，并 `UnlinkPair` 清理残留。
- **降级策略**：Redis 为空或写入失败时返回空串/无操作，与黑名单、refresh 登记表保持一致，不影响主流程可用性。

## 三、为什么这样修（三性对照）

- **可靠性**：先写后删的顺序保证任意时刻客户端持有的 refresh 至少有一个可用；Redis 故障时优雅降级而非拒绝服务。
- **安全性**：注销语义闭环——黑名单拦 access，配对表连带吊销 refresh，攻击者拿到旧 refresh 也换不出新 access。
- **可维护性**：配对逻辑收敛在 `tokenblk` 公共包，login/refresh/logout 三处复用同一套 API（LinkPair/PairedRefresh/UnlinkPair），测试覆盖降级路径。

## 四、验证

- 单元测试：`tokenblk/pair_test.go`（配对写入/读取/降级）、`logic_session_test.go`（注销联动吊销、刷新轮换顺序）；
- `go test ./common/tokenblk/... ./app/auth-service/internal/logic/...` 全部通过；
- `go build ./common/tokenblk/... ./app/auth-service/...` 无编译错误。

## 五、追问预演

1. **为什么不用 refresh token 表（DB）而用 Redis 配对键？** 高频读写 + 天然 TTL 过期，Redis 一键搞定，无表膨胀与清理任务。
2. **配对键写失败会怎样？** access 注销时查不到配对 → 只吊销 access，退化为修复前行为，不影响其他用户；属可接受的降级。
3. **刷新并发（同一 refresh 并发请求）如何处理？** 先登记新后吊销旧窗口内可能双发，但旧 refresh 吊销后第二次请求即被白名单拒绝，最终一致。
