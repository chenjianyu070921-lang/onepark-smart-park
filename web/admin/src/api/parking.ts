import { request } from './client'

// 字段与 app/parking-service/internal/types 对齐.

export const VEHICLE_TYPE: Record<number, string> = {
  1: '月卡',
  2: '临时',
  3: 'VIP',
  4: '异常',
}

export const PARKING_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '停车中', color: 'green' },
  2: { label: '已完成', color: 'default' },
}

export interface ParkingItem {
  id: number
  plate_no: string
  entry_time: number
  exit_time?: number
  status: number
  fee?: string
}

export function getActiveParking(params: { page: number; page_size: number }) {
  return request<{ total: number; list: ParkingItem[] }>({
    url: '/parking/active',
    method: 'get',
    params,
  })
}

export function listParkingRecords(params: {
  status?: number
  plate_no?: string
  page: number
  page_size: number
}) {
  return request<{ total: number; list: ParkingItem[] }>({
    url: '/parking/records',
    method: 'get',
    params,
  })
}

export function parkingEntry(data: { plate_no: string; vehicle_type: number; device_id_in: string }) {
  return request<ParkingItem>({ url: '/parking/entry', method: 'post', data })
}

export function parkingExit(data: { plate_no: string; device_id_out: string }) {
  return request<ParkingItem & { duration_min?: number }>({
    url: '/parking/exit',
    method: 'post',
    data,
  })
}

export interface MonthlyCardItem {
  id: number
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: number
  end_time: number
  status: number
  days_left?: number
}

export function listMonthlyCards(params: {
  status?: number
  plate_no?: string
  expiring_days?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: MonthlyCardItem[] }>({
    url: '/parking/monthly-cards',
    method: 'get',
    params,
  })
}

export function createMonthlyCard(data: {
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: number
  end_time: number
}) {
  return request<MonthlyCardItem>({ url: '/parking/monthly-card', method: 'post', data })
}

export function renewMonthlyCard(id: number, endTime: number) {
  return request<MonthlyCardItem>({
    url: '/parking/monthly-card/renew',
    method: 'post',
    data: { id, end_time: endTime },
  })
}

export function disableMonthlyCard(id: number) {
  return request<MonthlyCardItem>({
    url: '/parking/monthly-card/disable',
    method: 'post',
    data: { id },
  })
}

export interface FeeRule {
  id: number
  free_minutes: number
  hourly_fee: number
  daily_cap: number
  effective_from?: number
  effective_to?: number
}

export function getFeeRule() {
  return request<{ rule: FeeRule | null }>({ url: '/parking/fee-rule', method: 'get' })
}

export function upsertFeeRule(data: {
  free_minutes: number
  hourly_fee: number
  daily_cap?: number
}) {
  return request<FeeRule>({ url: '/parking/fee-rule', method: 'post', data })
}
