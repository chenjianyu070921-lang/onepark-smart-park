import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
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
import {
  assignRole,
  createUser,
  deleteUser,
  listRoles,
  listUsers,
  updateUser,
  type RoleInfo,
  type UserInfo,
} from '../../api/system'

export default function UsersPage() {
  const [users, setUsers] = useState<UserInfo[]>([])
  const [roles, setRoles] = useState<RoleInfo[]>([])
  const [loading, setLoading] = useState(false)

  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<UserInfo | null>(null)
  const [assignTarget, setAssignTarget] = useState<UserInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const [createForm] = Form.useForm()
  const [editForm] = Form.useForm()
  const [assignForm] = Form.useForm()

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const [u, r] = await Promise.all([listUsers(), listRoles()])
      setUsers(u.list ?? [])
      setRoles(r.list ?? [])
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchAll()
  }, [fetchAll])

  const columns: ColumnsType<UserInfo> = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    { title: '用户名', dataIndex: 'username', width: 140 },
    { title: '昵称', dataIndex: 'nickname' },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => (v === 1 ? <Tag color="green">启用</Tag> : <Tag>禁用</Tag>),
    },
    { title: '创建时间', dataIndex: 'created_at', width: 170 },
    {
      title: '操作',
      key: 'actions',
      width: 200,
      render: (_, row) => (
        <Space>
          <Button
            type="link"
            size="small"
            onClick={() => {
              setEditTarget(row)
              editForm.setFieldsValue({ nickname: row.nickname, status: row.status === 1 })
            }}
          >
            编辑
          </Button>
          <Button
            type="link"
            size="small"
            onClick={() => {
              setAssignTarget(row)
              assignForm.resetFields()
            }}
          >
            分配角色
          </Button>
          <Popconfirm title="确认删除该用户?" onConfirm={() => onDelete(row.id)}>
            <Button type="link" size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const onCreate = async (values: {
    username: string
    password: string
    nickname: string
    role_id: number
  }) => {
    setBusy(true)
    try {
      await createUser({ ...values, status: 1 })
      message.success('用户已创建')
      setCreateOpen(false)
      createForm.resetFields()
      fetchAll()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onEdit = async (values: { nickname: string; status: boolean }) => {
    if (!editTarget) return
    setBusy(true)
    try {
      await updateUser({ id: editTarget.id, nickname: values.nickname, status: values.status ? 1 : 0 })
      message.success('用户已更新')
      setEditTarget(null)
      fetchAll()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onAssign = async (values: { role_id: number }) => {
    if (!assignTarget) return
    setBusy(true)
    try {
      await assignRole({ user_id: assignTarget.id, role_id: values.role_id })
      message.success('角色已分配, 用户需重新登录生效')
      setAssignTarget(null)
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onDelete = async (id: number) => {
    try {
      await deleteUser(id)
      message.success('用户已删除')
      fetchAll()
    } catch {
      // 拦截器已提示
    }
  }

  const roleOptions = roles.map((r) => ({ value: r.id, label: `${r.role_name} (${r.role_key})` }))

  return (
    <Card
      title="用户管理"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          新建用户
        </Button>
      }
    >
      <Table<UserInfo> rowKey="id" columns={columns} dataSource={users} loading={loading} pagination={false} />

      <Modal
        title="新建用户"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={busy}
        onOk={() => createForm.submit()}
        destroyOnClose
      >
        <Form form={createForm} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item
            name="password"
            label="初始密码"
            rules={[{ required: true, message: '请输入初始密码' }]}
          >
            <Input.Password maxLength={64} />
          </Form.Item>
          <Form.Item name="nickname" label="昵称" rules={[{ required: true, message: '请输入昵称' }]}>
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item name="role_id" label="角色" rules={[{ required: true, message: '请选择角色' }]}>
            <Select options={roleOptions} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`编辑用户 ${editTarget?.username ?? ''}`}
        open={!!editTarget}
        onCancel={() => setEditTarget(null)}
        confirmLoading={busy}
        onOk={() => editForm.submit()}
        destroyOnClose
      >
        <Form form={editForm} layout="vertical" onFinish={onEdit} requiredMark={false}>
          <Form.Item name="nickname" label="昵称" rules={[{ required: true, message: '请输入昵称' }]}>
            <Input maxLength={32} />
          </Form.Item>
          <Form.Item name="status" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`为 ${assignTarget?.username ?? ''} 分配角色`}
        open={!!assignTarget}
        onCancel={() => setAssignTarget(null)}
        confirmLoading={busy}
        onOk={() => assignForm.submit()}
        destroyOnClose
      >
        <Form form={assignForm} layout="vertical" onFinish={onAssign} requiredMark={false}>
          <Form.Item name="role_id" label="角色" rules={[{ required: true, message: '请选择角色' }]}>
            <Select options={roleOptions} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
