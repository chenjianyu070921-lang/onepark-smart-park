import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Image,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Timeline,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  VERIFY_CHANNEL,
  VISITOR_STATUS,
  VISITOR_TRACK_ACTION,
  addBlocklist,
  checkoutVisitor,
  getVisitorDetail,
  inviteVisitor,
  listVisitors,
  unblockBlocklist,
  type VisitorDetail,
  type VisitorInviteResp,
  type VisitorItem,
} from '../../api/visitor'

const PAGE_SIZE = 10

interface Query {
  status?: number
  page: number
}

interface InviteFormValues {
  visitor_name: string
  visitor_phone: string
  visit_time: Dayjs
  expire_time: Dayjs
  reason?: string
  verify_channel: number
  id_no?: string
  face_token?: string
  badge_no?: string
}

interface BlocklistFormValues {
  visitor_name?: string
  phone?: string
  id_no?: string
  reason?: string
  effective_from?: Dayjs
  effective_to?: Dayjs
}

export default function VisitorListPage() {
  const [data, setData] = useState<VisitorItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<Query>({ page: 1 })

  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviting, setInviting] = useState(false)
  const [inviteForm] = Form.useForm<InviteFormValues>()
  const channel = Form.useWatch('verify_channel', inviteForm)
  const [inviteResult, setInviteResult] = useState<VisitorInviteResp | null>(null)

  const [detailOpen, setDetailOpen] = useState(false)
  const [detail, setDetail] = useState<VisitorDetail | null>(null)

  const [blockOpen, setBlockOpen] = useState(false)
  const [blocking, setBlocking] = useState(false)
  const [blockForm] = Form.useForm<BlocklistFormValues>()
  const [unblockOpen, setUnblockOpen] = useState(false)
  const [unblockId, setUnblockId] = useState<number | null>(null)

  const fetchList = useCallback(async (q: Query) => {
    setLoading(true)
    try {
      const resp = await listVisitors({ status: q.status, page: q.page, page_size: PAGE_SIZE })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
    } catch {
      // 错误提示已由拦截器统一弹出
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(query)
  }, [query, fetchList])

  const openDetail = async (id: number) => {
    setDetailOpen(true)
    setDetail(null)
    try {
      setDetail(await getVisitorDetail(id))
    } catch {
      setDetailOpen(false)
    }
  }

  const onCheckout = async (row: VisitorItem) => {
    try {
      await checkoutVisitor({ id: row.id })
      message.success('已签出')
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const openInvite = () => {
    inviteForm.resetFields()
    inviteForm.setFieldsValue({
      verify_channel: 1,
      visit_time: dayjs().add(1, 'hour'),
      expire_time: dayjs().add(1, 'day'),
    })
    setInviteOpen(true)
  }

  const onInvite = async (values: InviteFormValues) => {
    setInviting(true)
    try {
      const resp = await inviteVisitor({
        visitor_name: values.visitor_name,
        visitor_phone: values.visitor_phone,
        visit_time: values.visit_time.unix(),
        expire_time: values.expire_time.unix(),
        reason: values.reason,
        verify_channel: values.verify_channel,
        id_no: values.verify_channel === 3 ? values.id_no : undefined,
        face_token: values.verify_channel === 4 ? values.face_token : undefined,
        badge_no: values.verify_channel === 5 ? values.badge_no : undefined,
      })
      setInviteOpen(false)
      setInviteResult(resp)
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setInviting(false)
    }
  }

  const onBlock = async (values: BlocklistFormValues) => {
    setBlocking(true)
    try {
      const resp = await addBlocklist({
        visitor_name: values.visitor_name,
        phone: values.phone,
        id_no: values.id_no,
        reason: values.reason,
        effective_from: values.effective_from?.unix(),
        effective_to: values.effective_to?.unix(),
      })
      message.success(`已加入黑名单 (ID ${resp.id})`)
      setBlockOpen(false)
      blockForm.resetFields()
    } catch {
      // 拦截器已提示
    } finally {
      setBlocking(false)
    }
  }

  const onUnblock = async () => {
    if (!unblockId) {
      message.warning('请填写黑名单记录 ID')
      return
    }
    try {
      await unblockBlocklist(unblockId)
      message.success('已解除拉黑')
      setUnblockOpen(false)
      setUnblockId(null)
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<VisitorItem> = [
    { title: '访客姓名', dataIndex: 'visitor_name', width: 120 },
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
      title: '签出时间',
      dataIndex: 'checkout_at',
      width: 160,
      render: (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-'),
    },
    {
      title: '操作',
      key: 'actions',
      width: 150,
      render: (_, row) => (
        <Space>
          <Button type="link" size="small" onClick={() => openDetail(row.id)}>
            详情
          </Button>
          {row.status === 2 && (
            <Popconfirm title="确认该访客已离场?" onConfirm={() => onCheckout(row)}>
              <Button type="link" size="small">
                签出
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  return (
    <Card
      title="访客管理"
      extra={
        <Space>
          <Button icon={<StopOutlined />} onClick={() => setUnblockOpen(true)}>
            解除拉黑
          </Button>
          <Button danger icon={<StopOutlined />} onClick={() => setBlockOpen(true)}>
            拉黑
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openInvite}>
            邀请访客
          </Button>
        </Space>
      }
    >
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
        <Button icon={<ReloadOutlined />} onClick={() => fetchList(query)}>
          刷新
        </Button>
      </Space>

      <Table<VisitorItem>
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        scroll={{ x: 900 }}
        pagination={{
          current: query.page,
          pageSize: PAGE_SIZE,
          total,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (page) => setQuery((q) => ({ ...q, page })),
        }}
      />

      {/* 邀请访客 */}
      <Modal
        title="邀请访客"
        open={inviteOpen}
        onCancel={() => setInviteOpen(false)}
        confirmLoading={inviting}
        onOk={() => inviteForm.submit()}
        destroyOnClose
      >
        <Form form={inviteForm} layout="vertical" onFinish={onInvite} requiredMark={false}>
          <Form.Item name="visitor_name" label="访客姓名" rules={[{ required: true, message: '请输入访客姓名' }]}>
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item
            name="visitor_phone"
            label="访客手机号"
            rules={[
              { required: true, message: '请输入访客手机号' },
              { pattern: /^1[3-9]\d{9}$/, message: '手机号格式不正确' },
            ]}
          >
            <Input maxLength={11} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="visit_time" label="预期到访" rules={[{ required: true, message: '请选择预期到访时间' }]}>
              <DatePicker showTime format="YYYY-MM-DD HH:mm" />
            </Form.Item>
            <Form.Item
              name="expire_time"
              label="二维码过期"
              rules={[
                { required: true, message: '请选择过期时间' },
                {
                  validator: (_, v: Dayjs) =>
                    !v || v.isAfter(dayjs())
                      ? Promise.resolve()
                      : Promise.reject(new Error('过期时间必须晚于当前时间')),
                },
              ]}
            >
              <DatePicker showTime format="YYYY-MM-DD HH:mm" />
            </Form.Item>
          </Space>
          <Form.Item name="verify_channel" label="核验方式" initialValue={1}>
            <Select
              options={Object.entries(VERIFY_CHANNEL).map(([v, label]) => ({ value: Number(v), label }))}
            />
          </Form.Item>
          {channel === 3 && (
            <Form.Item name="id_no" label="身份证号" rules={[{ required: true, message: '身份证核验方式需填写身份证号' }]}>
              <Input maxLength={18} />
            </Form.Item>
          )}
          {channel === 4 && (
            <Form.Item name="face_token" label="人脸特征令牌" rules={[{ required: true, message: '人脸核验方式需填写特征令牌' }]}>
              <Input />
            </Form.Item>
          )}
          {channel === 5 && (
            <Form.Item name="badge_no" label="工牌号" rules={[{ required: true, message: '工牌核验方式需填写工牌号' }]}>
              <Input />
            </Form.Item>
          )}
          <Form.Item name="reason" label="来访原因">
            <Input maxLength={64} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 邀请结果: 二维码 */}
      <Modal
        title="邀请已生成"
        open={!!inviteResult}
        footer={<Button onClick={() => setInviteResult(null)}>关闭</Button>}
        onCancel={() => setInviteResult(null)}
      >
        {inviteResult && (
          <Space direction="vertical" style={{ width: '100%' }} align="center">
            {inviteResult.qr_image ? (
              <Image width={220} src={`data:image/png;base64,${inviteResult.qr_image}`} alt="访客通行二维码" />
            ) : null}
            <Typography.Text type="secondary">
              过期时间: {dayjs.unix(inviteResult.expire_at).format('YYYY-MM-DD HH:mm')}
            </Typography.Text>
            <Typography.Paragraph copyable={{ text: inviteResult.qr_code }} style={{ marginBottom: 0 }}>
              二维码内容: {inviteResult.qr_code.slice(0, 24)}...
            </Typography.Paragraph>
          </Space>
        )}
      </Modal>

      {/* 黑名单拉黑 */}
      <Modal
        title="加入访客黑名单"
        open={blockOpen}
        onCancel={() => setBlockOpen(false)}
        confirmLoading={blocking}
        onOk={() => blockForm.submit()}
        destroyOnClose
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="手机号与身份证号至少填写一项作为拉黑维度"
        />
        <Form form={blockForm} layout="vertical" onFinish={onBlock} requiredMark={false}>
          <Form.Item name="visitor_name" label="访客姓名">
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item
            name="phone"
            label="手机号"
            rules={[
              { pattern: /^1[3-9]\d{9}$/, message: '手机号格式不正确' },
              {
                validator: (_, v: string) =>
                  v || blockForm.getFieldValue('id_no')
                    ? Promise.resolve()
                    : Promise.reject(new Error('手机号与身份证号至少填写一项')),
              },
            ]}
          >
            <Input maxLength={11} />
          </Form.Item>
          <Form.Item name="id_no" label="身份证号">
            <Input maxLength={18} />
          </Form.Item>
          <Form.Item name="reason" label="拉黑原因">
            <Input.TextArea rows={2} maxLength={128} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="effective_from" label="生效起(留空=立即)">
              <DatePicker showTime format="YYYY-MM-DD HH:mm" />
            </Form.Item>
            <Form.Item name="effective_to" label="生效止(留空=永久)">
              <DatePicker showTime format="YYYY-MM-DD HH:mm" />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      {/* 解除拉黑 */}
      <Modal
        title="解除黑名单"
        open={unblockOpen}
        onCancel={() => setUnblockOpen(false)}
        onOk={onUnblock}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="后端暂未提供黑名单列表接口, 需手工填写黑名单记录 ID"
        />
        <InputNumber
          min={1}
          style={{ width: '100%' }}
          placeholder="黑名单记录 ID"
          value={unblockId ?? undefined}
          onChange={(v) => setUnblockId(typeof v === 'number' ? v : null)}
        />
      </Modal>

      {/* 详情 + 门禁轨迹 */}
      <Drawer title="访客详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={480}>
        {detail ? (
          <Space direction="vertical" style={{ width: '100%' }} size="large">
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="访客姓名">{detail.visitor_name}</Descriptions.Item>
              <Descriptions.Item label="手机号">{detail.visitor_phone}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={VISITOR_STATUS[detail.status]?.color}>
                  {VISITOR_STATUS[detail.status]?.label ?? detail.status}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="邀请人 ID">{detail.inviter_id}</Descriptions.Item>
              <Descriptions.Item label="预期到访">
                {detail.visit_time ? dayjs.unix(detail.visit_time).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="二维码过期">
                {detail.expire_time ? dayjs.unix(detail.expire_time).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="签入时间">
                {detail.checkin_at ? dayjs.unix(detail.checkin_at).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="签出时间">
                {detail.checkout_at ? dayjs.unix(detail.checkout_at).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="开门设备">{detail.device_id || '-'}</Descriptions.Item>
            </Descriptions>
            <div>
              <Typography.Title level={5}>门禁轨迹</Typography.Title>
              {detail.track?.length ? (
                <Timeline
                  items={detail.track.map((t) => ({
                    color:
                      t.action === 'checkin_open_door'
                        ? 'green'
                        : t.action === 'checkout'
                          ? 'gray'
                          : t.action === 'expired'
                            ? 'red'
                            : 'blue',
                    children: (
                      <Space direction="vertical" size={0}>
                        <span>{VISITOR_TRACK_ACTION[t.action] ?? t.action}</span>
                        <Typography.Text type="secondary">
                          {dayjs.unix(t.time).format('YYYY-MM-DD HH:mm:ss')}
                          {t.device_id ? ` · ${t.device_id}` : ''}
                        </Typography.Text>
                      </Space>
                    ),
                  }))}
                />
              ) : (
                <Typography.Text type="secondary">暂无轨迹</Typography.Text>
              )}
            </div>
          </Space>
        ) : (
          <Typography.Text type="secondary">加载中...</Typography.Text>
        )}
      </Drawer>
    </Card>
  )
}
