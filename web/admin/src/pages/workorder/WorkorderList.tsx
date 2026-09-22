import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  Descriptions,
  Drawer,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import {
  WORKORDER_PRIORITY,
  WORKORDER_STATUS,
  WORKORDER_TYPE,
  assignWorkOrder,
  createWorkOrder,
  getWorkOrder,
  listWorkOrders,
  updateWorkOrderStatus,
  type WorkOrderDetail,
  type WorkOrderItem,
} from '../../api/workorder'

const PAGE_SIZE = 10

const statusColor: Record<number, string> = {
  0: 'gold',
  1: 'processing',
  2: 'cyan',
  3: 'green',
  4: 'default',
}

interface Query {
  status?: number
  type?: number
  page: number
}

export default function WorkorderListPage() {
  const [data, setData] = useState<WorkOrderItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState<Query>({ page: 1 })

  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createForm] = Form.useForm()

  const [assignTarget, setAssignTarget] = useState<WorkOrderItem | null>(null)
  const [assigning, setAssigning] = useState(false)
  const [assignForm] = Form.useForm()

  const [detail, setDetail] = useState<WorkOrderDetail | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const fetchList = useCallback(async (q: Query) => {
    setLoading(true)
    try {
      const resp = await listWorkOrders({
        status: q.status,
        type: q.type,
        page: q.page,
        page_size: PAGE_SIZE,
      })
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

  const columns: ColumnsType<WorkOrderItem> = [
    { title: '工单号', dataIndex: 'order_no', width: 160 },
    { title: '标题', dataIndex: 'title', ellipsis: true },
    {
      title: '类型',
      dataIndex: 'type',
      width: 90,
      render: (v: number) => WORKORDER_TYPE[v] ?? v,
    },
    {
      title: '优先级',
      dataIndex: 'priority',
      width: 90,
      render: (v: number) => {
        const p = WORKORDER_PRIORITY[v]
        return p ? <Tag color={p.color}>{p.label}</Tag> : v
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => <Tag color={statusColor[v]}>{WORKORDER_STATUS[v] ?? v}</Tag>,
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
      width: 200,
      render: (_, row) => {
        const actions: MenuAction[] = []
        if (row.status === 0) actions.push({ key: 'assign', label: '派单' })
        if (row.status === 1) actions.push({ key: 'submit', label: '提交验收' })
        if (row.status === 2) {
          actions.push({ key: 'approve', label: '验收通过' })
          actions.push({ key: 'reject', label: '驳回' })
        }
        if (row.status >= 0 && row.status <= 2) actions.push({ key: 'close', label: '关闭' })
        return (
          <Space>
            <Button type="link" size="small" onClick={() => openDetail(row.id)}>
              详情
            </Button>
            {actions.length > 0 && (
              <Dropdown
                menu={{
                  items: actions,
                  onClick: ({ key }) => onAction(key, row),
                }}
              >
                <Button type="link" size="small">
                  流转
                </Button>
              </Dropdown>
            )}
          </Space>
        )
      },
    },
  ]

  type MenuAction = { key: string; label: string }

  const onAction = async (key: string, row: WorkOrderItem) => {
    if (key === 'assign') {
      setAssignTarget(row)
      assignForm.resetFields()
      return
    }
    // approve/reject 需要备注时改为 Modal 确认, 当前先带默认备注流转.
    const remarkMap: Record<string, string> = {
      submit: '处理完成, 提交验收',
      approve: '验收通过',
      reject: '驳回重做',
      close: '关闭工单',
    }
    try {
      await updateWorkOrderStatus(row.id, key, remarkMap[key])
      message.success('工单状态已更新')
      fetchList(query)
    } catch {
      // 拦截器已提示
    }
  }

  const openDetail = async (id: number) => {
    setDetailOpen(true)
    setDetail(null)
    try {
      setDetail(await getWorkOrder(id))
    } catch {
      setDetailOpen(false)
    }
  }

  const onCreate = async (values: {
    type: number
    title: string
    priority: number
    location: string
    description: string
  }) => {
    setCreating(true)
    try {
      const resp = await createWorkOrder(values)
      message.success(`工单 ${resp.order_no} 已创建`)
      setCreateOpen(false)
      createForm.resetFields()
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setCreating(false)
    }
  }

  const onAssign = async (values: { assignee_id: number; department_id?: number }) => {
    if (!assignTarget) return
    setAssigning(true)
    try {
      await assignWorkOrder(assignTarget.id, values)
      message.success('派单成功')
      setAssignTarget(null)
      fetchList(query)
    } catch {
      // 拦截器已提示
    } finally {
      setAssigning(false)
    }
  }

  return (
    <Card
      title="工单管理"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          创建工单
        </Button>
      }
    >
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery((q) => ({ ...q, status: v, page: 1 }))}
          options={Object.entries(WORKORDER_STATUS).map(([v, label]) => ({
            value: Number(v),
            label,
          }))}
        />
        <Select
          allowClear
          placeholder="类型筛选"
          style={{ width: 140 }}
          value={query.type}
          onChange={(v) => setQuery((q) => ({ ...q, type: v, page: 1 }))}
          options={Object.entries(WORKORDER_TYPE).map(([v, label]) => ({
            value: Number(v),
            label,
          }))}
        />
      </Space>

      <Table<WorkOrderItem>
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
        title="创建工单"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={creating}
        onOk={() => createForm.submit()}
        destroyOnClose
      >
        <Form form={createForm} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item name="title" label="标题" rules={[{ required: true, message: '请输入标题' }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Space wrap>
            <Form.Item name="type" label="类型" initialValue={1} rules={[{ required: true }]}>
              <Select
                style={{ width: 160 }}
                options={Object.entries(WORKORDER_TYPE).map(([v, label]) => ({
                  value: Number(v),
                  label,
                }))}
              />
            </Form.Item>
            <Form.Item name="priority" label="优先级" initialValue={2} rules={[{ required: true }]}>
              <Select
                style={{ width: 160 }}
                options={Object.entries(WORKORDER_PRIORITY).map(([v, p]) => ({
                  value: Number(v),
                  label: p.label,
                }))}
              />
            </Form.Item>
          </Space>
          <Form.Item name="location" label="位置">
            <Input placeholder="楼栋/房号" maxLength={64} />
          </Form.Item>
          <Form.Item name="description" label="详细描述">
            <Input.TextArea rows={4} maxLength={500} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`派单 ${assignTarget?.order_no ?? ''}`}
        open={!!assignTarget}
        onCancel={() => setAssignTarget(null)}
        confirmLoading={assigning}
        onOk={() => assignForm.submit()}
        destroyOnClose
      >
        <Form form={assignForm} layout="vertical" onFinish={onAssign} requiredMark={false}>
          <Form.Item
            name="assignee_id"
            label="处理人 ID"
            rules={[{ required: true, message: '请输入处理人 ID' }]}
          >
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="department_id" label="处理部门 ID">
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Typography.Text type="secondary">
            后续接入用户下拉选择, 当前联调阶段直接填写 ID
          </Typography.Text>
        </Form>
      </Modal>

      <Drawer title="工单详情" open={detailOpen} onClose={() => setDetailOpen(false)} width={480}>
        {detail ? (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="工单号">{detail.order_no}</Descriptions.Item>
            <Descriptions.Item label="标题">{detail.title}</Descriptions.Item>
            <Descriptions.Item label="状态">
              <Tag color={statusColor[detail.status]}>{WORKORDER_STATUS[detail.status]}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="类型">{WORKORDER_TYPE[detail.type]}</Descriptions.Item>
            <Descriptions.Item label="优先级">
              {WORKORDER_PRIORITY[detail.priority]?.label}
            </Descriptions.Item>
            <Descriptions.Item label="位置">{detail.location || '-'}</Descriptions.Item>
            <Descriptions.Item label="描述">{detail.description || '-'}</Descriptions.Item>
            <Descriptions.Item label="处理人 ID">{detail.assignee_id || '-'}</Descriptions.Item>
            <Descriptions.Item label="创建时间">
              {dayjs.unix(detail.created_at).format('YYYY-MM-DD HH:mm')}
            </Descriptions.Item>
            <Descriptions.Item label="更新时间">
              {dayjs.unix(detail.updated_at).format('YYYY-MM-DD HH:mm')}
            </Descriptions.Item>
          </Descriptions>
        ) : (
          <Typography.Text type="secondary">加载中</Typography.Text>
        )}
      </Drawer>
    </Card>
  )
}
