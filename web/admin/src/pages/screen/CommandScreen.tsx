import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Badge, Button, Card, Col, Row, Space, Statistic, Tag, Typography } from 'antd'
import {
  getOverview,
  type AlarmCard,
  type DeviceCard,
  type EnergyCard,
  type OverviewResp,
  type WorkOrderCard,
  type WsEvent,
} from '../../api/dashboard'
import { useAuthStore } from '../../stores/auth'

// 指挥大屏。四路聚合的呈现层, 三条设计口径直接来自后端:
//   1. 卡片为 null = **该路不可用**, 不是 0 —— 渲染成「暂不可用」并列出 degraded;
//   2. 数据 5 秒一帧由服务端 WS 推(快照), 事件到达即推增量; 页面只管渲染, 不自己轮询;
//   3. 具体某一路上游挂了不影响其余三路 —— 所以"部分不可用"是**正常状态**, 不该报警样式。
const REFRESH_FALLBACK_MS = 15000

export default function CommandScreenPage() {
  const token = useAuthStore((s) => s.token)
  const [data, setData] = useState<OverviewResp | null>(null)
  const [err, setErr] = useState('')
  const [wsState, setWsState] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [pushedAt, setPushedAt] = useState<number>(0)
  const [pushCount, setPushCount] = useState(0)
  const [lastEvent, setLastEvent] = useState<WsEvent | null>(null)
  const wsRef = useRef<WebSocket | null>(null)

  const loadOnce = useCallback(async () => {
    try {
      setData(await getOverview())
      setErr('')
    } catch {
      setErr('聚合接口暂时取不到, 稍后自动重试')
    }
  }, [])

  useEffect(() => {
    void loadOnce()

    // WS 优先: 浏览器无法给握手设 Authorization 头, 所以走 ?token=(与后端实现一致)
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${proto}://${location.host}/ws/dashboard?token=${encodeURIComponent(token ?? '')}`
    let closed = false
    let retry: number | undefined

    const connect = () => {
      if (closed) return
      setWsState('connecting')
      const ws = new WebSocket(url)
      wsRef.current = ws
      ws.onopen = () => setWsState('open')
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data as string) as { type: string } & Record<string, unknown>
          if (msg.type === 'snapshot') {
            setData(msg.data as unknown as OverviewResp)
            setPushedAt(Date.now())
            setPushCount((n) => n + 1)
          } else if (msg.type === 'event') {
            setLastEvent(msg as unknown as WsEvent)
          }
        } catch {
          // 推送内容异常不影响已渲染的数据
        }
      }
      ws.onclose = () => {
        setWsState('closed')
        // 断线自动重连, 3 秒一次; 重连期间页面仍显示最后一帧数据
        if (!closed) retry = window.setTimeout(connect, 3000)
      }
      ws.onerror = () => ws.close()
    }
    connect()

    // 兜底: WS 完全不可用(未配密钥/被拦)时, 页面仍要能看 —— 退化为轮询。
    const timer = window.setInterval(() => {
      if (wsRef.current?.readyState !== WebSocket.OPEN) void loadOnce()
    }, REFRESH_FALLBACK_MS)

    return () => {
      closed = true
      if (retry) window.clearTimeout(retry)
      window.clearInterval(timer)
      wsRef.current?.close()
    }
  }, [loadOnce, token])

  const degraded = data?.degraded ?? []
  const wsBadge =
    wsState === 'open' ? (
      <Badge status="processing" text="实时推送已连接" />
    ) : wsState === 'connecting' ? (
      <Badge status="warning" text="连接中" />
    ) : (
      <Badge status="default" text="推送已断开, 自动重连中" />
    )

  // 不可用的卡片统一渲染, 保证四张卡片的骨架一致(不会因为某路挂了而塌掉布局)
  const unavailable = (name: string) => (
    <Card>
      <Statistic title={name} value="暂不可用" valueStyle={{ fontSize: 22, color: '#8c8c8c' }} />
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        该路数据源未就绪, 其余三路不受影响
      </Typography.Text>
    </Card>
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Card>
        <Space wrap size={16} style={{ justifyContent: 'space-between', width: '100%' }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            指挥大屏
          </Typography.Title>
          <Space wrap size={12}>
            {wsBadge}
            <Typography.Text type="secondary">
              最近推送 {pushedAt ? new Date(pushedAt).toLocaleTimeString() : '—'} ｜ 已收 {pushCount} 帧
            </Typography.Text>
            {data ? (
              <Typography.Text type="secondary">
                聚合耗时 {data.elapsed_ms}ms ｜ {data.cached ? '本次命中缓存' : '本次实时聚合'}
              </Typography.Text>
            ) : null}
            <Button size="small" onClick={() => void loadOnce()}>
              手动刷新
            </Button>
          </Space>
        </Space>
      </Card>

      {err ? <Alert type="warning" showIcon message={err} /> : null}

      {/* 降级提示是**正常状态**而不是故障: 逐源降级的设计目标就是"某路挂了其余照常" */}
      {degraded.length > 0 ? (
        <Alert
          type="info"
          showIcon
          message={`当前有 ${degraded.length} 路数据源未就绪 (${degraded.join(', ')})`}
          description="大屏不会因为部分上游不可用而白屏; 对应卡片显示「暂不可用」, 其余卡片照常刷新。上游恢复后自动变回真实数据, 无需重启。"
        />
      ) : null}

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={6}>
          {data?.device ? (
            <Card>
              <Statistic title="设备" value={(data.device as DeviceCard).total} suffix="台" />
              <Space size={8} wrap style={{ marginTop: 8 }}>
                <Tag color="success">在线 {(data.device as DeviceCard).online}</Tag>
                <Tag>离线 {(data.device as DeviceCard).offline}</Tag>
                <Tag color="error">故障 {(data.device as DeviceCard).fault}</Tag>
              </Space>
            </Card>
          ) : (
            unavailable('设备')
          )}
        </Col>

        <Col xs={24} sm={12} xl={6}>
          {data?.work_order ? (
            <Card>
              <Statistic
                title="工单"
                value={(data.work_order as WorkOrderCard).unfinished}
                suffix="张待处理"
              />
              <Space size={8} wrap style={{ marginTop: 8 }}>
                <Tag color="processing">今日 {(data.work_order as WorkOrderCard).today_total}</Tag>
                <Tag>
                  完成率 {Math.round(((data.work_order as WorkOrderCard).complete_rate ?? 0) * 100)}%
                </Tag>
                {(data.work_order as WorkOrderCard).avg_handle_sec === null ? (
                  <Tag color="default">平均时长 上游未提供</Tag>
                ) : (
                  <Tag>平均 {Math.round((data.work_order as WorkOrderCard).avg_handle_sec ?? 0)}s</Tag>
                )}
              </Space>
            </Card>
          ) : (
            unavailable('工单')
          )}
        </Col>

        <Col xs={24} sm={12} xl={6}>
          {data?.alarm ? (
            <Card>
              <Statistic title="告警" value={(data.alarm as AlarmCard).total} suffix="条" />
              <Space size={8} wrap style={{ marginTop: 8 }}>
                <Tag color="red">紧急 {(data.alarm as AlarmCard).critical}</Tag>
                <Tag color="volcano">重要 {(data.alarm as AlarmCard).major}</Tag>
                <Tag color="orange">次要 {(data.alarm as AlarmCard).minor}</Tag>
                <Tag>提示 {(data.alarm as AlarmCard).info}</Tag>
              </Space>
            </Card>
          ) : (
            unavailable('告警')
          )}
        </Col>

        <Col xs={24} sm={12} xl={6}>
          {data?.energy ? (
            <Card>
              <Statistic
                title="能耗"
                value={
                  (data.energy as EnergyCard).total_power === null
                    ? '暂不可用'
                    : Number((data.energy as EnergyCard).total_power)
                }
                suffix={(data.energy as EnergyCard).total_power === null ? '' : 'kWh'}
              />
              <Space size={8} wrap style={{ marginTop: 8 }}>
                <Tag>
                  用水{' '}
                  {(data.energy as EnergyCard).total_water === null
                    ? '上游未提供'
                    : (data.energy as EnergyCard).total_water}
                </Tag>
              </Space>
            </Card>
          ) : (
            unavailable('能耗')
          )}
        </Col>
      </Row>

      <Card size="small" title="最近一条事件推送（kafka 事件到达即推，不等下一个快照周期）">
        {lastEvent ? (
          <Typography.Text code style={{ fontSize: 12 }}>
            {JSON.stringify(lastEvent)}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">
            暂时没有事件推送 —— 投一条告警事件即可看到（本服务消费 Kafka 的两个 topic 默认关闭, 见配置说明）
          </Typography.Text>
        )}
      </Card>
    </div>
  )
}
