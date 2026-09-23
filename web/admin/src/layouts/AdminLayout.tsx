import { useEffect, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { Avatar, Badge, Dropdown, Layout, Menu, Popover, List, Spin, theme } from 'antd'
import type { MenuProps } from 'antd'
import { BellOutlined, LogoutOutlined, UserOutlined } from '@ant-design/icons'
import { menuConfig } from '../router/menu'
import { useAuthStore } from '../stores/auth'
import { getUnreadNoticeCount, listNotices, type NoticeItem } from '../api/notice'
import { logout as apiLogout } from '../api/auth'

const { Header, Sider, Content } = Layout

function NoticeBell() {
  const [unread, setUnread] = useState(0)
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [notices, setNotices] = useState<NoticeItem[]>([])

  // 30s 轮询未读数; 大小屏一致的轻量方案, 后续可换站内信 WS.
  useEffect(() => {
    let alive = true
    const tick = () =>
      getUnreadNoticeCount()
        .then((d) => {
          if (alive) setUnread(typeof d.count === 'number' ? d.count : Number(d) || 0)
        })
        .catch(() => undefined)
    tick()
    const timer = setInterval(tick, 30_000)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [])

  const loadNotices = async () => {
    setLoading(true)
    try {
      const d = await listNotices()
      setNotices(Array.isArray(d) ? d : (d?.list ?? []))
    } catch {
      setNotices([])
    } finally {
      setLoading(false)
    }
  }

  const content = (
    <div style={{ width: 320 }}>
      {loading ? (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <Spin />
        </div>
      ) : notices.length === 0 ? (
        <div style={{ textAlign: 'center', padding: 24, color: '#8c8c8c' }}>暂无公告</div>
      ) : (
        <List
          size="small"
          dataSource={notices.slice(0, 5)}
          renderItem={(n) => <List.Item>{n.title}</List.Item>}
        />
      )}
    </div>
  )

  return (
    <Popover
      content={content}
      trigger="click"
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (v) loadNotices()
      }}
    >
      <Badge count={unread} size="small">
        <BellOutlined style={{ fontSize: 18, cursor: 'pointer' }} />
      </Badge>
    </Popover>
  )
}

export default function AdminLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const { token } = theme.useToken()
  const { nickname, token: accessToken, logout } = useAuthStore()

  const selectedKey =
    menuConfig
      .flatMap((m) => [m, ...(m.children ?? [])])
      .filter((m) => m.path)
      .sort((a, b) => (b.path ?? '').length - (a.path ?? '').length)
      .find((m) => location.pathname.startsWith(m.path ?? '\u0000'))?.key ?? 'home'

  const items: MenuProps['items'] = menuConfig.map((m) =>
    m.children
      ? { key: m.key, label: m.label, icon: m.icon, children: m.children.map((c) => ({ key: c.key, label: c.label })) }
      : { key: m.key, label: m.label, icon: m.icon },
  )

  const onMenuClick: MenuProps['onClick'] = ({ key }) => {
    const node = menuConfig.flatMap((m) => [m, ...(m.children ?? [])]).find((m) => m.key === key)
    if (node?.path) navigate(node.path)
  }

  const doLogout = async () => {
    if (accessToken) await apiLogout(accessToken).catch(() => undefined)
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <Layout style={{ minHeight: '100dvh' }}>
      <Header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0 24px',
          height: 64,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <div
            style={{
              width: 28,
              height: 28,
              borderRadius: 6,
              background: token.colorPrimary,
              color: '#fff',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontWeight: 700,
            }}
          >
            OP
          </div>
          <span style={{ color: '#fff', fontSize: 16, fontWeight: 600 }}>OnePark 智慧园区</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 24 }}>
          <NoticeBell />
          <Dropdown
            menu={{
              items: [
                {
                  key: 'logout',
                  icon: <LogoutOutlined />,
                  label: '退出登录',
                  onClick: doLogout,
                },
              ],
            }}
          >
            <span style={{ display: 'flex', alignItems: 'center', gap: 8, color: '#fff', cursor: 'pointer' }}>
              <Avatar size="small" icon={<UserOutlined />} />
              {nickname || '管理员'}
            </span>
          </Dropdown>
        </div>
      </Header>
      <Layout>
        <Sider width={208} theme="light" style={{ borderRight: '1px solid rgba(5,5,5,0.06)' }}>
          <Menu
            mode="inline"
            selectedKeys={[selectedKey]}
            items={items}
            onClick={onMenuClick}
            style={{ borderInlineEnd: 'none', paddingTop: 8 }}
          />
        </Sider>
        <Content style={{ padding: 24, background: '#f5f5f5' }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
