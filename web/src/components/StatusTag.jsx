import { Tag } from 'antd'

const COLORS = {
  success: 'success',
  warning: 'warning',
  danger: 'error',
  info: 'processing',
  neutral: 'default'
}

export default function StatusTag({ status = 'neutral', children, dot = false }) {
  return (
    <Tag color={COLORS[status] || 'default'} bordered={false}>
      {dot ? <span className={`status-dot ${status === 'success' ? 'is-running' : ''}`} style={{ marginRight: 6 }} /> : null}
      {children}
    </Tag>
  )
}
