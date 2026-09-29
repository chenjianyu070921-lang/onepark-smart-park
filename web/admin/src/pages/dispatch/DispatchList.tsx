import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  message,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Timeline,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  assignTask,
  getTaskDetail,
  listStaffs,
  listTasks,
  TASK_ACTION,
  TASK_ALLOWED_ACTIONS,
  TASK_BUTTON_LABEL,
  TASK_PRIORITY,
  TASK_SOURCE,
  TASK_STATUS,
  updateTaskStatus,
  type DispatchTask,
  type StaffItem,
  type TaskAction,
  type TaskDetail,
} from '../../api/dispatch'
import { listZones, type Zone } from '../../api/leasing'
import { useTableQuery, type BaseQuery } from '../../hooks/useTableQuery'
import { fmtTime, toOptions } from '../../utils/format'

const PAGE_SIZE = 10

interface TaskQuery extends BaseQuery {
  status?: number
  priority?: number
  zone_code?: string
}

export default function DispatchListPage() {
  const { list, loading, query, setQuery, reload, pagination } = useTableQuery<
    DispatchTask,
    TaskQuery
  >((q) => listTasks(q), { page: 1, page_size: PAGE_SIZE })

  // 园区下拉: 复用租赁的区域清单(同一套 zone_code)。取不到就降级为空选项, 不影响其余筛选。
  const [zones, setZones] = useState<Zone[]>([])
  const [zonesFailed, setZonesFailed] = useState(false)
  useEffect(() => {
    listZones({ page: 1, page_size: 200 })
      .then((r) => setZones(r.list ?? []))
      .catch(() => setZonesFailed(true))
  }, [])

  // 详情抽屉: 工单基本信息 + 状态流转时间线
  const [open, setOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detail, setDetail] = useState<TaskDetail | null>(null)

  const openDetail = async (row: DispatchTask) => {
    setOpen(true)
    setDetailLoading(true)
    setDetail(null)
    try {
      setDetail(await getTaskDetail(row.id))
    } catch {
      // 错误提示由 axios 拦截器统一弹出
    } finally {
      setDetailLoading(false)
    }
  }

  // ---- 指派 / 状态流转 ----
  // 操作成功后统一刷新: 列表必刷; 详情抽屉开着时也刷一次 —— 否则时间线会停在操作前,
  // 演示时刚点完「开始」却在时间线里看不到那一条, 很容易被当成"没生效"。
  const [actingId, setActingId] = useState<number | null>(null)
  const [assignRow, setAssignRow] = useState<DispatchTask | null>(null)
  const [staffs, setStaffs] = useState<StaffItem[]>([])
  const [staffsLoading, setStaffsLoading] = useState(false)
  const [assigneeId, setAssigneeId] = useState<number>(0)
  const [assigning, setAssigning] = useState(false)

  const refreshAfterAction = async () => {
    reload()
    if (open && detail?.task) {
      try {
        setDetail(await getTaskDetail(detail.task.id))
      } catch {
        // 详情刷新失败不影响列表已经刷新的结果
      }
    }
  }

  const openAssign = async (row: DispatchTask) => {
    setAssignRow(row)
    setAssigneeId(0) // 默认「自动指派」: 演示时一键就能看后端打分的结果
    setStaffsLoading(true)
    try {
      // 只列**在岗**人员: 指给不在岗的人只会白挨一条 4xx
      const r = await listStaffs({ on_duty: 1, page: 1, page_size: 100 })
      setStaffs(r.list ?? [])
    } catch {
      setStaffs([])
    } finally {
      setStaffsLoading(false)
    }
  }

  const doAssign = async () => {
    if (!assignRow) return
    setAssigning(true)
    try {
      const r = await assignTask(assignRow.id, assigneeId)
      const who = r.assignee_name || `#${r.assignee_id}`
      message.success(assigneeId === 0 ? `已自动指派: ${who}` : `已指派: ${who}`)
      setAssignRow(null)
      await refreshAfterAction()
    } catch {
      // 错误提示由 axios 拦截器统一弹出; 对话框保持打开, 便于改选别人
    } finally {
      setAssigning(false)
    }
  }

  const doAction = (row: DispatchTask, action: Exclude<TaskAction, 'assign'>) => {
    const label = TASK_BUTTON_LABEL[action]
    const run = async () => {
      setActingId(row.id)
      try {
        await updateTaskStatus(row.id, action)
        message.success(`已${label}: ${row.task_no}`)
        await refreshAfterAction()
      } catch {
        // 同上
      } finally {
        setActingId(null)
      }
    }
    // 「关闭」是终态、不可回退 -> 单独确认; 其余是常规推进, 不打断操作
    if (action === 'close') {
      Modal.confirm({
        title: `关闭工单 ${row.task_no}?`,
        content: '关闭后为终态, 不能再推进状态(审计流水仍保留)。',
        okText: '确认关闭',
        cancelText: '取消',
        onOk: run,
      })
      return
    }
    void run()
  }

  // 按钮集合由后端状态机的可达边生成(见 api/dispatch.ts 的 TASK_ALLOWED_ACTIONS):
  // UI 不提供后端必拒的操作, 终态则明确显示「终态」而不是给一排点不动的灰按钮。
  const renderActions = (row: DispatchTask) => {
    const allowed = TASK_ALLOWED_ACTIONS[row.status] ?? []
    if (allowed.length === 0) {
      return <Typography.Text type="secondary">终态</Typography.Text>
    }
    return (
      <Space size={0} wrap>
        {allowed.map((a) =>
          a === 'assign' ? (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={actingId === row.id}
              onClick={() => void openAssign(row)}
            >
              {/* 已指派的再指派就是改派, 文案跟着状态变 */}
              {row.status === 2 ? '改派' : TASK_BUTTON_LABEL[a]}
            </Button>
          ) : (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={actingId === row.id}
              onClick={() => doAction(row, a)}
            >
              {TASK_BUTTON_LABEL[a]}
            </Button>
          ),
        )}
      </Space>
    )
  }

  const columns: ColumnsType<DispatchTask> = [
    { title: '工单号', dataIndex: 'task_no', width: 190 },
    { title: '标题', dataIndex: 'title', ellipsis: true },
    { title: '园区', dataIndex: 'zone_code', width: 120 },
    {
      title: '优先级',
      dataIndex: 'priority',
      width: 90,
      render: (v: number) => (
        <Tag color={TASK_PRIORITY[v]?.color}>{TASK_PRIORITY[v]?.label ?? v}</Tag>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => <Tag color={TASK_STATUS[v]?.color}>{TASK_STATUS[v]?.label ?? v}</Tag>,
    },
    {
      title: '来源',
      dataIndex: 'source',
      width: 110,
      render: (v: number) => (
        <Tag color={TASK_SOURCE[v]?.color}>{TASK_SOURCE[v]?.label ?? v}</Tag>
      ),
    },
    {
      title: '处理人',
      dataIndex: 'assignee_name',
      width: 130,
      render: (v: string) => v || '-',
    },
    {
      // 重派次数只对「被 cron 自动改派过」的工单有意义, 0 时不必占视觉
      title: '重派',
      dataIndex: 'reassign_count',
      width: 70,
      render: (v: number) => (v > 0 ? <Tag color="volcano">{v}</Tag> : '-'),
    },
    { title: '创建时间', dataIndex: 'created_at', width: 160, render: (v: number) => fmtTime(v) },
    {
      title: '操作',
      key: 'actions',
      width: 300,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0} wrap>
          <Button type="link" size="small" onClick={() => void openDetail(row)}>
            时间线
          </Button>
          {renderActions(row)}
        </Space>
      ),
    },
  ]

  const logs = detail?.logs ?? []

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Card title="调度工单">
        {zonesFailed ? (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message="园区清单暂不可用"
            description="园区下拉依赖 leasing-service 的可租区域接口; 不影响按状态/优先级筛选与时间线查看。"
          />
        ) : null}

        <Space wrap style={{ marginBottom: 16 }}>
          <Select
            allowClear
            placeholder="状态筛选"
            style={{ width: 140 }}
            value={query.status}
            onChange={(v) => setQuery({ status: v })}
            options={toOptions(TASK_STATUS)}
          />
          <Select
            allowClear
            placeholder="优先级筛选"
            style={{ width: 140 }}
            value={query.priority}
            onChange={(v) => setQuery({ priority: v })}
            options={toOptions(TASK_PRIORITY)}
          />
          <Select
            allowClear
            showSearch
            placeholder="园区筛选"
            style={{ width: 180 }}
            value={query.zone_code}
            onChange={(v) => setQuery({ zone_code: v })}
            optionFilterProp="label"
            options={zones.map((z) => ({
              value: z.zone_code,
              label: z.zone_name ? `${z.zone_code} (${z.zone_name})` : z.zone_code,
            }))}
          />
        </Space>

        <Table<DispatchTask>
          rowKey="id"
          columns={columns}
          dataSource={list}
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1400 }}
          locale={{
            emptyText:
              '暂无工单 —— 可在「告警中心」投一条告警自动建单, 或调 POST /api/dispatch 人工建单',
          }}
        />
      </Card>

      <Drawer
        title={detail?.task ? `工单时间线 · ${detail.task.task_no}` : '工单时间线'}
        width={640}
        open={open}
        onClose={() => setOpen(false)}
        destroyOnClose
      >
        {detailLoading ? (
          <Typography.Text type="secondary">加载中…</Typography.Text>
        ) : !detail ? (
          <Alert type="error" showIcon message="加载失败" description="该工单详情或流转记录暂时取不到。" />
        ) : (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="标题">{detail.task.title}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={TASK_STATUS[detail.task.status]?.color}>
                  {TASK_STATUS[detail.task.status]?.label ?? detail.task.status}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="园区 / 所需技能">
                {detail.task.zone_code} / {detail.task.required_skill || '不限'}
              </Descriptions.Item>
              <Descriptions.Item label="处理人">
                {detail.task.assignee_name || '-'}
                {detail.task.assign_expire_at
                  ? `（接单截止 ${fmtTime(detail.task.assign_expire_at)}）`
                  : ''}
              </Descriptions.Item>
              <Descriptions.Item label="重派次数">{detail.task.reassign_count}</Descriptions.Item>
              <Descriptions.Item label="描述">{detail.task.description || '-'}</Descriptions.Item>
            </Descriptions>

            <div>
              <Typography.Title level={5} style={{ marginBottom: 12 }}>
                状态流转（共 {logs.length} 条）
              </Typography.Title>
              {logs.length === 0 ? (
                <Typography.Text type="secondary">暂无流转记录</Typography.Text>
              ) : (
                <Timeline
                  items={logs.map((l) => ({
                    color: l.to_status === 5 ? 'gray' : l.to_status === 4 ? 'green' : 'blue',
                    children: (
                      <div>
                        <Space size={8} wrap>
                          <Tag>{TASK_ACTION[l.action] ?? l.action}</Tag>
                          <span>
                            {l.from_status === 0
                              ? '建单'
                              : (TASK_STATUS[l.from_status]?.label ?? l.from_status)}
                            {' → '}
                            {TASK_STATUS[l.to_status]?.label ?? l.to_status}
                          </span>
                          <Typography.Text type="secondary">{fmtTime(l.created_at)}</Typography.Text>
                          <Typography.Text type="secondary">
                            {/* 0 = 系统(cron 超时重派 / 告警自动建单), 不是"没人操作" */}
                            {l.operator_id === 0 ? '系统' : `操作人 #${l.operator_id}`}
                          </Typography.Text>
                        </Space>
                        {l.remark ? (
                          <div style={{ color: '#595959', marginTop: 4 }}>{l.remark}</div>
                        ) : null}
                      </div>
                    ),
                  }))}
                />
              )}
            </div>
          </Space>
        )}
      </Drawer>

      <Modal
        title={assignRow ? `指派工单 · ${assignRow.task_no}` : '指派工单'}
        open={!!assignRow}
        onOk={() => void doAssign()}
        confirmLoading={assigning}
        onCancel={() => setAssignRow(null)}
        okText="确认指派"
        cancelText="取消"
        destroyOnClose
      >
        {assignRow ? (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="工单">{assignRow.title}</Descriptions.Item>
              <Descriptions.Item label="园区 / 所需技能">
                {assignRow.zone_code} / {assignRow.required_skill || '不限'}
              </Descriptions.Item>
              <Descriptions.Item label="当前处理人">
                {assignRow.assignee_name || '-'}
              </Descriptions.Item>
            </Descriptions>
            <Select
              style={{ width: '100%' }}
              loading={staffsLoading}
              value={assigneeId}
              onChange={setAssigneeId}
              options={[
                { value: 0, label: '自动指派（值班 + 技能 + 就近 + 负载）' },
                ...staffs.map((s) => ({
                  value: s.staff_id,
                  label: `${s.name} · ${s.zone_code || '未分园区'} · ${s.skills || '无技能标签'}`,
                })),
              ]}
            />
            {!staffsLoading && staffs.length === 0 ? (
              <Typography.Text type="secondary">
                暂无在岗人员 —— 仍可选「自动指派」；或先通过人员维护接口补在岗人员。
              </Typography.Text>
            ) : null}
          </Space>
        ) : null}
      </Modal>
    </div>
  )
}
