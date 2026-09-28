import { useState } from 'react'
import {
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Dropdown,
  Form,
  Input,
  InputNumber,
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
import dayjs from 'dayjs'
import {
  CONTRACT_STATUS,
  createContract,
  getContract,
  listContracts,
  listExpiringContracts,
  updateContract,
  type Contract,
  type ExpiringContract,
} from '../../api/leasing'
import { useTableQuery, type BaseQuery } from '../../hooks/useTableQuery'
import FormModal from '../../components/FormModal'
import { fmtMoney, fmtTime, toOptions } from '../../utils/format'

const PAGE_SIZE = 10

interface ContractQuery extends BaseQuery {
  status?: number
  tenant_id?: number
}

interface ExpiringQuery extends BaseQuery {
  days: number
}

// action -> 中文名与是否需要补充参数
const ACTION_LABEL: Record<string, string> = {
  activate: '生效',
  renew: '续签',
  terminate: '终止',
  expire: '标记到期',
  update: '变更条款',
}

type ActionModal = {
  action: 'renew' | 'terminate' | 'update'
  row: Contract
} | null

function ContractTable() {
  const { list, loading, query, setQuery, pagination, reload } = useTableQuery<
    Contract,
    ContractQuery
  >((q) => listContracts(q), { page: 1, page_size: PAGE_SIZE })

  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [acting, setActing] = useState(false)
  const [actionModal, setActionModal] = useState<ActionModal>(null)

  const [detail, setDetail] = useState<Contract | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const columns: ColumnsType<Contract> = [
    { title: '合同号', dataIndex: 'contract_no', width: 150 },
    { title: '租户', dataIndex: 'tenant_name', width: 140, ellipsis: true },
    { title: '区域', dataIndex: 'zone_code', width: 120 },
    {
      title: '面积(㎡)',
      dataIndex: 'area_sqm',
      width: 100,
      render: (v: number) => v?.toLocaleString('zh-CN'),
    },
    { title: '月租金', dataIndex: 'monthly_rent', width: 120, render: (v: string) => fmtMoney(v) },
    { title: '起租日', dataIndex: 'start_date', width: 110 },
    { title: '终止日', dataIndex: 'end_date', width: 110 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => (
        <Tag color={CONTRACT_STATUS[v]?.color}>{CONTRACT_STATUS[v]?.label ?? v}</Tag>
      ),
    },
    {
      title: '自动续约',
      dataIndex: 'auto_renew',
      width: 90,
      render: (v: number) => (v === 1 ? <Tag color="cyan">自动</Tag> : <Tag>到期即止</Tag>),
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
          <Dropdown
            menu={{
              items: Object.entries(ACTION_LABEL).map(([key, label]) => ({ key, label })),
              onClick: ({ key }) => onAction(key, row),
            }}
          >
            <Button type="link" size="small">
              流转
            </Button>
          </Dropdown>
        </Space>
      ),
    },
  ]

  const openDetail = async (id: number) => {
    setDetailOpen(true)
    setDetail(null)
    try {
      const resp = await getContract(id)
      setDetail(resp.contract)
    } catch {
      setDetailOpen(false)
    }
  }

  const onAction = (action: string, row: Contract) => {
    if (action === 'renew' || action === 'terminate' || action === 'update') {
      setActionModal({ action, row })
      return
    }
    void submitAction(action, row)
  }

  const submitAction = async (
    action: string,
    row: Contract,
    extra?: Record<string, unknown>,
  ) => {
    setActing(true)
    try {
      await updateContract(row.id, { action, ...extra })
      message.success(`已${ACTION_LABEL[action] ?? '更新'}`)
      setActionModal(null)
      reload()
    } catch {
      // 拦截器已提示
    } finally {
      setActing(false)
    }
  }

  const onCreate = async (values: Record<string, unknown>) => {
    setCreating(true)
    try {
      const resp = await createContract({
        tenant_id: Number(values.tenant_id),
        tenant_name: String(values.tenant_name),
        zone_code: String(values.zone_code),
        area_sqm: Number(values.area_sqm),
        monthly_rent: String(values.monthly_rent),
        deposit: values.deposit ? String(values.deposit) : undefined,
        start_date: (values.start_date as dayjs.Dayjs).format('YYYY-MM-DD'),
        end_date: (values.end_date as dayjs.Dayjs).format('YYYY-MM-DD'),
        auto_renew: Number(values.auto_renew ?? 0),
        renew_notice_days: Number(values.renew_notice_days ?? 30),
      })
      message.success(`合同 ${resp.contract_no} 已创建`)
      setCreateOpen(false)
      reload()
    } catch {
      // 拦截器已提示
    } finally {
      setCreating(false)
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
          onChange={(v) => setQuery({ status: v })}
          options={toOptions(CONTRACT_STATUS)}
        />
        <InputNumber
          placeholder="租户 ID"
          style={{ width: 140 }}
          min={1}
          onChange={(v) => setQuery({ tenant_id: v ?? undefined })}
        />
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          新建合同
        </Button>
      </Space>

      <Table<Contract>
        rowKey="id"
        columns={columns}
        dataSource={list}
        loading={loading}
        pagination={pagination}
        locale={{ emptyText: '暂无合同数据' }}
      />

      <FormModal
        open={createOpen}
        title="新建合同"
        loading={creating}
        width={640}
        onCancel={() => setCreateOpen(false)}
        onSubmit={onCreate}
      >
        <Space wrap>
          <Form.Item
            name="tenant_id"
            label="租户 ID"
            rules={[{ required: true, message: '请输入租户 ID' }]}
          >
            <InputNumber min={1} style={{ width: 160 }} />
          </Form.Item>
          <Form.Item
            name="tenant_name"
            label="租户名称"
            rules={[{ required: true, message: '请输入租户名称' }]}
          >
            <Input style={{ width: 200 }} maxLength={64} />
          </Form.Item>
        </Space>
        <Space wrap>
          <Form.Item
            name="zone_code"
            label="区域编码"
            rules={[{ required: true, message: '请输入区域编码' }]}
          >
            <Input placeholder="如 A-3F-301" style={{ width: 200 }} />
          </Form.Item>
          <Form.Item
            name="area_sqm"
            label="租赁面积(㎡)"
            rules={[{ required: true, message: '请输入面积' }]}
          >
            <InputNumber min={0} step={0.01} style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Space wrap>
          <Form.Item
            name="monthly_rent"
            label="月租金"
            rules={[{ required: true, message: '请输入月租金' }]}
          >
            <Input style={{ width: 160 }} />
          </Form.Item>
          <Form.Item name="deposit" label="押金">
            <Input style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Space wrap>
          <Form.Item
            name="start_date"
            label="起租日"
            rules={[{ required: true, message: '请选择起租日' }]}
          >
            <DatePicker style={{ width: 160 }} />
          </Form.Item>
          <Form.Item
            name="end_date"
            label="终止日"
            rules={[{ required: true, message: '请选择终止日' }]}
          >
            <DatePicker style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Space wrap>
          <Form.Item name="auto_renew" label="自动续约" initialValue={0}>
            <Select
              style={{ width: 160 }}
              options={[
                { value: 0, label: '到期即止' },
                { value: 1, label: '自动续约' },
              ]}
            />
          </Form.Item>
          <Form.Item name="renew_notice_days" label="续签提醒提前天数" initialValue={30}>
            <InputNumber min={0} style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Typography.Text type="secondary">
          金额用字符串提交, 避免浮点精度丢失; 自动续约仅当签约时明确约定才置为「自动续约」.
        </Typography.Text>
      </FormModal>

      {/* 续签 / 终止 / 变更: 三种 action 需要补充参数, 复用同一个 Modal 按类型渲染字段 */}
      <FormModal
        open={!!actionModal}
        title={actionModal ? `${ACTION_LABEL[actionModal.action]} ${actionModal.row.contract_no}` : ''}
        loading={acting}
        onCancel={() => setActionModal(null)}
        initialValues={
          actionModal
            ? ({
                monthly_rent: actionModal.row.monthly_rent,
                auto_renew: actionModal.row.auto_renew,
                renew_notice_days: actionModal.row.renew_notice_days,
              } as Record<string, unknown>)
            : undefined
        }
        onSubmit={(values: Record<string, unknown>) => {
          if (!actionModal) return
          const extra: Record<string, unknown> = {}
          if (actionModal.action === 'renew') {
            extra.new_end_date = (values.new_end_date as dayjs.Dayjs).format('YYYY-MM-DD')
            extra.monthly_rent = String(values.monthly_rent)
          }
          if (actionModal.action === 'terminate') extra.reason = String(values.reason ?? '')
          if (actionModal.action === 'update') {
            extra.auto_renew = Number(values.auto_renew)
            extra.renew_notice_days = Number(values.renew_notice_days)
          }
          void submitAction(actionModal.action, actionModal.row, extra)
        }}
      >
        {actionModal?.action === 'renew' && (
          <>
            <Form.Item
              name="new_end_date"
              label="新终止日"
              rules={[{ required: true, message: '请选择新的终止日' }]}
            >
              <DatePicker style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="monthly_rent" label="月租金(可调整)">
              <Input />
            </Form.Item>
          </>
        )}
        {actionModal?.action === 'terminate' && (
          <Form.Item name="reason" label="终止原因" rules={[{ required: true, message: '请填写原因' }]}>
            <Input.TextArea rows={3} maxLength={200} />
          </Form.Item>
        )}
        {actionModal?.action === 'update' && (
          <Space wrap>
            <Form.Item name="auto_renew" label="自动续约">
              <Select
                style={{ width: 160 }}
                options={[
                  { value: 0, label: '到期即止' },
                  { value: 1, label: '自动续约' },
                ]}
              />
            </Form.Item>
            <Form.Item name="renew_notice_days" label="续签提醒提前天数">
              <InputNumber min={0} style={{ width: 160 }} />
            </Form.Item>
          </Space>
        )}
      </FormModal>

      <Drawer
        title="合同详情"
        open={detailOpen}
        onClose={() => setDetailOpen(false)}
        width={480}
      >
        {detail ? (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="合同号">{detail.contract_no}</Descriptions.Item>
            <Descriptions.Item label="租户">{detail.tenant_name}</Descriptions.Item>
            <Descriptions.Item label="租户 ID">{detail.tenant_id}</Descriptions.Item>
            <Descriptions.Item label="区域">{detail.zone_code}</Descriptions.Item>
            <Descriptions.Item label="面积(㎡)">{detail.area_sqm}</Descriptions.Item>
            <Descriptions.Item label="月租金">{fmtMoney(detail.monthly_rent)}</Descriptions.Item>
            <Descriptions.Item label="押金">{fmtMoney(detail.deposit)}</Descriptions.Item>
            <Descriptions.Item label="租期">
              {detail.start_date} ~ {detail.end_date}
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Tag color={CONTRACT_STATUS[detail.status]?.color}>
                {CONTRACT_STATUS[detail.status]?.label ?? detail.status}
              </Tag>
            </Descriptions.Item>
            <Descriptions.Item label="自动续约">
              {detail.auto_renew === 1 ? '是' : '否'}
            </Descriptions.Item>
            <Descriptions.Item label="续签提醒提前">
              {detail.renew_notice_days} 天
            </Descriptions.Item>
            <Descriptions.Item label="创建时间">{fmtTime(detail.created_at)}</Descriptions.Item>
            <Descriptions.Item label="更新时间">{fmtTime(detail.updated_at)}</Descriptions.Item>
          </Descriptions>
        ) : (
          <Typography.Text type="secondary">加载中</Typography.Text>
        )}
      </Drawer>
    </>
  )
}

function ExpiringTable() {
  const { list, loading, query, setQuery, pagination } = useTableQuery<
    ExpiringContract,
    ExpiringQuery
  >((q) => listExpiringContracts(q), { days: 30, page: 1, page_size: PAGE_SIZE })

  const columns: ColumnsType<ExpiringContract> = [
    { title: '合同号', dataIndex: 'contract_no', width: 150 },
    { title: '租户', dataIndex: 'tenant_name', width: 140, ellipsis: true },
    { title: '区域', dataIndex: 'zone_code', width: 120 },
    { title: '月租金', dataIndex: 'monthly_rent', width: 120, render: (v: string) => fmtMoney(v) },
    { title: '终止日', dataIndex: 'end_date', width: 110 },
    {
      title: '剩余天数',
      dataIndex: 'days_left',
      width: 110,
      render: (v: number) =>
        v < 0 ? <Tag color="red">已逾期 {Math.abs(v)} 天</Tag> : <Tag color="orange">{v} 天</Tag>,
    },
    {
      title: '续签状态',
      key: 'renew',
      width: 140,
      render: (_, row) =>
        row.auto_renew === 1 ? (
          <Tag color="cyan">将自动续约</Tag>
        ) : row.need_notice ? (
          <Tag color="gold">待谈续签</Tag>
        ) : (
          <Tag>未进入提醒窗口</Tag>
        ),
    },
  ]

  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          value={query.days}
          style={{ width: 160 }}
          onChange={(v) => setQuery({ days: v })}
          options={[
            { value: 7, label: '未来 7 天' },
            { value: 30, label: '未来 30 天' },
            { value: 90, label: '未来 90 天' },
          ]}
        />
      </Space>
      <Table<ExpiringContract>
        rowKey="id"
        columns={columns}
        dataSource={list}
        loading={loading}
        pagination={pagination}
        locale={{ emptyText: '该时间窗口内没有到期合同' }}
      />
    </>
  )
}

export default function ContractListPage() {
  return (
    <Card title="合同管理">
      <Tabs
        destroyInactiveTabPane
        items={[
          { key: 'list', label: '合同列表', children: <ContractTable /> },
          { key: 'expiring', label: '到期提醒', children: <ExpiringTable /> },
        ]}
      />
    </Card>
  )
}
