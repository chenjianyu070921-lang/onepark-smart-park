import { request } from './client'

export function getUnreadNoticeCount() {
  return request<{ count: number }>({ url: '/notices/unread-count', method: 'get' })
}

export interface NoticeItem {
  id: number
  title: string
  content: string
  // 具体字段以 notice-service 出参为准, 此处为工作台展示所需的最小集.
  [key: string]: unknown
}

export function listNotices() {
  return request<{ list: NoticeItem[]; total: number } | NoticeItem[]>({
    url: '/notices',
    method: 'get',
  })
}
