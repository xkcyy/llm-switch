import { useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Drawer,
  Popconfirm,
  Row,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import {
  CheckCircleFilled,
  CopyOutlined,
  DisconnectOutlined,
  RocketOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text, Title, Paragraph } = Typography

const PREVIEW = `# ~/.workbuddy/models.json
[
  {
    "id": "DeepSeek/deepseek-chat",
    "name": "DeepSeek/deepseek-chat",
    "vendor": "LLM Switch",
    "url": "http://127.0.0.1:8317/v1/chat/completions",
    "apiKey": "llm-switch-local",
    "supportsToolCall": true,
    "supportsImages": false,
    "useCustomProtocol": true,
    "maxInputTokens": 128000
  }
]`

export default function WorkBuddyPage() {
  const app = useApp()
  const { message } = App.useApp()
  const [connecting, setConnecting] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [previewOpen, setPreviewOpen] = useState(false)

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectWorkBuddy()
    } catch (e) {
      message.error(e.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncWorkBuddy()
    } catch (e) {
      message.error(e.message)
    } finally {
      setSyncing(false)
    }
  }

  const warningsAlert = app.workbuddy.warnings?.length ? (
    <Alert
      type="warning"
      showIcon
      message="接入提示"
      description={
        <ul style={{ margin: 0, paddingInlineStart: 18 }}>
          {app.workbuddy.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      }
    />
  ) : null

  if (!app.workbuddy.connected) {
    return (
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {warningsAlert}
        <div className="codex-hero">
          <Space direction="vertical" size={14} style={{ width: '100%' }}>
            <div>
              <Title level={3} style={{ marginBottom: 4 }}>
                让 WorkBuddy 用上你的模型
              </Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>
                只改 <Text code>~/.workbuddy/models.json</Text> 里由本工具写入的自定义模型，
                你已有的自定义模型一律不动。
              </Paragraph>
            </div>
            <Space size={20} wrap>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>模型写进 WorkBuddy「自定义模型」分组，设置页里可切换</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>WorkBuddy 监听 models.json，改完下一次请求即生效</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>真实密钥留在 LLM Switch，配置里只放占位值</Text>
              </Space>
            </Space>
            <Space>
              <Button type="primary" size="large" icon={<RocketOutlined />} loading={connecting} onClick={connect}>
                一键接入 WorkBuddy
              </Button>
              <Button size="large" onClick={() => setPreviewOpen(true)}>
                看看会写入什么
              </Button>
            </Space>
          </Space>
        </div>

        <Card className="soft-card" variant="borderless" title="接入前检查">
          <Descriptions
            column={1}
            size="small"
            items={[
              {
                key: 'provider',
                label: '供应商',
                children: app.providers.length ? `${app.providers.length} 个可用` : <Text type="danger">请先添加供应商</Text>
              },
              {
                key: 'model',
                label: '模型',
                children: app.enabledModels.length ? (
                  `${app.enabledModels.length} 个可用模型`
                ) : (
                  <Text type="danger">请先添加模型</Text>
                )
              },
              {
                key: 'proxy',
                label: '本机代理',
                children: app.proxy.running ? (
                  `运行中（${app.proxy.host}:${app.proxy.port}）`
                ) : (
                  <Text type="warning">已停止，接入时会自动启动</Text>
                )
              },
              {
                key: 'client',
                label: 'WorkBuddy',
                children: app.workbuddy.installed ? (
                  <Text type="success">已检测到配置目录</Text>
                ) : (
                  <Text type="warning">未检测到配置目录，请先启动一次 WorkBuddy</Text>
                )
              },
              {
                key: 'file',
                label: '配置文件',
                children: <Text className="mono">{app.workbuddy.configPath}</Text>
              }
            ]}
          />
        </Card>

        <Drawer title="将写入的配置" width={680} open={previewOpen} onClose={() => setPreviewOpen(false)}>
          <pre className="mono">{app.workbuddy.generated || PREVIEW}</pre>
        </Drawer>
      </Space>
    )
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {warningsAlert}
      <Card className="soft-card" variant="borderless">
        <Row align="middle" gutter={16} wrap={false}>
          <Col flex="none">
            <CheckCircleFilled style={{ fontSize: 32, color: '#52c41a' }} />
          </Col>
          <Col flex="auto" style={{ minWidth: 0 }}>
            <div style={{ fontSize: 15, fontWeight: 600, lineHeight: 1.3 }}>WorkBuddy 已接入</div>
            <div style={{ fontSize: 12, color: '#9aa1b1', marginTop: 2 }}>
              在「设置 → 模型」的「自定义模型」里选 LLM Switch 下的模型即可，新增或删除模型会自动同步
            </div>
          </Col>
          <Col flex="none">
            <Space>
              <Button type="primary" icon={<SyncOutlined spin={syncing} />} loading={syncing} onClick={sync}>
                重新同步
              </Button>
              <Button
                icon={<ThunderboltOutlined />}
                onClick={() => {
                  const target = app.models.find((m) => m.slug === app.defaultModel) || app.enabledModels[0]
                  if (!target) return message.warning('还没有可用模型')
                  app.testModel(target.id)
                }}
              >
                测试一次调用
              </Button>
              <Popconfirm
                title="断开 WorkBuddy 接入？"
                description="只移除 LLM Switch 写入的自定义模型，不影响你其它的 WorkBuddy 设置。"
                okText="断开"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                onConfirm={() => app.disconnectWorkBuddy()}
              >
                <Button danger type="text" icon={<DisconnectOutlined />}>
                  断开接入
                </Button>
              </Popconfirm>
            </Space>
          </Col>
        </Row>
      </Card>

      <Card className="soft-card" variant="borderless" title="当前配置">
        <Descriptions
          column={2}
          size="small"
          items={[
            {
              key: 'models',
              label: '已写入模型',
              children: <Text>{app.workbuddy.models.length} 个</Text>
            },
            {
              key: 'base',
              label: '请求地址',
              children: <Text code>{app.workbuddy.chatUrl || app.workbuddy.baseUrl}</Text>
            },
            {
              key: 'format',
              label: '文件格式',
              children: <Tag bordered={false}>{app.workbuddy.format === 'object' ? '对象（含 models）' : '数组'}</Tag>
            },
            {
              key: 'last',
              label: '最近同步',
              children: app.workbuddy.lastSync ? app.workbuddy.lastSync.toLocaleTimeString('zh-CN') : '刚刚'
            },
            {
              key: 'file',
              label: '配置文件',
              children: (
                <Space size={4} align="center">
                  <Text className="mono" ellipsis={{ tooltip: app.workbuddy.configPath }} style={{ maxWidth: 380 }}>
                    {app.workbuddy.configPath}
                  </Text>
                  <Tooltip title="复制路径">
                    <Button
                      size="small"
                      type="text"
                      icon={<CopyOutlined />}
                      onClick={() => {
                        navigator.clipboard?.writeText(app.workbuddy.configPath)
                        message.success('已复制路径')
                      }}
                    />
                  </Tooltip>
                </Space>
              )
            }
          ]}
        />
        <Divider style={{ margin: '12px 0' }} />
        <Space align="start" size={12}>
          <Switch checked={app.workbuddy.autoSync} onChange={(v) => app.setWorkBuddy((c) => ({ ...c, autoSync: v }))} />
          <div>
            <Text strong>自动同步</Text>
            <div style={{ fontSize: 12, color: '#8c93a4' }}>
              推荐开启：新增或删除模型、修改代理端口时，自动更新 models.json。
            </div>
          </div>
        </Space>
        {app.workbuddy.otherModels?.length ? (
          <>
            <Divider style={{ margin: '12px 0' }} />
            <Space size={8} wrap>
              <Text type="secondary" style={{ fontSize: 12 }}>
                文件里还有其它自定义模型：
              </Text>
              {app.workbuddy.otherModels.map((m) => (
                <Tag key={m}>{m}</Tag>
              ))}
            </Space>
            <div style={{ fontSize: 12, color: '#8c93a4', marginTop: 6 }}>
              它们由 WorkBuddy 自己的模型页管理，本工具不会改动。
            </div>
          </>
        ) : null}
      </Card>

      <Alert
        type="success"
        showIcon
        message="无需重启 WorkBuddy"
        description="WorkBuddy 会监听 models.json 的变化；如果模型选择器没有立刻刷新，重新打开一次设置页即可。"
      />
    </Space>
  )
}
