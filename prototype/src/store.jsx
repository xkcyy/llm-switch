import { createContext, useContext, useEffect, useMemo, useState } from 'react'
import { App } from 'antd'
import { Api } from './api'

const Ctx = createContext(null)
export const useApp = () => useContext(Ctx)

export const PRESETS = [
  { key: 'km-aimodelhub', label: 'KmAiModelHub', protocol: 'responses', baseUrl: 'https://aimodelhub.ai.kmsoft.com.cn/v1', hint: '企业模型网关 · 支持 Responses' },
  { key: 'opencode-go', label: 'OpenCode Go', protocol: 'chat', baseUrl: 'https://opencode.ai/zen/go/v1', hint: 'OpenAI 兼容' },
  { key: 'deepseek', label: 'DeepSeek', protocol: 'chat', baseUrl: 'https://api.deepseek.com', hint: 'OpenAI 兼容 · 无需 /v1' },
  { key: 'openai', label: 'OpenAI', protocol: 'responses', baseUrl: 'https://api.openai.com/v1', hint: 'Responses API' },
  { key: 'anthropic', label: 'Anthropic', protocol: 'messages', baseUrl: 'https://api.anthropic.com/v1', hint: 'Messages API' },
  { key: 'custom', label: '自定义', protocol: 'chat', baseUrl: '', hint: '手动填写地址' }
]

const mapProvider = (p) => ({
  id: p.id,
  name: p.name,
  preset: p.preset,
  baseUrl: p.base_url,
  protocol: p.protocol,
  keyMasked: p.auth?.api_key_masked || '',
  enabled: p.enabled,
  modelCount: p.model_count || 0,
  testing: false
})

const mapModel = (m) => ({
  id: m.id,
  providerId: m.provider_id,
  slug: m.slug,
  name: m.name || m.id,
  description: m.description || '',
  contextWindow: m.context_window ?? null,
  maxOutputTokens: m.max_output_tokens ?? null,
  levels: m.reasoning?.levels?.length ? m.reasoning.levels : null,
  defaultLevel: m.reasoning?.default_level || null,
  caps: m.capabilities || [],
  protocol: m.protocol || '',
  enabled: m.enabled,
  auto: Boolean(m.context_window || m.max_output_tokens || m.reasoning?.levels?.length)
})

const mapSettings = (data) => ({
  autoStart: data.settings.auto_start,
  openPanelOnStart: data.settings.open_browser_on_start,
  logLevel: data.settings.log?.level || 'info',
  keepLogDays: data.settings.log?.retention_days || 7,
  configDir: data.paths?.config_dir || '',
  logDir: data.paths?.log_dir || '',
  configFile: data.paths?.config_file || ''
})

const mapCodex = (cx) => ({
  connected: cx.connected,
  autoSync: cx.auto_sync,
  baseUrl: cx.base_url,
  defaultModel: cx.default_model,
  configPath: cx.files?.config_toml?.path || '',
  catalogPath: cx.catalog_path,
  authPath: cx.files?.auth_json?.path || '',
  files: cx.files || {},
  generated: cx.generated || {},
  warnings: cx.warnings || [],
  lastSync: cx.last_sync ? new Date(cx.last_sync) : null
})

const midLevel = (levels) => levels[Math.floor((levels.length - 1) / 2)] || 'medium'

const toModelBody = (m) => ({
  id: m.id,
  name: m.name || m.id,
  description: m.description || '',
  capabilities: m.caps?.length ? m.caps : ['tools'],
  protocol: m.protocol || '',
  context_window: m.contextWindow ?? null,
  max_output_tokens: m.maxOutputTokens ?? null,
  reasoning: m.levels?.length ? { default_level: m.defaultLevel || midLevel(m.levels), levels: m.levels } : null,
  enabled: m.enabled !== false
})

export function AppStoreProvider({ children }) {
  const { message } = App.useApp()

  const [ready, setReady] = useState(false)
  const [proxy, setProxyState] = useState({ running: false, host: '127.0.0.1', port: 8317 })
  const [providers, setProviders] = useState([])
  const [models, setModels] = useState([])
  const [defaultModel, setDefaultModelState] = useState('')
  const [codex, setCodexState] = useState({ connected: false, autoSync: true, baseUrl: '', configPath: '', catalogPath: '', lastSync: null })
  const [settings, setSettingsState] = useState({ autoStart: false, openPanelOnStart: true, logLevel: 'info', keepLogDays: 7, configDir: '' })
  const [recent, setRecent] = useState([])
  const [repairs, setRepairs] = useState([])
  const [presets, setPresets] = useState(PRESETS)

  const refreshModels = async (providerList) => {
    const list = providerList || providers
    const results = await Promise.all(list.map((p) => Api.models.list(p.id).catch(() => [])))
    const flat = results.flat().map(mapModel)
    setModels(flat)
    return flat
  }

  // 逐项加载：单类数据出问题只提示，不影响其他功能可用
  const refresh = async () => {
    const labels = ['概览', '供应商', 'Codex 配置', '设置']
    const results = await Promise.allSettled([
      Api.overview(),
      Api.providers.list(),
      Api.codex.status(),
      Api.settings.get()
    ])
    const failures = []
    const value = (i) => {
      const r = results[i]
      if (r.status === 'fulfilled') return r.value
      failures.push(`${labels[i]}：${r.reason?.message || '加载失败'}`)
      return null
    }
    const ov = value(0)
    const providerList = value(1)
    const cx = value(2)
    const st = value(3)

    if (ov) {
      setProxyState(ov.proxy)
      setDefaultModelState(ov.default_model || '')
      setRecent(ov.recent_requests || [])
      setRepairs(ov.repairs || [])
    }
    if (cx) setCodexState(mapCodex(cx))
    if (st) setSettingsState(mapSettings(st))
    if (providerList) {
      const mapped = providerList.map(mapProvider)
      setProviders(mapped)
      await refreshModels(mapped)
    }
    if (failures.length) {
      message.warning(`部分数据加载失败：${failures.join('；')}（其余功能仍可正常使用）`, 6)
    }
    // 预置类型以后端为准，失败时沿用内置默认值
    Api.providers
      .presets()
      .then((list) => {
        if (Array.isArray(list) && list.length) {
          setPresets(
            list.map((p) => ({
              key: p.preset,
              label: p.name,
              protocol: p.protocol,
              baseUrl: p.base_url,
              hint: p.hint || ''
            }))
          )
        }
      })
      .catch(() => {})
  }

  useEffect(() => {
    refresh()
      .catch((e) => message.error(`加载数据失败：${e.message}`))
      .finally(() => setReady(true))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const actions = useMemo(
    () => ({
      async addProvider(values) {
        const preset = presets.find((p) => p.key === values.preset) || presets[presets.length - 1]
        const created = await Api.providers.create({
          id: values.id,
          name: values.name || preset.label,
          preset: preset.key,
          base_url: values.baseUrl || preset.baseUrl,
          protocol: values.protocol || preset.protocol,
          auth: { type: 'bearer' },
          api_key: values.apiKey,
          enabled: true
        })
        const mapped = mapProvider(created)
        setProviders((prev) => [...prev, mapped])
        return mapped
      },

      async updateProvider(id, patch) {
        const p = providers.find((x) => x.id === id)
        if (!p) throw new Error('供应商不存在')
        const updated = await Api.providers.update(id, {
          id: patch.id ?? p.id,
          name: patch.name ?? p.name,
          preset: p.preset,
          base_url: patch.baseUrl ?? p.baseUrl,
          protocol: patch.protocol ?? p.protocol,
          auth: { type: 'bearer' },
          api_key: patch.apiKey || '',
          enabled: patch.enabled ?? p.enabled
        })
        const mapped = mapProvider(updated)
        setProviders((prev) => prev.map((x) => (x.id === id ? mapped : x)))
        if (mapped.id !== id) {
          // 供应商 ID 变更：同步本地模型归属、请求名与默认模型，避免列表被清空
          setModels((prev) =>
            prev.map((m) => (m.providerId === id ? { ...m, providerId: mapped.id, slug: `${mapped.id}/${m.id}` } : m))
          )
          setDefaultModelState((prev) => (prev.startsWith(id + '/') ? mapped.id + prev.slice(id.length) : prev))
        }
        return mapped
      },

      async removeProvider(id) {
        await Api.providers.remove(id)
        setProviders((prev) => prev.filter((p) => p.id !== id))
        setModels((prev) => prev.filter((m) => m.providerId !== id))
        message.success('供应商已删除')
      },

      async testProvider(id) {
        setProviders((prev) => prev.map((p) => (p.id === id ? { ...p, testing: true } : p)))
        try {
          const res = await Api.providers.test(id)
          if (res.ok) message.success(`连接正常 · ${res.latency_ms}ms`)
          else message.error(res.message || '连接失败')
          return res
        } catch (e) {
          message.error(e.message)
          return { ok: false, message: e.message }
        } finally {
          setProviders((prev) => prev.map((p) => (p.id === id ? { ...p, testing: false } : p)))
        }
      },

      async fetchRemoteModels(providerId) {
        const res = await Api.providers.fetchModels(providerId)
        return res.models || []
      },

      async importModels(providerId, list) {
        const failed = []
        for (const remote of list) {
          try {
            await Api.models.create(providerId, toModelBody({
              id: remote.id,
              name: remote.name || remote.id,
              description: remote.description,
              contextWindow: remote.context_window ?? null,
              maxOutputTokens: remote.max_output_tokens ?? null,
              levels: remote.levels || null,
              protocol: remote.protocol || ''
            }))
          } catch {
            failed.push(remote.id) // 多为已存在，跳过而不是中断整批导入
          }
        }
        await refreshModels()
        if (failed.length) {
          message.warning(
            `已添加 ${list.length - failed.length} 个模型；${failed.length} 个已存在或失败（${failed.join('、')}）`,
            6
          )
        } else {
          message.success(`已添加 ${list.length} 个模型`)
        }
      },

      async toggleModel(id, enabled) {
        const m = models.find((x) => x.id === id)
        if (!m) return
        await Api.models.update(m.providerId, m.id, { ...toModelBody(m), enabled })
        setModels((prev) => prev.map((x) => (x.id === id ? { ...x, enabled } : x)))
      },

      async removeModel(id) {
        const m = models.find((x) => x.id === id)
        if (!m) return
        await Api.models.remove(m.providerId, m.id)
        setModels((prev) => prev.filter((x) => x.id !== id))
        message.success('模型已删除')
      },

      async testModel(id) {
        const m = models.find((x) => x.id === id)
        if (!m) return { ok: false, message: '模型不存在' }
        try {
          const res = await Api.models.test(m.providerId, m.id)
          if (res.ok) message.success(`调用成功 · ${res.latency_ms}ms`)
          else message.error(res.message || '调用失败')
          return res
        } catch (e) {
          message.error(e.message)
          return { ok: false, message: e.message }
        }
      },

      async saveModel(providerId, values, editId) {
        const body = toModelBody({
          id: values.id,
          name: values.name,
          contextWindow: values.contextWindow,
          maxOutputTokens: values.maxOutputTokens,
          levels: values.levels,
          protocol: values.protocol,
          enabled: values.enabled
        })
        if (editId) {
          await Api.models.update(providerId, editId, body)
        } else {
          await Api.models.create(providerId, body)
        }
        await refreshModels()
        message.success(editId ? '模型已更新' : '模型已添加')
      },

      async connectCodex() {
        await Api.codex.sync({})
        const cx = await Api.codex.status()
        setCodexState(mapCodex(cx))
        message.success('已接入 Codex')
      },

      async syncCodex(overrides = {}) {
        const res = await Api.codex.sync(overrides)
        const cx = await Api.codex.status()
        setCodexState(mapCodex(cx))
        if (res?.warnings?.length) message.warning(res.warnings.join('；'), 6)
        else message.success('配置已同步')
      },

      async disconnectCodex() {
        await Api.codex.disconnect()
        const cx = await Api.codex.status()
        setCodexState(mapCodex(cx))
        message.success('已断开接入')
      },

      async setDefaultModel(slug) {
        const res = await Api.settings.update({ default_model: slug })
        if (res.warning) message.warning(res.warning)
        setDefaultModelState(slug)
      },

      async setSettings(updater) {
        const next = typeof updater === 'function' ? updater(settings) : updater
        const res = await Api.settings.update({
          auto_start: next.autoStart,
          open_browser_on_start: next.openPanelOnStart,
          log: { level: next.logLevel, retention_days: next.keepLogDays }
        })
        if (res.warning) message.warning(res.warning)
        setSettingsState((prev) => ({ ...prev, ...next }))
      },

      async setProxy(updater) {
        const next = typeof updater === 'function' ? updater(proxy) : updater
        const res = await Api.settings.update({ proxy: { host: next.host, port: next.port } })
        if (res.warning) message.warning(res.warning)
        const status = await Api.proxy.status()
        setProxyState(status)
      },

      async setCodex(updater) {
        const next = typeof updater === 'function' ? updater(codex) : updater
        if (next.autoSync !== codex.autoSync) {
          await Api.codex.setAuto(next.autoSync)
        }
        setCodexState(next)
      },

      async restartProxy() {
        await Api.proxy.restart()
        const status = await Api.proxy.status()
        setProxyState(status)
        message.success('代理已重启')
      },

      async openConfigDir() {
        await Api.system.openConfigDir()
      },

      async openLogDir() {
        await Api.system.openLogDir()
      },

      async revealProviderKey(id) {
        return Api.providers.secret(id)
      }
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [providers, models, proxy, codex, settings, defaultModel, presets]
  )

  const enabledModels = models.filter((m) => m.enabled)
  const value = {
    ready,
    proxy,
    providers,
    models,
    enabledModels,
    defaultModel,
    codex,
    settings,
    recent,
    repairs,
    presets,
    refresh,
    ...actions
  }
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
