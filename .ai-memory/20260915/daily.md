# 2026-09-15

## [09:05] - 文档生成: M6 公共基础服务问题检测与修改方案

- **文件**: docs/m6-公共基础服务问题检测与修改方案.md
- **决策**: M6 检测结论 blocked（P1×2：gateway go.mod x/text v1.34.0 版本错误致编译失败、JwtSecret 明文弱密钥）；P2×4（端口 8888 三服务冲突、HttpStatus E 级全 500、响应格式不统一、JWT 链路空转）；P3×2（ES healthcheck 无 curl、TDengine DSN 密码错）；修复分三批执行
- **验证**: `go build ./gateway/...` 实证失败（unrecognized import path），go.sum 仅含 v0.34.0 失配确认；P3 两项标待确认（需 Docker 实跑）
- **上下文**: 分支 hanxia HEAD=040d8ee，gateway 有未提交改动（JWT 中间件 + go.mod/config/yaml）

## [10:55] - 代码审查: M6-P0-1~4 任务完成度核查（证据驱动）

- **文件**: gateway(proxy/apigateway.go/yaml)、app/auth-service(jwt/middleware/login/refresh/validate)、common/kafka/topics.go、deploy/docker-compose.yml、app/device-service/Dockerfile
- **决策**: P0-1 实现完成但 upstream 端口 12/13 失配（仅 workorder 8082 对齐）；P0-2 全部实测通过（login/validate 200/401/401/refresh 200）；P0-3 topic 与 M2 文档一致（onepark.workorder.status_changed）；P0-4 顶层 name: 字段与 Compose v2.0.0 不兼容=启动阻断，需删 name 行
- **验证**: go build gateway/auth/common 通过；curl 实测登录/鉴权/刷新；stub 8082 实证网关转发路径透传；docker compose config 报 Additional property name is not allowed
- **发现**: workorder yaml 环境变量无默认值致无中间件环境启动失败；.env.example 缺 JWT_SECRET 占位；ES healthcheck 仍用 curl

## [12:10] - 代码审查: M6-P0 二次自检（用户已修复部分 + 新回归）

- **修复确认**: 端口 13/13 对齐（各服务 yaml 已改）；http_status.go 按错误码精确映射；compose 删除 name:、ES healthcheck 改 wget、device 端口三层对齐(8001)；response.Init() 接入 16 服务
- **新回归 P2**: response.Ok(19处/6服务: auth/device/notice/parking/visitor/workorder) + response.Init() 全局 OkHandler 双重包装 → 登录响应 data 内再嵌 {code,msg,data}（curl 实测）
- **验证**: 全量 go build 20 模块零失败；stub 实测网关 devices→8001/workorders→8082 透传；auth 8088 登录/401 实测
- **残留**: .env.example 无 JWT_SECRET；device Dockerfile EXPOSE 8888 未同步 8001；minio healthcheck curl 待确认
- **修复方向**: response.Ok → httpx.OkJsonCtx 统一走全局包装

## [19:30] - 第三轮自检: user-manage 全量实现 + common/lock+idgen + gateway 中间件

- **确认修复(上轮遗留)**: http_status 补 429/502 映射; device Dockerfile EXPOSE 8001; .env.example 补 JWT_SECRET; compose 挂载 sys.sql; proxy ErrorHandler 改 response.Fail(ErrBadGateway/ErrNotFound); gateway 测试 4/4 PASS
- **新增代码质量**: lock(看门狗+token Lua) ✓ / idgen(雪花+回拨报错) ✓ / ratelimit(令牌桶+优雅降级) ✓ / accesslog(statusWriter) ✓ / user-manage(软删除+bcrypt+幂等分配+权限链路) ✓; 18 模块 go build 全绿
- **新发现 P2**: ①response.Ok 扩至 29 处(7 服务: +user-manage 10 处全用) → 双重包装未修; ②UserList 硬编码 page=1,size=20(usermanage api 无分页参数); ③RBAC 三套并存(auth 内存用户 / init.sql user_db 五表 / sys.sql sys_db 五表), init.sql RBAC 成死代码
- **新发现 P3**: UserCreate status=0 被吞强制 1; Create*Req 无必填校验; sys.sql 无种子数据; gateway 无 user-manage upstream(/api/users 经网关 404)
- **验证缺口**: user-manage 需 MySQL 未实测; docker compose 全环境未跑

## [20:10] - 修复: response.Ok 双重包装 29 处(已授权)

- **改动**: 23 文件 29 处 `response.Ok(w, x)` → `httpx.OkJsonCtx(r.Context(), w, x)`（含 response.Ok(w,nil)→httpx.OkJsonCtx(r.Context(),w,nil)）; response.Fail 保留(WriteJson 单层正确); 无需动 import(全文件已引 httpx)
- **验证**: 7 服务编译绿; gateway 测试 PASS; auth 实测 login/validate 单层 {code,msg,data:{...}}, 401 单层 M6-E-0002 ✓
- **遗留待决策**: P2-2 user-manage 分页参数; P2-3 RBAC 三套并存(init.sql user_db vs sys.sql sys_db vs auth 内存用户); P3-1 status=0 被吞; P3-2 无必填校验; P3-4 gateway 无 user-manage upstream

## [21:00] - 启动验证 + 修复 conf 环境变量替换(重大)

- **实测**: user-manage 首次起不来——go-zero conf.MustLoad 默认不做 env 替换(需 conf.UseEnv()); 且 `${VAR:-default}` 语法不受支持(变量名含 :- 查不到→空)
- **安全回归**: auth JWT 密钥此前是字面量 `${JWT_SECRET}`(固定密钥≈硬编码); 修复后未设环境变量→空→panic(servicecontext.go:29 已有检查)
- **修复**: ①21 个服务 main 加 `conf.UseEnv()`(先误写 WithEnv, v1.10.3 函数名是 UseEnv); ②3 个 yaml 6 处 `${VAR:-default}`→`${VAR}`(user-manage/device/gateway), 默认值改由 compose environment 注入; ③导入 sys.sql 建 sys_db
- **端到端验证通过**: auth 登录/validate/401 单层; user-manage 列表/创建 200 单层(DB 实连); gateway 鉴权链(无 token 401 → auth token 放行 → 转发 502 M6-E-0008 / 404 M6-E-0004 / skip 生效); token 跨服务兼容(auth 签发 gateway 解析); 21 模块编译绿; gateway 测试全过(含新增 auth_test)
- **注意**: 本机 mysql80 密码 root123456(非 onepark123); 本地起服务需设 MYSQL_*/REDIS_*/JWT_SECRET 环境变量; gateway 新增 Auth 中间件(common/jwt 包, AuthSkipPaths=/api/auth/login,/api/auth/refresh)
