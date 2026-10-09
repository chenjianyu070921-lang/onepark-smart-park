import { request } from './client'

// 工作台聚合数据. 字段以 dashboard-service 出参为准;
// 页面对该接口做容错处理, 后端未就绪时工作台仍可渲染.
export function getDashboardHome() {
  return request<Record<string, unknown>>({ url: '/dashboard/home', method: 'get', silent: true })
}

// ---- 指挥大屏 ----

// 四路卡片。**可空**: 该路不可用时后端给 null, 且源名会出现在 degraded 里。
// 页面必须把 null 渲染成「暂不可用」, 不能当 0 —— 0 会让"没有数据"和"数据是 0"混在一起。
export interface WorkOrderCard {
  available: boolean
  today_total: number
  unfinished: number
  avg_handle_sec: number | null // 可空: 上游契约没提供时不给 0 冒充
  complete_rate: number
}

export interface AlarmCard {
  total: number
  critical: number
  major: number
  minor: number
  info: number
}

export interface DeviceCard {
  total: number
  online: number
  offline: number
  fault: number
}

export interface EnergyCard {
  total_water: number | null
  total_power: number | null
  // 上游字段随契约变化, 这里不写死, 由页面按已知字段择要展示
  [k: string]: unknown
}

export interface OverviewResp {
  work_order: WorkOrderCard | null
  alarm: AlarmCard | null
  device: DeviceCard | null
  energy: EnergyCard | null
  degraded: string[] | null
  cached: boolean
  updated_at: number // 秒级时间戳
  elapsed_ms: number
}

export function getOverview() {
  return request<OverviewResp>({ url: '/dashboard/overview', method: 'get' })
}

// WS 推送消息。与 internal/wsserver 的 snapshotMsg / 事件消息对齐。
export interface WsSnapshot {
  type: 'snapshot'
  data: OverviewResp
}

export interface WsEvent {
  type: 'event'
  source?: string
  topic?: string
  received_at?: number
  [k: string]: unknown
}
