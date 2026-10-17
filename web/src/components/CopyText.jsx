import { App, Button, Tooltip, Typography } from 'antd'
import { CopyOutlined } from '@ant-design/icons'

export default function CopyText({ value, label = '复制', className = '', style }) {
  const { message } = App.useApp()
  if (!value) return <Typography.Text type="secondary">—</Typography.Text>
  return (
    <span className={`copyable ${className}`.trim()} style={style}>
      <Typography.Text className="mono copyable-text" ellipsis={{ tooltip: value }}>
        {value}
      </Typography.Text>
      <Tooltip title={label}>
        <Button
          size="small"
          type="text"
          icon={<CopyOutlined />}
          onClick={async () => {
            try {
              await navigator.clipboard?.writeText(value)
              message.success('已复制')
            } catch {
              message.error('复制失败')
            }
          }}
        />
      </Tooltip>
    </span>
  )
}
