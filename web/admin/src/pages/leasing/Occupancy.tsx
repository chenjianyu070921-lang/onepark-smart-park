import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Progress,
  Row,
  Space,
  Statistic,
  Table,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import { getOccupancy, listZones, upsertZone, type Zone } from '../../api/leasing'
import { useTableQuery } from '../../hooks/useTableQuery'
import FormModal from '../../components/FormModal'
import { fmtNumber, fmtTime } from '../../utils/format'

const PAGE_SIZE = 10

export default function OccupancyPage() {
  const [occupancy, setOccupancy] = useState<{
    total_area_sqm: number
    leased_area_sqm: number
    occupancy_rate: number
  } | null>(null)
  const [occError, setOccError] = useState(false)

  const { list, loading, pagination, reload } = useTableQuery<Zone, { page: number; page_size: number }>(
    (q) => listZones(q),
    { page: 1, page_size: PAGE_SIZE },
  )

  const [zoneOpen, setZoneOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  const loadOccupancy = async () => {
    setOccError(false)
    try {
      setOccupancy(await getOccupancy())
    } catch {
      setOccupancy(null)
      setOccError(true)
    }
  }

  useEffect(() => {
    void loadOccupancy()
  }, [])

  const onSaveZone = async (values: { zone_code: string; zone_name?: string; total_area_sqm: number }) => {
    setSaving(true)
    try {
      await upsertZone(values)
      message.success('区域已保存')
      setZoneOpen(false)
      reload()
      // 可租面积是入驻率的分母, 变更后会立即影响入驻率, 需要同步刷新.
      void loadOccupancy()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const columns: ColumnsType<Zone> = [
    { title: '区域编码', dataIndex: 'zone_code', width: 160 },
    { title: '区域名称', dataIndex: 'zone_name', ellipsis: true },
    {
      title: '可租面积(㎡)',
      dataIndex: 'total_area_sqm',
      width: 140,
      render: (v: number) => fmtNumber(v),
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 160, render: (v: number) => fmtTime(v) },
    {
      title: '操作',
      key: 'actions',
      width: 120,
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => openEdit(row)}>
          修改面积
        </Button>
      ),
    },
  ]

  // 编辑区域: zone_code 为业务唯一键, 后端按 upsert 语义覆盖, 直接回填原值即可.
  const [editZone, setEditZone] = useState<Zone | null>(null)

  const openEdit = (row: Zone) => {
    setEditZone(row)
    setZoneOpen(true)
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Card title="园区入驻率">
        {occError ? (
          <Alert
            type="warning"
            showIcon
            message="入驻率暂不可用"
            description="请确认 leasing-service 已启动; 若尚未维护可租区域, 入驻率的分母为 0."
          />
        ) : (
          <Row gutter={[16, 16]}>
            <Col xs={24} sm={8}>
              <Statistic
                title="可租总面积(㎡)"
                value={fmtNumber(occupancy?.total_area_sqm ?? 0)}
              />
            </Col>
            <Col xs={24} sm={8}>
              <Statistic
                title="已租面积(㎡)"
                value={fmtNumber(occupancy?.leased_area_sqm ?? 0)}
              />
            </Col>
            <Col xs={24} sm={8}>
              <Statistic
                title="入驻率"
                value={((occupancy?.occupancy_rate ?? 0) * 100).toFixed(1)}
                suffix="%"
              />
              <Progress
                percent={Math.round((occupancy?.occupancy_rate ?? 0) * 100)}
                status="active"
                style={{ marginTop: 8 }}
              />
            </Col>
          </Row>
        )}
      </Card>

      <Card
        title="可租区域"
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setEditZone(null)
              setZoneOpen(true)
            }}
          >
            新增区域
          </Button>
        }
      >
        <Table<Zone>
          rowKey="id"
          columns={columns}
          dataSource={list}
          loading={loading}
          pagination={pagination}
          locale={{ emptyText: '尚未维护可租区域, 入驻率的分母为 0' }}
        />
      </Card>

      <FormModal
        open={zoneOpen}
        title={editZone ? `修改区域 ${editZone.zone_code}` : '新增可租区域'}
        loading={saving}
        onCancel={() => setZoneOpen(false)}
        initialValues={
          editZone
            ? {
                zone_code: editZone.zone_code,
                zone_name: editZone.zone_name,
                total_area_sqm: editZone.total_area_sqm,
              }
            : undefined
        }
        onSubmit={(values) => onSaveZone(values as never)}
      >
        <Form.Item
          name="zone_code"
          label="区域编码"
          rules={[{ required: true, message: '请输入区域编码' }]}
        >
          <Input placeholder="如 A-3F" maxLength={64} />
        </Form.Item>
        <Form.Item name="zone_name" label="区域名称">
          <Input placeholder="如 A 栋 3 层" maxLength={64} />
        </Form.Item>
        <Form.Item
          name="total_area_sqm"
          label="可租总面积(㎡)"
          rules={[{ required: true, message: '请输入可租面积' }]}
        >
          <InputNumber min={0.01} step={0.01} style={{ width: '100%' }} />
        </Form.Item>
        <Space direction="vertical" size={4}>
          <span style={{ color: '#8c8c8c', fontSize: 12 }}>
            区域编码为业务唯一键, 已存在时将覆盖面积与名称(后端 upsert 语义).
          </span>
        </Space>
      </FormModal>
    </div>
  )
}
