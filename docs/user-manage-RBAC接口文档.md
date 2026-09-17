# user-manage RBAC 接口文档

> 服务：user-manage（M6 基础服务）｜端口 8088（经网关 8080 对外：`/api/*`）
> 数据模型：RBAC 五表（`sys_user` / `sys_role` / `sys_menu` / `sys_user_role` / `sys_role_menu`），建表见 `deploy/sql/sys.sql` + `deploy/sql/rbac_data_scope.sql`
> 鉴权：JWT 校验与租户注入已**收口到网关**，本服务仅通过 `common/middleware.IdentityFromHeader` 透传网关注入的 `x-user-id / x-role-ids / x-tenant-id / x-data-scope`，不再自行校验 JWT。

## 1. 鉴权与调用方式

- 所有 `/api/*` 请求经 `gateway` 转发；网关在 **非本地环境（prod/pre）强制校验 Bearer Token** 并注入身份 Header，本地（dev/test）默认关闭以便联调。
- 业务侧：`server.Use(middleware.IdentityFromHeader)` 将 Header 提升进 `ctxdata`，logic 通过 `ctxdata.GetUserId / GetTenantId / GetRoleIds / GetDataScope` 读取。
- `PermissionCheck` 以**网关注入的登录身份为准**（不再信任请求体 `user_id`，修复越权），仅在无身份的内部调用时回退到 `req.UserId`。

## 2. 接口列表（11 个）

> 通用响应体：`{"code":0,"msg":"ok","data":{...}}`；鉴权类错误 `code=M6-E-0002` 返回 401。

### 2.1 用户管理

| # | 方法 | 路径 | 说明 |
|---|------|------|------|
| 1 | POST | `/api/users` | 创建用户 |
| 2 | PUT  | `/api/users` | 更新用户（昵称/状态） |
| 3 | DELETE | `/api/users/:id` | 删除用户 |
| 4 | GET  | `/api/users/:id` | 用户详情 |
| 5 | GET  | `/api/users` | 用户列表 |

**创建用户** `POST /api/users`
```json
请求: {"username":"zhangsan","password":"Secret@123","nickname":"张三","status":1,"role_id":3}
响应: {"code":0,"msg":"ok","data":{"id":12}}
```
- 约束：`username` 唯一，重复返回 `M6-E-1003 用户名已存在`；`password` 以 bcrypt 存储；`status` 缺省为 1（启用）。
- `role_id` 可选（缺省 0 = 不分配角色）；传有效角色 ID 时，在用户创建成功后**内联分配**（管理员一次提交即带角色）；角色不存在返回 `M6-E-1004`。

**更新用户** `PUT /api/users`
```json
请求: {"id":12,"nickname":"张三丰","status":0}
响应: {"code":0,"msg":"ok","data":null}
```
**删除/详情/列表**
- `DELETE /api/users/12` → 用户不存在返回 `M6-E-1002`。
- `GET /api/users/12` → `{...,"data":{"id":12,"username":"zhangsan","nickname":"张三丰","status":0,"created_at":"2026-09-17 10:00:00"}}`。
- `GET /api/users` → `{"data":{"list":[...],"total":N}}`（分页固定 page=1,size=20）。

### 2.2 角色管理

| # | 方法 | 路径 | 说明 |
|---|------|------|------|
| 6 | POST | `/api/roles` | 创建角色 |
| 7 | GET  | `/api/roles` | 角色列表（前端分配角色时拉取） |
| 8 | POST | `/api/users/roles` | 用户-角色分配（幂等） |

**创建角色** `POST /api/roles`
```json
请求: {"role_key":"park_admin","role_name":"园区管理员","remark":"..."}
响应: {"code":0,"msg":"ok","data":{"id":3}}
```
- 约束：`role_key` 唯一，重复返回 `M6-E-1005 角色标识已存在`。

**角色列表** `GET /api/roles`
```json
响应: {"code":0,"msg":"ok","data":{"list":[{"id":3,"role_key":"park_admin","role_name":"园区管理员","remark":"..."}],"total":1}}
```
- 说明：返回全部角色（角色数量少，不分页）；前端在「用户-角色分配」下拉中调用此接口拉取可选项。

**分配角色** `POST /api/users/roles`
```json
请求: {"user_id":12,"role_id":3}
响应: {"code":0,"msg":"ok","data":null}
```
- 异常：用户不存在 `M6-E-1002`；角色不存在 `M6-E-1004`；已分配则幂等成功。

### 2.3 菜单 / 权限

| # | 方法 | 路径 | 说明 |
|---|------|------|------|
| 9 | POST | `/api/menus` | 创建菜单（含 permission） |
| 10 | POST | `/api/roles/menus` | 角色-菜单（权限）分配（幂等） |

**创建菜单** `POST /api/menus`
```json
请求: {"parent_id":0,"menu_key":"asset","menu_name":"资产管理","permission":"asset:read","path":"/asset","sort":1}
响应: {"code":0,"msg":"ok","data":{"id":7}}
```
- 约束：`menu_key` 唯一，重复返回 `M6-E-1007 菜单标识已存在`。

**分配权限** `POST /api/roles/menus`
```json
请求: {"role_id":3,"menu_id":7}
响应: {"code":0,"msg":"ok","data":null}
```
- 异常：角色不存在 `M6-E-1004`；菜单不存在 `M6-E-1006`；已分配则幂等成功。

### 2.4 权限校验

| # | 方法 | 路径 | 说明 |
|---|------|------|------|
| 11 | POST | `/api/permissions/check` | 校验用户是否拥有某 permission |

**校验权限** `POST /api/permissions/check`
```json
请求: {"user_id":12,"permission":"asset:read"}
响应: {"code":0,"msg":"ok","data":{"allowed":true}}
```
- 链路：`user → user_role → role_menu → menu.permission`（去重汇总）。
- 以网关注入身份为准；无身份内部调用回退 `req.UserId`；无角色返回 `allowed:false`。

## 3. 错误码

| 错误码 | 含义 |
|--------|------|
| M6-E-0002 | 未认证 / Token 无效（网关层 401） |
| M6-E-1002 | 用户不存在 |
| M6-E-1003 | 用户名已存在 |
| M6-E-1004 | 角色不存在 |
| M6-E-1005 | 角色标识已存在 |
| M6-E-1006 | 菜单不存在 |
| M6-E-1007 | 菜单标识已存在 |
| M6-E-5000 | 内部错误（DB 失败等） |

## 4. 自测（CRUD 接口自测证据）

见 `app/user-manage/internal/logic/rbac_crud_test.go`（`TestRBACCRUD`），覆盖上述 11 个接口（含 `GET /api/roles` 角色列表）的成功与异常路径：

```bash
# 1) 初始化五表
mysql -u<user> -p<pass> sys_db < deploy/sql/sys.sql

# 2) 运行自测(需真实 MySQL)
export USER_MANAGE_TEST_DSN='<user>:<pass>@tcp(127.0.0.1:3306)/sys_db?charset=utf8mb4&parseTime=true'
cd app/user-manage && go test -run TestRBACCRUD ./internal/logic/ -v
```

未配置 `USER_MANAGE_TEST_DSN` 时用例自动 `Skip`，保证无 DB 环境编译通过与 CI 绿灯。
