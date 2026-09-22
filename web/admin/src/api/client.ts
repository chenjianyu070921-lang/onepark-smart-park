import axios, { AxiosError, type AxiosRequestConfig } from 'axios'
import { message } from 'antd'
import type { ApiBody } from './types'
import { useAuthStore } from '../stores/auth'

// 业务错误: 携带后端错误码, 供页面按 code 精细处理.
export class ApiError extends Error {
  code: string
  constructor(code: string, msg: string) {
    super(msg)
    this.code = code
  }
}

const instance = axios.create({
  baseURL: '/api',
  timeout: 15000,
})

// 请求拦截: 注入 access token.
instance.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

// 单飞刷新: 并发 401 时只发一次 refresh, 其余请求等待复用同一 Promise.
let refreshing: Promise<string | null> | null = null

async function refreshAccessToken(): Promise<string | null> {
  const { refreshToken: rt } = useAuthStore.getState()
  if (!rt) return null
  try {
    // 裸 axios: 刷新请求本身不能再走统一拦截器, 否则 401 死循环.
    const { data } = await axios.post<ApiBody<{ token: string; refreshToken: string }>>(
      '/api/auth/refresh',
      { refreshToken: rt },
    )
    if (data.code !== '0' || !data.data) return null
    useAuthStore.getState().setSession({
      token: data.data.token,
      refreshToken: data.data.refreshToken,
    })
    return data.data.token
  } catch {
    return null
  }
}

async function tryRefreshAndRetry(
  config: (AxiosRequestConfig & { _retried?: boolean }) | undefined,
): Promise<unknown | null> {
  refreshing = refreshing ?? refreshAccessToken()
  const newToken = await refreshing
  refreshing = null
  if (!newToken || !config || config._retried) return null
  config._retried = true
  config.headers = { ...config.headers, Authorization: `Bearer ${newToken}` }
  // instance 的响应拦截器会把重放结果再次解包, 直接透传.
  return instance.request(config)
}

function onAuthDead() {
  useAuthStore.getState().logout()
  if (!location.pathname.startsWith('/login')) {
    location.href = '/login'
  }
}

// 响应拦截: 解包 {code,msg,data}; 鉴权失败静默续期并重放一次.
// 返回值已被解包为业务 data, 与 axios 类型声明不符, 统一用 any 视图收窄.
instance.interceptors.response.use(
  async (resp): Promise<any> => {
    const body = resp.data as ApiBody

    if (body.code === 'M6-E-0002' || resp.status === 401) {
      const retried = await tryRefreshAndRetry(resp.config)
      if (retried !== null) return retried
      onAuthDead()
      throw new ApiError(body.code, body.msg)
    }

    if (body.code === '0') return body.data

    if (!resp.config.silent) message.error(`${body.code} ${body.msg}`)
    throw new ApiError(body.code, body.msg)
  },
  async (err: AxiosError<ApiBody>): Promise<any> => {
    if (err.response?.status === 401) {
      const retried = await tryRefreshAndRetry(err.config)
      if (retried !== null) return retried
      onAuthDead()
      throw new ApiError('M6-E-0002', '登录已过期, 请重新登录')
    }
    const msg = err.response?.data?.msg ?? err.message
    if (!err.config?.silent) message.error(`请求失败 ${msg}`)
    throw new ApiError(err.response?.data?.code ?? 'NETWORK', msg)
  },
)

// 统一请求入口: 返回已解包的 data.
export async function request<T>(config: AxiosRequestConfig): Promise<T> {
  return instance.request<ApiBody<T>>(config) as unknown as Promise<T>
}
