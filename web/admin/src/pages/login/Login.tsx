import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Alert, Button, Form, Input, theme } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { login } from '../../api/auth'
import { useAuthStore } from '../../stores/auth'

export default function LoginPage() {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()
  const location = useLocation()
  const { token } = theme.useToken()
  const setSession = useAuthStore((s) => s.setSession)

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true)
    setError(null)
    try {
      const resp = await login(values.username, values.password)
      setSession({
        token: resp.token,
        refreshToken: resp.refreshToken,
        userId: resp.userId,
        roleIds: resp.roleIds,
        tenantId: resp.tenantId,
        nickname: values.username,
      })
      const from = (location.state as { from?: string } | null)?.from ?? '/home'
      navigate(from, { replace: true })
    } catch (e) {
      setError(e instanceof Error ? e.message : '登录失败, 请稍后重试')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100dvh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#f5f5f5',
        padding: 16,
      }}
    >
      <div
        style={{
          width: 400,
          maxWidth: '100%',
          background: '#fff',
          borderRadius: 8,
          padding: '40px 32px 32px',
          boxShadow: '0 6px 24px rgba(0, 0, 0, 0.06)',
        }}
      >
        <div style={{ marginBottom: 32, textAlign: 'left' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
            <div
              style={{
                width: 32,
                height: 32,
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
            <span style={{ fontSize: 17, fontWeight: 600 }}>OnePark 智慧园区</span>
          </div>
          <div style={{ color: '#6b6b6b' }}>园区统一管理平台登录</div>
        </div>

        {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}

        <Form layout="vertical" onFinish={onFinish} requiredMark={false}>
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input prefix={<UserOutlined />} placeholder="admin" autoComplete="username" size="large" />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="请输入密码"
              autoComplete="current-password"
              size="large"
            />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0, marginTop: 8 }}>
            <Button type="primary" htmlType="submit" block size="large" loading={loading}>
              登录
            </Button>
          </Form.Item>
        </Form>
      </div>
    </div>
  )
}
