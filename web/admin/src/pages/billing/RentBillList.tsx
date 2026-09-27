import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  DatePicker,
  Form,
  InputNumber,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ThunderboltOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import {
  BILL_STATUS,
  generateBills,
  getBillSummary,
  listBills,
  updateBillStatus,
  type Bill,
  type BillSummaryResp,
} from '../../api/leasing'
import { useTableQuery, type BaseQuery } from '../../hooks/useTableQuery'
import FormModal from '../../components/FormModal'
import { fmtMoney, fmtTime, toOptions } from '../../utils/format'

const PAGE_SIZE = 10

interface BillQuery extends BaseQuery {
  period?: string
  status?: number
  contract_id?: number
  tenant_id?: number
}

export default function RentBillListPage() {
  const { list, loading, query, setQuery, pagination, reload } = useTableQuery<Bill, BillQuery>(
    (q) => listBills(q),
    { page: 1, page_size: PAGE_SIZE },
  )

  const [summaryPeriod, setSummaryPeriod] = useState<string | undefined>(undefined)
  const [summary, setSummary] = useState<BillSummaryResp | null>(null)
  const [summaryError, setSummaryError] = useState(false)

  const [genOpen, setGenOpen] = useState(false)
  const [generating, setGenerating] = useState(false)

  const loadSummary = async (period?: string) => {
    setSummaryError(false)
    try {
      setSummary(await getBillSummary({ period }))
    } catch {
      setSummary(null)
      setSummaryError(true)
    }
  }

  useEffect(() => {
    void loadSummary(summaryPeriod)
  }, [summaryPeriod])

  const columns: ColumnsType<Bill> = [
    { title: '账单号', dataIndex: 'bill_no', width: 160 },
    { title: '合同 ID', dataIndex: 'contract_id', width: 100 },
    { title: '租户 ID', dataIndex: 'tenant_id', width: 100 },
    { title: '账期', dataIndex: 'billing_period', width: 110 },
    { title: '金额(元)', dataIndex: 'amount', width: 130, render: (v: string) => fmtMoney(v) },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => <Tag color={BILL_STATUS[v]?.color}>{BILL_STATUS[v]?.label ?? v}</Tag>,
    },
    { title: '出账时间', dataIndex: 'created_at', width: 160, render: (v: number) => fmtTime(v) },
    {
      title: '操作',
      key: 'actions',
      width: 160,
      render: (_, row) => (
        <Space>
          {row.status === 1 ? (
            <Button type="link" size="small" onClick={() => onStatus(row, 'pay')}>
              标记已缴
            </Button>
          ) : (
            <Button type="link" size="small" onClick={() => onStatus(row, 'unpay')}>
              撤销缴费
            </Button>
          )}
        </Space>
      ),
    },
  ]

  const onStatus = async (row: Bill, action: 'pay' | 'unpay') => {
    try {
      await updateBillStatus(row.id, action)
      message.success(action === 'pay' ? '已标记为已缴' : '已撤销缴费')
      reload()
      void loadSummary(summaryPeriod)
    } catch {
      // 拦截器已提示
    }
  }

  const onGenerate = async (values: { period?: dayjs.Dayjs }) => {
    setGenerating(true)
    try {
      const resp = await generateBills({
        period: values.period ? values.period.format('YYYY-MM') : undefined,
      })
      message.success(
        `账期 ${resp.period} 出账完成: 新生成 ${resp.created} 条, 幂等跳过 ${resp.skipped} 条`,
      )
      setGenOpen(false)
      reload()
      void loadSummary(summaryPeriod)
    } catch {
      // 拦截器已提示
    } finally {
      setGenerating(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Card
        title="租金账单汇总"
        extra={
          <Space wrap>
            <DatePicker
              picker="month"
              placeholder="账期(空=全部)"
              value={summaryPeriod ? dayjs(summaryPeriod) : null}
              onChange={(v) => setSummaryPeriod(v ? v.format('YYYY-MM') : undefined)}
            />
            <Button
              type="primary"
              icon={<ThunderboltOutlined />}
              onClick={() => setGenOpen(true)}
            >
              生成月度账单
            </Button>
          </Space>
        }
      >
        {summaryError ? (
          <Alert
            type="warning"
            showIcon
            message="账单汇总暂不可用"
            description="请确认 leasing-service 已启动; 未出账时汇总为空也属正常."
          />
        ) : (
          <Row gutter={[16, 16]}>
            <Col xs={12} sm={6}>
              <Statistic title="账期" value={summary?.period || '全部账期'} />
            </Col>
            <Col xs={12} sm={6}>
              <Statistic title="账单笔数" value={summary?.bill_count ?? 0} />
            </Col>
            <Col xs={12} sm={6}>
              <Statistic
                title="应收(元)"
                value={fmtMoney(summary?.total_amount ?? '0')}
              />
            </Col>
            <Col xs={12} sm={6}>
              <Statistic
                title="欠费(元)"
                value={fmtMoney(summary?.unpaid_amount ?? '0')}
                valueStyle={{ color: '#cf1322' }}
              />
            </Col>
            <Col xs={12} sm={6}>
              <Statistic
                title="已收(元)"
                value={fmtMoney(summary?.paid_amount ?? '0')}
                valueStyle={{ color: '#3f8600' }}
              />
            </Col>
            <Col xs={12} sm={6}>
              <Statistic
                title="未缴笔数"
                value={summary?.unpaid_count ?? 0}
                suffix={`/ ${summary?.bill_count ?? 0}`}
              />
            </Col>
          </Row>
        )}
      </Card>

      <Card title="账单列表">
        <Space wrap style={{ marginBottom: 16 }}>
          <DatePicker
            picker="month"
            placeholder="账期筛选"
            value={query.period ? dayjs(query.period) : null}
            onChange={(v) => setQuery({ period: v ? v.format('YYYY-MM') : undefined })}
          />
          <Select
            allowClear
            placeholder="状态筛选"
            style={{ width: 140 }}
            value={query.status}
            onChange={(v) => setQuery({ status: v })}
            options={toOptions(BILL_STATUS)}
          />
          <InputNumber
            placeholder="合同 ID"
            style={{ width: 140 }}
            min={1}
            onChange={(v) => setQuery({ contract_id: v ?? undefined })}
          />
          <InputNumber
            placeholder="租户 ID"
            style={{ width: 140 }}
            min={1}
            onChange={(v) => setQuery({ tenant_id: v ?? undefined })}
          />
        </Space>

        <Table<Bill>
          rowKey="id"
          columns={columns}
          dataSource={list}
          loading={loading}
          pagination={pagination}
          locale={{ emptyText: '暂无账单, 可点击右上角生成月度账单' }}
        />
      </Card>

      <FormModal
        open={genOpen}
        title="生成月度租金账单"
        loading={generating}
        onCancel={() => setGenOpen(false)}
        onSubmit={(values) => onGenerate(values as never)}
      >
        <Form.Item
          name="period"
          label="账期"
          extra="留空则按上一自然月出账; 已出账的合同会被幂等跳过, 不会重复计费."
        >
          <DatePicker picker="month" style={{ width: '100%' }} />
        </Form.Item>
      </FormModal>
    </div>
  )
}
