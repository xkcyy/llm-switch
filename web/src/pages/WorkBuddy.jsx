import { useState } from 'react'
import { App, Space, Tag, Typography } from 'antd'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import CopyText from '../components/CopyText'
import ConnectAgent from '../components/ConnectAgent'

const { Text } = Typography

const PREVIEW = `[
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

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectWorkBuddy()
    } catch (error) {
      message.error(error.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncWorkBuddy()
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
      label: 'WorkBuddy',
      value: app.workbuddy.installed ? (
        <Text type="success">已检测到配置目录</Text>
      ) : (
        <Text type="warning">未检测到配置目录，请先启动一次 WorkBuddy</Text>
      )
    },
    { label: '配置文件', value: <CopyText value={app.workbuddy.configPath || '尚未确定'} /> }
  ]

  const connectedInspection = [
    { label: '已写入模型', value: `${app.workbuddy.models.length} 个` },
    { label: '请求地址', value: <CopyText value={app.workbuddy.chatUrl || app.workbuddy.baseUrl} /> },
    {
      label: '文件格式',
      value: <Tag bordered={false}>{app.workbuddy.format === 'object' ? '对象（含 models）' : '数组'}</Tag>
    },
    { label: '配置文件', value: <CopyText value={app.workbuddy.configPath} /> },
    {
      label: '最近同步',
      value: app.workbuddy.lastSync ? app.workbuddy.lastSync.toLocaleString('zh-CN', { hour12: false }) : '刚刚'
    }
  ]

  const configExtra = app.workbuddy.otherModels?.length ? (
    <div className="other-providers">
      <Text type="secondary">文件里还有其它自定义模型：</Text>
      <Space size={6} wrap>
        {app.workbuddy.otherModels.map((model) => (
          <Tag key={model}>{model}</Tag>
        ))}
      </Space>
      <div className="section-desc">它们由 WorkBuddy 自己的模型页管理，本工具不会改动。</div>
    </div>
  ) : null

  return (
    <div className="stack">
      <PageHeader
        title="接入 WorkBuddy"
        description="只维护 models.json 中由本工具写入的自定义模型，变更后下一次请求即生效。"
      />

      <ConnectAgent
        connected={app.workbuddy.connected}
        warnings={app.workbuddy.warnings}
        heroTitle="让 WorkBuddy 用上你的模型"
        heroDescription="只改 ~/.workbuddy/models.json 里由本工具写入的自定义模型，你已有的自定义模型一律不动。"
        points={[
          '模型写进 WorkBuddy「自定义模型」分组，设置页里可切换',
          'WorkBuddy 监听 models.json，改完下一次请求即生效',
          '真实密钥留在 LLM Switch，配置里只放占位值'
        ]}
        connectLabel="一键接入 WorkBuddy"
        connecting={connecting}
        onConnect={connect}
        previewContent={app.workbuddy.generated || PREVIEW}
        disconnectedInspection={disconnectedInspection}
        connectedTitle="WorkBuddy 已接入"
        connectedDescription="在「设置 → 模型」的「自定义模型」里选 LLM Switch 下的模型即可，新增或删除模型会自动同步。"
        onSync={sync}
        syncing={syncing}
        onTest={test}
        onDisconnect={app.disconnectWorkBuddy}
        disconnectTitle="断开 WorkBuddy 接入？"
        disconnectDescription="只移除 LLM Switch 写入的自定义模型，不影响你其它的 WorkBuddy 设置。"
        autoSync={app.workbuddy.autoSync}
        onAutoSync={(value) => app.setWorkBuddy((current) => ({ ...current, autoSync: value }))}
        autoSyncDescription="新增或删除模型、修改代理端口时，自动更新 models.json。"
        inspection={connectedInspection}
        configExtra={configExtra}
        footnote="WorkBuddy 会监听 models.json 的变化；如果模型选择器没有立刻刷新，重新打开一次设置页即可。"
      />
    </div>
  )
}
