import { Alert, Badge, Button, Card, Col, Empty, Row, Space, Steps, Table, Tag, Tooltip, Typography } from 'antd'
import {
  ApiOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  ReloadOutlined,
  RocketOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text } = Typography

function StatCard({ label, value, extra, icon, accent }) {
  return (
    <Card className="soft-card stat-card" variant="borderless">
      <div className="stat-label">
        <span style={{ color: accent }}>{icon}</span>
        {label}
      </div>
      <div className="stat-value">{value}</div>
      <div className="stat-extra">{extra}</div>
    </Card>
  )
}

export default function Overview({ onNavigate }) {
  const app = useApp()
  const done1 = app.providers.length > 0
  const done2 = app.models.length > 0
  const done3 = app.codex.connected
  const activeStep = !done1 ? 0 : !done2 ? 1 : !done3 ? 2 : 3
  const agents = [app.codex.connected && 'Codex', app.opencode.connected && 'OpenCode'].filter(Boolean)

  const steps = [
    {
      title: '添加供应商',
      status: done1 ? 'finish' : 'process',
      description: done1 ? `${app.providers.length} 个已连接` : '选预置、粘贴 Key'
    },
    {
      title: '确认模型',
      status: done2 ? 'finish' : activeStep === 1 ? 'process' : 'wait',
      description: done2 ? `${app.models.length} 个可用模型` : '自动补全信息'
    },
    {
      title: '接入 Codex',
      status: done3 ? 'finish' : activeStep === 2 ? 'process' : 'wait',
      description: done3 ? '配置已写入' : '10 秒完成'
    }
  ]

  const entryLabel = { chat: 'Chat', responses: 'Responses', messages: 'Messages' }
  const relative = (iso) => {
    const t = new Date(iso).getTime()
    if (!t) return '—'
    const diff = Date.now() - t
    if (diff < 60_000) return '刚刚'
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
    return new Date(iso).toLocaleString('zh-CN', { hour12: false })
  }
  const requests = (app.recent || []).map((r, i) => ({
    key: i,
    time: relative(r.time),
    entry: `${r.source || '本机代理'} · ${entryLabel[r.entry] || r.entry}`,
    model: r.request_model,
    provider: r.provider,
    status: r.error ? 'error' : 'success',
    ms: r.duration_ms,
    error: r.error
  }))

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {app.repairs?.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message={`已自动修复 ${app.repairs.length} 项配置问题`}
          description={
            <ul style={{ margin: 0, paddingInlineStart: 18 }}>
              {app.repairs.map((r, i) => (
                <li key={i}>{r.detail}</li>
              ))}
            </ul>
          }
        />
      )}
      <Row gutter={16}>
        <Col span={6}>
          <StatCard
            label="代理服务"
            value={app.proxy.running ? '运行中' : '已停止'}
            extra={`${app.proxy.host}:${app.proxy.port}`}
            icon={<Badge status={app.proxy.running ? 'processing' : 'default'} />}
            accent="#2f6bff"
          />
        </Col>
        <Col span={6}>
          <StatCard label="供应商" value={app.providers.length} extra="已启用全部" icon={<CloudServerOutlined />} accent="#13a8a8" />
        </Col>
        <Col span={6}>
          <StatCard
            label="可用模型"
            value={app.enabledModels.length}
            extra={app.models.length - app.enabledModels.length > 0 ? `${app.models.length - app.enabledModels.length} 个已停用` : '全部启用'}
            icon={<ThunderboltOutlined />}
            accent="#fa8c16"
          />
        </Col>
        <Col span={6}>
          <StatCard
            label="Agent 接入"
            value={`${agents.length} / 2`}
            extra={
              [app.codex.connected ? 'Codex ✓' : 'Codex —', app.opencode.connected ? 'OpenCode ✓' : 'OpenCode —'].join(' · ')
            }
            icon={agents.length ? <CheckCircleFilled /> : <ApiOutlined />}
            accent={agents.length ? '#52c41a' : '#8c93a4'}
          />
        </Col>
      </Row>

      <Card className="soft-card steps-card" variant="borderless">
        <Row align="middle" gutter={16}>
          <Col flex="auto">
            <Steps
              size="small"
              current={activeStep}
              items={steps}
            />
          </Col>
          <Col>
            {!done3 && (
              <Button
                type="primary"
                icon={<RocketOutlined />}
                style={{ whiteSpace: 'nowrap' }}
                onClick={() => onNavigate(!done1 ? 'providers' : 'codex')}
              >
                {!done1 ? '去添加供应商' : !done2 ? '去确认模型' : '一键接入 Codex'}
              </Button>
            )}
            {done3 && (
              <Space>
                <Button icon={<ReloadOutlined />} onClick={() => app.syncCodex()}>
                  重新同步
                </Button>
                <Button type="link" onClick={() => onNavigate('codex')}>
                  查看配置
                </Button>
                {!app.opencode.connected && (
                  <Button type="primary" ghost icon={<RocketOutlined />} onClick={() => onNavigate('opencode')}>
                    接入 OpenCode
                  </Button>
                )}
              </Space>
            )}
          </Col>
        </Row>
      </Card>

      {app.codex.connected && (
        <Alert
          type="success"
          showIcon
          message="Codex 已可以正常使用"
          description={
            <span>
              在 Codex 里发起对话即可，请求会经过本机代理转发到{' '}
              {app.defaultModel || app.codex.defaultModel ? (
                <Text code>{app.defaultModel || app.codex.defaultModel}</Text>
              ) : (
                '对应供应商'
              )}
              。新增模型后配置会自动更新，无需任何操作。
            </span>
          }
        />
      )}

      {app.opencode.connected ? (
        <Alert
          type="success"
          showIcon
          message="OpenCode 已可以正常使用"
          description={
            <span>
              在 OpenCode 里用 <Text code>/models</Text> 选择 <Text code>llm-switch/*</Text> 即可，配置改动会自动重载，无需重启。
            </span>
          }
        />
      ) : (
        <Alert
          type="info"
          showIcon
          message="还可以接入 OpenCode"
          description={
            <Space>
              <span>写入 opencode.json，模型清单随 LLM Switch 自动同步。</span>
              <Button size="small" type="primary" ghost icon={<RocketOutlined />} onClick={() => onNavigate('opencode')}>
                去接入
              </Button>
            </Space>
          }
        />
      )}

      <Card
        className="soft-card"
        variant="borderless"
        title="最近调用"
        extra={
          <Space>
            <Text type="secondary" style={{ fontSize: 12 }}>
              仅保留最近记录
            </Text>
            <Button size="small" type="text" onClick={() => onNavigate('providers')}>
              查看模型
            </Button>
          </Space>
        }
      >
        <Table
          size="small"
          pagination={false}
          dataSource={requests}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="还没有调用记录，接入 Codex 或 OpenCode 后这里会显示最近请求"
              />
            )
          }}
          columns={[
            { title: '时间', dataIndex: 'time', width: 110, render: (v) => <Text type="secondary">{v}</Text> },
            { title: '入口', dataIndex: 'entry', width: 180, render: (v) => <Tag bordered={false}>{v}</Tag> },
            {
              title: '请求模型',
              dataIndex: 'model',
              render: (v) => <Text className="mono">{v}</Text>
            },
            {
              title: '结果',
              dataIndex: 'status',
              width: 110,
              render: (v, r) =>
                v === 'success' ? (
                  <Tooltip title={`已转发到 ${r.provider || '上游'}`}>
                    <Tag color="success" bordered={false}>
                      成功
                    </Tag>
                  </Tooltip>
                ) : (
                  <Tooltip title={r.error}>
                    <Tag color="error" bordered={false}>
                      失败
                    </Tag>
                  </Tooltip>
                )
            },
            { title: '耗时', dataIndex: 'ms', width: 100, render: (v) => <Text type="secondary">{v ? `${(v / 1000).toFixed(1)}s` : '—'}</Text> }
          ]}
        />
      </Card>
    </Space>
  )
}

