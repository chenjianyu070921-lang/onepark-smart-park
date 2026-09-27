import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  DatePicker,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import {
  NOTICE_STATUS,
  NOTICE_TYPE,
  createNotice,
  listNotices,
  markNoticeRead,
  recallNotice,
  type NoticeItem,
} from '../../api/notice'

const PAGE_SIZE = 10

interface Query {
  type?: number
  status?: number
  page: number
}

export default function NoticeListPage() {
  const [data, setData] = useState<NoticeItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<Query>({ page: 1 })

  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm()

  const fetchList = useCallback(async (q: Query) => {
    setLoading(true)
    try {
      const resp = await listNotices({ ...q, page_size: PAGE_SIZE })
      setData(resp.list ?? [])
      setTotal(resp.total ?? 0)
    } catch {
      // 错误提示由拦截器统一处理
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchList(query)
  }, [query, fetchList])

  const columns: ColumnsType<NoticeItem> = [
    {
      title: '标题',
      dataIndex: 'title',
      ellipsis: true,
      render: (v: string, row) => (
        <Space size={4}>
          {row.top && <Tag color="red">置顶</Tag>}
          {v}
        </Space>
      ),
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 90,
      render: (v: number) => NOTICE_TYPE[v] ?? v,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => {
        const s = NOTICE_STATUS[v]
        return s ? <Tag color={s.color}>{s.label}</Tag> : v
      },
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
      width: 160,
      render: (_, row) => (
        <Space>
          {row.status === 2 && (
            <Button type="link" size="small" onClick={() => onRead(row)}>
              标记已读
            </Button>
          )}
          {row.status === 2 && (
            <Popconfirm
              title="确认撤回该公告?"
              onConfirm={() => onRecall(row.id)}
              okText="撤回"
              cancelText="取消"
            >
              <Button type="link" size="small" danger>
                撤回
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  const onRead = async (row: NoticeItem) => {
    try {
      const resp = await markNoticeRead(row.id)
      message.success(resp.updated > 0 ? '已标记为已读' : '该公告此前已读')
    } catch {
      // 拦截器已提示
    }
  }

  const onRecall = async (id: number) => {
    try {
      await recallNotice(id, '管理员撤回')
      message.success('公告已撤回')
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const onCreate = async (values: {
    title: string
    content: string
    type: number
    top: boolean
    publish_at?: Dayjs
  }) => {
    setCreating(true)
    try {
      await createNotice({
        title: values.title,
        content: values.content,
        type: values.type,
        top: values.top,
        publish_at: values.publish_at ? values.publish_at.unix() : 0,
      })
      message.success('公告已创建')
      setCreateOpen(false)
      form.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setCreating(false)
    }
  }

  return (
    <Card
      title="公告通知"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          发布公告
        </Button>
      }
    >
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="类型筛选"
          style={{ width: 140 }}
          value={query.type}
          onChange={(v) => setQuery((q) => ({ ...q, type: v, page: 1 }))}
          options={Object.entries(NOTICE_TYPE).map(([v, label]) => ({ value: Number(v), label }))}
        />
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(NOTICE_STATUS).map(([v, s]) => ({
            value: Number(v),
            label: s.label,
          }))}
        />
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
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={creating}
        onOk={() => form.submit()}
        destroyOnClose
        width={560}
      >
        <Form form={form} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item name="title" label="标题" rules={[{ required: true, message: '请输入标题' }]}>
            <Input maxLength={100} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="type" label="类型" initialValue={2} rules={[{ required: true }]}>
              <Select
                style={{ width: 160 }}
                options={Object.entries(NOTICE_TYPE).map(([v, label]) => ({
                  value: Number(v),
                  label,
                }))}
              />
            </Form.Item>
            <Form.Item name="top" label="置顶" valuePropName="checked" initialValue={false}>
              <Switch />
            </Form.Item>
            <Form.Item name="publish_at" label="定时发布(留空立即发布)">
              <DatePicker showTime style={{ width: 220 }} />
            </Form.Item>
          </Space>
          <Form.Item name="content" label="正文" rules={[{ required: true, message: '请输入正文' }]}>
            <Input.TextArea rows={5} maxLength={2000} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
