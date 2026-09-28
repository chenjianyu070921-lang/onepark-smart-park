import { request } from './client'

// 字段与 app/visitor-service/internal/types (visitor.api 生成) 一一对齐.

export interface VisitorItem {
  id: number
  visitor_name: string
  visitor_phone: string
  status: number // 1待使用 2已签入 3已签出 4已过期
  visit_time: number // 预期到访时间(秒级)
  checkin_at?: number
  checkout_at?: number
  device_id?: string
}

export interface VisitorListResp {
  total: number
  list: VisitorItem[]
}

// 门禁轨迹项(来源 visitor_record 动作留痕).
export interface VisitorTrackItem {
  action: string // invite / checkin_open_door / checkout / expired
  time: number
  device_id?: string
  remark?: string
}

export interface VisitorDetail {
  id: number
  inviter_id: number
  visitor_name: string
  visitor_phone: string
  status: number
  visit_time: number
  expire_time: number
  checkin_at: number
  checkout_at: number
  device_id?: string
  track: VisitorTrackItem[]
}

export interface VisitorInviteReq {
  visitor_name: string
  visitor_phone: string
  visit_time: number
  expire_time: number
  reason?: string
  id_no?: string
  face_token?: string
  badge_no?: string
  verify_channel?: number
}

export interface VisitorInviteResp {
  id: number
  qr_code: string
  qr_image?: string // base64 PNG(无 data: 前缀, 前端自行拼前缀渲染)
  expire_at: number
}

export interface VisitorCheckinResp {
  id: number
  status: number
  checkin_at: number
  device_id?: string
  open_msg?: string
}

export interface VisitorCheckoutResp {
  id: number
  status: number
  checkout_at: number
}

export interface AddBlocklistReq {
  visitor_name?: string
  phone?: string
  id_no?: string
  reason?: string
  effective_from?: number
  effective_to?: number
}

// 访客状态: 1待使用 2已签入 3已签出 4已过期
export const VISITOR_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '待使用', color: 'gold' },
  2: { label: '已签入', color: 'processing' },
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

// 门禁轨迹动作 → 中文
export const VISITOR_TRACK_ACTION: Record<string, string> = {
  invite: '邀请生成码',
  checkin_open_door: '签入并开门',
  checkout: '签出',
  expired: '二维码过期',
}

export function listVisitors(params: { status?: number; page: number; page_size: number }) {
  return request<VisitorListResp>({ url: '/visitors', method: 'get', params })
}

export function getVisitorDetail(id: number) {
  return request<VisitorDetail>({ url: `/visitor/${id}`, method: 'get' })
}

export function inviteVisitor(data: VisitorInviteReq) {
  return request<VisitorInviteResp>({ url: '/visitor/invite', method: 'post', data })
}

export function checkinVisitor(data: {
  qr_code?: string
  verify_channel?: number
  credential?: string
}) {
  return request<VisitorCheckinResp>({ url: '/visitor/checkin', method: 'post', data })
}

export function checkoutVisitor(data: { id?: number; qr_code?: string }) {
  return request<VisitorCheckoutResp>({ url: '/visitor/checkout', method: 'post', data })
}

export function addBlocklist(data: AddBlocklistReq) {
  return request<{ id: number }>({ url: '/visitor/blocklist', method: 'post', data })
}

// 解除黑名单: 入参为黑名单记录 ID(后端无"黑名单列表"接口, 故由人工填写 ID).
export function unblockBlocklist(id: number) {
  return request<{ id: number; status: number }>({
    url: `/visitor/blocklist/${id}/unblock`,
    method: 'post',
  })
}
