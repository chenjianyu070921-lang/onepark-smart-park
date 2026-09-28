import { request } from './client'

// 字段与 app/dispatch-service/dispatch.api 对齐。
// 时间字段一律是**秒级**时间戳（交给 utils/format 的 fmtTime 统一处理，别自己 *1000）。

export interface DispatchTask {
  id: number
  task_no: string
  title: string
  source: number // 1 人工创建 2 告警自动创建
  alarm_id: string // 来源告警 ID(自动创建时非空)
  zone_code: string // 事发区域, 用于就近指派
  required_skill: string // 所需技能标签, 空=不限
  priority: number // 1 紧急 2 高 3 普通
  status: number // 1 待指派 2 已指派 3 处理中 4 已完成 5 已关闭
  assignee_id: number
  assignee_name: string
  description: string
  assign_expire_at: number | null // 指派超时时刻(秒); null = 当前没有待接单的指派窗口
  reassign_count: number // 被 cron 自动重派的次数; 达上限后释放回「待指派」
  created_at: number
  updated_at: number
}

// 工单状态: 1待指派 2已指派 3处理中 4已完成 5已关闭
export const TASK_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '待指派', color: 'orange' },
  2: { label: '已指派', color: 'blue' },
  3: { label: '处理中', color: 'processing' },
  4: { label: '已完成', color: 'green' },
  5: { label: '已关闭', color: 'default' },
}

// 优先级: 1紧急 2高 3普通
export const TASK_PRIORITY: Record<number, { label: string; color: string }> = {
  1: { label: '紧急', color: 'red' },
  2: { label: '高', color: 'volcano' },
  3: { label: '普通', color: 'default' },
}

// 来源: 1人工创建 2告警自动创建
export const TASK_SOURCE: Record<number, { label: string; color: string }> = {
  1: { label: '人工创建', color: 'default' },
  2: { label: '告警自动', color: 'magenta' },
}

// 流转动作 -> 展示文案。取值来自后端状态机(internal/state/fsm.go)。
export const TASK_ACTION: Record<string, string> = {
  create: '建单',
  assign: '指派/改派',
  release: '释放回待指派',
  start: '开始处理',
  finish: '完成',
  close: '关闭',
}

// TaskLog 状态流转审计 —— 详情页时间线的一格。
// from_status = 0 表示「不存在」(建单那一刻); operator_id = 0 表示**系统**(cron 重派 / 告警自动建单)。
export interface TaskLog {
  id: number
  from_status: number
  to_status: number
  action: string
  remark: string
  operator_id: number
  created_at: number
}

export interface TaskDetail {
  task: DispatchTask
  logs: TaskLog[]
}

export function listTasks(params: {
  status?: number
  priority?: number
  zone_code?: string
  page: number
  page_size: number
}) {
  return request<{ total: number; list: DispatchTask[] }>({
    url: '/dispatches',
    method: 'get',
    params,
  })
}

// 详情: task + logs(时间线, 已按发生顺序返回, 直接顺序渲染即可)
export function getTaskDetail(id: number) {
  return request<TaskDetail>({ url: `/dispatch/${id}`, method: 'get' })
}
