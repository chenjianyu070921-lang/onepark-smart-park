import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  NOTICE_STATUS,
  NOTICE_TYPE,
  createNotice,
  listNotices,
  recallNotice,
  type NoticeItem,
} from '../../api/notice'

const PAGE_SIZE = 10

interface Query {
  type?: number
  status?: number
  page: number
}

interface PublishFormValues {
  title: string
  content: string
  type: number
  top: boolean
  publish_at?: Dayjs
}

export default function NoticeListPage() {
  const [data, setData] = useState<NoticeItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<Query>({ page: 1 })

  const [publishOpen, setPublishOpen] = useState(false)
  const [publishForm] = Form.useForm<PublishFormValues>()

  const [detailOpen, setDetailOpen] = useState(false)
  const [detail, setDetail] = useState<NoticeItem | null>(null)

  const [recallOpen, setRecallOpen] = useState(false)
  const [recallId, setRecallId] = useState<number>()
  const [recallReason, setRecallReason] = useState('')

  const fetchList = useCallback(async (q: Query) => {
    setLoading(true)
    try {
      const resp = await listNotices({
        type: q.type,
        status: q.status,
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

  const openPublish = () => {
    publishForm.resetFields()
    publishForm.setFieldsValue({ type: 2, top: false })
    setPublishOpen(true)
  }

  const onPublish = async (values: PublishFormValues) => {
    try {
      await createNotice({
        title: values.title,
        content: values.content,
        type: values.type,
        top: values.top,
        publish_at: values.publish_at?.unix(),
      })
      message.success(values.publish_at ? '公告已定时发布' : '公告已发布')
      setPublishOpen(false)
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const openRecall = (id: number) => {
    setRecallId(id)
    setRecallReason('')
    setRecallOpen(true)
  }

  const onRecall = async () => {
    if (recallId == null) return
    try {
      await recallNotice(recallId, recallReason || undefined)
      message.success('公告已撤回')
      setRecallOpen(false)
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<NoticeItem> = [
    { title: '标题', dataIndex: 'title', ellipsis: true },
    {
      title: '类型',
      dataIndex: 'type',
      width: 100,
      render: (v: number) => NOTICE_TYPE[v] ?? v,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => (
        <Tag color={NOTICE_STATUS[v]?.color}>{NOTICE_STATUS[v]?.label ?? v}</Tag>
      ),
    },
    {
      title: '置顶',
      dataIndex: 'top',
      width: 80,
      render: (v: boolean) => (v ? <Tag color="red">置顶</Tag> : '-'),
    },
    {
      title: '发布时间',
      dataIndex: 'publish_at',
      width: 160,
      render: (v?: number) => (v ? dayjs.unix(v).format('YYYY-MM-DD HH:mm') : '-'),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: number) => dayjs.unix(v).format('YYYY-MM-DD HH:mm'),
    },
    {
      title: '操作',
      key: 'actions',
      width: 140,
      render: (_, row) => (
        <Space>
          <Button type="link" size="small" onClick={() => { setDetail(row); setDetailOpen(true) }}>
            详情
          </Button>
          {row.status === 2 && (
            <Popconfirm title="确认撤回该公告?" onConfirm={() => openRecall(row.id)}>
              <Button type="link" size="small" danger>
                撤回
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  return (
    <Card
      title="公告通知"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={openPublish}>
          发布公告
        </Button>
      }
    >
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="类型"
          style={{ width: 120 }}
          value={query.type}
          onChange={(v) => setQuery((q) => ({ ...q, type: v, page: 1 }))}
          options={Object.entries(NOTICE_TYPE).map(([v, label]) => ({ value: Number(v), label }))}
        />
        <Select
          allowClear
          placeholder="状态"
          style={{ width: 120 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(NOTICE_STATUS).map(([v, s]) => ({ value: Number(v), label: s.label }))}
        />
        <Button icon={<ReloadOutlined />} onClick={() => fetchList(query)}>
          刷新
        </Button>
      </Space>

      <Table<NoticeItem>
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
        title="发布公告"
        open={publishOpen}
        onCancel={() => setPublishOpen(false)}
        onOk={() => publishForm.submit()}
        destroyOnClose
      >
        <Form form={publishForm} layout="vertical" onFinish={onPublish} requiredMark={false}>
          <Form.Item name="title" label="标题" rules={[{ required: true, message: '请输入标题' }]}>
            <Input maxLength={128} />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select
              options={Object.entries(NOTICE_TYPE).map(([v, label]) => ({ value: Number(v), label }))}
            />
          </Form.Item>
          <Form.Item name="content" label="正文" rules={[{ required: true, message: '请输入正文' }]}>
            <Input.TextArea rows={4} maxLength={2000} />
          </Form.Item>
          <Form.Item name="top" label="置顶" valuePropName="checked">
            <Select
              options={[
                { value: false, label: '不置顶' },
                { value: true, label: '置顶' },
              ]}
            />
          </Form.Item>
          <Form.Item name="publish_at" label="发布方式">
            <DatePicker
              showTime
              format="YYYY-MM-DD HH:mm"
              placeholder="留空=立即发布; 选择时间=定时发布"
              style={{ width: '100%' }}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal title="撤回公告" open={recallOpen} onCancel={() => setRecallOpen(false)} onOk={onRecall}>
        <Input.TextArea
          rows={3}
          maxLength={128}
          placeholder="撤回原因(可选)"
          value={recallReason}
          onChange={(e) => setRecallReason(e.target.value)}
        />
      </Modal>

      <Drawer title="公告详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={480}>
        {detail ? (
          <Space direction="vertical" style={{ width: '100%' }} size="middle">
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="标题">{detail.title}</Descriptions.Item>
              <Descriptions.Item label="类型">{NOTICE_TYPE[detail.type] ?? detail.type}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={NOTICE_STATUS[detail.status]?.color}>
                  {NOTICE_STATUS[detail.status]?.label ?? detail.status}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="置顶">{detail.top ? '是' : '否'}</Descriptions.Item>
              <Descriptions.Item label="发布时间">
                {detail.publish_at ? dayjs.unix(detail.publish_at).format('YYYY-MM-DD HH:mm') : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="创建时间">
                {dayjs.unix(detail.created_at).format('YYYY-MM-DD HH:mm')}
              </Descriptions.Item>
            </Descriptions>
            <Typography.Paragraph>{detail.content ?? '-'}</Typography.Paragraph>
          </Space>
        ) : (
          <Typography.Text type="secondary">加载中...</Typography.Text>
        )}
      </Drawer>
    </Card>
  )
}
