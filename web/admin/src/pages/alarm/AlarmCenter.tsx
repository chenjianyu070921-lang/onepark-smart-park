import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  ALARM_LEVEL,
  ALARM_STATUS,
  DLQ_STATUS,
  ackAlarm,
  createAlarmRule,
  getAlarm,
  listActiveAlarms,
  listAlarmRules,
  listAlarms,
  listDLQ,
  replayDLQ,
  resolveAlarm,
  updateAlarmRule,
  type AlarmDetail,
  type AlarmItem,
  type DLQItem,
  type RuleItem,
} from '../../api/alarm'
import { useAuthStore } from '../../stores/auth'

const PAGE_SIZE = 10

const fmtTime = (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-')

function LevelTag({ level }: { level: number }) {
  const l = ALARM_LEVEL[level]
  return l ? <Tag color={l.color}>{l.label}</Tag> : <Tag>{level}</Tag>
}

function StatusTag({ status }: { status: number }) {
  const s = ALARM_STATUS[status]
  return s ? <Tag color={s.color}>{s.label}</Tag> : <Tag>{status}</Tag>
}

// 活跃告警 WebSocket: 收到告警事件即刷新列表; Kafka 未运行时静默断开重试.
function useAlarmWS(onEvent: () => void) {
  const [connected, setConnected] = useState(false)
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent

  useEffect(() => {
    let ws: WebSocket | null = null
    let timer: ReturnType<typeof setTimeout>
    let alive = true

    const connect = () => {
      const token = useAuthStore.getState().token
      if (!alive || !token) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/ws/alarm?token=${encodeURIComponent(token)}`)
      ws.onopen = () => setConnected(true)
      ws.onmessage = () => onEventRef.current()
      ws.onclose = () => {
        setConnected(false)
        if (alive) timer = setTimeout(connect, 10_000)
      }
      ws.onerror = () => ws?.close()
    }
    connect()
    return () => {
      alive = false
      clearTimeout(timer)
      ws?.close()
    }
  }, [])

  return connected
}

function ActiveAlarms() {
  const [data, setData] = useState<AlarmItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<{ level?: number; page: number }>({ page: 1 })
  const [lastRefresh, setLastRefresh] = useState<Dayjs | null>(null)

  const [actionTarget, setActionTarget] = useState<{ alarm: AlarmItem; action: 'ack' | 'resolve' } | null>(null)
  const [acting, setActing] = useState(false)
  const [actionForm] = Form.useForm()

  const [detail, setDetail] = useState<AlarmDetail | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const fetchList = useCallback(async (q: typeof query, silent = false) => {
    if (!silent) setLoading(true)
    try {
      const resp = await listActiveAlarms({ level: q.level, page: q.page, page_size: PAGE_SIZE })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
      setLastRefresh(dayjs())
    } catch {
      // 拦截器已提示(静默刷新时 Kafka 等异常不打扰)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(query)
  }, [query, fetchList])

  const wsConnected = useAlarmWS(() => fetchList(query, true))

  const columns: ColumnsType<AlarmItem> = [
    { title: '告警编号', dataIndex: 'alarm_no', width: 180 },
    { title: '等级', dataIndex: 'level', width: 80, render: (v: number) => <LevelTag level={v} /> },
    { title: '事件类型', dataIndex: 'event_type', width: 120 },
    { title: '内容', dataIndex: 'content', ellipsis: true },
    { title: '设备', dataIndex: 'device_id', width: 120, render: (v: string) => v || '-' },
    { title: '时间', dataIndex: 'created_at', width: 160, render: fmtTime },
    {
      title: '操作',
      key: 'actions',
      width: 200,
      render: (_, row) => (
        <Space>
          <Button type="link" size="small" onClick={() => openDetail(row.id)}>
            详情
          </Button>
          <Button type="link" size="small" onClick={() => setActionTarget({ alarm: row, action: 'ack' })}>
            确认
          </Button>
          <Button type="link" size="small" onClick={() => setActionTarget({ alarm: row, action: 'resolve' })}>
            解决
          </Button>
        </Space>
      ),
    },
  ]

  const openDetail = async (id: number) => {
    setDetailOpen(true)
    setDetail(null)
    try {
      setDetail(await getAlarm(id))
    } catch {
      setDetailOpen(false)
    }
  }

  const onAction = async (values: { remark?: string }) => {
    if (!actionTarget) return
    setActing(true)
    try {
      if (actionTarget.action === 'ack') {
        await ackAlarm(actionTarget.alarm.id, values.remark)
        message.success('告警已确认')
      } else {
        await resolveAlarm(actionTarget.alarm.id, values.remark)
        message.success('告警已解决')
      }
      setActionTarget(null)
      actionForm.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setActing(false)
    }
  }

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Badge
          status={wsConnected ? 'success' : 'default'}
          text={wsConnected ? '实时推送已连接' : '实时推送未连接'}
        />
        {lastRefresh && (
          <Typography.Text type="secondary">
            最近刷新 {lastRefresh.format('HH:mm:ss')}
          </Typography.Text>
        )}
        <Select
          allowClear
          placeholder="等级筛选"
          style={{ width: 120 }}
          value={query.level}
          onChange={(v) => setQuery((q) => ({ ...q, level: v, page: 1 }))}
          options={Object.entries(ALARM_LEVEL).map(([v, l]) => ({ value: Number(v), label: l.label }))}
        />
        <Button icon={<ReloadOutlined />} onClick={() => fetchList(query)}>
          刷新
        </Button>
      </Space>

      <Table<AlarmItem>
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        pagination={{
          current: query.page,
          pageSize: PAGE_SIZE,
          total,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (page) => setQuery((q) => ({ ...q, page })),
        }}
      />

      <Modal
        title={actionTarget?.action === 'ack' ? `确认告警 ${actionTarget.alarm.alarm_no}` : `解决告警 ${actionTarget?.alarm.alarm_no ?? ''}`}
        open={!!actionTarget}
        onCancel={() => setActionTarget(null)}
        confirmLoading={acting}
        onOk={() => actionForm.submit()}
        destroyOnClose
      >
        <Form form={actionForm} layout="vertical" onFinish={onAction} requiredMark={false}>
          <Form.Item name="remark" label="处理备注">
            <Input.TextArea rows={3} placeholder={actionTarget?.action === 'ack' ? '如: 已派安保现场核实' : '如: 现场确认无异常'} maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>

      <Drawer title="告警详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={480}>
        {detail ? (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="告警编号">{detail.alarm_no}</Descriptions.Item>
            <Descriptions.Item label="等级"><LevelTag level={detail.level} /></Descriptions.Item>
            <Descriptions.Item label="状态"><StatusTag status={detail.status} /></Descriptions.Item>
            <Descriptions.Item label="事件类型">{detail.event_type}</Descriptions.Item>
            <Descriptions.Item label="内容">{detail.content}</Descriptions.Item>
            <Descriptions.Item label="设备">{detail.device_id || '-'}</Descriptions.Item>
            <Descriptions.Item label="区域 ID">{detail.area_id || '-'}</Descriptions.Item>
            <Descriptions.Item label="发生时间">{fmtTime(detail.created_at)}</Descriptions.Item>
            <Descriptions.Item label="确认人 / 时间">
              {detail.ack_by ? `${detail.ack_by} / ${fmtTime(detail.ack_at)}` : '-'}
            </Descriptions.Item>
            <Descriptions.Item label="解决人 / 时间">
              {detail.resolve_by ? `${detail.resolve_by} / ${fmtTime(detail.resolve_at)}` : '-'}
            </Descriptions.Item>
          </Descriptions>
        ) : (
          <Typography.Text type="secondary">加载中</Typography.Text>
        )}
      </Drawer>
    </>
  )
}

function HistoryAlarms() {
  const [data, setData] = useState<AlarmItem[]>([])
  const [aggs, setAggs] = useState<Record<string, number>>({})
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<{
    level?: number
    status?: number
    keyword?: string
    range?: [Dayjs, Dayjs]
    page: number
  }>({ page: 1 })
  const [keywordInput, setKeywordInput] = useState('')

  const fetchList = useCallback(async (q: typeof query) => {
    setLoading(true)
    try {
      const resp = await listAlarms({
        level: q.level,
        status: q.status,
        keyword: q.keyword || undefined,
        start_time: q.range?.[0]?.unix(),
        end_time: q.range?.[1]?.unix(),
        page: q.page,
        page_size: PAGE_SIZE,
      })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
      setAggs(resp.aggs?.level_count ?? {})
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(query)
  }, [query, fetchList])

  const columns: ColumnsType<AlarmItem> = [
    { title: '告警编号', dataIndex: 'alarm_no', width: 180 },
    { title: '等级', dataIndex: 'level', width: 80, render: (v: number) => <LevelTag level={v} /> },
    { title: '状态', dataIndex: 'status', width: 90, render: (v: number) => <StatusTag status={v} /> },
    { title: '事件类型', dataIndex: 'event_type', width: 120 },
    { title: '内容', dataIndex: 'content', ellipsis: true },
    { title: '时间', dataIndex: 'created_at', width: 160, render: fmtTime },
  ]

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="等级"
          style={{ width: 110 }}
          value={query.level}
          onChange={(v) => setQuery((q) => ({ ...q, level: v, page: 1 }))}
          options={Object.entries(ALARM_LEVEL).map(([v, l]) => ({ value: Number(v), label: l.label }))}
        />
        <Select
          allowClear
          placeholder="状态(默认未处理)"
          style={{ width: 150 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={[
            ...Object.entries(ALARM_STATUS).map(([v, s]) => ({ value: Number(v), label: s.label })),
            { value: -1, label: '全部状态' },
          ]}
        />
        <DatePicker.RangePicker
          showTime
          onChange={(v) => setQuery((q) => ({ ...q, range: (v as [Dayjs, Dayjs] | null) ?? undefined, page: 1 }))}
        />
        <Input.Search
          placeholder="内容关键词"
          value={keywordInput}
          onChange={(e) => setKeywordInput(e.target.value)}
          onSearch={(v) => setQuery((q) => ({ ...q, keyword: v, page: 1 }))}
          style={{ width: 200 }}
          allowClear
        />
        {Object.entries(aggs).length > 0 && (
          <Space size={4}>
            {Object.entries(aggs).map(([level, count]) => (
              <Tag key={level} color={ALARM_LEVEL[Number(level)]?.color}>
                {ALARM_LEVEL[Number(level)]?.label ?? level}: {count}
              </Tag>
            ))}
          </Space>
        )}
      </Space>
      <Table<AlarmItem>
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        pagination={{
          current: query.page,
          pageSize: PAGE_SIZE,
          total,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (page) => setQuery((q) => ({ ...q, page })),
        }}
      />
    </>
  )
}

function AlarmRules() {
  const [data, setData] = useState<RuleItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)

  const [createOpen, setCreateOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [form] = Form.useForm()

  const fetchList = useCallback(async (p: number) => {
    setLoading(true)
    try {
      const resp = await listAlarmRules({ page: p, page_size: PAGE_SIZE })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(page)
  }, [page, fetchList])

  const columns: ColumnsType<RuleItem> = [
    { title: '规则名', dataIndex: 'name', width: 160 },
    { title: '设备类型', dataIndex: 'device_type', width: 110, render: (v: string) => v || '不限' },
    { title: '事件类型', dataIndex: 'event_type', width: 110 },
    { title: '等级', dataIndex: 'level', width: 80, render: (v: number) => <LevelTag level={v} /> },
    { title: '规则类型', dataIndex: 'rule_type', width: 100 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 80,
      render: (v: number) => (v === 1 ? <Tag color="green">启用</Tag> : <Tag>禁用</Tag>),
    },
    {
      title: '操作',
      key: 'actions',
      width: 110,
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => onToggle(row)}>
          {row.status === 1 ? '禁用' : '启用'}
        </Button>
      ),
    },
  ]

  const onToggle = async (row: RuleItem) => {
    try {
      await updateAlarmRule(row.id, { status: row.status === 1 ? 0 : 1 })
      message.success(row.status === 1 ? '规则已禁用' : '规则已启用')
      fetchList(page)
    } catch {
      // 拦截器已提示
    }
  }

  const onCreate = async (values: {
    name: string
    device_type?: string
    event_type: string
    level: number
    rule_type: string
    conditions: string
    window_seconds?: number
  }) => {
    setBusy(true)
    try {
      await createAlarmRule({ ...values, status: 1 })
      message.success('规则已创建')
      setCreateOpen(false)
      form.resetFields()
      fetchList(page)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => setCreateOpen(true)}
        style={{ marginBottom: 16 }}
      >
        新建规则
      </Button>
      <Table<RuleItem>
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        pagination={{
          current: page,
          pageSize: PAGE_SIZE,
          total,
          showTotal: (t) => `共 ${t} 条`,
          onChange: setPage,
        }}
      />

      <Modal
        title="新建告警规则"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={busy}
        onOk={() => form.submit()}
        destroyOnClose
      >
        <Form form={form} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item name="name" label="规则名" rules={[{ required: true, message: '请输入规则名' }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="event_type" label="事件类型" rules={[{ required: true, message: '请输入事件类型' }]}>
              <Input style={{ width: 180 }} placeholder="如 smoke_detected" />
            </Form.Item>
            <Form.Item name="device_type" label="设备类型(留空不限)">
              <Input style={{ width: 180 }} />
            </Form.Item>
          </Space>
          <Space wrap>
            <Form.Item name="level" label="告警等级" initialValue={2} rules={[{ required: true }]}>
              <Select
                style={{ width: 180 }}
                options={Object.entries(ALARM_LEVEL).map(([v, l]) => ({ value: Number(v), label: l.label }))}
              />
            </Form.Item>
            <Form.Item name="rule_type" label="规则类型" initialValue="threshold" rules={[{ required: true }]}>
              <Select
                style={{ width: 180 }}
                options={[
                  { value: 'threshold', label: '阈值' },
                  { value: 'event', label: '事件' },
                ]}
              />
            </Form.Item>
            <Form.Item name="window_seconds" label="统计窗口(秒)">
              <InputNumber min={0} style={{ width: 180 }} />
            </Form.Item>
          </Space>
          <Form.Item
            name="conditions"
            label="触发条件(JSON)"
            rules={[{ required: true, message: '请输入触发条件' }]}
          >
            <Input.TextArea rows={3} placeholder='{"field":"smoke","op":">","value":80}' />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function DLQPanel() {
  const [data, setData] = useState<DLQItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<{ status?: number; page: number }>({ page: 1 })
  const [replaying, setReplaying] = useState<number | null>(null)

  const fetchList = useCallback(async (q: typeof query) => {
    setLoading(true)
    try {
      const resp = await listDLQ({ status: q.status, page: q.page, page_size: PAGE_SIZE })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(query)
  }, [query, fetchList])

  const onReplay = async (row: DLQItem) => {
    setReplaying(row.id)
    try {
      const resp = await replayDLQ(row.id)
      message[resp.replayed ? 'success' : 'warning'](
        resp.replayed ? '重放成功, 消息已回到主链路' : '重放未成功, 主链路仍未消费, 可稍后重试',
      )
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setReplaying(null)
    }
  }

  const columns: ColumnsType<DLQItem> = [
    { title: 'Topic', dataIndex: 'topic', width: 160 },
    { title: '设备', dataIndex: 'device_id', width: 110, render: (v: string) => v || '-' },
    { title: '事件类型', dataIndex: 'event_type', width: 110 },
    {
      title: '错误',
      dataIndex: 'error_msg',
      ellipsis: true,
    },
    { title: '重试', dataIndex: 'retry_count', width: 70 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => {
        const s = DLQ_STATUS[v]
        return s ? <Tag color={s.color}>{s.label}</Tag> : v
      },
    },
    { title: '时间', dataIndex: 'created_at', width: 160, render: fmtTime },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      render: (_, row) =>
        row.status === 0 ? (
          <Button type="link" size="small" loading={replaying === row.id} onClick={() => onReplay(row)}>
            重放
          </Button>
        ) : null,
    },
  ]

  return (
    <>
      <Space style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(DLQ_STATUS).map(([v, s]) => ({ value: Number(v), label: s.label }))}
        />
      </Space>
      <Table<DLQItem>
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        expandable={{
          expandedRowRender: (row) => (
            <Typography.Paragraph copyable style={{ fontFamily: 'monospace', fontSize: 12 }}>
              {row.payload}
            </Typography.Paragraph>
          ),
        }}
        pagination={{
          current: query.page,
          pageSize: PAGE_SIZE,
          total,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (page) => setQuery((q) => ({ ...q, page })),
        }}
      />
    </>
  )
}

export default function AlarmCenterPage() {
  return (
    <Card title="告警中心">
      <Tabs
        items={[
          { key: 'active', label: '活跃告警', children: <ActiveAlarms /> },
          { key: 'history', label: '历史告警', children: <HistoryAlarms /> },
          { key: 'rules', label: '告警规则', children: <AlarmRules /> },
          { key: 'dlq', label: '死信队列', children: <DLQPanel /> },
        ]}
      />
    </Card>
  )
}
