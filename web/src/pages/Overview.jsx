import { Alert, Badge, Button, Empty, Table, Tag, Typography } from 'antd'
import {
  ApiOutlined,
  ArrowRightOutlined,
  CheckOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  CodeOutlined,
  DeploymentUnitOutlined,
  NodeIndexOutlined,
  PlusOutlined,
  ReloadOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'
import { fmtTokens } from '../format'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import SignalNode from '../components/SignalNode'
import { EmptyState } from '../components/States'

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
  const connected = ['codex', 'opencode', 'dsh', 'workbuddy'].filter((key) => app[key]?.connected)
  if (app.trae?.ready) connected.push('trae')
  const hasAgent = connected.length > 0
  const verifiedAgent = connected.find((key) => app.agentVerifications?.[key]?.ok)
  const hasVerification = Boolean(verifiedAgent)
  const recentFailure = (app.recentErrors || [])[0]

  let title = '路由已就绪'
  let description = '供应商、模型、Agent 接入与链路验证均已通过，可以开始使用。'
  let label = '查看请求轨迹'
  let target = 'trajectory'
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
  } else if (!hasVerification) {
    title = '验证调用链路'
    description = '配置已写入，但还没有经本机代理完成一次真实调用；验证通过后流程才算闭环。'
    label = '去验证'
    target = `connect/${connected[0]}`
    icon = <ThunderboltOutlined />
  } else if (!app.proxy.running) {
    title = '本机代理已停止'
    description = 'Agent 配置仍然存在，但当前请求无法转发；重新启动代理后再发起调用。'
    label = '查看设置'
    target = 'settings'
    icon = <ReloadOutlined />
  } else if (recentFailure) {
    title = '最近有调用失败'
    description = `${recentFailure.request_model || '未知模型'}：${recentFailure.error || '请求失败'}`
    label = '排查失败请求'
    target = 'trajectory'
    icon = <NodeIndexOutlined />
  }

  return (
    <Panel
      className="next-action-panel"
      title="下一步"
      subtitle="按当前状态只保留一个主行动"
    >
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
          <span className="next-step-index">{hasProvider ? <CheckOutlined /> : '1'}</span>
          <span>供应商</span>
        </div>
        <div className={`next-step ${hasModel ? 'is-done' : hasProvider ? 'is-current' : ''}`}>
          <span className="next-step-index">{hasModel ? <CheckOutlined /> : '2'}</span>
          <span>可用模型</span>
        </div>
        <div className={`next-step ${hasAgent ? 'is-done' : hasModel ? 'is-current' : ''}`}>
          <span className="next-step-index">{hasAgent ? <CheckOutlined /> : '3'}</span>
          <span>Agent 接入</span>
        </div>
        <div className={`next-step ${hasVerification ? 'is-done' : hasAgent ? 'is-current' : ''}`}>
          <span className="next-step-index">{hasVerification ? <CheckOutlined /> : '4'}</span>
          <span>链路验证</span>
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
      verified: Boolean(app.agentVerifications?.codex?.ok),
      meta: app.codex.connected ? '配置已写入' : '未接入'
    },
    {
      key: 'opencode',
      title: 'OpenCode',
      icon: <CodeOutlined />,
      connected: app.opencode.connected,
      verified: Boolean(app.agentVerifications?.opencode?.ok),
      meta: app.opencode.connected ? '配置已写入' : '未接入'
    },
    {
      key: 'dsh',
      title: 'DeepSeek Harness',
      icon: <DeploymentUnitOutlined />,
      connected: app.dsh.connected,
      verified: Boolean(app.agentVerifications?.dsh?.ok),
      meta: app.dsh.connected ? `profile · ${app.dsh.profile}` : '未接入'
    },
    {
      key: 'workbuddy',
      title: 'WorkBuddy',
      icon: <ThunderboltOutlined />,
      connected: app.workbuddy.connected,
      verified: Boolean(app.agentVerifications?.workbuddy?.ok),
      meta: app.workbuddy.connected ? 'models.json 已同步' : '未接入'
    }
  ]

  const verifiedAgents = agents.filter((agent) => agent.verified).length
  const visibleProviders = app.providers.slice(0, 4)

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

      <div className="overview-workbench">
        <Panel
          className="route-panel"
          title="信号路由"
          subtitle="Agent 请求经本机代理转发到已启用的供应商模型"
          extra={<Text type="secondary">{verifiedAgents} / {agents.length} 链路已验证</Text>}
        >
        <div className="signal-board">
          <div className="signal-column">
            <div className="signal-column-title">
              <span>Agent 接入</span>
              <span className="signal-column-count">{agents.length}</span>
            </div>
            {agents.map((agent) => (
              <button
                type="button"
                key={agent.key}
                className="signal-node-action"
                onClick={() => navigate(`connect/${agent.key}`)}
              >
                <SignalNode
                  title={agent.title}
                  meta={agent.verified ? `${agent.meta} · 已验证` : agent.meta}
                  icon={agent.icon}
                  active={agent.verified}
                  muted={!agent.connected}
                  status={agent.verified ? 'success' : agent.connected ? 'warning' : 'default'}
                />
              </button>
            ))}
          </div>

          <div className="signal-core">
            <div className={`signal-core-box ${app.proxy.running ? 'is-running' : ''}`}>
              <div className="signal-core-label">本机代理</div>
              <div className="signal-core-value">{app.proxy.running ? '运行中' : '已停止'}</div>
              <div className="signal-core-port">
                {app.proxy.host}:{app.proxy.port}
              </div>
              <div className="signal-core-meta">{app.enabledModels.length} 个可用模型</div>
            </div>
          </div>

          <div className="signal-column">
            <div className="signal-column-title">
              <span>供应商节点</span>
              <span className="signal-column-count">{app.providers.length}</span>
            </div>
            {visibleProviders.length ? (
              visibleProviders.map((provider) => (
                <button
                  type="button"
                  key={provider.id}
                  className="signal-node-action"
                  onClick={() => navigate('providers')}
                >
                  <SignalNode
                    title={provider.name}
                    meta={`${provider.modelCount} 个模型 · ${provider.enabled ? '已启用' : '已停用'}`}
                    icon={<CloudServerOutlined />}
                    active={provider.enabled}
                    muted={!provider.enabled}
                    status={provider.enabled ? 'success' : 'default'}
                  />
                </button>
              ))
            ) : (
              <EmptyState
                title="还没有供应商"
                description="先添加一个供应商，代理才可转发模型请求。"
                actionLabel="添加供应商"
                onAction={() => navigate('providers')}
              />
            )}
          </div>
        </div>
        </Panel>

        <NextAction app={app} navigate={navigate} />
      </div>

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

      {app.recentErrors?.length > 0 ? (
        <Alert
          type="error"
          showIcon
          message={`最近有 ${app.recentErrors.length} 次调用失败`}
          description={
            <span>
              {app.recentErrors[0].request_model || '未知模型'} · {app.recentErrors[0].error || '请求失败'}
            </span>
          }
          action={
            <Button size="small" onClick={() => navigate('trajectory')}>
              排查
            </Button>
          }
        />
      ) : null}

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
          scroll={{ x: 760 }}
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
