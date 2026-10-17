import { Alert, Badge, Button, Empty, Table, Tag, Typography } from 'antd'
import {
  ApiOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  CodeOutlined,
  DeploymentUnitOutlined,
  NodeIndexOutlined,
  PlusOutlined,
  ReloadOutlined,
  RobotOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'
import { fmtTokens } from '../format'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import SignalNode from '../components/SignalNode'
import { EmptyState } from '../components/States'
import { uiTokens } from '../theme'

const { Text } = Typography

const ENTRY_LABEL = { chat: 'Chat', responses: 'Responses', messages: 'Messages' }

const relative = (iso) => {
  const t = new Date(iso).getTime()
  if (!t) return '—'
  const diff = Date.now() - t
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function NextAction({ app, navigate }) {
  const hasProvider = app.providers.length > 0
  const hasModel = app.enabledModels.length > 0
  const hasAgent = app.codex.connected || app.opencode.connected || app.dsh.connected || app.workbuddy.connected

  let title = '路由已就绪'
  let description = '代理可用模型与 Agent 接入都已配置，接下来可以直接发起调用。'
  let label = '查看供应商与模型'
  let target = 'providers'
  let icon = <CheckCircleFilled />

  if (!hasProvider) {
    title = '先添加供应商'
    description = '选择预设、粘贴 API Key，地址与协议会自动填好。'
    label = '添加供应商'
    target = 'providers'
    icon = <PlusOutlined />
  } else if (!hasModel) {
    title = '确认可用模型'
    description = '供应商已保存，但还没有启用模型；拉取或手动添加后即可接入 Agent。'
    label = '确认模型'
    target = 'providers'
    icon = <CloudServerOutlined />
  } else if (!hasAgent) {
    title = '接入第一个 Agent'
    description = '代理和模型已经就绪，把 Agent 的请求地址指向本机代理即可。'
    label = '去接入'
    target = 'connect/codex'
    icon = <ApiOutlined />
  }

  return (
    <Panel title="下一步" subtitle="按当前状态只保留一个主行动">
      <div className="next-action">
        <div className="next-action-icon">{icon}</div>
        <div className="next-action-copy">
          <div className="next-action-title">{title}</div>
          <div className="next-action-desc">{description}</div>
        </div>
        <Button type="primary" icon={<ArrowRightOutlined />} onClick={() => navigate(target)}>
          {label}
        </Button>
      </div>
      <div className="next-action-steps">
        <div className={`next-step ${hasProvider ? 'is-done' : 'is-current'}`}>
          <span className="next-step-index">{hasProvider ? '✓' : '1'}</span>
          <span>供应商</span>
        </div>
        <div className={`next-step ${hasModel ? 'is-done' : hasProvider ? 'is-current' : ''}`}>
          <span className="next-step-index">{hasModel ? '✓' : '2'}</span>
          <span>可用模型</span>
        </div>
        <div className={`next-step ${hasAgent ? 'is-done' : hasModel ? 'is-current' : ''}`}>
          <span className="next-step-index">{hasAgent ? '✓' : '3'}</span>
          <span>Agent 接入</span>
        </div>
      </div>
    </Panel>
  )
}

export default function Overview({ navigate }) {
  const app = useApp()

  const agents = [
    {
      key: 'codex',
      title: 'Codex',
      icon: <ApiOutlined />,
      connected: app.codex.connected,
      meta: app.codex.connected ? '配置已写入' : '未接入'
    },
    {
      key: 'opencode',
      title: 'OpenCode',
      icon: <CodeOutlined />,
      connected: app.opencode.connected,
      meta: app.opencode.connected ? '配置已写入' : '未接入'
    },
    {
      key: 'trae',
      title: 'Trae SOLO',
      icon: <RobotOutlined />,
      connected: Boolean(app.trae.ready || app.trae.models?.length),
      meta: app.trae.ready ? '界面已就绪' : app.trae.installed ? '已安装，未就绪' : '未检测到'
    },
    {
      key: 'dsh',
      title: 'DeepSeek Harness',
      icon: <DeploymentUnitOutlined />,
      connected: app.dsh.connected,
      meta: app.dsh.connected ? `profile · ${app.dsh.profile}` : '未接入'
    },
    {
      key: 'workbuddy',
      title: 'WorkBuddy',
      icon: <ThunderboltOutlined />,
      connected: app.workbuddy.connected,
      meta: app.workbuddy.connected ? 'models.json 已同步' : '未接入'
    }
  ]

  const connectedAgents = agents.filter((agent) => agent.connected).length
  const visibleProviders = app.providers.slice(0, 5)
  const remainingProviders = Math.max(0, app.providers.length - visibleProviders.length)

  const requests = (app.recent || []).map((request, index) => ({
    key: index,
    time: relative(request.time),
    agent: request.source || '未知调用方',
    entry: `${request.source || '本机代理'} · ${ENTRY_LABEL[request.entry] || request.entry}`,
    model: request.request_model,
    provider: request.provider,
    status: request.error ? 'error' : 'success',
    duration: request.duration_ms,
    error: request.error
  }))

  return (
    <div className="stack">
      <PageHeader
        title="总览"
        description="代理、模型与 Agent 接入的当前状态。"
        actions={
          <>
            <Button icon={<CloudServerOutlined />} onClick={() => navigate('providers')}>
              管理供应商
            </Button>
            <Button type="primary" icon={<ApiOutlined />} onClick={() => navigate('connect/codex')}>
              接入 Agent
            </Button>
          </>
        }
      />

      {app.repairs?.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          message={`已自动修复 ${app.repairs.length} 项配置问题`}
          description={
            <ul className="plain-list">
              {app.repairs.map((repair, index) => (
                <li key={index}>{repair.detail}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      <div className="overview-top">
        <Panel
          title="信号路由"
          subtitle="Agent 请求经本机代理转发到已启用的供应商模型"
          extra={<Text type="secondary">{connectedAgents} / {agents.length} Agent 已接入</Text>}
        >
          <div className="signal-board">
            <div className="signal-column">
              <div className="signal-column-title">Agent 端口</div>
              {agents.map((agent) => (
                <div key={agent.key} onClick={() => navigate(`connect/${agent.key}`)} style={{ cursor: 'pointer' }}>
                  <SignalNode
                    title={agent.title}
                    meta={agent.meta}
                    icon={agent.icon}
                    active={agent.connected}
                    muted={!agent.connected}
                    status={agent.connected ? 'success' : 'default'}
                  />
                </div>
              ))}
            </div>

            <div className="signal-core">
              <div className={`signal-core-box ${app.proxy.running ? 'is-running' : ''}`}>
                <div className="signal-core-label">本机代理</div>
                <div className="signal-core-value">{app.proxy.running ? '运行中' : '已停止'}</div>
                <div className="signal-core-port">
                  {app.proxy.host}:{app.proxy.port}
                </div>
              </div>
              <Text type="secondary" className="text-xs">
                {app.enabledModels.length} 个可用模型
              </Text>
            </div>

            <div className="signal-column">
              <div className="signal-column-title">供应商节点</div>
              {visibleProviders.length ? (
                visibleProviders.map((provider) => (
                  <div key={provider.id} onClick={() => navigate('providers')} style={{ cursor: 'pointer' }}>
                    <SignalNode
                      title={provider.name}
                      meta={`${provider.modelCount} 个模型 · ${provider.enabled ? '已启用' : '已停用'}`}
                      icon={<CloudServerOutlined />}
                      active={provider.enabled}
                      muted={!provider.enabled}
                      status={provider.enabled ? 'success' : 'default'}
                    />
                  </div>
                ))
              ) : (
                <EmptyState
                  title="还没有供应商"
                  description="先添加一个供应商，代理才可转发模型请求。"
                  actionLabel="添加供应商"
                  onAction={() => navigate('providers')}
                />
              )}
              {remainingProviders > 0 ? (
                <button className="signal-more" onClick={() => navigate('providers')}>
                  还有 {remainingProviders} 个供应商
                  <ArrowRightOutlined />
                </button>
              ) : null}
            </div>
          </div>
        </Panel>

        <NextAction app={app} navigate={navigate} />
      </div>

      <div className="metric-strip">
        <div className="metric-strip-item">
          <div className="metric-strip-label">今日调用</div>
          <div className="metric-strip-value">{app.usage?.today?.requests ?? 0}</div>
          <div className="metric-strip-extra">{app.usage?.today?.err ? `${app.usage.today.err} 次失败` : '全部成功'}</div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">今日 Token</div>
          <div className="metric-strip-value">{fmtTokens(app.usage?.today?.total_tokens)}</div>
          <div className="metric-strip-extra">
            输入 {fmtTokens(app.usage?.today?.input_tokens)} · 输出 {fmtTokens(app.usage?.today?.output_tokens)}
          </div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">累计 Token</div>
          <div className="metric-strip-value">{fmtTokens(app.usage?.total?.total_tokens)}</div>
          <div className="metric-strip-extra">累计 {app.usage?.total?.requests ?? 0} 次调用</div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">可用模型</div>
          <div className="metric-strip-value">{app.enabledModels.length}</div>
          <div className="metric-strip-extra">
            {app.models.length - app.enabledModels.length > 0 ? `${app.models.length - app.enabledModels.length} 个已停用` : '全部启用'}
          </div>
        </div>
      </div>

      <Panel
        title="最近调用"
        subtitle="最近入站请求及其转发结果"
        extra={
          <Button size="small" type="text" icon={<NodeIndexOutlined />} onClick={() => navigate('trajectory')}>
            查看轨迹
          </Button>
        }
        bodyClassName="flush"
      >
        <Table
          size="small"
          pagination={false}
          dataSource={requests}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="还没有调用记录；接入 Agent 并完成一次对话后会显示在这里"
              >
                <Button type="primary" onClick={() => navigate('connect/codex')}>
                  接入 Codex
                </Button>
              </Empty>
            )
          }}
          columns={[
            { title: '时间', dataIndex: 'time', width: 108, render: (value) => <Text type="secondary">{value}</Text> },
            {
              title: '调用方',
              dataIndex: 'entry',
              width: 190,
              render: (value) => <Tag bordered={false}>{value}</Tag>
            },
            {
              title: '请求模型',
              dataIndex: 'model',
              render: (value) => <Text className="mono">{value || '—'}</Text>
            },
            {
              title: '结果',
              dataIndex: 'status',
              width: 96,
              render: (value, row) =>
                value === 'success' ? (
                  <Tag color="success" bordered={false}>
                    成功
                  </Tag>
                ) : (
                  <Tag color="error" bordered={false} title={row.error}>
                    失败
                  </Tag>
                )
            },
            {
              title: '耗时',
              dataIndex: 'duration',
              width: 92,
              render: (value) => (
                <Text type="secondary">{value ? `${(value / 1000).toFixed(1)}s` : '—'}</Text>
              )
            }
          ]}
        />
      </Panel>
    </div>
  )
}
