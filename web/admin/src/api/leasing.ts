import { request } from './client'

// 字段与 app/leasing-service/leasing.api 对齐.
// 金额字段(monthly_rent/deposit/amount)后端用 decimal 字符串承载, 前端不做数值运算,
// 仅用 utils/format 的 fmtMoney 展示, 避免浮点精度问题.

export interface Contract {
  id: number
  contract_no: string
  tenant_id: number
  tenant_name: string
  zone_code: string
  area_sqm: number
  monthly_rent: string
  deposit: string
  start_date: string
  end_date: string
  status: number
  auto_renew: number
  renew_notice_days: number
  created_at: number
  updated_at: number
}

// 合同状态: 1待生效 2生效中 3已到期 4已终止
export const CONTRACT_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '待生效', color: 'blue' },
  2: { label: '生效中', color: 'green' },
  3: { label: '已到期', color: 'default' },
  4: { label: '已终止', color: 'red' },
}

export interface ExpiringContract {
  id: number
  contract_no: string
  tenant_id: number
  tenant_name: string
  zone_code: string
  monthly_rent: string
  end_date: string
  days_left: number
  status: number
  auto_renew: number
  need_notice: boolean
}

export interface Zone {
  id: number
  zone_code: string
  zone_name: string
  total_area_sqm: number
  created_at: number
  updated_at: number
}

export interface Bill {
  id: number
  bill_no: string
  contract_id: number
  tenant_id: number
  billing_period: string
  amount: string
  status: number
  created_at: number
  updated_at: number
}

// 账单状态: 1未缴 2已缴
export const BILL_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '未缴', color: 'orange' },
  2: { label: '已缴', color: 'green' },
}

export interface OccupancyResp {
  total_area_sqm: number
  leased_area_sqm: number
  occupancy_rate: number
}

export interface BillSummaryResp {
  period: string
  bill_count: number
  unpaid_count: number
  unpaid_amount: string
  paid_count: number
  paid_amount: string
  total_amount: string
}

// ==================== 合同 ====================

export function listContracts(params: {
  status?: number
  tenant_id?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: Contract[] }>({
    url: '/lease/contracts',
    method: 'get',
    params,
  })
}

export function getContract(id: number) {
  return request<{ contract: Contract }>({ url: `/lease/contract/${id}`, method: 'get' })
}

export function createContract(data: {
  tenant_id: number
  tenant_name: string
  zone_code: string
  area_sqm: number
  monthly_rent: string
  deposit?: string
  start_date: string
  end_date: string
  auto_renew?: number
  renew_notice_days?: number
}) {
  return request<{ id: number; contract_no: string; status: number }>({
    url: '/lease/contract',
    method: 'post',
    data,
  })
}

// action: activate 生效 | renew 续签 | terminate 终止 | expire 到期 | update 变更
export function updateContract(
  id: number,
  data: {
    action: string
    new_end_date?: string
    monthly_rent?: string
    reason?: string
    auto_renew?: number
    renew_notice_days?: number
  },
) {
  return request<{ id: number; status: number }>({
    url: `/lease/contract/${id}`,
    method: 'put',
    data,
  })
}

export function listExpiringContracts(params: {
  days?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: ExpiringContract[] }>({
    url: '/lease/contracts/expiring',
    method: 'get',
    params,
  })
}

// ==================== 入驻率与可租区域 ====================

export function getOccupancy(params: { tenant_id?: number } = {}) {
  return request<OccupancyResp>({ url: '/lease/occupancy', method: 'get', params })
}

export function listZones(params: { page: number; page_size: number }) {
  return request<{ total: number; list: Zone[] }>({
    url: '/lease/zones',
    method: 'get',
    params,
  })
}

// zone_code 为业务唯一键, 已存在则更新面积/名称(后端 upsert 语义).
export function upsertZone(data: { zone_code: string; zone_name?: string; total_area_sqm: number }) {
  return request<{ id: number; zone_code: string }>({
    url: '/lease/zone',
    method: 'post',
    data,
  })
}

// ==================== 租金账单 ====================

export function listBills(params: {
  contract_id?: number
  period?: string
  status?: number
  tenant_id?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: Bill[] }>({
    url: '/lease/bills',
    method: 'get',
    params,
  })
}

export function getBillSummary(params: { period?: string; tenant_id?: number } = {}) {
  return request<BillSummaryResp>({ url: '/lease/bills/summary', method: 'get', params })
}

// period 为空时后端取上一自然月; 幂等, 已出账的会 skipped.
export function generateBills(data: { period?: string } = {}) {
  return request<{ period: string; created: number; skipped: number }>({
    url: '/lease/bill/auto',
    method: 'post',
    data,
  })
}

// action: pay 标记已缴 | unpay 撤销缴费
export function updateBillStatus(id: number, action: 'pay' | 'unpay') {
  return request<{ id: number; status: number }>({
    url: `/lease/bill/${id}/status`,
    method: 'put',
    data: { action },
  })
}
