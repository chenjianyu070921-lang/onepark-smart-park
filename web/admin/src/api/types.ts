// 统一响应体 {code, msg, data}, 见 common/response.
// 成功 code === '0', 失败为 {Module}-{Level}-{Code} 如 'M1-E-1001'.
export interface ApiBody<T = unknown> {
  code: string
  msg: string
  data?: T
}

// 登录响应, 见 app/auth-service/internal/types.LoginResp.
export interface LoginResp {
  token: string
  refreshToken: string
  expire: number
  userId: number
  roleIds: string
  tenantId: number
}
