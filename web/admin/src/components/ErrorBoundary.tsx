import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button, Result, Typography } from 'antd'

interface Props {
  children: ReactNode
  /** 用于定位是哪一个区域崩了 */
  name?: string
}

interface State {
  error: Error | null
}

// 路由级兜底: 任一页面渲染异常时不整站白屏, 而是给出可恢复的错误卡.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error('[ErrorBoundary]', this.props.name, error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    return (
      <Result
        status="error"
        title={this.props.name ? `${this.props.name} 加载失败` : '页面加载失败'}
        subTitle="渲染过程中发生异常, 可刷新重试; 若持续出现请查看控制台日志."
        extra={[
          <Button key="retry" type="primary" onClick={() => this.setState({ error: null })}>
            重试
          </Button>,
          <Button key="reload" onClick={() => window.location.reload()}>
            刷新页面
          </Button>,
        ]}
      >
        <Typography.Text type="secondary" style={{ wordBreak: 'break-all' }}>
          {error.message}
        </Typography.Text>
      </Result>
    )
  }
}
