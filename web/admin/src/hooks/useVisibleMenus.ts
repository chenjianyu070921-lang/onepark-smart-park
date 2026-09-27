import { useEffect, useMemo, useState } from 'react'
import { listRoleMenus } from '../api/system'
import { menuConfig, type MenuNode } from '../router/menu'
import { useAuthStore } from '../stores/auth'

// 侧边栏可见菜单: 由后端 sys_menu(经角色授权)驱动, 而不是前端硬编码.
//
// 为什么不让后端直接返回菜单树渲染: 前端菜单带图标/分组/路由组件, 这些是 UI 资产,
// 不适合塞进数据库; 后端只回答"该角色能看到哪些 menu_key", 前端用这个集合过滤本地配置.
//
// 降级策略: 未登录/无角色/接口失败/返回空 => 渲染全量菜单, 绝不因权限问题让侧边栏空白.
function filterMenu(nodes: MenuNode[], allowed: Set<string> | null): MenuNode[] {
  if (!allowed) return menuConfig
  const result: MenuNode[] = []
  for (const node of nodes) {
    const children = node.children ? filterMenu(node.children, allowed) : undefined
    // 父节点自身未授权但有可见子节点时保留(否则子菜单不可达).
    if (allowed.has(node.key) || (children && children.length > 0)) {
      result.push(children ? { ...node, children } : node)
    }
  }
  return result
}

export function useVisibleMenus(): MenuNode[] {
  const roleIds = useAuthStore((s) => s.roleIds)
  // null 表示"未拿到权限数据", 此时按全量渲染.
  const [allowedKeys, setAllowedKeys] = useState<Set<string> | null>(null)

  useEffect(() => {
    const ids = (roleIds ?? '')
      .split(',')
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isFinite(n) && n > 0)

    if (ids.length === 0) {
      setAllowedKeys(null)
      return
    }

    let alive = true
    // 多角色取并集: 任一角色授权即视为可见.
    Promise.all(ids.map((id) => listRoleMenus(id)))
      .then((resps) => {
        if (!alive) return
        const keys = new Set<string>()
        for (const resp of resps) {
          const list = Array.isArray(resp) ? resp : (resp?.list ?? [])
          for (const m of list) if (m.menu_key) keys.add(m.menu_key)
        }
        // 返回空说明该角色未配置任何菜单: 与其显示空侧边栏, 不如降级全量并告警.
        if (keys.size === 0) {
          console.warn('[menu] 角色未授权任何菜单, 降级为全量渲染; 请检查 sys_role_menu 配置')
          setAllowedKeys(null)
          return
        }
        setAllowedKeys(keys)
      })
      .catch(() => {
        if (alive) setAllowedKeys(null)
      })

    return () => {
      alive = false
    }
  }, [roleIds])

  return useMemo(() => filterMenu(menuConfig, allowedKeys), [allowedKeys])
}
