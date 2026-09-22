# 讲解卡：管理后台前端 · Axios 统一封装与双 Token 自动续期

- 日期：2026-09-22
- 涉及文件与关键行：`web/admin/src/api/client.ts:15-104`、`web/admin/src/stores/auth.ts:17-42`
- 闸门结果：1 轮通过
- 关联需求：管理后台（Admin Console）前端接入后端登录鉴权——登录态持久化、请求自动携带 Bearer Token、401 时静默刷新并重放

## 一、这段代码做什么（3 句话说清）

管理后台所有 HTTP 请求走一个统一的 axios 实例（`client.ts`），请求前自动注入 access token，响应后统一解包后端的 `{code, msg, data}` 信封。当 access token 过期（HTTP 401 或业务码 `M6-E-0002`）时，前端用 refreshToken 静默换取新 token 并重放原请求，用户无感知。登录态（token/refreshToken/userId/roleIds 等）由 zustand + persist 存在 localStorage（key `onepark-auth`），供路由守卫与请求拦截器读取。

## 二、为什么这么设计

对比过"页面各自 try-catch 401 后跳登录"的朴素方案——用户填了一半的表单直接丢、且每个页面都要重复处理。选 axios 拦截器集中处理 + zustand 全局状态：续期对页面完全透明，页面只拿解包后的业务 data。

## 三、流程 / 时序

1. 页面调用 `request<T>()`（`client.ts:102`），进入 axios 实例。
2. 请求拦截器（`client.ts:21-25`）从 store 读 token，注入 `Authorization: Bearer <token>`。
3. 后端返回 `{code, msg, data}`；`code === '0'` → 拦截器直接返回 `body.data`，页面拿到的就是业务数据。
4. 若 `code === 'M6-E-0002'` 或 HTTP 401 → 进入 `tryRefreshAndRetry`（`client.ts:50`）。
5. 单飞检查：`refreshing = refreshing ?? refreshAccessToken()`——并发多个 401 时只有第一个真正发起刷新，其余复用同一个 Promise（`client.ts:53`）。
6. 刷新请求用**裸 axios**（不走实例），携带 refreshToken 调 `/api/auth/refresh`（`client.ts:34-38`）。
7. 刷新成功 → `setSession` 写回新 token 对 → 给原请求打 `_retried` 标记、换新 Authorization 重放一次（`client.ts:56-60`）。
8. 刷新失败（refreshToken 也过期 / 接口异常）→ `onAuthDead()` 清空登录态并跳 `/login`（`client.ts:63-68`）。
9. 失败分支：业务 code 非 `'0'` → antd message 弹错误（`config.silent` 可静默）并抛 `ApiError`。

## 四、关键代码点（2-4 处，必填）

| 位置 | 作用 | 删掉/改错会怎样 |
|---|---|---|
| client.ts:34 | 刷新请求用裸 `axios.post` 而非实例 | 走实例拦截器 → 刷新接口自身 401 又触发刷新 → 无限递归死循环 |
| client.ts:28,53 | `refreshing` 单飞变量，`??=` 保证只刷新一次 | 并发 401 各自刷新 → 旧 refreshToken 被轮换作废、后到的刷新全失败，且多次 `setSession` 竞态互相覆盖 |
| client.ts:56-57 | `config._retried` 标记限制单请求最多重放 1 次 | 新 token 仍无效时同请求反复 401→刷新→重放，无限循环 |
| stores/auth.ts:38 | persist 中间件 `name: 'onepark-auth'` | 刷新页面丢登录态；改名则老用户本地旧 key 变孤儿数据 |

## 五、参数与阈值（必填）

| 参数 | 值 | 为什么是这个值 |
|---|---|---|
| 请求 timeout | 15000ms | 管理后台常规接口 P99 远低于此，超时多为网关/服务异常，及时报错 |
| 单请求最大重放次数 | 1（`_retried`） | 重放只为覆盖"access 过期"这一种瞬态错误，超过 1 次说明是鉴权真死了 |
| 并发刷新合并 | 单飞（module 级 1 个 Promise） | 后端 refreshToken 轮换是一次性消费，并发多次刷新必然自相残杀 |
| persist key | `onepark-auth` | 项目命名空间前缀，避免同域多应用 localStorage 冲突 |

## 六、失败与边界场景（至少 3 条，必填）

| 场景 | 系统行为 | 兜底手段 |
|---|---|---|
| 页面 5 个请求并发收到 401 | 只有 1 个刷新真正发出，5 个请求等同一 Promise 后全部带新 token 重放 | `refreshing` 单飞；重放仍失败由 `_retried` 截停 |
| refreshToken 也已过期 | 刷新接口返回非 0 / 抛错 → `refreshAccessToken` 返回 null → `onAuthDead()` 登出跳登录 | `tryRefreshAndRetry` 返回 null 判断；`location.pathname` 判断防登录页重复跳转 |
| 新 token 依然 401（如被顶号/角色变更） | 重放结果再进拦截器，`_retried` 已标记 → 不再刷新，`onAuthDead()` 登出 | `_retried` 单次重放上限 |
| 网络断开 / 网关 5xx | axios 抛 AxiosError → 错误拦截器取 `err.message` 弹提示，抛 `ApiError('NETWORK', ...)` | 页面可 catch `ApiError.code` 精细分支 |

## 七、闸门问答摘录

1. **为什么刷新请求用裸 axios？** 答："裸`axios.post`不会走实例的响应拦截器，防止刷新 token 接口 401 时触发递归死循环。换成`instance.post`：刷新接口返回 401 会再次进入拦截器，无限循环调用刷新 token。" —— 通过。
2. **单飞 `refreshing` 机制？没有它会怎样？** 答："第 1 个 401 请求发起刷新并保存 Promise；剩下 4 个直接等待这个 Promise，只刷新 1 次，全部重试。无`refreshing`：5 个请求并发调用刷新接口，refreshToken 一次性失效，所有请求鉴权失败跳登录。" —— 通过（漏答点：多次 `setSession` 竞态覆盖）。
3. **`_retried` 的作用？删掉会怎样？** 答："限制同一个请求最多重试 1 次。删掉：若刷新得到的 token 依旧无效，该请求会反复 401，无限重试死循环。" —— 通过。
4. **后端改为只靠 HTTP 401、body 不带 code，要改哪里、波及什么？** 答："响应拦截器判断条件，从判断业务 code `M6-E-0002`改为判断`status === 401`。波及：要区分刷新接口 401（直接登出）和普通接口 401；防止其他业务 401 误触发刷新；错误日志、提示逻辑同步调整。" —— 通过（补充遗漏：`client.ts:93` 硬编码的 `'M6-E-0002'` 字符串也在波及面内）。

## 八、可能的追问（3 个 + 一句话答题方向）

1. 为什么 `tryRefreshAndRetry` 里重放用 `instance.request(config)` 而不是再发一次新请求？ → 复用原 config（URL/参数/method），重放的必须是与用户发起时完全相同的请求。
2. `stores/auth.ts` 把 token 存 localStorage 有什么安全风险，更稳妥的做法？ → XSS 可窃取；HttpOnly Cookie + CSRF 防护，或至少配合短 TTL + 刷新轮换缩小窗口。
3. 如果后端把 refresh 接口改成需要带旧 access token 一起校验，前端要动哪里？ → `refreshAccessToken`（client.ts:30-48）加请求体字段即可，因走裸 axios，不会触发拦截器递归。
