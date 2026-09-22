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
  Form,
  Input,
  Popconfirm,
  Row,
  Select,
  Space,
  Switch,
  Tabs,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import {
  CheckCircleFilled,
  CopyOutlined,
  DisconnectOutlined,
  EditOutlined,
  ReloadOutlined,
  RocketOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text, Title, Paragraph } = Typography

const CONFIG_PREVIEW = `model_provider = "llm-switch"
model = "DeepSeek/deepseek-chat"
model_catalog_json = 'C:\\Users\\xkcyy\\.codex\\llm-switch-models.json'
disable_response_storage = true

[model_providers.llm-switch]
name = "LLM Switch"
base_url = "http://127.0.0.1:8317/v1"
wire_api = "responses"
requires_openai_auth = true`

const AUTH_PREVIEW = `{
  "OPENAI_API_KEY": "llm-switch-local"
}`

const CATALOG_PREVIEW = `{
  "models": [
    {
      "slug": "DeepSeek/deepseek-chat",
      "display_name": "DeepSeek Chat",
      "context_window": 131072,
      "default_reasoning_level": "medium",
      "supported_reasoning_levels": ["low", "medium", "high"],
      "supports_parallel_tool_calls": true
    }
  ]
}`

function AdvancedEditors({ app }) {
  const { message } = App.useApp()
  const files = app.codex.files || {}
  const generated = app.codex.generated || {}
  const initial = {
    config: files.config_toml?.content || generated.config_toml || '',
    auth: files.auth_json?.content || generated.auth_json || '',
    catalog: generated.catalog || ''
  }
  const [values, setValues] = useState(initial)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setValues({
      config: files.config_toml?.content || generated.config_toml || '',
      auth: files.auth_json?.content || generated.auth_json || '',
      catalog: generated.catalog || ''
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [files.config_toml?.content, files.auth_json?.content, generated.config_toml, generated.auth_json, generated.catalog])

  const format = (key) => {
    if (key === 'config') {
      setValues((v) => ({ ...v, config: v.config.replace(/[ \t]+$/gm, '') }))
      message.success('已整理空白')
      return
    }
    try {
      const parsed = JSON.parse(values[key])
      setValues((v) => ({ ...v, [key]: JSON.stringify(parsed, null, 2) }))
      message.success('已格式化')
    } catch {
      message.warning('内容不是合法 JSON，无法格式化')
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      await app.syncCodex({ config_toml: values.config, auth_json: values.auth })
    } catch (e) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  const pane = (key, title, tip) => ({
    key,
    label: title,
    children: (
      <Space direction="vertical" style={{ width: '100%' }} size={8}>
        <Alert type="info" showIcon message={tip} />
        <Input.TextArea
          className="editor-area"
          value={values[key]}
          onChange={(e) => setValues((v) => ({ ...v, [key]: e.target.value }))}
          autoSize={{ minRows: 8, maxRows: 16 }}
        />
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => format(key)}>
            格式化
          </Button>
          <Button size="small" type="primary" loading={saving} onClick={save}>
            保存并同步
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            保存前会校验语法，写入失败不会影响现有配置
          </Text>
        </Space>
      </Space>
    )
  })

  return (
    <Collapse
      items={[
        {
          key: 'advanced',
          label: (
            <Space>
              <EditOutlined />
              <span>高级：手动编辑配置</span>
              <Text type="secondary" style={{ fontSize: 12 }}>
                通常不需要，自动模式会生成同样的内容
              </Text>
            </Space>
          ),
          children: (
            <Tabs
              size="small"
              items={[
                pane('config', 'config.toml', 'Codex 的主配置：模型供应商指向本机代理，并指定模型清单文件'),
                pane('auth', 'auth.json', '本机代理不校验密钥，这里保留占位值即可'),
                pane('catalog', '模型清单', '由 LLM Switch 生成，包含全部可用模型与推理档位')
              ]}
            />
          )
        }
      ]}
    />
  )
}

export default function CodexPage() {
  const app = useApp()
  const { message } = App.useApp()
  const [connecting, setConnecting] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [previewOpen, setPreviewOpen] = useState(false)

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectCodex()
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncCodex()
    } finally {
      setSyncing(false)
    }
  }

  const warningsAlert = app.codex.warnings?.length ? (
    <Alert
      type="warning"
      showIcon
      message="配置已自动调整"
      description={
        <ul style={{ margin: 0, paddingInlineStart: 18 }}>
          {app.codex.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      }
    />
  ) : null

  if (!app.codex.connected) {
    return (
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {warningsAlert}
        <div className="codex-hero">
          <Space direction="vertical" size={14} style={{ width: '100%' }}>
            <div>
              <Title level={3} style={{ marginBottom: 4 }}>
                让 Codex 用上你的模型
              </Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>
                一键完成配置，之后新增或删除模型都会自动同步，不用再碰任何配置文件。
              </Paragraph>
            </div>
            <Space size={20} wrap>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>自动写入 config.toml / auth.json / 模型清单</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>默认模型与请求地址自动填好</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>写入前校验、失败不影响原配置</Text>
              </Space>
            </Space>
            <Space>
              <Button type="primary" size="large" icon={<RocketOutlined />} loading={connecting} onClick={connect}>
                一键接入 Codex
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
                children: app.models.length ? `${app.models.length} 个，默认使用 ${app.defaultModel}` : <Text type="danger">请先添加模型</Text>
              },
              {
                key: 'proxy',
                label: '本机代理',
                children: app.proxy.running ? `运行中（${app.proxy.host}:${app.proxy.port}）` : <Text type="warning">已停止，接入时会自动启动</Text>
              }
            ]}
          />
        </Card>

        <Drawer title="将写入的配置" width={620} open={previewOpen} onClose={() => setPreviewOpen(false)}>
          <Tabs
            items={[
              { key: 'a', label: 'config.toml', children: <pre className="mono">{app.codex.generated?.config_toml || CONFIG_PREVIEW}</pre> },
              { key: 'b', label: 'auth.json', children: <pre className="mono">{app.codex.generated?.auth_json || AUTH_PREVIEW}</pre> },
              { key: 'c', label: '模型清单', children: <pre className="mono">{app.codex.generated?.catalog || CATALOG_PREVIEW}</pre> }
            ]}
          />
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
            <div style={{ fontSize: 15, fontWeight: 600, lineHeight: 1.3 }}>Codex 已接入</div>
            <div style={{ fontSize: 12, color: '#9aa1b1', marginTop: 2 }}>
              在 Codex 里正常对话即可，请求会经过本机代理转发到对应供应商
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
                  const target = app.models.find((m) => m.slug === app.defaultModel) || app.models[0]
                  if (!target) return message.warning('还没有可用模型')
                  app.testModel(target.id)
                }}
              >
                测试一次调用
              </Button>
              <Popconfirm
                title="断开 Codex 接入？"
                description="只移除 LLM Switch 写入的配置，不影响你其他的 Codex 设置。"
                okText="断开"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                onConfirm={() => app.disconnectCodex()}
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
              key: 'model',
              label: '默认模型',
              children: (
                <Select
                  size="small"
                  showSearch
                  style={{ width: 280 }}
                  placeholder="自动选择"
                  value={app.defaultModel || app.codex.defaultModel || undefined}
                  onChange={app.setDefaultModel}
                  options={app.enabledModels.map((m) => ({ value: m.slug, label: m.slug }))}
                />
              )
            },
            {
              key: 'base',
              label: '请求地址',
              children: <Text code>{app.codex.baseUrl}</Text>
            },
            {
              key: 'file',
              label: '配置文件',
              children: (
                <Space size={4} align="center">
                  <Text className="mono" ellipsis={{ tooltip: app.codex.configPath }} style={{ maxWidth: 380 }}>
                    {app.codex.configPath}
                  </Text>
                  <Tooltip title="复制路径">
                    <Button
                      size="small"
                      type="text"
                      icon={<CopyOutlined />}
                      onClick={() => {
                        navigator.clipboard?.writeText(app.codex.configPath)
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
              children: app.codex.lastSync ? app.codex.lastSync.toLocaleTimeString('zh-CN') : '刚刚'
            }
          ]}
        />
        <Divider style={{ margin: '12px 0' }} />
        <Space align="start" size={12}>
          <Switch checked={app.codex.autoSync} onChange={(v) => app.setCodex((c) => ({ ...c, autoSync: v }))} />
          <div>
            <Text strong>自动同步</Text>
            <div style={{ fontSize: 12, color: '#8c93a4' }}>
              推荐开启：新增或删除模型、修改默认模型、修改代理端口时，自动更新 Codex 配置。
            </div>
          </div>
        </Space>
      </Card>

      <Card className="soft-card" variant="borderless">
        <AdvancedEditors app={app} />
      </Card>

      <Alert
        type="warning"
        showIcon
        message="需要重启 Codex 后生效"
        description="Codex 只在启动时读取配置与模型清单，修改后请重新打开 Codex。"
      />
    </Space>
  )
}

