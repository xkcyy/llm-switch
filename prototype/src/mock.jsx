import { createContext, useContext, useMemo, useState } from 'react'
import { App } from 'antd'

const Ctx = createContext(null)
export const useApp = () => useContext(Ctx)

const delay = (ms = 400) => new Promise((r) => setTimeout(r, ms))

const normalizeId = (id) => (id || '').trim().replace(/\s+/g, '-').replace(/\//g, '').toLowerCase()

export const PRESETS = [
  { key: 'opencode-go', label: 'OpenCode Go', protocol: 'chat', baseUrl: 'https://opencode.ai/zen/v1', hint: 'OpenAI 兼容' },
  { key: 'deepseek', label: 'DeepSeek', protocol: 'chat', baseUrl: 'https://api.deepseek.com/v1', hint: 'OpenAI 兼容' },
  { key: 'openai', label: 'OpenAI', protocol: 'responses', baseUrl: 'https://api.openai.com/v1', hint: 'Responses API' },
  { key: 'anthropic', label: 'Anthropic', protocol: 'messages', baseUrl: 'https://api.anthropic.com/v1', hint: 'Messages API' },
  { key: 'custom', label: '自定义', protocol: 'chat', baseUrl: '', hint: '手动填写地址' }
]

// 模拟"从上游拉取"的模型元数据（演示用，真实实现会由 models.dev 等来源自动补全）
const REMOTE_MODELS = {
  'p1': [
    { id: 'deepseek-chat', name: 'DeepSeek Chat', contextWindow: 131072, output: 16384, levels: ['low', 'medium', 'high'], caps: ['tools', 'reasoning'] },
    { id: 'deepseek-reasoner', name: 'DeepSeek Reasoner', contextWindow: 131072, output: 32768, levels: ['low', 'medium', 'high'], caps: ['tools', 'reasoning'] }
  ],
  'p3': [
    { id: 'glm-5.3', name: 'GLM-5.3', contextWindow: 204800, output: 32768, levels: ['low', 'high', 'max'], caps: ['tools', 'reasoning', 'vision'] },
    { id: 'qwen3.6-max', name: 'Qwen3.6 Max', contextWindow: 1000000, output: 65536, levels: ['low', 'medium', 'high'], caps: ['tools', 'vision'], protocol: 'messages' }
  ]
}

export function AppStoreProvider({ children }) {
  const { message } = App.useApp()

  const [proxy, setProxy] = useState({ running: true, host: '127.0.0.1', port: 8317 })
  const [providers, setProviders] = useState([
    { id: 'p1', name: 'DeepSeek', preset: 'deepseek', baseUrl: 'https://api.deepseek.com/v1', protocol: 'chat', keyMasked: 'sk-…3f2a', enabled: true, testing: false },
    { id: 'p2', name: 'Anthropic', preset: 'anthropic', baseUrl: 'https://api.anthropic.com/v1', protocol: 'messages', keyMasked: 'sk-ant-…88c1', enabled: true, testing: false }
  ])
  const [models, setModels] = useState([
    { id: 'deepseek-chat', providerId: 'p1', slug: 'DeepSeek/deepseek-chat', name: 'DeepSeek Chat', contextWindow: 131072, maxOutputTokens: 16384, levels: ['low', 'medium', 'high'], defaultLevel: 'medium', caps: ['tools', 'reasoning'], enabled: true, auto: true },
    { id: 'deepseek-reasoner', providerId: 'p1', slug: 'DeepSeek/deepseek-reasoner', name: 'DeepSeek Reasoner', contextWindow: 131072, maxOutputTokens: 32768, levels: ['low', 'medium', 'high'], defaultLevel: 'high', caps: ['tools', 'reasoning'], enabled: true, auto: true },
    { id: 'claude-sonnet-4-6', providerId: 'p2', slug: 'Anthropic/claude-sonnet-4-6', name: 'Claude Sonnet 4.6', contextWindow: 1000000, maxOutputTokens: 128000, levels: ['low', 'medium', 'high', 'max'], defaultLevel: 'medium', caps: ['tools', 'reasoning', 'vision'], enabled: true, auto: true }
  ])
  const [defaultModel, setDefaultModel] = useState('DeepSeek/deepseek-chat')
  const [codex, setCodex] = useState({
    connected: false,
    autoSync: true,
    baseUrl: 'http://127.0.0.1:8317/v1',
    configPath: 'C:\\Users\\xkcyy\\.codex\\config.toml',
    catalogPath: 'C:\\Users\\xkcyy\\.codex\\llm-switch-models.json',
    warnings: [],
    lastSync: null
  })
  const [settings, setSettings] = useState({
    autoStart: true,
    openPanelOnStart: true,
    logLevel: 'info',
    keepLogDays: 7,
    configDir: 'C:\\Users\\xkcyy\\.llm-switch'
  })

  const enabledModels = models.filter((m) => m.enabled)

  const actions = useMemo(() => {
    const deriveId = (name) =>
      (name || '')
        .toLowerCase()
        .trim()
        .replace(/[\s_]+/g, '-')
        .replace(/[^a-z0-9\-\u4e00-\u9fa5]/g, '')
        .replace(/^-+|-+$/g, '')

    const addProvider = async (values) => {
      await delay()
      const preset = PRESETS.find((p) => p.key === values.preset) || PRESETS[4]
      const id = normalizeId(values.id) || deriveId(values.name || preset.label) || 'provider'
      const provider = {
        id,
        name: values.name || preset.label,
        preset: preset.key,
        baseUrl: values.baseUrl || preset.baseUrl,
        protocol: values.protocol || preset.protocol,
        keyMasked: values.apiKey ? values.apiKey.slice(0, 4) + '…' + values.apiKey.slice(-4) : '••••',
        enabled: true,
        testing: false
      }
      setProviders((prev) => [...prev, provider])
      return provider
    }

    const updateProvider = async (id, patch) => {
      await delay(300)
      const newId = normalizeId(patch.id) || id
      setProviders((prev) => prev.map((p) => (p.id === id ? { ...p, ...patch, id: newId } : p)))
      if (newId !== id) {
        setModels((prev) => prev.map((m) => (m.providerId === id ? { ...m, providerId: newId, slug: `${newId}/${m.id}` } : m)))
      }
    }

    const removeProvider = async (id) => {
      await delay(300)
      setProviders((prev) => prev.filter((p) => p.id !== id))
      setModels((prev) => prev.filter((m) => m.providerId !== id))
      message.success('供应商已删除')
    }

    const testProvider = async (id) => {
      setProviders((prev) => prev.map((p) => (p.id === id ? { ...p, testing: true } : p)))
      await delay(800)
      setProviders((prev) => prev.map((p) => (p.id === id ? { ...p, testing: false } : p)))
      message.success('连接正常')
    }

    const fetchRemoteModels = async (providerId) => {
      await delay(900)
      return REMOTE_MODELS[providerId] || [
        { id: 'gpt-5.6-sol', name: 'gpt-5.6-sol', contextWindow: 1050000, output: 128000, levels: ['none', 'low', 'medium', 'high', 'xhigh', 'max'], caps: ['tools', 'reasoning', 'vision'], protocol: 'responses' },
        { id: 'qwen3.8-max', name: 'Qwen3.8 Max', contextWindow: 1000000, output: 65536, levels: ['low', 'medium', 'high'], caps: ['tools'], protocol: 'messages' },
        { id: 'glm-5.3', name: 'GLM-5.3', contextWindow: 204800, output: 32768, levels: ['low', 'high', 'max'], caps: ['tools'] }
      ]
    }

    const importModels = async (providerId, remoteList) => {
      await delay(400)
      const provider = providers.find((p) => p.id === providerId)
      const created = remoteList.map((r) => ({
        id: r.id,
        providerId,
        slug: `${provider?.id || 'custom'}/${r.id}`,
        name: r.name || r.id,
        contextWindow: r.contextWindow,
        maxOutputTokens: r.output,
        levels: r.levels,
        protocol: r.protocol || '',
        defaultLevel: r.levels?.[Math.floor((r.levels?.length || 1) / 2)] || 'medium',
        caps: r.caps || ['tools'],
        enabled: true,
        auto: true
      }))
      setModels((prev) => {
        const exists = new Set(prev.filter((m) => m.providerId === providerId).map((m) => m.id))
        return [...prev, ...created.filter((m) => !exists.has(m.id))]
      })
      message.success(`已添加 ${created.length} 个模型`)
      return created
    }

    const toggleModel = (id, enabled) => setModels((prev) => prev.map((m) => (m.id === id ? { ...m, enabled } : m)))
    const restartProxy = async () => {
      await delay(600)
      message.success('代理已重启')
    }
    const revealProviderKey = async (id) => {
      await delay(250)
      return { api_key: 'sk-demo-full-key-1234567890abcdef' }
    }
    const openConfigDir = async () => {
      await delay(200)
    }
    const openLogDir = async () => {
      await delay(200)
    }
    const removeModel = (id) => {
      setModels((prev) => prev.filter((m) => m.id !== id))
      message.success('模型已删除')
    }
    const testModel = async (id) => {
      await delay(700)
      message.success('调用成功 · 428ms')
    }

    const saveModel = async (providerId, values, editId) => {
      await delay(350)
      const provider = providers.find((p) => p.id === providerId)
      const model = {
        id: values.id,
        providerId,
        slug: `${provider?.id || 'custom'}/${values.id}`,
        name: values.name || values.id,
        contextWindow: values.contextWindow,
        maxOutputTokens: values.maxOutputTokens,
        protocol: values.protocol || '',
        levels: values.levels || ['low', 'medium', 'high'],
        defaultLevel: (values.levels || ['medium'])[Math.floor(((values.levels || ['medium']).length - 1) / 2)],
        caps: values.caps || ['tools'],
        enabled: values.enabled !== false,
        auto: false
      }
      setModels((prev) =>
        editId
          ? prev.map((m) => (m.providerId === providerId && m.id === editId ? { ...m, ...model } : m))
          : [...prev.filter((m) => !(m.providerId === providerId && m.id === values.id)), model]
      )
      message.success(editId ? '模型已更新' : '模型已添加')
    }

    const connectCodex = async () => {
      await delay(1200)
      setCodex((prev) => ({ ...prev, connected: true, lastSync: new Date() }))
      message.success('已接入 Codex')
    }
    const syncCodex = async () => {
      await delay(900)
      setCodex((prev) => ({ ...prev, lastSync: new Date() }))
      message.success('配置已同步')
    }
    const disconnectCodex = async () => {
      await delay(500)
      setCodex((prev) => ({ ...prev, connected: false }))
      message.success('已断开接入')
    }

    return {
      addProvider, updateProvider, removeProvider, testProvider,
      fetchRemoteModels, importModels, toggleModel, removeModel, testModel, saveModel,
      restartProxy, openConfigDir, openLogDir, revealProviderKey,
      connectCodex, syncCodex, disconnectCodex,
      setDefaultModel, setSettings, setProxy, setCodex
    }
  }, [providers, message])

  const value = {
    proxy, providers, models, enabledModels, defaultModel, codex, settings,
    recent: [], repairs: [],
    ...actions
  }
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
