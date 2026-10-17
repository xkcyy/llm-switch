import { useEffect, useState } from 'react'
import { Alert, App, Button, Collapse, Input, Select, Space, Switch, Tag, Typography } from 'antd'
import { EditOutlined } from '@ant-design/icons'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import CopyText from '../components/CopyText'
import ConnectAgent from '../components/ConnectAgent'

const { Text } = Typography

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
    } catch (error) {
      message.error(error.message)
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
      className="advanced-collapse"
      items={[
        {
          key: 'advanced',
          label: (
            <Space>
              <EditOutlined />
              <span>手动编辑 opencode.json</span>
              <Text type="secondary">通常不需要，自动模式会生成同样的内容</Text>
            </Space>
          ),
          children: (
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              <Alert
                type="info"
                showIcon
                message="这里编辑整份 opencode.json；已有 provider、plugins 等内容原样保留，只有 provider.llm-switch 段由 LLM Switch 维护。"
              />
              <Input.TextArea
                className="editor-area"
                value={value}
                onChange={(event) => setValue(event.target.value)}
                autoSize={{ minRows: 10, maxRows: 18 }}
              />
              <Space>
                <Button size="small" icon={<EditOutlined />} onClick={format}>
                  格式化
                </Button>
                <Button size="small" type="primary" loading={saving} onClick={save}>
                  保存并同步
                </Button>
                <Text type="secondary">保存前会校验 JSON，写入失败不会影响现有配置。</Text>
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

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectOpenCode()
    } catch (error) {
      message.error(error.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncOpenCode()
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

  const disconnectedInspection = [
    {
      label: '供应商',
      value: app.providers.length ? `${app.providers.length} 个可用` : <Text type="danger">请先添加供应商</Text>
    },
    {
      label: '模型',
      value: app.enabledModels.length ? `${app.enabledModels.length} 个可用模型` : <Text type="danger">请先添加模型</Text>
    },
    {
      label: '本机代理',
      value: app.proxy.running
        ? `运行中（${app.proxy.host}:${app.proxy.port}）`
        : <Text type="warning">已停止，接入时会自动启动</Text>
    },
    {
      label: '目标文件',
      value: <CopyText value={app.opencode.configPath || '尚未确定'} />
    }
  ]

  const connectedInspection = [
    {
      label: '已写入模型',
      value: `${app.opencode.models.length} 个，/models 里显示为 llm-switch/*`
    },
    {
      label: '请求地址',
      value: <CopyText value={app.opencode.baseUrl} />
    },
    {
      label: '配置文件',
      value: <CopyText value={app.opencode.configPath} />
    },
    {
      label: '配置形状',
      value: (
        <Space size={8} wrap>
          <Select
            size="small"
            className="inspection-select"
            value={app.opencode.shapeSetting}
            onChange={(value) => app.setOpenCodeShape(value).catch((error) => message.error(error.message))}
            options={[
              { value: 'auto', label: '自动（跟随现有文件）' },
              { value: 'v1', label: 'V1（OpenCode 1.x 兼容）' },
              { value: 'v2', label: 'V2（OpenCode 2.x 原生）' }
            ]}
          />
          <Text type="secondary">
            当前写入 {app.opencode.shape === 'v2' ? 'V2 原生' : 'V1 兼容'} 形状
            {app.opencode.shapeSetting === 'auto' ? '（按现有文件判断）' : '（手动指定）'}
          </Text>
        </Space>
      )
    },
    {
      label: '最近同步',
      value: app.opencode.lastSync ? app.opencode.lastSync.toLocaleString('zh-CN', { hour12: false }) : '刚刚'
    }
  ]

  const configExtra = (
    <div className="stack stack-sm config-extra">
      <div className="config-toggle-row">
        <Switch
          checked={app.opencode.useDefaultModel}
          onChange={(value) => app.syncOpenCode({ use_default_model: value }).catch((error) => message.error(error.message))}
        />
        <div>
          <Text strong>设为 OpenCode 默认模型</Text>
          <div className="section-desc">
            打开后把根级 model 写为 llm-switch/{app.defaultModel || '…'}；关闭则移除该字段。
          </div>
        </div>
      </div>
      {app.opencode.otherProviders?.length ? (
        <div className="other-providers">
          <Text type="secondary">配置里还有其它直连 provider：</Text>
          <Space size={6} wrap>
            {app.opencode.otherProviders.map((provider) => (
              <Tag key={provider}>{provider}</Tag>
            ))}
          </Space>
          <div className="section-desc">它们不经过本机代理，如需统一管理可在 OpenCode 里删除或禁用。</div>
        </div>
      ) : null}
    </div>
  )

  return (
    <div className="stack">
      <PageHeader
        title="接入 OpenCode"
        description="只维护 opencode.json 中的 provider.llm-switch 段，模型变化时自动同步。"
      />

      <ConnectAgent
        connected={app.opencode.connected}
        warnings={app.opencode.warnings}
        heroTitle="让 OpenCode 用上你的模型"
        heroDescription="只改 provider.llm-switch 一段，你已有的 provider 和插件设置一律不动。"
        points={[
          '模型清单直接写进 opencode.json，/models 里可切换',
          'OpenCode 自动重载配置，无需重启',
          '真实密钥留在 LLM Switch，配置文件里只放占位值'
        ]}
        connectLabel="一键接入 OpenCode"
        connecting={connecting}
        onConnect={connect}
        previewContent={app.opencode.generated || PREVIEW}
        disconnectedInspection={disconnectedInspection}
        connectedTitle="OpenCode 已接入"
        connectedDescription="在 OpenCode 里用 /models 选择 llm-switch/* 即可，新增或删除模型会自动同步。"
        onSync={sync}
        syncing={syncing}
        onTest={test}
        onDisconnect={app.disconnectOpenCode}
        disconnectTitle="断开 OpenCode 接入？"
        disconnectDescription="只移除 LLM Switch 写入的 provider.llm-switch 段，不影响你其他的 OpenCode 设置。"
        autoSync={app.opencode.autoSync}
        onAutoSync={(value) => app.setOpenCode((current) => ({ ...current, autoSync: value }))}
        autoSyncDescription="新增或删除模型、修改代理端口时，自动更新 opencode.json。"
        inspection={connectedInspection}
        configExtra={configExtra}
        advanced={<Panel title="高级：手动编辑配置"><AdvancedEditor app={app} /></Panel>}
        footnote="OpenCode 会自动重新加载配置，之后新开的会话即可看到最新模型列表。"
      />
    </div>
  )
}
