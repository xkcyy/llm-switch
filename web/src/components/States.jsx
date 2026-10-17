import { Alert, Button, Empty, Skeleton } from 'antd'
import { ArrowRightOutlined, ReloadOutlined } from '@ant-design/icons'

export function EmptyState({ title = '暂无数据', description, action, actionLabel, onAction }) {
  return (
    <div className="state-block">
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <>
            <div className="section-title">{title}</div>
            {description ? <div className="section-desc">{description}</div> : null}
          </>
        }
      >
        {action || (actionLabel && onAction ? (
          <Button type="primary" icon={<ArrowRightOutlined />} onClick={onAction}>
            {actionLabel}
          </Button>
        ) : null)}
      </Empty>
    </div>
  )
}

export function ErrorState({ title = '加载失败', message, onRetry }) {
  return (
    <div className="state-block">
      <Alert
        type="error"
        showIcon
        message={title}
        description={message}
        action={
          onRetry ? (
            <Button size="small" icon={<ReloadOutlined />} onClick={onRetry}>
              重试
            </Button>
          ) : null
        }
      />
    </div>
  )
}

export function LoadingState({ rows = 6 }) {
  return (
    <div className="loading-panel">
      <Skeleton active paragraph={{ rows }} />
    </div>
  )
}
