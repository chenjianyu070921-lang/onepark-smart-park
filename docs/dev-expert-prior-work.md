# dev-expert 前期工程工作记录（不含今日）

> 范围：本会话（2026-09-24）之前的工程业务，**不含今日的 M1~M6 进度审计与排期**。
> 性质：AI 助手（dev-expert）实际动手执行的代码/测试/配置改动与提交处理。
> 说明：本文档位于 `docs/`，该目录已加入 `.gitignore`，仅本地留存、不进仓。

---

## 一、业务工作清单

### 1.1 双 Token 续签 + 白名单写入时机缺陷修复（M6 公共鉴权）
- **问题根因**：原 refresh 轮换逻辑存在「白名单写入时机错误」——若先吊销旧 refresh 再登记新 refresh，并发/失败场景下会静默弄丢会话；且 access 注销时未联动吊销配套 refresh，导致 7 天 refresh 仍可换发新 access。
- **修复动作**：
  - 新增 `common/tokenblk/pair.go`：实现 `LinkPair` / `PairedRefresh` / `UnlinkPair`（access↔refresh 配对登记，键 `auth:pair:<accessJti>`）。
  - 改 `app/auth-service/internal/logic/refresh.go`：刷新轮换改为**先登记新 refresh、后吊销旧**（修写入时机根因）。
  - 改 `app/auth-service/internal/logic/logout.go`：用 access 注销时 **联动吊销配套 refresh**（`PairedRefresh` 取配套 → `RevokeRefresh` + `UnlinkPair`）。
  - 改 `app/auth-service/internal/logic/login.go`：登录成功时建立 access↔refresh 配对。

### 1.2 tokenblk ↔ auth 登出集成端到端验证
- 新增 `app/auth-service/internal/logic/logic_session_test.go`，含 `TestLogoutAccessBlocksPairedRefreshRoundTrip`：
  - 模拟 登录 → access 注销 → 用配套 refresh 调 `Refresh` → 断言被拒（走真实 `Refresh` 入口，非仅单测 `RefreshExists`），确保「登出联动吊销」闭环生效。

### 1.3 commit-msg 讲解闸门处理
- **背景**：仓库 `scripts/hooks/commit-msg` 强制「提交 Go 代码时必须同批提交 `docs/walkthrough/*.md` 讲解卡」，否则拦截。
- **处理动作**：
  - 恢复被误删的 5 张 `docs/walkthrough/` 讲解卡。
  - 删除 0 字节垃圾文件 `git`（非 git 目录，系误建）。
  - 新增讲解卡 `docs/walkthrough/auth-service-token-pair.md`（对应 1.1 的配对修复）。
  - 满足闸门后重新提交（分支 `hanxia`，提交信息「黑名单和缓存进行修复」）。

### 1.4 .gitignore 整理
- 追加忽略规则：`scripts/`、`skills/`、`docs/`（你明确为「非必要文件忽略而已」——仅忽略新增，已跟踪历史文件不动）。

---

## 二、使用的方法

| 方法 | 应用点 |
|---|---|
| 根因调试硬闭环（复现→假设→只修根因→回归留仓） | 1.1 令牌轮换时机缺陷（禁症状修补，改写入时序） |
| 代码审查 + 单测驱动 | 1.2 端到端集成测试覆盖登出联动吊销 |
| git 操作 + hook 规则解读 | 1.3 恢复讲解卡、清垃圾文件、绕过/满足 commit-msg 闸门 |
| 配置管理（.gitignore） | 1.4 收窄提交范围，隔离工具链/文档 |

---

## 三、涉及知识点

- **鉴权体系**：双 Token（access/refresh）续签时序、JWT `jti`、Redis 黑名单（`tokblk:<jti>`）/白名单（`auth:refresh:<jti>`）/配对（`auth:pair:<accessJti>`）、登出联动吊销。
- **go-zero 分层架构**：`api` 契约 → `handler` → `logic` → `model`（GORM）；`internal/logic` 业务实现与单测。
- **工程化**：git hooks（commit-msg 讲解闸门机制）、`.gitignore` 忽略语义（对已跟踪文件无回溯效力）、分支 `hanxia`。
- **测试**：Go 集成测试（真实入口调用 + 断言），避免「仅单测内部函数」的脆弱覆盖。

---

## 四、对应产出物清单

| 类型 | 产出物 | 状态 |
|---|---|---|
| 代码（新增） | `common/tokenblk/pair.go` | 已落盘 |
| 代码（改） | `app/auth-service/internal/logic/refresh.go`、`logout.go`、`login.go` | 已落盘 |
| 测试（新增） | `app/auth-service/internal/logic/logic_session_test.go`（`TestLogoutAccessBlocksPairedRefreshRoundTrip` 等） | 已落盘，**未单独提交** |
| 讲解卡（新增） | `docs/walkthrough/auth-service-token-pair.md` | 已落盘（已随闸门提交） |
| 提交 | 分支 `hanxia` 提交「黑名单和缓存进行修复」（含 tokenblk 修复 + 讲解卡，满足 commit-msg 闸门） | 已提交，领先 14 提交未推 |
| 配置（改） | `.gitignore` 追加 `scripts/`、`skills/`、`docs/` | 已落盘，**未提交** |

---

## 五、未提交 / 遗留项

1. **集成测试未单独提交**：`logic_session_test.go` 若与 Go 代码同批提交，会再次触发 commit-msg 闸门（需同批带 `docs/walkthrough/*.md`，或写 `no-gate:` 逃生前缀）。
2. **`.gitignore` 的 `docs/` 规则未提交**：当前为本地工作区改动。
3. **潜在冲突提醒**：`docs/` 整体忽略后，新建讲解卡也会被忽略，后续 Go 提交要么用 `no-gate:` 前缀，要么将忽略收窄为「忽略 `docs/` 下非 `walkthrough/` 子目录」以保留闸门可用性。

---

*生成时间：2026-09-24（dev-expert 自述，不含当日 M1~M6 审计内容）*
