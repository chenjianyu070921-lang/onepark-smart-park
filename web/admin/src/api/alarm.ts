import { request } from './client'

// 字段与 app/alarm-service/internal/types 对齐.

// 等级: 1提示 2一般 3严重 4紧急
export const ALARM_LEVEL: Record<number, { label: string; color: string }> = {
  1: { label: '提示', color: 'blue' },
  2: { label: '一般', color: 'gold' },
  3: { label: '严重', color: 'orange' },
  4: { label: '紧急', color: 'red' },
}

// 状态: 0未处理 1已确认 2已解决
export const ALARM_STATUS: Record<number, { label: string; color: string }> = {
  0: { label: '未处理', color: 'red' },
  1: { label: '已确认', color: 'gold' },
  2: { label: '已解决', color: 'green' },
}

export interface AlarmItem {
  id: number
  alarm_no: string
  device_id: string
  area_id: number
  event_type: string
  level: number
  status: number
  content: string
  created_at: number
}

export interface AlarmDetail extends AlarmItem {
  tenant_id: number
  rule_id: number
  request_id: string
  ack_by: number
  ack_at: number
  resolve_by: number
  resolve_at: number
  updated_at: number
}

export interface AlarmListResp {
  total: number
  page: number
  page_size: number
  list: AlarmItem[]
  aggs?: { level_count: Record<string, number> }
}

export function listActiveAlarms(params: {
  level?: number
  device_id?: string
  page: number
  page_size: number
}) {
  return request<AlarmListResp>({ url: '/alarm/active', method: 'get', params })
}

export function listAlarms(params: {
  start_time?: number
  end_time?: number
  level?: number
  status?: number
  keyword?: string
  page: number
  page_size: number
}) {
  return request<AlarmListResp>({ url: '/alarms', method: 'get', params })
}

export function getAlarm(id: number) {
  return request<AlarmDetail>({ url: `/alarm/${id}`, method: 'get' })
}

export function ackAlarm(id: number, remark?: string) {
  return request<{ id: number; status: number }>({
    url: `/alarm/${id}/ack`,
    method: 'post',
    data: { remark },
  })
}

export function resolveAlarm(id: number, remark?: string) {
  return request<{ id: number; status: number }>({
    url: `/alarm/${id}/resolve`,
    method: 'post',
    data: { remark },
  })
}

// ---- 告警规则 ----

export interface RuleItem {
  id: number
  name: string
  device_type: string
  device_id: string
  area_id: number
  event_type: string
  level: number
  rule_type: string
  conditions: string
  window_seconds: number
  status: number
  created_at: number
  updated_at: number
}

export function listAlarmRules(params: {
  device_type?: string
  event_type?: string
  status?: number
  page: number
  page_size: number
}) {
  return request<{ total: number; list: RuleItem[] }>({
    url: '/alarm/rules',
    method: 'get',
    params,
  })
}

export function createAlarmRule(data: {
  name: string
  device_type?: string
  event_type: string
  level: number
  rule_type: string
  conditions: string
  window_seconds?: number
  status: number
}) {
  return request<{ id: number }>({ url: '/alarm/rule', method: 'post', data })
}

export function updateAlarmRule(id: number, data: Partial<Omit<RuleItem, 'id'>>) {
  return request<{ id: number }>({ url: `/alarm/rule/${id}`, method: 'put', data })
}

// ---- 死信队列 ----

export interface DLQItem {
  id: number
  topic: string
  partition_no: number
  msg_offset: number
  request_id: string
  device_id: string
  event_type: string
  payload: string
  error_msg: string
  retry_count: number
  status: number
  created_at: number
}

export const DLQ_STATUS: Record<number, { label: string; color: string }> = {
  0: { label: '待处理', color: 'gold' },
  1: { label: '已重放', color: 'green' },
  2: { label: '已丢弃', color: 'default' },
}

export function listDLQ(params: { status?: number; page: number; page_size: number }) {
  return request<{ total: number; list: DLQItem[] }>({
    url: '/alarm/dlq',
    method: 'get',
    params,
  })
}

export function replayDLQ(id: number) {
  return request<{ id: number; replayed: boolean }>({
    url: `/alarm/dlq/${id}/replay`,
    method: 'post',
  })
}
