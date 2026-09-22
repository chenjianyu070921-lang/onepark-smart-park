import { request } from './client'

// 字段与 app/workorder-service/internal/types 对齐.

export interface WorkOrderItem {
  id: number
  order_no: string
  type: number
  title: string
  status: number
  priority: number
  assignee_id: number
  created_at: number
}

export interface WorkOrderDetail extends WorkOrderItem {
  tenant_id: number
  description: string
  location: string
  reporter_id: number
  department_id: number
  attachments: string
  version: number
  updated_at: number
  finished_at?: number
}

export interface WorkOrderListResp {
  total: number
  list: WorkOrderItem[]
}

export interface WorkOrderStatistics {
  total: number
  pending_count: number
  today_count: number
  completed_today: number
  avg_process_minutes: number
  completion_rate: number
}

// 工单类型: 1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他
export const WORKORDER_TYPE: Record<number, string> = {
  1: '报修',
  2: '投诉',
  3: '巡检',
  4: '保洁',
  5: '装修',
  6: '搬运',
  7: '其他',
}

// 状态: 0待派单 1处理中 2待验收 3已完成 4已关闭
export const WORKORDER_STATUS: Record<number, string> = {
  0: '待派单',
  1: '处理中',
  2: '待验收',
  3: '已完成',
  4: '已关闭',
}

// 优先级: 1紧急 2普通 3低
export const WORKORDER_PRIORITY: Record<number, { label: string; color: string }> = {
  1: { label: '紧急', color: 'red' },
  2: { label: '普通', color: 'blue' },
  3: { label: '低', color: 'default' },
}

export function listWorkOrders(params: {
  status?: number
  type?: number
  assignee_id?: number
  page: number
  page_size: number
}) {
  return request<WorkOrderListResp>({ url: '/workorders', method: 'get', params })
}

export function getWorkOrder(id: number) {
  return request<WorkOrderDetail>({ url: `/workorder/${id}`, method: 'get' })
}

export function createWorkOrder(data: {
  type: number
  title: string
  description: string
  priority: number
  location: string
}) {
  return request<{ id: number; order_no: string; status: number }>({
    url: '/workorder',
    method: 'post',
    data,
  })
}

export function assignWorkOrder(id: number, data: { assignee_id: number; department_id?: number }) {
  return request<{ id: number; order_no: string; status: number }>({
    url: `/workorder/${id}/assign`,
    method: 'post',
    data,
  })
}

// action: submit approve reject close
export function updateWorkOrderStatus(id: number, action: string, remark?: string) {
  return request<{ id: number; order_no: string; status: number }>({
    url: `/workorder/${id}/status`,
    method: 'post',
    data: { action, remark },
  })
}

export function getWorkOrderStatistics() {
  return request<WorkOrderStatistics>({ url: '/workorder/statistics', method: 'get' })
}
