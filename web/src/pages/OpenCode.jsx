import { useEffect, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Collapse,
  Descriptions,
  Divider,
  Drawer,
  Input,
  Popconfirm,
  Row,
  Select,
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
  EditOutlined,
  RocketOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text, Title, Paragraph } = Typography

const PREVIEW = `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "llm-switch": {
      "name": "LLM Switch",
      "npm": "@ai-sdk/openai-compatible",
      "models": {
        "DeepSeek/deepseek-chat": { "name": "DeepSeek/deepseek-chat" }
      },
      "options": {
        "baseURL": "http://127.0.0.1:8317/v1",
        "apiKey": "llm-switch-local"
      }
    }
  }
}`

function AdvancedEditor({ app }) {
  const { message } = App.useApp()
  const current = app.opencode.files?.config?.content || ''
  const generated = app.opencode.generated || ''
  const [value, setValue] = useState(current || generated)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setValue(current || generated)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, generated])

  const save = async () => {
    setSaving(true)
    try {
      await app.syncOpenCode({ config_json: value })
    } catch (e) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  const format = () => {
    try {
      setValue(JSON.stringify(JSON.parse(value), null, 2))
      message.success('已格式化')
    } catch {
      message.warning('内容不是合法 JSON，无法格式化')
    }
  }

  return (
    <Collapse
      items={[
        {
          key: 'advanced',
          label: (
            <Space>
              <EditOutlined />
              <span>高级：手动编辑 opencode.json</span>
              <Text type="secondary" style={{ fontSize: 12 }}>
                通常不需要，自动模式会生成同样的内容
              </Text>
            </Space>
          ),
          children: (
            <Space direction="vertical" style={{ width: '100%' }} size={8}>
              <Alert
                type="info"
                showIcon
                message="这里编辑的是整份 opencode.json：你已有的 provider、plugins 等内容都会原样保留，只有 provider.llm-switch 段由 LLM Switch 维护"
              />
              <Input.TextArea
                className="editor-area"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                autoSize={{ minRows: 10, maxRows: 18 }}
              />
              <Space>
                <Button size="small" icon={<EditOutlined />} onClick={format}>
                  格式化
                </Button>
                <Button size="small" type="primary" loading={saving} onClick={save}>
                  保存并同步
                </Button>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  保存前会校验 JSON，写入失败不会影响现有配置
                </Text>
              </Space>
            </Space>
          )
        }
      ]}
    />
  )
}

export default function OpenCodePage() {
  const app = useApp()
  const { message } = App.useApp()
  const [connecting, setConnecting] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [previewOpen, setPreviewOpen] = useState(false)

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectOpenCode()
    } catch (e) {
      message.error(e.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncOpenCode()
    } finally {
      setSyncing(false)
    }
  }

  const warningsAlert = app.opencode.warnings?.length ? (
    <Alert
      type="warning"
      showIcon
      message="接入提示"
      description={
        <ul style={{ margin: 0, paddingInlineStart: 18 }}>
          {app.opencode.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      }
    />
  ) : null

  if (!app.opencode.connected) {
    return (
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {warningsAlert}
        <div className="codex-hero">
          <Space direction="vertical" size={14} style={{ width: '100%' }}>
            <div>
              <Title level={3} style={{ marginBottom: 4 }}>
                让 OpenCode 用上你的模型
              </Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>
                只改 <Text code>provider.llm-switch</Text> 一段，你已有的 provider 和插件设置一律不动。
              </Paragraph>
            </div>
            <Space size={20} wrap>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>模型清单直接写进 opencode.json，/models 里可切换</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>OpenCode 自动重载配置，无需重启</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>真实密钥留在 LLM Switch，配置文件里只放占位值</Text>
              </Space>
            </Space>
            <Space>
              <Button type="primary" size="large" icon={<RocketOutlined />} loading={connecting} onClick={connect}>
                一键接入 OpenCode
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
                key: 'file',
                label: '配置文件',
                children: <Text className="mono">{app.opencode.configPath}</Text>
              }
            ]}
          />
        </Card>

        <Drawer title="将写入的配置" width={620} open={previewOpen} onClose={() => setPreviewOpen(false)}>
          <pre className="mono">{app.opencode.generated || PREVIEW}</pre>
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
            <div style={{ fontSize: 15, fontWeight: 600, lineHeight: 1.3 }}>OpenCode 已接入</div>
            <div style={{ fontSize: 12, color: '#9aa1b1', marginTop: 2 }}>
              在 OpenCode 里用 <Text code>/models</Text> 选择 <Text code>llm-switch/*</Text> 即可，新增或删除模型会自动同步
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
                title="断开 OpenCode 接入？"
                description="只移除 LLM Switch 写入的 provider.llm-switch 段，不影响你其他的 OpenCode 设置。"
                okText="断开"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                onConfirm={() => app.disconnectOpenCode()}
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
              children: <Text>{app.opencode.models.length} 个（/models 里显示为 llm-switch/*）</Text>
            },
            {
              key: 'base',
              label: '请求地址',
              children: <Text code>{app.opencode.baseUrl}</Text>
            },
            {
              key: 'file',
              label: '配置文件',
              children: (
                <Space size={4} align="center">
                  <Text className="mono" ellipsis={{ tooltip: app.opencode.configPath }} style={{ maxWidth: 380 }}>
                    {app.opencode.configPath}
                  </Text>
                  <Tooltip title="复制路径">
                    <Button
                      size="small"
                      type="text"
                      icon={<CopyOutlined />}
                      onClick={() => {
                        navigator.clipboard?.writeText(app.opencode.configPath)
                        message.success('已复制路径')
                      }}
                    />
                  </Tooltip>
                </Space>
              )
            },
            {
              key: 'last',
              label: '最近同步',
              children: app.opencode.lastSync ? app.opencode.lastSync.toLocaleTimeString('zh-CN') : '刚刚'
            },
            {
              key: 'shape',
              label: '配置形状',
              children: (
                <Space size={8} align="center">
                  <Select
                    size="small"
                    style={{ width: 210 }}
                    value={app.opencode.shapeSetting}
                    onChange={(v) => app.setOpenCodeShape(v).catch((e) => message.error(e.message))}
                    options={[
                      { value: 'auto', label: '自动（跟随现有文件）' },
                      { value: 'v1', label: 'V1（OpenCode 1.x 兼容）' },
                      { value: 'v2', label: 'V2（OpenCode 2.x 原生）' }
                    ]}
                  />
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    当前写入 {app.opencode.shape === 'v2' ? 'V2 原生' : 'V1 兼容'} 形状
                    {app.opencode.shapeSetting === 'auto' ? '（按现有文件判断）' : '（手动指定）'}
                  </Text>
                </Space>
              )
            }
          ]}
        />
        <Divider style={{ margin: '12px 0' }} />
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Space align="start" size={12}>
            <Switch
              checked={app.opencode.autoSync}
              onChange={(v) => app.setOpenCode((c) => ({ ...c, autoSync: v }))}
            />
            <div>
              <Text strong>自动同步</Text>
              <div style={{ fontSize: 12, color: '#8c93a4' }}>
                推荐开启：新增或删除模型、修改代理端口时，自动更新 opencode.json。
              </div>
            </div>
          </Space>
          <Space align="start" size={12}>
            <Switch
              checked={app.opencode.useDefaultModel}
              onChange={(v) => app.syncOpenCode({ use_default_model: v }).catch((e) => message.error(e.message))}
            />
            <div>
              <Text strong>设为 OpenCode 默认模型</Text>
              <div style={{ fontSize: 12, color: '#8c93a4' }}>
                打开后会把根级 model 写为 llm-switch/{app.defaultModel || '…'}；关闭则移除该字段。
              </div>
            </div>
          </Space>
        </Space>
        {app.opencode.otherProviders?.length ? (
          <>
            <Divider style={{ margin: '12px 0' }} />
            <Space size={8} wrap>
              <Text type="secondary" style={{ fontSize: 12 }}>
                配置里还有其它直连 provider：
              </Text>
              {app.opencode.otherProviders.map((p) => (
                <Tag key={p}>{p}</Tag>
              ))}
              <Text type="secondary" style={{ fontSize: 12 }}>
                它们不经过本机代理，如需统一管理可在 OpenCode 里删除或禁用。
              </Text>
            </Space>
          </>
        ) : null}
      </Card>

      <Card className="soft-card" variant="borderless">
        <AdvancedEditor app={app} />
      </Card>

      <Alert
        type="success"
        showIcon
        message="无需重启 OpenCode"
        description="OpenCode 会自动重新加载配置，之后新开的会话即可看到最新模型列表。"
      />
    </Space>
  )
}
