import { request } from './client'

// 工作台聚合数据. 字段以 dashboard-service 出参为准;
// 页面对该接口做容错处理, 后端未就绪时工作台仍可渲染.
export function getDashboardHome() {
  return request<Record<string, unknown>>({ url: '/dashboard/home', method: 'get', silent: true })
}
