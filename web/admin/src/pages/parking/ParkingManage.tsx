import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  DatePicker,
  Descriptions,
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
  message,
} from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  MONTHLY_CARD_STATUS,
  PARKING_STATUS,
  VEHICLE_TYPE,
  createMonthlyCard,
  disableMonthlyCard,
  getParkingFeeRule,
  listActiveParking,
  listMonthlyCards,
  listParkingRecords,
  parkingEntry,
  parkingExit,
  renewMonthlyCard,
  upsertParkingFeeRule,
  type MonthlyCardItem,
  type ParkingFeeRuleItem,
  type ParkingItem,
} from '../../api/parking'

const PAGE_SIZE = 10

interface EntryFormValues {
  plate_no: string
  vehicle_type: number
  device_id_in: string
}
interface ExitFormValues {
  device_id_out: string
}
interface MonthlyFormValues {
  plate_no: string
  owner_name?: string
  phone?: string
  start_time: Dayjs
  end_time: Dayjs
}
interface RenewFormValues {
  end_time: Dayjs
}
interface FeeFormValues {
  free_minutes: number
  hourly_fee: number
  daily_cap: number
  effective_from?: Dayjs
  effective_to?: Dayjs
}

export default function ParkingManagePage() {
  const [activeTab, setActiveTab] = useState('active')

  const [active, setActive] = useState<ParkingItem[]>([])
  const [activeTotal, setActiveTotal] = useState(0)
  const [activePage, setActivePage] = useState(1)
  const [activeLoading, setActiveLoading] = useState(false)

  const [records, setRecords] = useState<ParkingItem[]>([])
  const [recTotal, setRecTotal] = useState(0)
  const [recPage, setRecPage] = useState(1)
  const [recLoading, setRecLoading] = useState(false)
  const [recStatus, setRecStatus] = useState<number>()
  const [recPlate, setRecPlate] = useState('')

  const [cards, setCards] = useState<MonthlyCardItem[]>([])
  const [cardTotal, setCardTotal] = useState(0)
  const [cardPage, setCardPage] = useState(1)
  const [cardLoading, setCardLoading] = useState(false)
  const [cardStatus, setCardStatus] = useState<number>()
  const [cardPlate, setCardPlate] = useState('')
  const [cardExpiring, setCardExpiring] = useState<number>()

  const [rule, setRule] = useState<ParkingFeeRuleItem | null>(null)

  const [entryOpen, setEntryOpen] = useState(false)
  const [entryForm] = Form.useForm<EntryFormValues>()
  const [exitOpen, setExitOpen] = useState(false)
  const [exitPlate, setExitPlate] = useState('')
  const [exitForm] = Form.useForm<ExitFormValues>()
  const [monthlyOpen, setMonthlyOpen] = useState(false)
  const [monthlyForm] = Form.useForm<MonthlyFormValues>()
  const [renewOpen, setRenewOpen] = useState(false)
  const [renewId, setRenewId] = useState<number>()
  const [renewForm] = Form.useForm<RenewFormValues>()
  const [feeOpen, setFeeOpen] = useState(false)
  const [feeForm] = Form.useForm<FeeFormValues>()

  const fetchActive = useCallback(async (page: number) => {
    setActiveLoading(true)
    try {
      const resp = await listActiveParking({ page, page_size: PAGE_SIZE })
      setActive(resp.list ?? [])
      setActiveTotal(resp.total ?? 0)
    } catch {
      // 拦截器已提示
    } finally {
      setActiveLoading(false)
    }
  }, [])

  const fetchRecords = useCallback(async () => {
    setRecLoading(true)
    try {
      const resp = await listParkingRecords({
        status: recStatus,
        plate_no: recPlate || undefined,
        page: recPage,
        page_size: PAGE_SIZE,
      })
      setRecords(resp.list ?? [])
      setRecTotal(resp.total ?? 0)
    } catch {
      // 拦截器已提示
    } finally {
      setRecLoading(false)
    }
  }, [recStatus, recPlate, recPage])

  const fetchCards = useCallback(async () => {
    setCardLoading(true)
    try {
      const resp = await listMonthlyCards({
        status: cardStatus,
        plate_no: cardPlate || undefined,
        expiring_days: cardExpiring,
        page: cardPage,
        page_size: PAGE_SIZE,
      })
      setCards(resp.list ?? [])
      setCardTotal(resp.total ?? 0)
    } catch {
      // 拦截器已提示
    } finally {
      setCardLoading(false)
    }
  }, [cardStatus, cardPlate, cardExpiring, cardPage])

  const fetchRule = useCallback(async () => {
    try {
      const resp = await getParkingFeeRule()
      setRule(resp.rule ?? null)
    } catch {
      // 拦截器已提示
    }
  }, [])

  useEffect(() => {
    fetchActive(activePage)
  }, [activePage, fetchActive])

  useEffect(() => {
    fetchRecords()
  }, [fetchRecords])

  useEffect(() => {
    fetchCards()
  }, [fetchCards])

  useEffect(() => {
    fetchRule()
  }, [fetchRule])

  const onEntry = async (values: EntryFormValues) => {
    try {
      await parkingEntry(values)
      message.success('已人工记录入场')
      setEntryOpen(false)
      entryForm.resetFields()
      fetchActive(activePage)
    } catch {
      // 拦截器已提示
    }
  }

  const openExit = (plate: string) => {
    setExitPlate(plate)
    exitForm.resetFields()
    setExitOpen(true)
  }

  const onExit = async (values: ExitFormValues) => {
    try {
      await parkingExit({ plate_no: exitPlate, device_id_out: values.device_id_out })
      message.success('已离场(触发计费)')
      setExitOpen(false)
      fetchActive(activePage)
    } catch {
      // 拦截器已提示
    }
  }

  const onCreateMonthly = async (values: MonthlyFormValues) => {
    try {
      await createMonthlyCard({
        plate_no: values.plate_no,
        owner_name: values.owner_name,
        phone: values.phone,
        start_time: values.start_time.unix(),
        end_time: values.end_time.unix(),
      })
      message.success('月卡已创建')
      setMonthlyOpen(false)
      monthlyForm.resetFields()
      fetchCards()
    } catch {
      // 拦截器已提示
    }
  }

  const openRenew = (id: number) => {
    setRenewId(id)
    renewForm.resetFields()
    setRenewOpen(true)
  }

  const onRenew = async (values: RenewFormValues) => {
    if (!renewId) return
    try {
      await renewMonthlyCard({ id: renewId, end_time: values.end_time.unix() })
      message.success('月卡已续费')
      setRenewOpen(false)
      fetchCards()
    } catch {
      // 拦截器已提示
    }
  }

  const onDisable = async (id: number) => {
    try {
      await disableMonthlyCard({ id })
      message.success('月卡已停用')
      fetchCards()
    } catch {
      // 拦截器已提示
    }
  }

  const openFee = () => {
    feeForm.setFieldsValue({
      free_minutes: rule?.free_minutes ?? 0,
      hourly_fee: rule?.hourly_fee ?? 0,
      daily_cap: rule?.daily_cap ?? 0,
      effective_from: rule?.effective_from ? dayjs.unix(rule.effective_from) : undefined,
      effective_to: rule?.effective_to ? dayjs.unix(rule.effective_to) : undefined,
    })
    setFeeOpen(true)
  }

  const onSaveFee = async (values: FeeFormValues) => {
    try {
      await upsertParkingFeeRule({
        free_minutes: values.free_minutes,
        hourly_fee: values.hourly_fee,
        daily_cap: values.daily_cap,
        effective_from: values.effective_from?.unix(),
        effective_to: values.effective_to?.unix(),
      })
      message.success('计费规则已保存')
      setFeeOpen(false)
      fetchRule()
    } catch {
      // 拦截器已提示
    }
  }

  return (
    <Card title="停车管理">
      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'active',
            label: '在场车辆',
            children: (
              <>
                <Space wrap style={{ marginBottom: 16 }}>
                  <Button type="primary" icon={<PlusOutlined />} onClick={() => setEntryOpen(true)}>
                    人工入场
                  </Button>
                  <Button icon={<ReloadOutlined />} onClick={() => fetchActive(activePage)}>
                    刷新
                  </Button>
                </Space>
                <Table<ParkingItem>
                  rowKey="id"
                  loading={activeLoading}
                  dataSource={active}
                  pagination={{
                    current: activePage,
                    pageSize: PAGE_SIZE,
                    total: activeTotal,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: setActivePage,
                  }}
                  columns={[
                    { title: '车牌', dataIndex: 'plate_no', width: 140 },
                    {
                      title: '入场时间',
                      dataIndex: 'entry_time',
                      render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD HH:mm'),
                    },
                    {
                      title: '状态',
                      dataIndex: 'status',
                      render: (v: number) => (
                        <Tag color={PARKING_STATUS[v]?.color}>{PARKING_STATUS[v]?.label ?? v}</Tag>
                      ),
                    },
                    {
                      title: '操作',
                      key: 'actions',
                      render: (_, row) => (
                        <Button type="link" size="small" onClick={() => openExit(row.plate_no)}>
                          离场
                        </Button>
                      ),
                    },
                  ]}
                />
              </>
            ),
          },
          {
            key: 'records',
            label: '停车记录',
            children: (
              <>
                <Space wrap style={{ marginBottom: 16 }}>
                  <Select
                    allowClear
                    placeholder="状态"
                    style={{ width: 120 }}
                    value={recStatus}
                    onChange={(v) => setRecStatus(v)}
                    options={Object.entries(PARKING_STATUS).map(([v, s]) => ({
                      value: Number(v),
                      label: s.label,
                    }))}
                  />
                  <Input.Search
                    placeholder="车牌号"
                    allowClear
                    style={{ width: 160 }}
                    onSearch={(v) => {
                      setRecPlate(v)
                      setRecPage(1)
                    }}
                  />
                  <Button icon={<ReloadOutlined />} onClick={fetchRecords}>
                    查询
                  </Button>
                </Space>
                <Table<ParkingItem>
                  rowKey="id"
                  loading={recLoading}
                  dataSource={records}
                  pagination={{
                    current: recPage,
                    pageSize: PAGE_SIZE,
                    total: recTotal,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: setRecPage,
                  }}
                  columns={[
                    { title: '车牌', dataIndex: 'plate_no', width: 140 },
                    {
                      title: '入场时间',
                      dataIndex: 'entry_time',
                      render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD HH:mm'),
                    },
                    {
                      title: '离场时间',
                      dataIndex: 'exit_time',
                      render: (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-'),
                    },
                    {
                      title: '时长(分)',
                      dataIndex: 'duration_min',
                      render: (v?: number) => v ?? '-',
                    },
                    {
                      title: '费用(元)',
                      dataIndex: 'fee',
                      render: (v?: string) => (v != null ? `¥${v}` : '-'),
                    },
                    {
                      title: '状态',
                      dataIndex: 'status',
                      render: (v: number) => (
                        <Tag color={PARKING_STATUS[v]?.color}>{PARKING_STATUS[v]?.label ?? v}</Tag>
                      ),
                    },
                  ]}
                />
              </>
            ),
          },
          {
            key: 'monthly',
            label: '月卡管理',
            children: (
              <>
                <Space wrap style={{ marginBottom: 16 }}>
                  <Select
                    allowClear
                    placeholder="状态"
                    style={{ width: 120 }}
                    value={cardStatus}
                    onChange={(v) => setCardStatus(v)}
                    options={Object.entries(MONTHLY_CARD_STATUS).map(([v, s]) => ({
                      value: Number(v),
                      label: s.label,
                    }))}
                  />
                  <Input.Search
                    placeholder="车牌号"
                    allowClear
                    style={{ width: 160 }}
                    onSearch={(v) => {
                      setCardPlate(v)
                      setCardPage(1)
                    }}
                  />
                  <InputNumber
                    min={1}
                    placeholder="N天内到期"
                    style={{ width: 130 }}
                    value={cardExpiring}
                    onChange={(v) => setCardExpiring(typeof v === 'number' ? v : undefined)}
                  />
                  <Button icon={<ReloadOutlined />} onClick={fetchCards}>
                    查询
                  </Button>
                  <Button type="primary" icon={<PlusOutlined />} onClick={() => setMonthlyOpen(true)}>
                    新建月卡
                  </Button>
                </Space>
                <Table<MonthlyCardItem>
                  rowKey="id"
                  loading={cardLoading}
                  dataSource={cards}
                  pagination={{
                    current: cardPage,
                    pageSize: PAGE_SIZE,
                    total: cardTotal,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: setCardPage,
                  }}
                  columns={[
                    { title: '车牌', dataIndex: 'plate_no', width: 130 },
                    { title: '车主', dataIndex: 'owner_name', render: (v?: string) => v || '-' },
                    { title: '电话', dataIndex: 'phone', render: (v?: string) => v || '-' },
                    {
                      title: '生效起',
                      dataIndex: 'start_time',
                      render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD'),
                    },
                    {
                      title: '生效止',
                      dataIndex: 'end_time',
                      render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD'),
                    },
                    {
                      title: '剩余(天)',
                      dataIndex: 'days_left',
                      render: (v?: number) => (v != null ? v : '-'),
                    },
                    {
                      title: '状态',
                      dataIndex: 'status',
                      render: (v: number) => (
                        <Tag color={MONTHLY_CARD_STATUS[v]?.color}>
                          {MONTHLY_CARD_STATUS[v]?.label ?? v}
                        </Tag>
                      ),
                    },
                    {
                      title: '操作',
                      key: 'actions',
                      render: (_, row) => (
                        <Space>
                          <Button type="link" size="small" onClick={() => openRenew(row.id)}>
                            续费
                          </Button>
                          {row.status === 1 && (
                            <Popconfirm title="确认停用该月卡?" onConfirm={() => onDisable(row.id)}>
                              <Button type="link" size="small" danger>
                                停用
                              </Button>
                            </Popconfirm>
                          )}
                        </Space>
                      ),
                    },
                  ]}
                />
              </>
            ),
          },
          {
            key: 'fee',
            label: '计费规则',
            children: (
              <>
                <Alert
                  type="info"
                  showIcon
                  style={{ marginBottom: 16 }}
                  message="计费规则: 免费时长内不收费; 超出部分按每小时单价向上取整计费; 每日封顶为单日累计上限(0 表示不封顶)"
                />
                <Space wrap style={{ marginBottom: 16 }}>
                  <Button type="primary" onClick={openFee}>
                    编辑计费规则
                  </Button>
                </Space>
                <Descriptions bordered column={1} size="small">
                  <Descriptions.Item label="免费时长(分钟)">
                    {rule ? rule.free_minutes : '-'}
                  </Descriptions.Item>
                  <Descriptions.Item label="每小时单价(元)">
                    {rule ? rule.hourly_fee : '-'}
                  </Descriptions.Item>
                  <Descriptions.Item label="每日封顶(元)">
                    {rule ? rule.daily_cap : '-'}
                  </Descriptions.Item>
                  <Descriptions.Item label="生效起">
                    {rule?.effective_from ? dayjs.unix(rule.effective_from).format('YYYY-MM-DD') : '长期'}
                  </Descriptions.Item>
                  <Descriptions.Item label="生效止">
                    {rule?.effective_to ? dayjs.unix(rule.effective_to).format('YYYY-MM-DD') : '长期'}
                  </Descriptions.Item>
                </Descriptions>
              </>
            ),
          },
        ]}
      />
      {/* 人工入场 */}
      <Modal
        title="人工入场(联调补录)"
        open={entryOpen}
        onCancel={() => setEntryOpen(false)}
        onOk={() => entryForm.submit()}
        destroyOnClose
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="生产环境入场由 M1 设备经 Kafka 驱动, 此接口仅用于联调/人工补录"
        />
        <Form form={entryForm} layout="vertical" onFinish={onEntry} requiredMark={false}>
          <Form.Item name="plate_no" label="车牌号" rules={[{ required: true, message: '请输入车牌号' }]}>
            <Input maxLength={12} />
          </Form.Item>
          <Form.Item name="vehicle_type" label="车辆类型" initialValue={2}>
            <Select
              options={Object.entries(VEHICLE_TYPE).map(([v, label]) => ({ value: Number(v), label }))}
            />
          </Form.Item>
          <Form.Item
            name="device_id_in"
            label="入场设备"
            rules={[{ required: true, message: '请输入入场设备 ID' }]}
          >
            <Input />
          </Form.Item>
        </Form>
      </Modal>

      {/* 离场 */}
      <Modal
        title="车辆离场(联调补录)"
        open={exitOpen}
        onCancel={() => setExitOpen(false)}
        onOk={() => exitForm.submit()}
        destroyOnClose
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="生产环境离场由 M1 设备经 Kafka 驱动; 此操作将触发计费, 仅用于联调/人工补录"
        />
        <p>车牌: {exitPlate}</p>
        <Form form={exitForm} layout="vertical" onFinish={onExit} requiredMark={false}>
          <Form.Item
            name="device_id_out"
            label="出场设备"
            rules={[{ required: true, message: '请输入出场设备 ID' }]}
          >
            <Input />
          </Form.Item>
        </Form>
      </Modal>

      {/* 新建月卡 */}
      <Modal
        title="新建月卡"
        open={monthlyOpen}
        onCancel={() => setMonthlyOpen(false)}
        onOk={() => monthlyForm.submit()}
        destroyOnClose
      >
        <Form form={monthlyForm} layout="vertical" onFinish={onCreateMonthly} requiredMark={false}>
          <Form.Item name="plate_no" label="车牌号" rules={[{ required: true, message: '请输入车牌号' }]}>
            <Input maxLength={12} />
          </Form.Item>
          <Form.Item name="owner_name" label="车主姓名">
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item name="phone" label="联系电话" rules={[{ pattern: /^1[3-9]\d{9}$/, message: '手机号格式不正确' }]}>
            <Input maxLength={11} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="start_time" label="生效起" rules={[{ required: true, message: '请选择生效起' }]}>
              <DatePicker format="YYYY-MM-DD" />
            </Form.Item>
            <Form.Item name="end_time" label="生效止" rules={[{ required: true, message: '请选择生效止' }]}>
              <DatePicker format="YYYY-MM-DD" />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      {/* 续费 */}
      <Modal
        title="月卡续费"
        open={renewOpen}
        onCancel={() => setRenewOpen(false)}
        onOk={() => renewForm.submit()}
        destroyOnClose
      >
        <Form form={renewForm} layout="vertical" onFinish={onRenew} requiredMark={false}>
          <Form.Item
            name="end_time"
            label="新的生效止"
            rules={[{ required: true, message: '请选择新的生效止' }]}
          >
            <DatePicker format="YYYY-MM-DD" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 计费规则编辑 */}
      <Modal
        title="编辑计费规则"
        open={feeOpen}
        onCancel={() => setFeeOpen(false)}
        onOk={() => feeForm.submit()}
        destroyOnClose
      >
        <Form form={feeForm} layout="vertical" onFinish={onSaveFee} requiredMark={false}>
          <Form.Item
            name="free_minutes"
            label="免费时长(分钟)"
            rules={[{ required: true, message: '请输入免费时长' }]}
          >
            <InputNumber min={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item
            name="hourly_fee"
            label="每小时单价(元)"
            rules={[{ required: true, message: '请输入每小时单价' }]}
          >
            <InputNumber min={0} step={0.5} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item
            name="daily_cap"
            label="每日封顶(元, 0=不封顶)"
            rules={[{ required: true, message: '请输入每日封顶' }]}
          >
            <InputNumber min={0} step={0.5} style={{ width: '100%' }} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="effective_from" label="生效起(留空=长期)">
              <DatePicker format="YYYY-MM-DD" />
            </Form.Item>
            <Form.Item name="effective_to" label="生效止(留空=长期)">
              <DatePicker format="YYYY-MM-DD" />
            </Form.Item>
          </Space>
        </Form>
      </Modal>
    </Card>
  )
}
