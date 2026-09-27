import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  DatePicker,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  PARKING_STATUS,
  VEHICLE_TYPE,
  createMonthlyCard,
  disableMonthlyCard,
  getActiveParking,
  getFeeRule,
  listMonthlyCards,
  listParkingRecords,
  parkingEntry,
  parkingExit,
  renewMonthlyCard,
  upsertFeeRule,
  type FeeRule,
  type MonthlyCardItem,
  type ParkingItem,
} from '../../api/parking'

const PAGE_SIZE = 10

const fmtTime = (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-')

function parkingColumns(): ColumnsType<ParkingItem> {
  return [
    { title: '车牌号', dataIndex: 'plate_no', width: 120 },
    { title: '入场时间', dataIndex: 'entry_time', width: 160, render: fmtTime },
    { title: '离场时间', dataIndex: 'exit_time', width: 160, render: fmtTime },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => {
        const s = PARKING_STATUS[v]
        return s ? <Tag color={s.color}>{s.label}</Tag> : v
      },
    },
    { title: '停车费(元)', dataIndex: 'fee', width: 100, render: (v?: string) => v ?? '-' },
  ]
}

function ActiveParking() {
  const [data, setData] = useState<ParkingItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)

  const [entryOpen, setEntryOpen] = useState(false)
  const [exitOpen, setExitOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [entryForm] = Form.useForm()
  const [exitForm] = Form.useForm()

  const fetchList = useCallback(async (p: number) => {
    setLoading(true)
    try {
      const resp = await getActiveParking({ page: p, page_size: PAGE_SIZE })
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

  const onEntry = async (values: { plate_no: string; vehicle_type: number; device_id_in: string }) => {
    setBusy(true)
    try {
      await parkingEntry(values)
      message.success(`${values.plate_no} 已入场`)
      setEntryOpen(false)
      entryForm.resetFields()
      fetchList(page)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onExit = async (values: { plate_no: string; device_id_out: string }) => {
    setBusy(true)
    try {
      const resp = await parkingExit(values)
      message.success(
        `${values.plate_no} 已离场, 时长 ${resp.duration_min ?? '-'} 分钟, 费用 ${resp.fee ?? '-'} 元`,
      )
      setExitOpen(false)
      exitForm.resetFields()
      fetchList(page)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setEntryOpen(true)}>
          模拟入场
        </Button>
        <Button onClick={() => setExitOpen(true)}>模拟离场</Button>
      </Space>
      <Table<ParkingItem>
        rowKey="id"
        columns={parkingColumns()}
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
        title="车辆入场"
        open={entryOpen}
        onCancel={() => setEntryOpen(false)}
        confirmLoading={busy}
        onOk={() => entryForm.submit()}
        destroyOnClose
      >
        <Form form={entryForm} layout="vertical" onFinish={onEntry} requiredMark={false}>
          <Form.Item name="plate_no" label="车牌号" rules={[{ required: true, message: '请输入车牌号' }]}>
            <Input placeholder="粤B12345" maxLength={10} />
          </Form.Item>
          <Form.Item name="vehicle_type" label="车辆类型" initialValue={2}>
            <Select
              options={Object.entries(VEHICLE_TYPE).map(([v, label]) => ({
                value: Number(v),
                label,
              }))}
            />
          </Form.Item>
          <Form.Item name="device_id_in" label="入场设备 ID" rules={[{ required: true }]}>
            <Input placeholder="闸机/地磁设备 ID" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="车辆离场"
        open={exitOpen}
        onCancel={() => setExitOpen(false)}
        confirmLoading={busy}
        onOk={() => exitForm.submit()}
        destroyOnClose
      >
        <Form form={exitForm} layout="vertical" onFinish={onExit} requiredMark={false}>
          <Form.Item name="plate_no" label="车牌号" rules={[{ required: true, message: '请输入车牌号' }]}>
            <Input placeholder="粤B12345" maxLength={10} />
          </Form.Item>
          <Form.Item name="device_id_out" label="出场设备 ID" rules={[{ required: true }]}>
            <Input placeholder="闸机/地磁设备 ID" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function ParkingRecords() {
  const [data, setData] = useState<ParkingItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<{ status?: number; plate_no?: string; page: number }>({
    page: 1,
  })
  const [plateInput, setPlateInput] = useState('')

  const fetchList = useCallback(async (q: typeof query) => {
    setLoading(true)
    try {
      const resp = await listParkingRecords({
        status: q.status,
        plate_no: q.plate_no || undefined,
        page: q.page,
        page_size: PAGE_SIZE,
      })
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

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(PARKING_STATUS).map(([v, s]) => ({
            value: Number(v),
            label: s.label,
          }))}
        />
        <Input.Search
          placeholder="车牌号查询"
          value={plateInput}
          onChange={(e) => setPlateInput(e.target.value)}
          onSearch={(v) => setQuery((q) => ({ ...q, plate_no: v, page: 1 }))}
          style={{ width: 220 }}
          allowClear
        />
      </Space>
      <Table<ParkingItem>
        rowKey="id"
        columns={parkingColumns()}
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

function MonthlyCards() {
  const [data, setData] = useState<MonthlyCardItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<{ status?: number; expiring_days?: number; page: number }>({
    page: 1,
  })

  const [createOpen, setCreateOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [createForm] = Form.useForm()

  const [renewTarget, setRenewTarget] = useState<MonthlyCardItem | null>(null)
  const [renewForm] = Form.useForm()

  const fetchList = useCallback(async (q: typeof query) => {
    setLoading(true)
    try {
      const resp = await listMonthlyCards({ ...q, page_size: PAGE_SIZE })
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

  const columns: ColumnsType<MonthlyCardItem> = [
    { title: '车牌号', dataIndex: 'plate_no', width: 120 },
    { title: '车主', dataIndex: 'owner_name', width: 100, render: (v?: string) => v ?? '-' },
    { title: '电话', dataIndex: 'phone', width: 130, render: (v?: string) => v ?? '-' },
    { title: '生效起', dataIndex: 'start_time', width: 120, render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD') },
    {
      title: '到期时间',
      dataIndex: 'end_time',
      width: 120,
      render: (v: number, row) => (
        <Space size={4}>
          {dayjs.unix(v).format('YYYY-MM-DD')}
          {row.days_left !== undefined && row.days_left <= 30 && row.status === 1 && (
            <Tag color="orange">剩 {row.days_left} 天</Tag>
          )}
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 80,
      render: (v: number) => (v === 1 ? <Tag color="green">生效</Tag> : <Tag>停用</Tag>),
    },
    {
      title: '操作',
      key: 'actions',
      width: 140,
      render: (_, row) =>
        row.status === 1 ? (
          <Space>
            <Button type="link" size="small" onClick={() => setRenewTarget(row)}>
              续费
            </Button>
            <Popconfirm title="确认停用该月卡?" onConfirm={() => onDisable(row.id)}>
              <Button type="link" size="small" danger>
                停用
              </Button>
            </Popconfirm>
          </Space>
        ) : null,
    },
  ]

  const onCreate = async (values: {
    plate_no: string
    owner_name?: string
    phone?: string
    range: [Dayjs, Dayjs]
  }) => {
    setBusy(true)
    try {
      await createMonthlyCard({
        plate_no: values.plate_no,
        owner_name: values.owner_name,
        phone: values.phone,
        start_time: values.range[0].unix(),
        end_time: values.range[1].unix(),
      })
      message.success('月卡已创建')
      setCreateOpen(false)
      createForm.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onRenew = async (values: { end_time: Dayjs }) => {
    if (!renewTarget) return
    setBusy(true)
    try {
      await renewMonthlyCard(renewTarget.id, values.end_time.unix())
      message.success('续费成功')
      setRenewTarget(null)
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onDisable = async (id: number) => {
    try {
      await disableMonthlyCard(id)
      message.success('月卡已停用')
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="到期提醒"
          style={{ width: 160 }}
          value={query.expiring_days}
          onChange={(v) => setQuery((q) => ({ ...q, expiring_days: v, page: 1 }))}
          options={[
            { value: 7, label: '7 天内到期' },
            { value: 30, label: '30 天内到期' },
          ]}
        />
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          办理月卡
        </Button>
      </Space>
      <Table<MonthlyCardItem>
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
        title="办理月卡"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={busy}
        onOk={() => createForm.submit()}
        destroyOnClose
      >
        <Form form={createForm} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item name="plate_no" label="车牌号" rules={[{ required: true, message: '请输入车牌号' }]}>
            <Input placeholder="粤B12345" maxLength={10} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="owner_name" label="车主姓名">
              <Input style={{ width: 200 }} maxLength={32} />
            </Form.Item>
            <Form.Item name="phone" label="联系电话">
              <Input style={{ width: 200 }} maxLength={11} />
            </Form.Item>
          </Space>
          <Form.Item name="range" label="有效期" rules={[{ required: true, message: '请选择有效期' }]}>
            <DatePicker.RangePicker showTime style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`续费 ${renewTarget?.plate_no ?? ''}`}
        open={!!renewTarget}
        onCancel={() => setRenewTarget(null)}
        confirmLoading={busy}
        onOk={() => renewForm.submit()}
        destroyOnClose
      >
        <Typography.Paragraph type="secondary">
          当前到期 {renewTarget ? dayjs.unix(renewTarget.end_time).format('YYYY-MM-DD HH:mm') : '-'}
        </Typography.Paragraph>
        <Form form={renewForm} layout="vertical" onFinish={onRenew} requiredMark={false}>
          <Form.Item
            name="end_time"
            label="新到期时间"
            rules={[{ required: true, message: '请选择新的到期时间' }]}
          >
            <DatePicker showTime style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function FeeRulePanel() {
  const [rule, setRule] = useState<FeeRule | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm()

  const fetchRule = useCallback(async () => {
    setLoading(true)
    try {
      const resp = await getFeeRule()
      setRule(resp.rule)
      if (resp.rule) {
        form.setFieldsValue({
          free_minutes: resp.rule.free_minutes,
          hourly_fee: resp.rule.hourly_fee,
          daily_cap: resp.rule.daily_cap,
        })
      }
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [form])

  useEffect(() => {
    fetchRule()
  }, [fetchRule])

  const onSave = async (values: { free_minutes: number; hourly_fee: number; daily_cap: number }) => {
    setSaving(true)
    try {
      await upsertFeeRule(values)
      message.success('计费规则已保存, 立即生效')
      fetchRule()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ maxWidth: 420 }}>
      <Typography.Paragraph type="secondary">
        {rule ? '当前已有生效规则, 保存后将生成新规则并替换' : '尚未配置计费规则, 临时车辆按无计费放行'}
      </Typography.Paragraph>
      <Form form={form} layout="vertical" onFinish={onSave} disabled={loading} requiredMark={false}>
        <Form.Item name="free_minutes" label="免费时长(分钟)" rules={[{ required: true }]}>
          <InputNumber min={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="hourly_fee" label="每小时费用(元)" rules={[{ required: true }]}>
          <InputNumber min={0.01} step={0.5} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="daily_cap" label="每日封顶(元, 0 为不封顶)" initialValue={0}>
          <InputNumber min={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>
            保存规则
          </Button>
        </Form.Item>
      </Form>
    </div>
  )
}

export default function ParkingPage() {
  return (
    <Card title="停车管理">
      <Tabs
        items={[
          { key: 'active', label: '在场车辆', children: <ActiveParking /> },
          { key: 'records', label: '停车记录', children: <ParkingRecords /> },
          { key: 'cards', label: '月卡管理', children: <MonthlyCards /> },
          { key: 'rule', label: '计费规则', children: <FeeRulePanel /> },
        ]}
      />
    </Card>
  )
}
