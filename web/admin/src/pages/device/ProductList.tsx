import { Card, Select, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { listProducts, PRODUCT_STATUS, type ProductItem } from '../../api/device'
import { useTableQuery, type BaseQuery } from '../../hooks/useTableQuery'

const PAGE_SIZE = 10

interface ProductQuery extends BaseQuery {
  status?: number
}

export default function ProductListPage() {
  const { list, loading, query, setQuery, pagination } = useTableQuery<ProductItem, ProductQuery>(
    (q) => listProducts(q),
    { page: 1, page_size: PAGE_SIZE },
  )

  const columns: ColumnsType<ProductItem> = [
    { title: '产品Key', dataIndex: 'productKey', width: 220, ellipsis: true },
    { title: '产品名称', dataIndex: 'productName', ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: number) => (
        <Tag color={PRODUCT_STATUS[v]?.color}>{PRODUCT_STATUS[v]?.label ?? `${v}`}</Tag>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      width: 180,
      render: (v: string) => v || '-',
    },
  ]

  return (
    <Card title="产品管理">
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          allowClear
          placeholder="状态筛选"
          style={{ width: 140 }}
          value={query.status}
          onChange={(v) => setQuery({ status: v })}
          options={Object.entries(PRODUCT_STATUS).map(([k, v]) => ({
            value: Number(k),
            label: v.label,
          }))}
        />
        <Typography.Text type="secondary">
          产品是「一类设备的模板」，挂物模型 JSON（properties / events / services）；设备必须归属一个产品
        </Typography.Text>
      </Space>

      <Table<ProductItem>
        rowKey="productKey"
        columns={columns}
        dataSource={list}
        loading={loading}
        pagination={pagination}
        scroll={{ x: 760 }}
        locale={{ emptyText: '暂无产品 —— 可用 POST /api/product 创建产品后再接入设备' }}
      />
    </Card>
  )
}
