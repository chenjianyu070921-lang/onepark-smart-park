// Package rbac 定义 M2 物业管理模块的角色枚举与行级数据范围(scope)辅助函数.
//
// 设计依据: docs/M2物业管理服务模块设计文档.md 第四章"用户角色与权限",
// 角色由 M6 认证服务 + Gateway 注入到 context(x-role-ids, 逗号分隔),
// 本包仅做"数值枚举 + scope 判定", 业务 code 不散落 magic number.
//
// ⚠️ 角色 ID 与 M6 RBAC 五表(user_db.role)对齐; 若 M6 侧角色数值调整,
// 仅在本文件校准常量即可, 业务 logic 无需改动.
package rbac

import (
	"strconv"
	"strings"
)

// 角色 ID 枚举(与 M6 RBAC 五表对齐, 联调时若不一致以 M6 为准并在此校准).
const (
	RoleSystemAdmin  int64 = 1 // 系统管理员: 权限/系统配置(全部)
	RoleParkAdmin    int64 = 2 // 园区管理员: 查看全部工单/访客/停车, 分配工单, 发布公告
	RoleService      int64 = 3 // 物业客服: 受理工单、派单、发布公告
	RoleRepair       int64 = 4 // 物业维修人员: 接单/处理/提交完成(受限: 仅自己关联单)
	RoleOwner        int64 = 5 // 业主/企业租户: 报修、发起访客邀请、查看公告(受限: 仅自己关联)
	RoleSecurity     int64 = 6 // 保安: 审核访客、查看门禁记录
	RoleParkingAdmin int64 = 7 // 停车管理员: 管理停车记录、收费规则
)

// ParseRoleIds 解析网关注入的逗号分隔角色 ID 串(如 "3,4"), 跳过非法项.
// 入参: s 上下文中的 x-role-ids 原串(可能为空).
// 返回: 角色 ID 切片; 空串返回 nil.
func ParseRoleIds(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v, e := strconv.ParseInt(p, 10, 64); e == nil {
			out = append(out, v)
		}
	}
	return out
}

// HasRole 判断角色集合是否包含指定角色.
func HasRole(roles []int64, role int64) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsFullScope 是否拥有园区级全量数据可见权限(列表查询不加个人维度过滤).
// 全量角色: 系统管理员/园区管理员/物业客服/保安/停车管理员.
// 受限角色: 维修人员、业主(仅看与自己关联的工单/访客).
func IsFullScope(roles []int64) bool {
	for _, r := range roles {
		switch r {
		case RoleSystemAdmin, RoleParkAdmin, RoleService, RoleSecurity, RoleParkingAdmin:
			return true
		}
	}
	return false
}

// CanManageWorkOrder 是否拥有工单写操作权限(派单/状态流转不受单人限制).
// 含: 系统管理员/园区管理员/物业客服. 维修/业主仅能操作与自己关联的单(见 logic 内校验).
func CanManageWorkOrder(roles []int64) bool {
	return HasRole(roles, RoleSystemAdmin) || HasRole(roles, RoleParkAdmin) || HasRole(roles, RoleService)
}
