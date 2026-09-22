import axios from 'axios'
import type { ApiBody, LoginResp } from './types'

export async function login(username: string, password: string): Promise<LoginResp> {
  // 登录/刷新走裸 axios: 此时尚无 token, 且失败不该触发统一拦截器的续期逻辑.
  const { data } = await axios.post<ApiBody<LoginResp>>('/api/auth/login', {
    username,
    password,
  })
  if (data.code !== '0' || !data.data) throw new Error(`${data.code} ${data.msg}`)
  return data.data
}

export async function logout(token: string): Promise<void> {
  await axios.post<ApiBody<void>>('/api/auth/logout', { token }).catch(() => undefined)
}

export async function refreshToken(rt: string): Promise<LoginResp> {
  const { data } = await axios.post<ApiBody<LoginResp>>('/api/auth/refresh', {
    refreshToken: rt,
  })
  if (data.code !== '0' || !data.data) throw new Error(`${data.code} ${data.msg}`)
  return data.data
}
