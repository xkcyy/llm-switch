import { useState } from 'react'
import { Alert, Button, Divider, Drawer, Popconfirm, Switch, Typography } from 'antd'
import { CheckCircleFilled, DisconnectOutlined, RocketOutlined, SyncOutlined, ThunderboltOutlined } from '@ant-design/icons'
import Panel from './Panel'

const { Text } = Typography

function Warnings({ warnings }) {
  if (!warnings?.length) return null
  return (
    <Alert
      type="warning"
      showIcon
      message="接入提示"
      description={
        <ul className="plain-list">
          {warnings.map((warning, index) => (
            <li key={index}>{warning}</li>
          ))}
        </ul>
      }
    />
  )
}

function Inspection({ items }) {
  return (
    <div className="inspection">
      {items.map((item, index) => (
        <div className="inspection-row" key={`${item.label}-${index}`}>
          <div className="inspection-label">{item.label}</div>
          <div className="inspection-value">{item.value}</div>
        </div>
      ))}
    </div>
  )
}

export default function ConnectAgent({
  connected = false,
  warnings = [],
  heroTitle,
  heroDescription,
  points = [],
  connectLabel,
  connecting = false,
  connectDisabled = false,
  onConnect,
  previewTitle = '将写入的配置',
  previewContent,
  previewPlaceholder = '还没有可预览的配置。',
  inspection = [],
  disconnectedInspection = [],
  connectedTitle,
  connectedDescription,
  actions,
  autoSync,
  onAutoSync,
  autoSyncDescription,
  configExtra,
  advanced,
  children,
  footnote,
  disconnectedExtra,
  onSync,
  syncing = false,
  syncLabel = '重新同步',
  onTest,
  onDisconnect,
  disconnectTitle = '断开接入？',
  disconnectDescription = '只移除 LLM Switch 写入的配置，不影响 Agent 的其他设置。'
}) {
  const [previewOpen, setPreviewOpen] = useState(false)
  const hasPreview = Boolean(previewContent || previewPlaceholder)

  if (!connected) {
    return (
      <div className="stack">
        <Warnings warnings={warnings} />
        <div className="connect-hero">
          <div>
            <h2>{heroTitle}</h2>
            <p>{heroDescription}</p>
            {points.length ? (
              <div className="connect-points">
                {points.map((point) => (
                  <span className="connect-point" key={point}>
                    <CheckCircleFilled style={{ color: 'var(--ls-green)' }} />
                    {point}
                  </span>
                ))}
              </div>
            ) : null}
          </div>
          <div className="connect-actions">
            <Button
              type="primary"
              size="large"
              icon={<RocketOutlined />}
              loading={connecting}
              disabled={connectDisabled}
              onClick={onConnect}
            >
              {connectLabel}
            </Button>
            {hasPreview ? (
              <Button size="large" onClick={() => setPreviewOpen(true)}>
                看看会写入什么
              </Button>
            ) : null}
          </div>
        </div>

        {(disconnectedInspection.length ? disconnectedInspection : inspection).length ? (
          <Panel title="接入前检查" subtitle="确认前置条件；缺少项会在接入时提示。">
            <Inspection items={disconnectedInspection.length ? disconnectedInspection : inspection} />
          </Panel>
        ) : null}

        {disconnectedExtra}
        {children}

        <Drawer
          title={previewTitle}
          width={720}
          open={previewOpen}
          onClose={() => setPreviewOpen(false)}
        >
          <pre className="trace-pre">{previewContent || previewPlaceholder}</pre>
        </Drawer>
      </div>
    )
  }

  return (
    <div className="stack">
      <Warnings warnings={warnings} />
      <Panel bodyClassName="connect-status-panel">
        <div className="connect-status">
          <div className="connect-status-icon">
            <CheckCircleFilled />
          </div>
          <div>
            <div className="connect-status-title">{connectedTitle}</div>
            <div className="connect-status-sub">{connectedDescription}</div>
          </div>
        </div>
        <div className="connect-actions">
          {actions}
          {onSync ? (
            <Button type="primary" icon={<SyncOutlined spin={syncing} />} loading={syncing} onClick={onSync}>
              {syncLabel}
            </Button>
          ) : null}
          {onTest ? (
            <Button icon={<ThunderboltOutlined />} onClick={onTest}>
              测试一次调用
            </Button>
          ) : null}
          {onDisconnect ? (
            <Popconfirm
              title={disconnectTitle}
              description={disconnectDescription}
              okText="断开"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={onDisconnect}
            >
              <Button danger type="text" icon={<DisconnectOutlined />}>
                断开接入
              </Button>
            </Popconfirm>
          ) : null}
        </div>
      </Panel>

      <Panel title="当前配置" subtitle="只维护本工具写入的片段，其他内容保持不变。">
        <Inspection items={inspection} />
        {autoSync !== undefined && onAutoSync ? (
          <>
            <Divider className="config-divider" />
            <div className="config-toggle-row">
              <Switch checked={autoSync} onChange={onAutoSync} />
              <div>
                <Text strong>自动同步</Text>
                <div className="section-desc">{autoSyncDescription}</div>
              </div>
            </div>
          </>
        ) : null}
        {configExtra}
      </Panel>

      {advanced}
      {children}

      {hasPreview ? (
        <>
          <Button onClick={() => setPreviewOpen(true)}>查看当前写入内容</Button>
          <Drawer
            title={previewTitle}
            width={720}
            open={previewOpen}
            onClose={() => setPreviewOpen(false)}
          >
            <pre className="trace-pre">{previewContent || previewPlaceholder}</pre>
          </Drawer>
        </>
      ) : null}

      {footnote ? <div className="connect-footnote">{footnote}</div> : null}
    </div>
  )
}
