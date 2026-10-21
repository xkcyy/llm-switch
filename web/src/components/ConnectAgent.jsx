import { useEffect, useState } from 'react'
import { Alert, Button, Divider, Drawer, Popconfirm, Switch, Typography } from 'antd'
import {
  CheckCircleFilled,
  DisconnectOutlined,
  RocketOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
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

const ENTRY_LABEL = { chat: 'Chat', responses: 'Responses', messages: 'Messages' }

function VerificationResult({ result }) {
  if (!result) return null
  const checkedAt = result.checked_at
    ? new Date(result.checked_at).toLocaleString('zh-CN', { hour12: false })
    : ''
  return (
    <Alert
      type={result.ok ? 'success' : 'error'}
      showIcon
      message={result.ok ? '调用链路验证通过' : '调用链路验证失败'}
      description={
        <div className="verification-result">
          <div>{result.message}</div>
          <div className="verification-meta">
            {typeof result.configured === 'boolean'
              ? result.configured
                ? '配置已就绪 · '
                : '配置未检测到 · '
              : null}
            {result.entry ? `入口 ${ENTRY_LABEL[result.entry] || result.entry}` : null}
            {result.model ? ` · 模型 ${result.model}` : null}
            {result.status ? ` · HTTP ${result.status}` : null}
            {result.latency_ms ? ` · ${result.latency_ms}ms` : null}
            {checkedAt ? ` · ${checkedAt}` : null}
          </div>
          {!result.ok && result.message?.includes('代理') ? (
            <div className="verification-hint">先确认顶部代理状态，再检查默认模型和供应商配置。</div>
          ) : null}
        </div>
      }
    />
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
  verification,
  onDisconnect,
  disconnectTitle = '断开接入？',
  disconnectDescription = '只移除 LLM Switch 写入的配置，不影响 Agent 的其他设置。'
}) {
  const [previewOpen, setPreviewOpen] = useState(false)
  const [testState, setTestState] = useState(null)
  const [testing, setTesting] = useState(false)
  const hasPreview = Boolean(previewContent || previewPlaceholder)
  const currentVerification = testState || verification

  useEffect(() => {
    setTestState(null)
  }, [verification])

  const runTest = async () => {
    if (!onTest || testing) return
    setTesting(true)
    setTestState(null)
    try {
      const result = await onTest()
      if (result) setTestState(result)
    } catch (error) {
      setTestState({ ok: false, message: error.message || '验证失败' })
    } finally {
      setTesting(false)
    }
  }

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
          {onTest ? (
            <Button
              type={currentVerification?.ok ? 'default' : 'primary'}
              icon={<ThunderboltOutlined />}
              loading={testing}
              onClick={runTest}
            >
              {currentVerification?.ok ? '重新验证' : '验证调用链路'}
            </Button>
          ) : null}
          {onSync ? (
            <Button
              type={currentVerification?.ok ? 'primary' : 'default'}
              icon={<SyncOutlined spin={syncing} />}
              loading={syncing}
              onClick={onSync}
            >
              {syncLabel}
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

      <VerificationResult result={currentVerification} />

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
