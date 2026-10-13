import { request } from './client'

// 字段与 app/kb-service/kb.api 对齐。

export interface Citation {
  chunkId: number
  documentId: number
  title: string
  source: string
  seq: number // 文档内第几段（从 0 开始）
  score: number // 0~1，越大越相关
  snippet: string
}

// mode 三种取值，前端据此决定怎么展示（这是"不编造"约定的落点）：
//   llm        生成式（配了模型 key）
//   extractive 抽取式（默认，离线可用）
//   no_hit     没有依据 -> 拒答，引用一定为空
export type AskMode = 'llm' | 'extractive' | 'no_hit'

export interface AskResp {
  answer: string
  mode: AskMode
  qaId: number
  citations: Citation[]
  topScore: number
  elapsedMs: number
}

export interface DocumentItem {
  id: number
  title: string
  source: string
  category: string
  chunkCount: number
  status: number
  createdAt: string
}

export interface QaLog {
  id: number
  question: string
  answer: string
  mode: AskMode
  topScore: number
  elapsedMs: number
  createdAt: string
}

export function ask(question: string, topK?: number) {
  return request<AskResp>({ url: '/kb/ask', method: 'post', data: { question, topK } })
}

export function listDocuments(params: { category?: string; page?: number; pageSize?: number }) {
  return request<{ total: number; list: DocumentItem[] }>({
    url: '/kb/documents',
    method: 'get',
    params,
  })
}

export function createDocument(data: {
  title: string
  source?: string
  category?: string
  content: string
}) {
  return request<{ id: number; chunkCount: number }>({
    url: '/kb/documents',
    method: 'post',
    data,
  })
}

export function deleteDocument(id: number) {
  return request<{ deleted: number }>({ url: `/kb/documents/${id}`, method: 'delete' })
}

export function listQaLogs(params: { page?: number; pageSize?: number }) {
  return request<{ total: number; list: QaLog[] }>({ url: '/kb/qa-logs', method: 'get', params })
}

export function sendFeedback(qaId: number, vote: 1 | 2, comment?: string) {
  return request<{ ok: boolean }>({ url: '/kb/feedback', method: 'post', data: { qaId, vote, comment } })
}

// 演示用示例问题：答辩现场点一下就能问，不用手打（也顺便展示"什么问题答不了"）
export const SAMPLE_QUESTIONS = [
  '工单派出去没人接怎么办？',
  '租户欠费多久会被限制服务？',
  '消防通道被堵多久算超时？',
  '某栋楼用电突然变高，怎么判断是不是异常？',
  '食堂今天中午吃什么？', // 故意超纲：用来演示"没有依据就拒答"
]
