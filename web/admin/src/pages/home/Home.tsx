import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, Col, Row, Statistic, Typography } from 'antd'
import {
  AuditOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  PlusOutlined,
} from '@ant-design/icons'
import { getWorkOrderStatistics, type WorkOrderStatistics } from '../../api/workorder'
import { getDashboardHome } from '../../api/dashboard'
import { useAuthStore } from '../../stores/auth'
import dayjs from 'dayjs'

function StatCard(props: {
  icon: React.ReactNode
  title: string
  value: number | string
  suffix?: string
  onClick?: () => void
}) {
  return (
    <Card hoverable={!!props.onClick} onClick={props.onClick} styles={{ body: { padding: 20 } }}>
      <Statistic
        title={
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {props.icon}
            {props.title}
          </span>
        }
        value={props.value}
        suffix={props.suffix}
      />
    </Card>
  )
}

export default function HomePage() {
  const navigate = useNavigate()
  const nickname = useAuthStore((s) => s.nickname)
  const [stats, setStats] = useState<WorkOrderStatistics | null>(null)
  const [statsError, setStatsError] = useState(false)

  useEffect(() => {
    getWorkOrderStatistics()
      .then(setStats)
      .catch(() => setStatsError(true))
    // 工作台聚合接口做静默容错: dashboard-service 未就绪时页面不受影响.
    getDashboardHome().catch(() => undefined)
  }, [])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {dayjs().format('YYYY年MM月DD日 dddd')}
        </Typography.Title>
        <Typography.Text type="secondary">
          {nickname ? `${nickname}, 欢迎回来` : '欢迎回来'}
        </Typography.Text>
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} lg={6}>
          <StatCard
            icon={<ClockCircleOutlined />}
            title="待处理工单"
            value={stats?.pending_count ?? '-'}
            onClick={() => navigate('/workorder')}
          />
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <StatCard
            icon={<PlusOutlined />}
            title="今日新建"
            value={stats?.today_count ?? '-'}
            onClick={() => navigate('/workorder')}
          />
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <StatCard
            icon={<CheckCircleOutlined />}
            title="今日完成"
            value={stats?.completed_today ?? '-'}
          />
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <StatCard
            icon={<AuditOutlined />}
            title="工单完成率"
            value={stats ? Math.round(stats.completion_rate) : '-'}
            suffix="%"
          />
        </Col>
      </Row>

      {statsError && (
        <Card>
          <Typography.Text type="secondary">
            工单统计暂不可用, 请确认 workorder-service 与网关已启动
          </Typography.Text>
        </Card>
      )}

      {stats && (
        <Card title="处理效率" styles={{ body: { padding: 20 } }}>
          <Row gutter={[16, 16]}>
            <Col xs={24} sm={8}>
              <Statistic title="工单总数" value={stats.total} />
            </Col>
            <Col xs={24} sm={8}>
              <Statistic title="平均处理时长" value={stats.avg_process_minutes} suffix="分钟" />
            </Col>
            <Col xs={24} sm={8}>
              <Statistic title="完成率" value={stats.completion_rate} suffix="%" precision={1} />
            </Col>
          </Row>
        </Card>
      )}
    </div>
  )
}
