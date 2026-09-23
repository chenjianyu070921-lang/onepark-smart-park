import { request } from './client'

// 字段与 app/notice-service/internal/types 对齐.

// 类型: 1通知 2公告 3活动 4停水 5停电
export const NOTICE_TYPE: Record<number, string> = {
  1: '通知',
  2: '公告',
  3: '活动',
  4: '停水',
  5: '停电',
}

// 状态: 1草稿 2已发布 3已撤回
export const NOTICE_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '草稿', color: 'default' },
  2: { label: '已发布', color: 'green' },
  3: { label: '已撤回', color: 'warning' },
}

export interface NoticeItem {
  id: number
  title: string
  type: number
  status: number
  top: boolean
  publish_at?: number
  created_at: number
}

export function listNotices(params: {
  type?: number
  status?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: NoticeItem[] }>({
    url: '/notices',
    method: 'get',
    params,
  })
}

export function createNotice(data: {
  title: string
  content: string
  type: number
  top?: boolean
  publish_at?: number
}) {
  return request<NoticeItem>({ url: '/notice', method: 'post', data })
}

export function recallNotice(id: number, reason?: string) {
  return request<{ id: number; status: number }>({
    url: `/notice/${id}/recall`,
    method: 'post',
    data: { reason },
  })
}

export function markNoticeRead(noticeId: number) {
  return request<{ updated: number }>({
    url: `/notice/${noticeId}/read`,
    method: 'post',
  })
}

export function getUnreadNoticeCount() {
  return request<{ unread_count: number }>({
    url: '/notices/unread-count',
    method: 'get',
  })
}
