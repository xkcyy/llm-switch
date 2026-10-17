import { useEffect, useState } from 'react'
import { Alert, App, Button, Collapse, Input, Select, Space, Tabs, Typography } from 'antd'
import { EditOutlined } from '@ant-design/icons'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import CopyText from '../components/CopyText'
import ConnectAgent from '../components/ConnectAgent'

const { Text } = Typography

const CONFIG_PREVIEW = `model_provider = "llm-switch"
model = "DeepSeek/deepseek-chat"
model_catalog_json = 'C:\\Users\\<user>\\.codex\\llm-switch-models.json'
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
      "context_window": 131072
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
      setValues((current) => ({ ...current, config: current.config.replace(/[ \t]+$/gm, '') }))
      message.success('已整理空白')
      return
    }
    try {
      const parsed = JSON.parse(values[key])
      setValues((current) => ({ ...current, [key]: JSON.stringify(parsed, null, 2) }))
      message.success('已格式化')
    } catch {
      message.warning('内容不是合法 JSON，无法格式化')
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      await app.syncCodex({ config_toml: values.config, auth_json: values.auth })
    } catch (error) {
      message.error(error.message)
    } finally {
      setSaving(false)
    }
  }

  const pane = (key, title, tip) => ({
    key,
    label: title,
    children: (
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        <Alert type="info" showIcon message={tip} />
        <Input.TextArea
          className="editor-area"
          value={values[key]}
          onChange={(event) => setValues((current) => ({ ...current, [key]: event.target.value }))}
          autoSize={{ minRows: 8, maxRows: 16 }}
        />
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => format(key)}>
            格式化
          </Button>
          <Button size="small" type="primary" loading={saving} onClick={save}>
            保存并同步
          </Button>
          <Text type="secondary">保存前会校验语法，写入失败不会影响现有配置。</Text>
        </Space>
      </Space>
    )
  })

  return (
    <Collapse
      className="advanced-collapse"
      items={[
        {
          key: 'advanced',
          label: (
            <Space>
              <EditOutlined />
              <span>手动编辑配置</span>
              <Text type="secondary">通常不需要，自动模式会生成同样的内容</Text>
            </Space>
          ),
          children: (
            <Tabs
              size="small"
              items={[
                pane('config', 'config.toml', 'Codex 主配置：模型供应商指向本机代理，并指定模型清单文件'),
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

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectCodex()
    } catch (error) {
      message.error(error.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncCodex()
    } catch (error) {
      message.error(error.message)
    } finally {
      setSyncing(false)
    }
  }

  const test = () => {
    const target = app.models.find((model) => model.slug === app.defaultModel) || app.enabledModels[0]
    if (!target) {
      message.warning('还没有可用模型')
      return
    }
    app.testModel(target.id)
  }

  const generated = app.codex.generated || {}
  const preview = [
    '# config.toml',
    generated.config_toml || CONFIG_PREVIEW,
    '# auth.json',
    generated.auth_json || AUTH_PREVIEW,
    '# 模型清单',
    generated.catalog || CATALOG_PREVIEW
  ].join('\n\n')

  const disconnectedInspection = [
    {
      label: '供应商',
      value: app.providers.length ? `${app.providers.length} 个可用` : <Text type="danger">请先添加供应商</Text>
    },
    {
      label: '模型',
      value: app.enabledModels.length
        ? `${app.enabledModels.length} 个可用${app.defaultModel ? `，默认使用 ${app.defaultModel}` : ''}`
        : <Text type="danger">请先添加可用模型</Text>
    },
    {
      label: '本机代理',
      value: app.proxy.running
        ? `运行中（${app.proxy.host}:${app.proxy.port}）`
        : <Text type="warning">已停止，接入时会自动启动</Text>
    },
    {
      label: '目标文件',
      value: <CopyText value={app.codex.configPath || '尚未确定'} />
    }
  ]

  const connectedInspection = [
    {
      label: '默认模型',
      value: (
        <Select
          size="small"
          showSearch
          className="inspection-select"
          placeholder="自动选择"
          value={app.defaultModel || app.codex.defaultModel || undefined}
          onChange={app.setDefaultModel}
          options={app.enabledModels.map((model) => ({ value: model.slug, label: model.slug }))}
        />
      )
    },
    {
      label: '请求地址',
      value: <CopyText value={app.codex.baseUrl} />
    },
    {
      label: '配置文件',
      value: <CopyText value={app.codex.configPath} />
    },
    {
      label: '模型清单',
      value: <CopyText value={app.codex.catalogPath} />
    },
    {
      label: '最近同步',
      value: app.codex.lastSync ? app.codex.lastSync.toLocaleString('zh-CN', { hour12: false }) : '刚刚'
    }
  ]

  return (
    <div className="stack">
      <PageHeader
        title="接入 Codex"
        description="写入 config.toml、auth.json 与模型清单，模型变化时自动同步。"
      />

      <ConnectAgent
        connected={app.codex.connected}
        warnings={app.codex.warnings}
        heroTitle="让 Codex 用上你的模型"
        heroDescription="一键完成配置，之后新增或删除模型都会自动同步，不用再碰配置文件。"
        points={[
          '自动写入 config.toml / auth.json / 模型清单',
          '默认模型与请求地址自动填好',
          '写入前校验，失败不影响原配置'
        ]}
        connectLabel="一键接入 Codex"
        connecting={connecting}
        onConnect={connect}
        previewContent={preview}
        disconnectedInspection={disconnectedInspection}
        connectedTitle="Codex 已接入"
        connectedDescription="在 Codex 里正常对话即可，请求会经过本机代理转发到对应供应商。"
        onSync={sync}
        syncing={syncing}
        onTest={test}
        onDisconnect={app.disconnectCodex}
        disconnectTitle="断开 Codex 接入？"
        disconnectDescription="只移除 LLM Switch 写入的配置，不影响你其他的 Codex 设置。"
        autoSync={app.codex.autoSync}
        onAutoSync={(value) => app.setCodex((current) => ({ ...current, autoSync: value }))}
        autoSyncDescription="新增或删除模型、修改默认模型、修改代理端口时，自动更新 Codex 配置。"
        inspection={connectedInspection}
        advanced={<Panel title="高级：手动编辑配置"><AdvancedEditors app={app} /></Panel>}
        footnote="修改配置后需要重新打开 Codex 才会读取新的模型清单。"
      />
    </div>
  )
}
