import { Empty, Typography } from 'antd'

export default function PlaceholderPage({ title }: { title: string }) {
  return (
    <div
      style={{
        background: '#fff',
        borderRadius: 8,
        minHeight: 360,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 8,
      }}
    >
      <Empty description={false} />
      <Typography.Title level={4} style={{ margin: 0 }}>
        {title}
      </Typography.Title>
      <Typography.Text type="secondary">该模块建设中, 对应后端服务就绪后开放</Typography.Text>
    </div>
  )
}
