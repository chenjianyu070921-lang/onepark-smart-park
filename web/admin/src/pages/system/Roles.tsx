import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Row,
  Select,
  Table,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import {
  assignMenu,
  createRole,
  listMenus,
  listRoles,
  type MenuInfo,
  type RoleInfo,
} from '../../api/system'

function normalizeMenus(resp: { list: MenuInfo[]; total: number } | MenuInfo[]): MenuInfo[] {
  return Array.isArray(resp) ? resp : (resp?.list ?? [])
}

export default function RolesPage() {
  const [roles, setRoles] = useState<RoleInfo[]>([])
  const [menus, setMenus] = useState<MenuInfo[]>([])
  const [loading, setLoading] = useState(false)

  const [createOpen, setCreateOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [createForm] = Form.useForm()
  const [assignForm] = Form.useForm()

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const [r, m] = await Promise.all([listRoles(), listMenus()])
      setRoles(r.list ?? [])
      setMenus(normalizeMenus(m).sort((a, b) => a.sort - b.sort))
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchAll()
  }, [fetchAll])

  const roleColumns: ColumnsType<RoleInfo> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '角色标识', dataIndex: 'role_key', width: 140 },
    { title: '角色名', dataIndex: 'role_name', width: 140 },
    { title: '说明', dataIndex: 'remark', ellipsis: true },
  ]

  const menuColumns: ColumnsType<MenuInfo> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '菜单名', dataIndex: 'menu_name', width: 140 },
    { title: '标识', dataIndex: 'menu_key', width: 130 },
    { title: '权限串', dataIndex: 'permission', width: 140, ellipsis: true },
    { title: '路径', dataIndex: 'path', ellipsis: true },
    { title: '父级', dataIndex: 'parent_id', width: 70 },
  ]

  const onCreate = async (values: { role_key: string; role_name: string; remark?: string }) => {
    setBusy(true)
    try {
      await createRole(values)
      message.success('角色已创建')
      setCreateOpen(false)
      createForm.resetFields()
      fetchAll()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  const onAssign = async (values: { role_id: number; menu_ids: number[] }) => {
    setBusy(true)
    try {
      // 后端按 (role_id, menu_id) 单条分配, 多条逐个提交
      for (const menuId of values.menu_ids) {
        await assignMenu({ role_id: values.role_id, menu_id: menuId })
      }
      message.success(`已为角色分配 ${values.menu_ids.length} 个菜单`)
      assignForm.resetFields()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title="角色与菜单"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          新建角色
        </Button>
      }
    >
      <Row gutter={16}>
        <Col span={10}>
          <Typography.Title level={5}>角色列表</Typography.Title>
          <Table<RoleInfo>
            rowKey="id"
            columns={roleColumns}
            dataSource={roles}
            loading={loading}
            pagination={false}
            size="small"
          />
        </Col>
        <Col span={14}>
          <Typography.Title level={5}>菜单(权限项)</Typography.Title>
          <Table<MenuInfo>
            rowKey="id"
            columns={menuColumns}
            dataSource={menus}
            loading={loading}
            pagination={false}
            size="small"
          />
        </Col>
      </Row>

      <Card size="small" title="为角色分配菜单" style={{ marginTop: 24 }}>
        <Form form={assignForm} layout="inline" onFinish={onAssign} requiredMark={false}>
          <Form.Item name="role_id" label="角色" rules={[{ required: true, message: '请选择角色' }]}>
            <Select
              style={{ width: 220 }}
              options={roles.map((r) => ({ value: r.id, label: r.role_name }))}
            />
          </Form.Item>
          <Form.Item name="menu_ids" label="菜单" rules={[{ required: true, message: '请选择菜单' }]}>
            <Select
              mode="multiple"
              style={{ width: 420 }}
              options={menus.map((m) => ({ value: m.id, label: `${m.menu_name} (${m.permission})` }))}
            />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={busy}>
              分配
            </Button>
          </Form.Item>
        </Form>
      </Card>

      <Modal
        title="新建角色"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={busy}
        onOk={() => createForm.submit()}
        destroyOnClose
      >
        <Form form={createForm} layout="vertical" onFinish={onCreate} requiredMark={false}>
          <Form.Item
            name="role_key"
            label="角色标识(英文)"
            rules={[{ required: true, message: '请输入角色标识' }]}
          >
            <Input placeholder="如 security_lead" maxLength={32} />
          </Form.Item>
          <Form.Item name="role_name" label="角色名" rules={[{ required: true, message: '请输入角色名' }]}>
            <Input placeholder="如 安保主管" maxLength={32} />
          </Form.Item>
          <Form.Item name="remark" label="说明">
            <Input.TextArea rows={2} maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
