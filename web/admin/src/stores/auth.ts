import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  token: string | null
  refreshToken: string | null
  userId: number | null
  /** 后端原始 roleIds 字符串(逗号分隔的角色ID), RBAC 过滤升级时配合 /api/roles 映射为 role_key. */
  roleIds: string
  tenantId: number | null
  nickname: string

  setSession: (s: Partial<Pick<AuthState, 'token' | 'refreshToken' | 'userId' | 'roleIds' | 'tenantId' | 'nickname'>>) => void
  logout: () => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      refreshToken: null,
      userId: null,
      roleIds: '',
      tenantId: null,
      nickname: '',

      setSession: (s) => set(s),
      logout: () =>
        set({
          token: null,
          refreshToken: null,
          userId: null,
          roleIds: '',
          tenantId: null,
          nickname: '',
        }),
    }),
    { name: 'onepark-auth' },
  ),
)

export const isLoggedIn = () => useAuthStore.getState().token !== null
