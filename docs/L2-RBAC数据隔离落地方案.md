# L2 RBAC 数据隔离落地方案

> 关联：M6 公共基础服务整改（L1 中间件统一+管道打通已完成）
> 配套脚本：`deploy/sql/rbac_data_scope.sql`（sys_role.data_scope，已备）、`deploy/sql/l2_tenant_id_migration.sql`（本方案 tenant_id 加列）
> 范围：仅 RBAC 行级数据隔离（按租户/园区），不含菜单/接口级功能权限（已由 sys_role/sys_menu 承担）
> 状态：方案 + SQL 草案就绪；业务 logic 改造待本方案评审通过后分步实施

---

## 一、目标

让每个业务查询自动按"当前用户的数据范围"过滤，实现多租户/多园区数据隔离，消除各业务服务手写 `.Where("tenant_id=?", ...)` 散落、易遗漏的问题。

数据范围三级（已定义于 `common/datascope`）：

| 级别 | 值 | 语义 | WHERE |
|------|----|------|-------|
| ScopeAll | 1 | 全部（超管） | 不过滤 |
| ScopeTenant | 2 | 本园区/租户 | `tenant_id = ?` |
| ScopeSelf | 4 | 本人 | `create_by = user_id` |

---

## 二、现状盘点（本轮调研结论）

### 2.1 已具备的能力（L1 + 此前设计）
- `common/ctxdata`：完整 `GetUserId/GetTenantId/GetRoleIds/GetDataScope`。
- `common/datascope.FromCtx(ctx)`：opt-in gorm 作用域，按 `ctxdata.GetDataScope` 构造 WHERE；未设置时**默认兜底 ScopeTenant**（安全）。
- 网关已开启 `Auth: Enabled: true`，按 `SkipPaths`（`/api/auth/login`、`/api/auth/refresh`）放行，其余注入 `x-user-id/x-role-ids/x-tenant-id`。
- 业务服务已挂载 `IdentityFromHeader`（L1 已完成），将 Header 写入 context。
- `rbac_data_scope.sql` 已备 `ALTER sys_role ADD data_scope`。

### 2.2 缺口（必须补齐才能生效）
1. **`sys_user` 无 `tenant_id`** → 登录 JWT 无真实 tenant_id 维度，`TenantId=0` 根因。
2. **多数业务表无 `tenant_id` 字段**（见下表）。
3. **`datascope.FromCtx` 全仓零调用** → 查询未接作用域。
4. **业务服务未 `SetDataScope`** → 仅默认 ScopeTenant，超管/本人级别未生效。
5. **历史数据 `tenant_id=0`** → 未回填前 ScopeTenant 会过滤真实数据。

### 2.3 各业务表 `tenant_id` 现状
| 模块 | 库 | 表 | tenant_id |
|------|----|----|-----------|
| sys | sys_db | **sys_user** | ❌ 缺（根因） |
| M2 | workorder_db | work_order / work_order_flow / work_order_attachment | ❌ 缺 |
| M2 | visitor_db | visitor_record | ❌ 缺 |
| M2 | parking_db | parking_record（/ parking_fee_rule 主数据） | ❌ 缺（规则表不隔离） |
| M2 | notice_db | notice / notice_read | ❌ 缺 |
| M1 | onepark-smart-park* | device / command_log / shadow（/ product 主数据） | ❌ 缺（product 不隔离） |
| M3 | alarm_db | alarm / alarm_rule / alarm_operate_log | ✅ 已具备 |
| M5 | leasing_db | lease_contract / lease_bill | ✅ 已具备 |
| M5 | leasing_db | lease_contract_status_log | ❌ 缺 |
| M5 | dispatch_db | dispatch_task / dispatch_task_log（/ lease_zone 主数据） | ❌ 缺（zone 不隔离） |

> *M1 库名以 device-service 实际 DataSource 为准（m1 建表脚本注释指向 onepark-smart-park）。

---

## 三、总体架构（数据隔离管线）

```
请求 → 网关(Auth 解析 JWT, 注入 x-tenant-id/x-user-id/x-role-ids)
     → 业务服务 IdentityFromHeader(Header → ctxdata context)
     → logic: ① 按角色 SetDataScope(ctx, scope)  ② query.Scopes(datascope.FromCtx(ctx))
     → gorm 自动追加 WHERE tenant_id=? / create_by=? / 不过滤
```

- 网关注入的是"身份事实"，业务服务只信 context，不信任前端直传参数。
- `datascope.FromCtx` 为纯函数作用域，便于单测，不侵入业务 SQL。
- 设计为 **opt-in**：仅业务数据表查询调用 `.Scopes`，全局配置/主数据表（sys_menu、product、lease_zone…）不调用即"全部可见"。

---

## 四、分步改造清单

> 原则：单一职责、最小变更、每步可独立验证、DB 变更先于 code 落地。

### 步骤 0 — 执行 DB 变更（脚本已备，待评审）
1. `deploy/sql/rbac_data_scope.sql`（sys_role.data_scope）
2. `deploy/sql/l2_tenant_id_migration.sql`（上述缺列表 + 索引）
3. 历史数据回填（见脚本文末说明；回填与步骤 2 配套上线，或上线初期 `data_scope` 默认 1 过渡）

### 步骤 1 — 业务 model 加 `TenantId` 字段（gorm tag）
- 为 2.3 中"❌ 缺"的表在对应 gorm model struct 增加 `TenantId int64 `gorm:"column:tenant_id"`。
- 说明：`tenant_id` 列已存在（步骤0）才能被 gorm 读写；model 加字段不改变表结构。

### 步骤 2 — logic 接入 `datascope.FromCtx`（opt-in）
- 在业务数据表的列表/详情/更新查询处追加 `.Scopes(datascope.FromCtx(ctx))`。
- 写入（create）时由业务 logic 从 `ctxdata.GetTenantId(ctx)` 填充 `tenant_id`，保证新数据自带隔离维度。
- 仅对"❌ 缺且属业务数据"的表接入；主数据表不接。

### 步骤 3 — 业务服务按角色 `SetDataScope`
- 在鉴权/上下文装配处，依据 `sys_user` 角色对应的 `sys_role.data_scope` 写入 `ctxdata.SetDataScope`：
  - 超管（data_scope=1）→ ScopeAll
  - 普通运营（data_scope=2）→ ScopeTenant
  - 个人数据（data_scope=4，且表有 `create_by`）→ ScopeSelf；无 `create_by` 字段的表降级 ScopeTenant。

### 步骤 4 — 网关注入真实 `tenant_id`
- 确保登录链路（`auth-service`）从 `sys_user.tenant_id`（步骤0 补列+回填）写入 JWT claim `tenant_id`，网关透传 `x-tenant-id`。
- 当前 `DefaultTenantId: 1` 仅作无鉴权兜底，Auth 开启后由真实 claim 覆盖。

### 步骤 5 — 验证与灰度
- 单测：`datascope.whereOf` 三级别 WHERE 断言（已为纯函数）。
- 集成：带超管/普通/个人 token 分别查同表，验证返回集差异。
- 灰度：先对一个模块（建议 M5 leasing/billing，已具备 tenant_id）全链路跑通，再推广。

---

## 五、关键决策点

1. **device 的 `park_id`(VARCHAR) 与 `tenant_id`(BIGINT) 异构**：并存，datascope 以 tenant_id 为准；后续可评估统一为 tenant_id。
2. **ScopeSelf 依赖 `create_by`**：仅含 `create_by` 字段的表启用本人级别，否则降级租户级别。
3. **主数据/全局配置不隔离**：product、lease_zone、parking_fee_rule、sys_* 系列不调 `.Scopes`。
4. **默认 `data_scope` 兜底**：未显式设置时 `datascope` 默认 ScopeTenant；上线过渡期可临时默认 1（全部）避免误过滤。
5. **M2 建表 SQL 缺失**：`deploy/sql` 无 M2 建表脚本，表结构以 gorm model 为准；本迁移脚本对已知表名加列，执行前需确认表已存在。

---

## 六、风险与回滚

| 风险 | 缓解 |
|------|------|
| 未回填 tenant_id 即上线 → 真实数据被 ScopeTenant 过滤 | 步骤0 回填与步骤2 配套；或过渡期默认 data_scope=1 |
| ALTER 加列锁表（大表） | 选低峰执行；MySQL 8.0  Online DDL 多数 ALTER 不锁表 |
| M1 库名错位导致 ALTER 报错 | 执行前核对 device-service DataSource |
| 主数据误隔离 | opt-in 设计，主数据表不调 .Scopes |

回滚：本 SQL 仅加列，回滚用 `ALTER TABLE <t> DROP COLUMN tenant_id, DROP KEY idx_tenant`；logic 改动按提交分模块 revert。

---

## 七、文件清单

| 文件 | 动作 | 说明 |
|------|------|------|
| `deploy/sql/rbac_data_scope.sql` | 已备 | sys_role.data_scope |
| `deploy/sql/l2_tenant_id_migration.sql` | 新增 | 本方案 tenant_id 加列脚本 |
| `docs/L2-RBAC数据隔离落地方案.md` | 新增 | 本方案 |
| 各业务 `internal/model/*.go` | 待步骤1 | 加 TenantId 字段 |
| 各业务 `internal/logic/*.go` | 待步骤2/3 | 接入 .Scopes + SetDataScope |

---

## 八、与 M6 整改的关系

- L1（已完成）：中间件统一 + 身份管道打通，使 `ctxdata` 在全链路可用——本方案的前提。
- 本方案（L2）：在 L1 管道之上补全"数据权限"闭环，是 M6 缺口清单"RBAC 数据隔离"的落地路径。
- M6 文档（m6-公共基础服务问题检测与修改方案.md）未覆盖业务表 tenant_id 落地，本方案补其范围。
