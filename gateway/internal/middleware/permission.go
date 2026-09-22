package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/redisx"
	"onepark/common/response"
	userpb "onepark/proto/user"
)

// Permission 网关统一 RBAC 中间件: 在鉴权(Auth)之后、转发之前运行.
// 仅对"受保护写/读操作"(注册表命中)调用 user-manage gRPC CheckPermission 校验权限,
// 无权返回 403; 注册表未命中(其余 18 服务写操作)默认放行(渐进覆盖策略, 见方案 A).
//
// 缓存: 以 (roleIds, permission) 为键缓存校验结果到 Redis(TTL 5min), 避免每次请求回查 user-manage,
// 同角色批量请求命中率高, 显著提升性能; Redis 不可用时自动降级为实时调用.
//
// 身份来源: user_id / role_ids 由前置 Auth 中间件从 JWT 校验结果注入(可信), 作为 CheckPermission 入参.
func Permission(client userpb.UserManageClient, rdb *redisx.Client) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			perm, ok := matchPermission(r.Method, r.URL.Path)
			if !ok {
				// 未命中注册表: 不在本期 RBAC 覆盖范围内, 默认放行(不破坏其他服务).
				next(w, r)
				return
			}
			userID := atoi64(r.Header.Get(ctxdata.CtxUserId))
			if userID == 0 {
				// 鉴权未注入身份(理论不会到这, Auth 已拦截匿名), 按无权限拒绝(fail-closed).
				response.Fail(w, errorx.NewError(errorx.ErrForbidden, "缺少已鉴权用户身份"))
				return
			}
			roleIDs := r.Header.Get(ctxdata.CtxRoleIds)

			// 1) 查 Redis 缓存: 同 (角色, 权限) 共享结果, 减少 gRPC 回查.
			cacheKey := fmt.Sprintf("gw:perm:%s:%s", roleIDs, perm)
			if rdb != nil {
				if v, err := rdb.Get(r.Context(), cacheKey).Result(); err == nil {
					if v == "1" {
						next(w, r)
						return
					}
					if v == "0" {
						response.Fail(w, errorx.NewError(errorx.ErrForbidden, "无权限: "+perm))
						return
					}
				}
			}

			// 2) 缓存未命中: 调 user-manage gRPC CheckPermission(实时).
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			resp, err := client.CheckPermission(ctx, &userpb.CheckPermissionReq{
				UserId:     uint64(userID),
				Permission: perm,
			})
			allowed := err == nil && resp != nil && resp.Allowed
			// 回填缓存(无论结果, 短 TTL 避免权限变更后长时间不一致).
			if rdb != nil {
				val := "0"
				if allowed {
					val = "1"
				}
				_ = rdb.Set(r.Context(), cacheKey, val, 5*time.Minute).Err()
			}
			if !allowed {
				response.Fail(w, errorx.NewError(errorx.ErrForbidden, "无权限: "+perm))
				return
			}
			next(w, r)
		}
	}
}

// permRule 单条路由->权限映射.
type permRule struct {
	prefix     string // 路径前缀(最长前缀优先匹配, 可含 :id 段, 匹配时按前缀剥离尾段)
	permission string // 目标权限串, 对齐 deploy/sql/sys.sql 的 sys_menu.permission
}

// permRegistry 受保护路由注册表(渐进覆盖: 已接入 user-manage + 保安/物业经理业务域).
// 同方法下按前缀从长到短匹配, 保证 /api/alarm/:id/status 优先于 /api/alarm、/api/users/roles 优先于 /api/users.
// 词表来源: deploy/sql/sys.sql 种子(sys_menu.permission, resource:action 粒度); 其余服务逐步扩表.
var permRegistry = map[string][]permRule{
	http.MethodPost: {
		{"/api/users/roles", "user:write"}, // 用户分配角色(用户管理写操作)
		{"/api/roles/menus", "menu:write"}, // 角色分配菜单(菜单权限写操作)
		{"/api/users", "user:write"},       // 新建用户
		{"/api/roles", "role:write"},       // 新建角色
		{"/api/menus", "menu:write"},       // 新建菜单
		{"/api/alarm", "alarm:write"},      // 告警规则创建 / 死信重放(仅 super_admin)
		{"/api/access", "access:write"},    // 门禁授权/撤销/远程开门(仅 super_admin)
		{"/api/workorder", "workorder:write"}, // 创建工单 / 附件(物业经理)
		{"/api/parking", "parking:write"},     // 车辆出入场 / 月卡管理(物业经理)
	},
	http.MethodPut: {
		{"/api/users", "user:write"},             // 更新用户
		{"/api/alarm/:id/status", "alarm:confirm"}, // 告警确认(保安), 最长前缀优先于 /api/alarm
		{"/api/alarm", "alarm:write"},             // 告警规则更新(仅 super_admin)
		{"/api/workorder", "workorder:write"},     // 派单 / 状态流转(物业经理)
	},
	http.MethodDelete: {
		{"/api/users", "user:write"},  // 删除用户
		{"/api/access", "access:write"}, // 撤销授权(仅 super_admin)
	},
	http.MethodGet: {
		{"/api/users", "user:read"},    // 用户列表/详情
		{"/api/roles", "role:read"},    // 角色列表
		{"/api/alarms", "alarm:read"},  // 告警列表(复数路径, 单独列)
		{"/api/alarm", "alarm:read"},   // 告警活跃/规则/详情/死信(保安)
		{"/api/access", "access:read"}, // 门禁记录(保安)
		{"/api/workorder", "workorder:read"},  // 工单详情
		{"/api/workorders", "workorder:read"}, // 工单列表(物业经理)
		{"/api/parking", "parking:read"},      // 在场车辆/停车记录/月卡(物业经理)
	},
}

// matchPermission 按 (方法, 路径) 最长前缀命中注册表, 返回目标权限串.
func matchPermission(method, path string) (string, bool) {
	rules, ok := permRegistry[method]
	if !ok {
		return "", false
	}
	// 同方法规则已按写入顺序(长前缀在前); 为稳妥按长度降序排序一次.
	sorted := make([]permRule, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].prefix) > len(sorted[j].prefix) })
	for _, rule := range sorted {
		if rule.prefix == path || strings.HasPrefix(path, rule.prefix+"/") || path == rule.prefix {
			return rule.permission, true
		}
	}
	return "", false
}

// atoi64 安全解析 int64(失败返回 0).
func atoi64(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
