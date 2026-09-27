import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Card,
  Col,
  DatePicker,
  Empty,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tabs,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import dayjs from 'dayjs'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { ApiError } from '../../api/client'
import {
  getEnergyDaily,
  getEnergyMonthly,
  getEnergyZone,
  type DailyResponse,
  type DeviceUsage,
  type MonthlyResponse,
  type ZoneDetailResponse,
} from '../../api/energy'
import { fmtNumber, fmtPercent } from '../../utils/format'

// 图表配色: 与 antd 主色系接近, 避免图表与整体视觉割裂.
const CHART_COLORS = ['#1677ff', '#52c41a', '#faad14', '#f5222d', '#722ed1', '#13c2c2', '#eb2f96']

// 区域无数据时后端返回 M4-E-1004(业务语义), 这里把消息透出给用户, 而不是笼统的"接口错误".
function errText(e: unknown): string {
  return e instanceof ApiError
    ? `${e.code} ${e.message}`
    : '请确认 energy-analysis-service 已启动且 energy_reading 已有遥测数据'
}

function DailyTab() {
  const [date, setDate] = useState<string>(dayjs().format('YYYY-MM-DD'))
  const [data, setData] = useState<DailyResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    setLoading(true)
    setErrorMsg(null)
    getEnergyDaily({ date })
      .then((d) => alive && setData(d))
      .catch((e: unknown) => alive && setErrorMsg(errText(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [date])

  const pieData = useMemo(
    () =>
      (data?.zones ?? []).map((z, i) => ({
        name: z.zoneId,
        value: Number(z.usage.toFixed(2)),
        percent: z.percent,
        color: CHART_COLORS[i % CHART_COLORS.length],
      })),
    [data],
  )

  const columns: ColumnsType<DailyResponse['zones'][number]> = [
    { title: '区域', dataIndex: 'zoneId' },
    { title: '用量(度)', dataIndex: 'usage', render: (v: number) => fmtNumber(v) },
    { title: '设备数', dataIndex: 'deviceCount' },
    { title: '占比', dataIndex: 'percent', render: (v: number) => fmtNumber(v) + '%' },
  ]

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Space wrap>
        <DatePicker
          value={dayjs(date)}
          allowClear={false}
          onChange={(v) => setDate((v ?? dayjs()).format('YYYY-MM-DD'))}
        />
      </Space>

      {errorMsg && (
        <Alert
          type="warning"
          showIcon
          message="能耗日报暂不可用"
          description={errorMsg}
        />
      )}

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={8}>
          <Statistic title="全园区用量(度)" value={fmtNumber(data?.totalUsage ?? 0)} loading={loading} />
        </Col>
        <Col xs={24} sm={8}>
          <Statistic title="有数据设备数" value={data?.deviceCount ?? 0} loading={loading} />
        </Col>
        <Col xs={24} sm={8}>
          <Statistic title="覆盖区域数" value={data?.zones?.length ?? 0} loading={loading} />
        </Col>
      </Row>

      <Card title="区域用量占比" size="small">
        {pieData.length === 0 ? (
          <Empty description="无区域用量数据" />
        ) : (
          <ResponsiveContainer width="100%" height={280}>
            <PieChart>
              <Pie data={pieData} dataKey="value" nameKey="name" outerRadius={100} label>
                {pieData.map((entry) => (
                  <Cell key={entry.name} fill={entry.color} />
                ))}
              </Pie>
              <Tooltip formatter={(v) => `${fmtNumber(Number(v))} 度`} />
              <Legend />
            </PieChart>
          </ResponsiveContainer>
        )}
      </Card>

      <Table
        rowKey="zoneId"
        size="small"
        columns={columns}
        dataSource={data?.zones ?? []}
        loading={loading}
        pagination={false}
        locale={{ emptyText: '无数据' }}
      />
    </Space>
  )
}

function MonthlyTab() {
  const [month, setMonth] = useState<string>(dayjs().format('YYYY-MM'))
  const [data, setData] = useState<MonthlyResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    setLoading(true)
    setErrorMsg(null)
    getEnergyMonthly({ month })
      .then((d) => alive && setData(d))
      .catch((e: unknown) => alive && setErrorMsg(errText(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [month])

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <DatePicker
        picker="month"
        value={dayjs(month)}
        allowClear={false}
        onChange={(v) => setMonth((v ?? dayjs()).format('YYYY-MM'))}
      />

      {errorMsg && <Alert type="warning" showIcon message="能耗月报暂不可用" description={errorMsg} />}

      <Row gutter={[16, 16]}>
        <Col xs={12} sm={6}>
          <Statistic title="本月总用量(度)" value={fmtNumber(data?.totalUsage ?? 0)} loading={loading} />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic title="日均用量(度)" value={fmtNumber(data?.avgDailyUsage ?? 0)} loading={loading} />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic
            title="同比"
            value={fmtPercent(data?.yoy)}
            valueStyle={{
              color: (data?.yoy ?? 0) > 0 ? '#cf1322' : (data?.yoy ?? 0) < 0 ? '#3f8600' : undefined,
            }}
            loading={loading}
          />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic
            title="环比"
            value={fmtPercent(data?.mom)}
            valueStyle={{
              color: (data?.mom ?? 0) > 0 ? '#cf1322' : (data?.mom ?? 0) < 0 ? '#3f8600' : undefined,
            }}
            loading={loading}
          />
        </Col>
      </Row>

      <Typography.Text type="secondary">
        同比/环比在没有历史数据时显示 —; 红色表示上升, 绿色表示下降.
      </Typography.Text>

      <Card title="每日用量趋势" size="small">
        {(data?.days ?? []).length === 0 ? (
          <Empty description="本月暂无数据" />
        ) : (
          <ResponsiveContainer width="100%" height={300}>
            <LineChart data={data?.days ?? []}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="date" tickFormatter={(v: string) => v.slice(5)} />
              <YAxis />
              <Tooltip formatter={(v) => `${fmtNumber(Number(v))} 度`} />
              <Legend />
              <Line type="monotone" dataKey="usage" name="用量(度)" stroke="#1677ff" />
            </LineChart>
          </ResponsiveContainer>
        )}
      </Card>
    </Space>
  )
}

function ZoneTab() {
  const [zoneId, setZoneId] = useState<string>('')
  const [range, setRange] = useState<[string, string]>([
    dayjs().subtract(6, 'day').format('YYYY-MM-DD'),
    dayjs().format('YYYY-MM-DD'),
  ])
  const [data, setData] = useState<ZoneDetailResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  // 区域下拉来自日报: 日报已经返回当天有数据的区域列表, 避免另外维护区域字典.
  const [zones, setZones] = useState<string[]>([])
  useEffect(() => {
    getEnergyDaily()
      .then((d) => {
        const ids = (d.zones ?? []).map((z) => z.zoneId)
        setZones(ids)
        if (!zoneId && ids.length > 0) setZoneId(ids[0])
      })
      .catch(() => setZones([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!zoneId) return
    let alive = true
    setLoading(true)
    setErrorMsg(null)
    getEnergyZone({ zoneId, start: range[0], end: range[1] })
      .then((d) => alive && setData(d))
      .catch((e: unknown) => alive && setErrorMsg(errText(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [zoneId, range])

  const deviceColumns: ColumnsType<DeviceUsage> = [
    { title: '设备 ID', dataIndex: 'deviceId' },
    { title: '用量(度)', dataIndex: 'usage', render: (v: number) => fmtNumber(v) },
    { title: '占比', dataIndex: 'percent', render: (v: number) => fmtNumber(v) + '%' },
    { title: '最后读数', dataIndex: 'lastReading', render: (v: number) => fmtNumber(v) },
    { title: '最后上报', dataIndex: 'lastReportedAt' },
  ]

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Space wrap>
        <Select
          showSearch
          placeholder="选择区域"
          style={{ width: 200 }}
          value={zoneId || undefined}
          onChange={setZoneId}
          options={zones.map((z) => ({ value: z, label: z }))}
          notFoundContent="暂无区域(需先有遥测数据)"
        />
        <DatePicker.RangePicker
          value={[dayjs(range[0]), dayjs(range[1])]}
          allowClear={false}
          onChange={(v) => {
            if (!v?.[0] || !v?.[1]) return
            setRange([v[0].format('YYYY-MM-DD'), v[1].format('YYYY-MM-DD')])
          }}
        />
      </Space>

      {errorMsg && (
        <Alert type="warning" showIcon message="区域能耗详情暂不可用" description={errorMsg} />
      )}

      <Row gutter={[16, 16]}>
        <Col xs={12} sm={8}>
          <Statistic title="区间总用量(度)" value={fmtNumber(data?.totalUsage ?? 0)} loading={loading} />
        </Col>
        <Col xs={12} sm={8}>
          <Statistic title="设备数" value={data?.deviceCount ?? 0} loading={loading} />
        </Col>
      </Row>

      <Card title="按天用量" size="small">
        {(data?.days ?? []).length === 0 ? (
          <Empty description="该区间暂无数据" />
        ) : (
          <ResponsiveContainer width="100%" height={280}>
            <BarChart data={data?.days ?? []}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="date" tickFormatter={(v: string) => v.slice(5)} />
              <YAxis />
              <Tooltip formatter={(v) => `${fmtNumber(Number(v))} 度`} />
              <Legend />
              <Bar dataKey="usage" name="用量(度)" fill="#1677ff" />
            </BarChart>
          </ResponsiveContainer>
        )}
      </Card>

      <Card title="设备用量排名" size="small">
        <Table
          rowKey="deviceId"
          size="small"
          columns={deviceColumns}
          dataSource={data?.devices ?? []}
          loading={loading}
          pagination={false}
          locale={{ emptyText: '无设备数据' }}
        />
      </Card>
    </Space>
  )
}

export default function EnergyAnalysisPage() {
  return (
    <Card title="能耗分析">
      <Tabs
        destroyInactiveTabPane
        items={[
          { key: 'daily', label: '日报', children: <DailyTab /> },
          { key: 'monthly', label: '月报', children: <MonthlyTab /> },
          { key: 'zone', label: '区域详情', children: <ZoneTab /> },
        ]}
      />
    </Card>
  )
}
