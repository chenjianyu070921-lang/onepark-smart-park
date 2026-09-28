import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Select,
  Space,
  Table,
  Tag,
  Timeline,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  getTaskDetail,
  listTasks,
  TASK_ACTION,
  TASK_PRIORITY,
  TASK_SOURCE,
  TASK_STATUS,
  type DispatchTask,
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
  const { list, loading, query, setQuery, pagination } = useTableQuery<DispatchTask, TaskQuery>(
    (q) => listTasks(q),
    { page: 1, page_size: PAGE_SIZE },
  )

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
      width: 110,
      fixed: 'right',
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => void openDetail(row)}>
          时间线
        </Button>
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
          scroll={{ x: 1200 }}
          locale={{ emptyText: '暂无工单 —— 可在「告警中心」投一条告警自动建单, 或调 POST /api/dispatch 人工建单' }}
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
    </div>
  )
}
