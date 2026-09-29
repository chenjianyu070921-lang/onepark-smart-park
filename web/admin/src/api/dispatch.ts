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

// 调度人员(指派对话框的候选池)。on_duty: -1 不限 1 只看在岗 0 只看不在岗。
export interface StaffItem {
  staff_id: number
  name: string
  phone: string
  zone_code: string
  skills: string // 技能标签, 逗号分隔
  on_duty: number // 1 在岗 0 不在岗
  status: number
  updated_at: number
}

export function listStaffs(params: { on_duty?: number; page: number; page_size: number }) {
  return request<{ total: number; list: StaffItem[] }>({
    url: '/dispatch/staffs',
    method: 'get',
    params,
  })
}

// 指派 / 改派。
// assigneeId = 0 表示走**自动指派**(后端按 值班 + 技能 + 就近 + 负载 打分), 非 0 则指给具体某人。
export function assignTask(id: number, assigneeId: number) {
  return request<{ id: number; assignee_id: number; assignee_name: string }>({
    url: `/dispatch/${id}/assign`,
    method: 'put',
    data: { assignee_id: assigneeId },
  })
}

// 状态流转。action 的取值**必须**是后端状态机里的合法边(见 internal/state/fsm.go 的 transitions)。
//
// ⚠️ 这里刻意不放 `assign`: 改派要带指派对象, 走 assignTask 才对;
//    用本接口的 assign 只会改状态、不会写处理人, 会造出"已指派但没处理人"的脏数据。
export function updateTaskStatus(
  id: number,
  action: 'release' | 'start' | 'finish' | 'close',
  remark?: string,
) {
  return request<{ id: number; status: number }>({
    url: `/dispatch/${id}/status`,
    method: 'put',
    data: { action, remark },
  })
}

// 各状态下**允许的操作** —— 与后端 FSM 的转移表逐格对齐:
//   1 待指派: assign, close
//   2 已指派: assign(改派), release, start, close
//   3 处理中: finish, close
//   4 已完成: close
//   5 已关闭: (终态, 无出边)
//
// 为什么要在前端也列一份: 按钮由它生成, **UI 不会提供后端必拒的操作** ——
// 演示时点不出 4xx, 也不会让人以为"这个操作应该能点"。
export type TaskAction = 'assign' | 'release' | 'start' | 'finish' | 'close'

export const TASK_ALLOWED_ACTIONS: Record<number, TaskAction[]> = {
  1: ['assign', 'close'],
  2: ['assign', 'release', 'start', 'close'],
  3: ['finish', 'close'],
  4: ['close'],
  5: [],
}

// 按钮文案(比 TASK_ACTION 的时间线文案更短, 且区分「指派」与「改派」)
export const TASK_BUTTON_LABEL: Record<TaskAction, string> = {
  assign: '指派',
  release: '释放',
  start: '开始',
  finish: '完成',
  close: '关闭',
}
