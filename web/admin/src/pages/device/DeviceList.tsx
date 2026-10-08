import { useEffect, useState } from 'react'
import { Card, Select, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DEVICE_STATUS,
  listDevices,
  listProducts,
  type DeviceItem,
  type ProductItem,
} from '../../api/device'
import { useTableQuery, type BaseQuery } from '../../hooks/useTableQuery'

const PAGE_SIZE = 10

interface DeviceQuery extends BaseQuery {
  status?: number
  productKey?: string
}

export default function DeviceListPage() {
  const { list, loading, query, setQuery, pagination } = useTableQuery<DeviceItem, DeviceQuery>(
    (q) => listDevices(q),
    { page: 1, page_size: PAGE_SIZE },
  )

  // 产品下拉：用于按产品筛选。取不到就退化成空选项，不影响按状态筛选。
  const [products, setProducts] = useState<ProductItem[]>([])
  useEffect(() => {
    listProducts({ page: 1, page_size: 200 })
      .then((r) => setProducts(r.list ?? []))
      .catch(() => setProducts([]))
  }, [])

  const columns: ColumnsType<DeviceItem> = [
    { title: '设备ID', dataIndex: 'deviceId', width: 190, ellipsis: true },
    { title: '设备名称', dataIndex: 'deviceName', width: 200, ellipsis: true },
    { title: '所属产品', dataIndex: 'productKey', width: 160, ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => (
        <Tag color={DEVICE_STATUS[v]?.color}>{DEVICE_STATUS[v]?.label ?? `${v}`}</Tag>
      ),
    },
    {
      title: '位置',
      dataIndex: 'location',
      ellipsis: true,
      render: (v: string) => v || '-',
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      width: 180,
      // M1 返回的就是字符串（不是秒级时间戳），直接展示，别走 fmtTime
      render: (v: string) => v || '-',
    },
  ]

  return (
    <Card title="设备列表">
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery({ status: v })}
          options={Object.entries(DEVICE_STATUS).map(([k, v]) => ({
            value: Number(k),
            label: v.label,
          }))}
        />
        <Select
          allowClear
          showSearch
          placeholder="产品筛选"
          style={{ width: 220 }}
          value={query.productKey}
          onChange={(v) => setQuery({ productKey: v })}
          optionFilterProp="label"
          options={products.map((p) => ({
            value: p.productKey,
            label: p.productName ? `${p.productName} (${p.productKey})` : p.productKey,
          }))}
        />
        <Typography.Text type="secondary">
          设备由 M1 接入：MQTT 上报或 POST /api/device/register 注册后出现在这里
        </Typography.Text>
      </Space>

      <Table<DeviceItem>
        rowKey="deviceId"
        columns={columns}
        dataSource={list}
        loading={loading}
        pagination={pagination}
        scroll={{ x: 900 }}
        locale={{ emptyText: '暂无设备 —— M1 侧注册设备后即可在此查看' }}
      />
    </Card>
  )
}
