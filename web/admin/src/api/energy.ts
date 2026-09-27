import { request } from './client'

// 字段与 app/energy-analysis-service/energyanalysis.api 对齐.
// 注意网关前缀最长匹配: /api/energy/daily|monthly|zone 走 energy-analysis(8062),
// /api/energy/* 其余走 energy-data(8061), 前端不要改这两个前缀.

export interface ZoneUsage {
  zoneId: string
  usage: number
  deviceCount: number
  percent: number
}

export interface DailyResponse {
  date: string
  totalUsage: number
  deviceCount: number
  zones: ZoneUsage[]
}

export interface DayUsage {
  date: string
  usage: number
}

export interface MonthlyResponse {
  month: string
  totalUsage: number
  avgDailyUsage: number
  days: DayUsage[]
  // 无历史数据时为 null, 前端需展示 '—' 而不是 NaN
  lastYearUsage?: number | null
  lastMonthUsage?: number | null
  yoy?: number | null
  mom?: number | null
}

export interface DeviceUsage {
  deviceId: string
  usage: number
  percent: number
  lastReading: number
  lastReportedAt: string
}

export interface ZoneDetailResponse {
  zoneId: string
  start: string
  end: string
  totalUsage: number
  deviceCount: number
  devices: DeviceUsage[]
  days: DayUsage[]
}

// 三个接口统一 silent: 能耗页自带空态与 Alert 降级,
// 不再叠加 axios 拦截器的全局 error toast(区域无数据时后端返回 M4-E-1004, 属正常语义).

// 日报: 不传 date 时后端取今天
export function getEnergyDaily(params: { date?: string } = {}) {
  return request<DailyResponse>({ url: '/energy/daily', method: 'get', params, silent: true })
}

// 月报: 不传 month 时后端取本月
export function getEnergyMonthly(params: { month?: string } = {}) {
  return request<MonthlyResponse>({ url: '/energy/monthly', method: 'get', params, silent: true })
}

// 区域详情: zoneId 必填, 起止不传默认当天 0 点 ~ 次日 0 点
export function getEnergyZone(params: { zoneId: string; start?: string; end?: string }) {
  return request<ZoneDetailResponse>({ url: '/energy/zone', method: 'get', params, silent: true })
}
