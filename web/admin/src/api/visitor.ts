import { request } from './client'

// 字段与 app/visitor-service/internal/types 对齐.

// 状态: 1待使用 2已签入 3已签出 4已过期
export const VISITOR_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '待使用', color: 'gold' },
  2: { label: '已签入', color: 'green' },
  3: { label: '已签出', color: 'default' },
  4: { label: '已过期', color: 'red' },
}

// 核验方式: 1二维码 2手机号 3身份证 4人脸 5工牌
export const VERIFY_CHANNEL: Record<number, string> = {
  1: '二维码',
  2: '手机号',
  3: '身份证',
  4: '人脸',
  5: '工牌',
}

export interface VisitorItem {
  id: number
  visitor_name: string
  visitor_phone: string
  status: number
  visit_time: number
  checkin_at?: number
  checkout_at?: number
  device_id?: string
}

export interface VisitorTrackItem {
  action: string
  time: number
  device_id?: string
  remark?: string
}

export interface VisitorDetail extends VisitorItem {
  inviter_id: number
  expire_time: number
  track: VisitorTrackItem[]
}

export function listVisitors(params: { status?: number; page: number; page_size: number }) {
  return request<{ total: number; list: VisitorItem[] }>({
    url: '/visitors',
    method: 'get',
    params,
  })
}

export function getVisitor(id: number) {
  return request<VisitorDetail>({ url: `/visitor/${id}`, method: 'get' })
}

export interface InviteResp {
  id: number
  qr_code: string
  qr_image?: string
  expire_at: number
}

export function inviteVisitor(data: {
  visitor_name: string
  visitor_phone: string
  visit_time: number
  expire_time: number
  reason?: string
  verify_channel?: number
}) {
  return request<InviteResp>({ url: '/visitor/invite', method: 'post', data })
}

export function checkinVisitor(data: {
  qr_code?: string
  verify_channel?: number
  credential?: string
}) {
  return request<{
    id: number
    status: number
    checkin_at: number
    device_id?: string
    open_msg?: string
  }>({ url: '/visitor/checkin', method: 'post', data })
}

export function checkoutVisitor(data: { id?: number; qr_code?: string }) {
  return request<{ id: number; status: number; checkout_at: number }>({
    url: '/visitor/checkout',
    method: 'post',
    data,
  })
}

export function addVisitorBlocklist(data: {
  visitor_name?: string
  phone?: string
  id_no?: string
  reason?: string
  effective_from?: number
  effective_to?: number
}) {
  return request<{ id: number }>({ url: '/visitor/blocklist', method: 'post', data })
}

export function unblockVisitorBlocklist(id: number) {
  return request<{ id: number; status: number }>({
    url: `/visitor/blocklist/${id}/unblock`,
    method: 'post',
  })
}
