# 讲解卡 · tokenblk 双向配对索引与原子登出 / auth 登出双向联动

> 模块: common/tokenblk, app/auth-service
> 日期: 2026-09-26
> 关联提交: feat: tokenblk 双向配对索引与原子登出、auth 登出双向联动，CI 增强 lint/cover/bench/镜像构建

## 1. 需求背景（为什么改）

原登出逻辑存在两个会话无法彻底结束的漏洞：

- **用 access 注销**：只把 access 加入黑名单，但配对的 refresh（有效期 7d）仍可换发新 access，注销未真正生效。
- **用 refresh 注销**：只吊销 refresh 本身，配对的 access 仍有效至自然过期（数小时），会话未彻底结束。

根因是配对关系只维护了**单向索引** `access→refresh`（`pairPrefix`），缺少反向索引 `refresh→access`，且"连带吊销配套 refresh"是两步独立的 `Del` 写，非原子，存在配对残留风险。

## 2. 业务与技术难点

- 难点 1：需让"任意一方注销"都能连带吊销另一方，构成双向闭环。
- 难点 2：配对关系的写操作必须原子，否则并发/异常下会出现"只删了登记表但索引残留"或反之。
- 难点 3：Redis 不可用或字段为空时必须降级（不阻断登出主流程）。

## 3. 解决方案

### 3.1 tokenblk/pair.go 新增反向索引与原子吊销

- `rpairPrefix = "auth:rpair:"`：反向索引 `refresh jti -> access jti`，与原有的 `pairPrefix(access→refresh)` 构成双向索引。
- `LinkRefreshAccess`：登录/刷新时记录 `refresh→access`，TTL 取 access 有效期；`rdb==nil` 或 jti 为空时直接返回（降级）。
- `PairedAccess` / `UnlinkRefreshPair`：读/删反向索引，失败或空时返回空串（降级）。
- `RevokePairedRefresh`：**用 `TxPipeline` 原子删除** refresh 登记表 + `pairPrefix(access→refresh)` + `rpairPrefix(refresh→access)` 三条记录，保证三步同步，消除此前两步独立 `Del` 的配对残留。

### 3.2 auth 服务双向联动

- `login.go` / `refresh.go`：在建立 `access↔refresh` 配对的同时，调用 `LinkRefreshAccess` 同步维护反向索引；刷新换发新 refresh 后用 `UnlinkRefreshPair` 清旧 refresh 的反向索引残留。
- `logout.go`：
  - 用 **refresh** 注销时，先吊销 refresh，再经 `PairedAccess` 取配套 access 并 `Revoke` + `UnlinkRefreshPair`，连带结束 access 会话。
  - 用 **access** 注销时，保留原有的连带吊销 refresh 逻辑，但改用 `RevokePairedRefresh`（原子三步删除）替代原先两步独立 `Del`。

## 4. 优化成果

- 登出实现真正的双向闭环：无论用 access 还是 refresh 注销，配对双方均被吊销，会话彻底结束。
- 配对写操作原子化，消除并发/异常下的索引残留。
- 全部新增/修改函数均保留降级分支（Redis 不可用或空 jti 不阻断主流程）。

## 5. 测试与质量保障

- `common/tokenblk/pair_test.go`：覆盖反向索引读写、`RevokePairedRefresh` 原子删除与降级分支。
- `app/auth-service/internal/logic/logic_session_test.go`：补充 tokenblk 与 auth 登出的集成验证用例（登录→刷新→双向注销链路）。

## 6. 面试官/老师追问预测

1. **为什么用 TxPipeline 而不是普通 Pipeline？**
   普通 Pipeline 只合并网络往返，命令仍可能部分成功；`TxPipeline` 在 Redis 服务端以 MULTI/EXEC 事务执行，保证三条 Del 要么全成功要么全失败，避免配对残留。

2. **反向索引会不会和正向索引不一致？**
   两者在同一业务动作（login/refresh/logout）中同步写入与清理，且均有降级保护；即便极端情况下 Redis 写入部分失败，最坏结果是"未连带吊销"，下次令牌自然过期仍安全，不会造成安全越权。

3. **TTL 怎么选，会不会一边先过期？**
   `rpairPrefix` 的 TTL 取 access 有效期，`pairPrefix` 取 refresh 有效期；注销时会主动 `Unlink`，不依赖 TTL 兜底，TTL 仅作最终清理保险。

4. **降级时用户登出会不会失效？**
   降级只跳过"连带吊销配对"，主令牌（用户主动提交的那个）的吊销不受影响，会话主体仍然结束；只是理论上配对一方可能残留至自然过期，风险可控。
