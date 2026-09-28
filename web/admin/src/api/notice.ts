import { request } from './client'

// 字段与 app/notice-service/internal/types (notice.api 生成) 一一对齐.

export interface NoticeItem {
  id: number
  title: string
  type: number // 1通知 2公告 3活动 4停水 5停电
  status: number // 1草稿 2已发布 3已撤回
  top: boolean
  publish_at?: number
  created_at: number
  content?: string // 列表可能省略; 详情抽屉展示, 缺失时显示 '-'
}

export interface NoticeListResp {
  total: number
  list: NoticeItem[]
}

export interface NoticeResp {
  id: number
  title: string
  type: number
  status: number
  top: boolean
  publish_at?: number
}

// 公告类型: 1通知 2公告 3活动 4停水 5停电
export const NOTICE_TYPE: Record<number, string> = {
  1: '通知',
  2: '公告',
  3: '活动',
  4: '停水',
  5: '停电',
}

// 公告状态: 1草稿 2已发布 3已撤回
export const NOTICE_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '草稿', color: 'gold' },
  2: { label: '已发布', color: 'green' },
  3: { label: '已撤回', color: 'default' },
}

export function listNotices(params?: {
  type?: number
  status?: number
  page?: number
  page_size?: number
}) {
  return request<NoticeListResp>({ url: '/notices', method: 'get', params })
}

export function createNotice(data: {
  title: string
  content: string
  type: number
  top?: boolean
  publish_at?: number
}) {
  return request<NoticeResp>({ url: '/notice', method: 'post', data })
}

// 撤回: 仅已发布(2)可撤回, 置 3 并重投 notice-event.
export function recallNotice(id: number, reason?: string) {
  return request<{ id: number; status: number }>({
    url: `/notice/${id}/recall`,
    method: 'post',
    data: { reason },
  })
}

// 已读回填(当前登录用户).
export function markNoticeRead(notice_id: number) {
  return request<{ updated: number }>({
    url: '/notices/read',
    method: 'post',
    data: { notice_id },
  })
}

// 站内信未读计数: 后端字段名为 unread_count.
export function getUnreadNoticeCount() {
  return request<{ unread_count: number }>({ url: '/notices/unread-count', method: 'get' })
}
