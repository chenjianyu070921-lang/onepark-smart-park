import { request } from './client'

// 字段与 app/parking-service/internal/types (parking.api 生成) 一一对齐.

export interface ParkingItem {
  id: number
  plate_no: string
  entry_time: number
  exit_time?: number
  status: number // 1停车中 2已完成
  fee?: string // decimal 字符串(避免浮点误差)
}

export interface ParkingListResp {
  total: number
  list: ParkingItem[]
}

export interface ParkingRecordResp {
  id: number
  plate_no: string
  entry_time: number
  exit_time?: number
  duration_min?: number
  fee?: string
  status: number
}

export interface MonthlyCardItem {
  id: number
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: number
  end_time: number
  status: number // 1生效 2停用
  days_left?: number
}

export interface MonthlyCardListResp {
  total: number
  list: MonthlyCardItem[]
}

export interface MonthlyCardResp {
  id: number
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: number
  end_time: number
  status: number
}

export interface ParkingFeeRuleItem {
  id: number
  free_minutes: number
  hourly_fee: number
  daily_cap: number
  effective_from?: number
  effective_to?: number
}

// 当前生效计费规则; 无配置时 rule 为 null.
export interface ParkingFeeRuleResp {
  rule?: ParkingFeeRuleItem | null
}

// 停车状态: 1停车中 2已完成
export const PARKING_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '停车中', color: 'processing' },
  2: { label: '已完成', color: 'default' },
}

// 车辆类型: 1月卡 2临时 3VIP 4异常
export const VEHICLE_TYPE: Record<number, string> = {
  1: '月卡',
  2: '临时',
  3: 'VIP',
  4: '异常',
}

// 月卡状态: 1生效 2停用
export const MONTHLY_CARD_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '生效', color: 'green' },
  2: { label: '停用', color: 'default' },
}

export function listActiveParking(params: { page: number; page_size: number }) {
  return request<ParkingListResp>({ url: '/parking/active', method: 'get', params })
}

export function listParkingRecords(params: {
  status?: number
  plate_no?: string
  page: number
  page_size: number
}) {
  return request<ParkingListResp>({ url: '/parking/records', method: 'get', params })
}

// 人工补录入场: 生产由 M1 设备 Kafka 驱动, 此接口用于联调/人工补录.
export function parkingEntry(data: { plate_no: string; vehicle_type: number; device_id_in: string }) {
  return request<ParkingRecordResp>({ url: '/parking/entry', method: 'post', data })
}

// 离场(触发计费); 人工/兜底离场同接口.
export function parkingExit(data: { plate_no: string; device_id_out: string }) {
  return request<ParkingRecordResp>({ url: '/parking/exit', method: 'post', data })
}

export function listMonthlyCards(params: {
  status?: number
  plate_no?: string
  expiring_days?: number
  page: number
  page_size: number
}) {
  return request<MonthlyCardListResp>({ url: '/parking/monthly-cards', method: 'get', params })
}

export function createMonthlyCard(data: {
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: number
  end_time: number
}) {
  return request<MonthlyCardResp>({ url: '/parking/monthly-card', method: 'post', data })
}

export function renewMonthlyCard(data: { id: number; end_time: number }) {
  return request<MonthlyCardResp>({ url: '/parking/monthly-card/renew', method: 'post', data })
}

export function disableMonthlyCard(data: { id: number }) {
  return request<MonthlyCardResp>({ url: '/parking/monthly-card/disable', method: 'post', data })
}

export function getParkingFeeRule() {
  return request<ParkingFeeRuleResp>({ url: '/parking/fee-rule', method: 'get' })
}

export function upsertParkingFeeRule(data: {
  free_minutes: number
  hourly_fee: number
  daily_cap?: number
  effective_from?: number
  effective_to?: number
}) {
  return request<ParkingFeeRuleResp>({ url: '/parking/fee-rule', method: 'post', data })
}
