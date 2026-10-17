import { useState } from 'react'
import { App, Select, Space, Tag, Typography } from 'antd'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import CopyText from '../components/CopyText'
import ConnectAgent from '../components/ConnectAgent'

const { Text } = Typography

const PREVIEW = `# $DSH_HOME/profiles/desktop/cordis.patch.yml
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      llm-switch:
        displayName: LLM Switch
        api: openai-completions
        baseURL: http://127.0.0.1:8317/v1
        headers:
          Authorization: Bearer llm-switch-local
        models:
          - id: DeepSeek/deepseek-chat
            name: DeepSeek/deepseek-chat
            input: [text]`

export default function DshPage() {
  const app = useApp()
  const { message } = App.useApp()
  const [connecting, setConnecting] = useState(false)
  const [syncing, setSyncing] = useState(false)

  const profileOptions = (() => {
    const list = new Set(app.dsh.profiles || [])
    list.add('desktop')
    list.add('web')
    return [...list].sort().map((profile) => ({
      value: profile,
      label: profile === 'desktop' ? 'desktop（桌面端）' : profile === 'web' ? 'web（Web UI）' : profile
    }))
  })()

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectDsh()
    } catch (error) {
      message.error(error.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncDsh()
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

  const profileSelect = (
    <Space size={8} wrap>
      <Select
        size="small"
        className="inspection-select"
        value={app.dsh.profileSetting}
        onChange={(profile) => app.setDshProfile(profile).catch((error) => message.error(error.message))}
        options={profileOptions}
      />
      {!app.dsh.profileExists ? <Text type="warning">该 profile 目录尚不存在，请先启动一次 dsh</Text> : null}
    </Space>
  )

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
    { label: '目标 profile', value: profileSelect },
    { label: '配置文件', value: <CopyText value={app.dsh.configPath || '尚未确定'} /> }
  ]

  const connectedInspection = [
    { label: '已写入模型', value: `${app.dsh.models.length} 个` },
    { label: '请求地址', value: <CopyText value={app.dsh.baseUrl} /> },
    { label: '目标 profile', value: profileSelect },
    { label: '配置文件', value: <CopyText value={app.dsh.configPath} /> },
    {
      label: '最近同步',
      value: app.dsh.lastSync ? app.dsh.lastSync.toLocaleString('zh-CN', { hour12: false }) : '刚刚'
    }
  ]

  const configExtra = app.dsh.otherProviders?.length ? (
    <div className="other-providers">
      <Text type="secondary">llm-pi-ai 段里还有其它 provider：</Text>
      <Space size={6} wrap>
        {app.dsh.otherProviders.map((provider) => (
          <Tag key={provider}>{provider}</Tag>
        ))}
      </Space>
      <div className="section-desc">它们同样由 dsh 模型页管理，本工具不会改动。</div>
    </div>
  ) : null

  return (
    <div className="stack">
      <PageHeader
        title="接入 DeepSeek Harness"
        description="只维护 cordis.patch.yml 中的 providers.llm-switch 段，模型变化时自动同步。"
      />

      <ConnectAgent
        connected={app.dsh.connected}
        warnings={app.dsh.warnings}
        heroTitle="让 DeepSeek Harness 用上你的模型"
        heroDescription="只改 cordis.patch.yml 里的 llm-pi-ai 一段，你已有的 provider、插件与注释一律不动。"
        points={[
          '模型清单直接写进 profile patch，模型选择器里可切换',
          'dsh 在下一次请求时重读配置，无需重启',
          '真实密钥留在 LLM Switch，配置文件里只放占位值'
        ]}
        connectLabel="一键接入 DeepSeek Harness"
        connecting={connecting}
        onConnect={connect}
        previewContent={app.dsh.generated || PREVIEW}
        disconnectedInspection={disconnectedInspection}
        connectedTitle="DeepSeek Harness 已接入"
        connectedDescription="在 dsh 的模型选择器里选 LLM Switch 下的模型即可，新增或删除模型会自动同步。"
        onSync={sync}
        syncing={syncing}
        onTest={test}
        onDisconnect={app.disconnectDsh}
        disconnectTitle="断开 DeepSeek Harness 接入？"
        disconnectDescription="只移除 LLM Switch 写入的 providers.llm-switch 段，不影响你其他的 dsh 设置。"
        autoSync={app.dsh.autoSync}
        onAutoSync={(value) => app.setDsh((current) => ({ ...current, autoSync: value }))}
        autoSyncDescription="新增或删除模型、修改代理端口时，自动更新 cordis.patch.yml。"
        inspection={connectedInspection}
        configExtra={configExtra}
        footnote="dsh 会在下一次请求时重读 profile patch；如果模型选择器没有立刻刷新，重新打开一次设置页即可。"
      />
    </div>
  )
}
