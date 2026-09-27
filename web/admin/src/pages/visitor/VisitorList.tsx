import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Timeline,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  VERIFY_CHANNEL,
  VISITOR_STATUS,
  addVisitorBlocklist,
  checkinVisitor,
  checkoutVisitor,
  getVisitor,
  inviteVisitor,
  listVisitors,
  unblockVisitorBlocklist,
  type InviteResp,
  type VisitorDetail,
  type VisitorItem,
} from '../../api/visitor'

const PAGE_SIZE = 10

const TRACK_ACTION: Record<string, string> = {
  invite: '邀请生成二维码',
  checkin_open_door: '签入并开门',
  checkout: '签出',
  expired: '过期失效',
}

interface Query {
  status?: number
  page: number
}

function VisitorRecords() {
  const [data, setData] = useState<VisitorItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<Query>({ page: 1 })

  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviting, setInviting] = useState(false)
  const [inviteResult, setInviteResult] = useState<InviteResp | null>(null)
  const [inviteForm] = Form.useForm()

  const [checkinOpen, setCheckinOpen] = useState(false)
  const [checking, setChecking] = useState(false)
  const [checkinForm] = Form.useForm()

  const [detail, setDetail] = useState<VisitorDetail | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const fetchList = useCallback(async (q: Query) => {
    setLoading(true)
    try {
      const resp = await listVisitors({ ...q, page_size: PAGE_SIZE })
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

  const columns: ColumnsType<VisitorItem> = [
    { title: '姓名', dataIndex: 'visitor_name', width: 120 },
    { title: '手机号', dataIndex: 'visitor_phone', width: 140 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => {
        const s = VISITOR_STATUS[v]
        return s ? <Tag color={s.color}>{s.label}</Tag> : v
      },
    },
    {
      title: '预期到访',
      dataIndex: 'visit_time',
      width: 160,
      render: (v: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-'),
    },
    {
      title: '签入时间',
      dataIndex: 'checkin_at',
      width: 160,
      render: (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-'),
    },
    {
      title: '操作',
      key: 'actions',
      width: 160,
      render: (_, row) => (
        <Space>
          <Button type="link" size="small" onClick={() => openDetail(row.id)}>
            详情
          </Button>
          {row.status === 2 && (
            <Button type="link" size="small" onClick={() => onCheckout(row.id)}>
              签出
            </Button>
          )}
        </Space>
      ),
    },
  ]

  const openDetail = async (id: number) => {
    setDetailOpen(true)
    setDetail(null)
    try {
      setDetail(await getVisitor(id))
    } catch {
      setDetailOpen(false)
    }
  }

  const onCheckout = async (id: number) => {
    try {
      await checkoutVisitor({ id })
      message.success('已签出')
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const onInvite = async (values: {
    visitor_name: string
    visitor_phone: string
    visit_time: Dayjs
    expire_time: Dayjs
    reason?: string
    verify_channel: number
  }) => {
    setInviting(true)
    try {
      const resp = await inviteVisitor({
        visitor_name: values.visitor_name,
        visitor_phone: values.visitor_phone,
        visit_time: values.visit_time.unix(),
        expire_time: values.expire_time.unix(),
        reason: values.reason,
        verify_channel: values.verify_channel,
      })
      setInviteResult(resp)
      setInviteOpen(false)
      inviteForm.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setInviting(false)
    }
  }

  const onCheckin = async (values: { qr_code?: string; credential?: string }) => {
    setChecking(true)
    try {
      const resp = await checkinVisitor({
        qr_code: values.qr_code || undefined,
        credential: values.credential || undefined,
        verify_channel: values.qr_code ? 1 : 2,
      })
      message.success(resp.open_msg ? `签入成功, ${resp.open_msg}` : '签入成功')
      setCheckinOpen(false)
      checkinForm.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setChecking(false)
    }
  }

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(VISITOR_STATUS).map(([v, s]) => ({
            value: Number(v),
            label: s.label,
          }))}
        />
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setInviteOpen(true)}>
          发起邀请
        </Button>
        <Button onClick={() => setCheckinOpen(true)}>人工签入</Button>
      </Space>

      <Table<VisitorItem>
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
        title="发起访客邀请"
        open={inviteOpen}
        onCancel={() => setInviteOpen(false)}
        confirmLoading={inviting}
        onOk={() => inviteForm.submit()}
        destroyOnClose
      >
        <Form form={inviteForm} layout="vertical" onFinish={onInvite} requiredMark={false}>
          <Space wrap>
            <Form.Item
              name="visitor_name"
              label="访客姓名"
              rules={[{ required: true, message: '请输入姓名' }]}
            >
              <Input style={{ width: 200 }} maxLength={32} />
            </Form.Item>
            <Form.Item
              name="visitor_phone"
              label="手机号"
              rules={[{ required: true, message: '请输入手机号' }]}
            >
              <Input style={{ width: 200 }} maxLength={11} />
            </Form.Item>
          </Space>
          <Space wrap>
            <Form.Item
              name="visit_time"
              label="预期到访时间"
              rules={[{ required: true, message: '请选择到访时间' }]}
            >
              <DatePicker showTime style={{ width: 200 }} />
            </Form.Item>
            <Form.Item
              name="expire_time"
              label="二维码过期时间"
              rules={[{ required: true, message: '请选择过期时间' }]}
            >
              <DatePicker showTime style={{ width: 200 }} />
            </Form.Item>
          </Space>
          <Form.Item name="verify_channel" label="核验方式" initialValue={1}>
            <Select
              style={{ width: 200 }}
              options={Object.entries(VERIFY_CHANNEL).map(([v, label]) => ({
                value: Number(v),
                label,
              }))}
            />
          </Form.Item>
          <Form.Item name="reason" label="来访原因">
            <Input maxLength={100} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="邀请成功"
        open={!!inviteResult}
        onCancel={() => setInviteResult(null)}
        footer={<Button onClick={() => setInviteResult(null)}>关闭</Button>}
        centered
      >
        {inviteResult && (
          <div style={{ textAlign: 'center' }}>
            {inviteResult.qr_image ? (
              <img
                src={`data:image/png;base64,${inviteResult.qr_image}`}
                alt="访客二维码"
                style={{ width: 200, height: 200 }}
              />
            ) : (
              <Typography.Paragraph copyable ellipsis style={{ maxWidth: 320, margin: '0 auto' }}>
                {inviteResult.qr_code}
              </Typography.Paragraph>
            )}
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
              有效期至 {dayjs.unix(inviteResult.expire_at).format('YYYY-MM-DD HH:mm')}
            </Typography.Text>
          </div>
        )}
      </Modal>

      <Modal
        title="人工签入"
        open={checkinOpen}
        onCancel={() => setCheckinOpen(false)}
        confirmLoading={checking}
        onOk={() => checkinForm.submit()}
        destroyOnClose
      >
        <Alert
          type="info"
          message="二维码与手机号二选一, 用于现场无闸机时人工核验"
          style={{ marginBottom: 16 }}
        />
        <Form form={checkinForm} layout="vertical" onFinish={onCheckin} requiredMark={false}>
          <Form.Item name="qr_code" label="加密二维码内容">
            <Input.TextArea rows={3} placeholder="粘贴访客二维码内容" />
          </Form.Item>
          <Form.Item name="credential" label="或手机号">
            <Input maxLength={11} />
          </Form.Item>
        </Form>
      </Modal>

      <Drawer title="访客详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={480}>
        {detail ? (
          <>
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="姓名">{detail.visitor_name}</Descriptions.Item>
              <Descriptions.Item label="手机号">{detail.visitor_phone}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={VISITOR_STATUS[detail.status]?.color}>
                  {VISITOR_STATUS[detail.status]?.label}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="预期到访">
                {detail.visit_time ? dayjs.unix(detail.visit_time).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="签入时间">
                {detail.checkin_at ? dayjs.unix(detail.checkin_at).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="签出时间">
                {detail.checkout_at
                  ? dayjs.unix(detail.checkout_at).format('YYYY-MM-DD HH:mm')
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="开门设备">{detail.device_id || '-'}</Descriptions.Item>
            </Descriptions>
            {detail.track && detail.track.length > 0 && (
              <>
                <Typography.Title level={5} style={{ marginTop: 24 }}>
                  进出轨迹
                </Typography.Title>
                <Timeline
                  items={detail.track.map((t) => ({
                    children: (
                      <>
                        <div>{TRACK_ACTION[t.action] ?? t.action}</div>
                        <Typography.Text type="secondary">
                          {dayjs.unix(t.time).format('YYYY-MM-DD HH:mm')}
                          {t.remark ? ` ${t.remark}` : ''}
                        </Typography.Text>
                      </>
                    ),
                  }))}
                />
              </>
            )}
          </>
        ) : (
          <Typography.Text type="secondary">加载中</Typography.Text>
        )}
      </Drawer>
    </>
  )
}

function VisitorBlocklist() {
  const [form] = Form.useForm()
  const [unblockId, setUnblockId] = useState<string>('')
  const [busy, setBusy] = useState(false)

  const onAdd = async (values: {
    visitor_name?: string
    phone?: string
    id_no?: string
    reason?: string
  }) => {
    if (!values.phone && !values.id_no) {
      message.warning('手机号与身份证号至少提供一个')
      return
    }
    setBusy(true)
    try {
      await addVisitorBlocklist(values)
      message.success('已加入黑名单')
      form.resetFields()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onUnblock = async () => {
    const id = Number(unblockId)
    if (!id) {
      message.warning('请输入黑名单记录 ID')
      return
    }
    setBusy(true)
    try {
      await unblockVisitorBlocklist(id)
      message.success('已解除拉黑')
      setUnblockId('')
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ maxWidth: 520 }}>
      <Alert
        type="info"
        message="拉黑后该手机号/身份证的访客邀请与签入将被拒绝"
        style={{ marginBottom: 16 }}
      />
      <Form form={form} layout="vertical" onFinish={onAdd} requiredMark={false}>
        <Space wrap>
          <Form.Item name="visitor_name" label="姓名">
            <Input style={{ width: 220 }} maxLength={32} />
          </Form.Item>
          <Form.Item name="phone" label="手机号">
            <Input style={{ width: 220 }} maxLength={11} />
          </Form.Item>
        </Space>
        <Form.Item name="id_no" label="身份证号">
          <Input maxLength={18} />
        </Form.Item>
        <Form.Item name="reason" label="拉黑原因">
          <Input.TextArea rows={2} maxLength={200} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" loading={busy}>
            加入黑名单
          </Button>
        </Form.Item>
      </Form>
      <Space>
        <Input
          placeholder="黑名单记录 ID"
          value={unblockId}
          onChange={(e) => setUnblockId(e.target.value)}
          style={{ width: 200 }}
        />
        <Button onClick={onUnblock} loading={busy}>
          解除拉黑
        </Button>
      </Space>
    </div>
  )
}

export default function VisitorListPage() {
  return (
    <Card title="访客管理">
      <Tabs
        items={[
          { key: 'records', label: '访客记录', children: <VisitorRecords /> },
          { key: 'blocklist', label: '黑名单', children: <VisitorBlocklist /> },
        ]}
      />
    </Card>
  )
}
