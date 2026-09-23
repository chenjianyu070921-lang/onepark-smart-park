import { request } from './client'

// 字段与 app/user-manage/internal/types 对齐.

export interface UserInfo {
  id: number
  username: string
  nickname: string
  status: number
  created_at: string
}

export interface RoleInfo {
  id: number
  role_key: string
  role_name: string
  remark: string
}

export interface MenuInfo {
  id: number
  parent_id: number
  menu_key: string
  menu_name: string
  permission: string
  path: string
  sort: number
}

export function listUsers() {
  return request<{ list: UserInfo[]; total: number }>({ url: '/users', method: 'get' })
}

export function createUser(data: {
  username: string
  password: string
  nickname: string
  status: number
  role_id: number
}) {
  return request<{ id: number }>({ url: '/users', method: 'post', data })
}

export function updateUser(data: { id: number; nickname: string; status: number }) {
  return request<{ id: number }>({ url: '/users', method: 'put', data })
}

export function deleteUser(id: number) {
  return request<void>({ url: `/users/${id}`, method: 'delete' })
}

export function listRoles() {
  return request<{ list: RoleInfo[]; total: number }>({ url: '/roles', method: 'get' })
}

export function createRole(data: { role_key: string; role_name: string; remark?: string }) {
  return request<{ id: number }>({ url: '/roles', method: 'post', data })
}

export function assignRole(data: { user_id: number; role_id: number }) {
  return request<void>({ url: '/users/roles', method: 'post', data })
}

// 不传 role_id: 返回全量菜单目录(角色授权面板用).
// 传 role_id: 只返回该角色已授权的菜单(侧边栏用), 见 usermanage.api MenuListReq.
export function listMenus(roleId?: number) {
  return request<{ list: MenuInfo[]; total: number } | MenuInfo[]>({
    url: '/menus',
    method: 'get',
    params: roleId && roleId > 0 ? { role_id: roleId } : undefined,
  })
}

// 侧边栏专用: 静默失败(silent), 权限服务不可用时降级为全量菜单, 不弹错误提示.
export function listRoleMenus(roleId: number) {
  return request<{ list: MenuInfo[]; total: number } | MenuInfo[]>({
    url: '/menus',
    method: 'get',
    params: { role_id: roleId },
    silent: true,
  })
}

export function createMenu(data: {
  parent_id: number
  menu_key: string
  menu_name: string
  permission: string
  path: string
  sort: number
}) {
  return request<{ id: number }>({ url: '/menus', method: 'post', data })
}

export function assignMenu(data: { role_id: number; menu_id: number }) {
  return request<void>({ url: '/roles/menus', method: 'post', data })
}
