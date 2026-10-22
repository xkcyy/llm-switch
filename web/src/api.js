// 与 Go 后端 /api/* 的通信层。
// 管理端点在 index.html 的 meta 标签中注入随机令牌；也支持 URL 片段兜底。

function readToken() {
  const meta = document.querySelector('meta[name="llm-switch-token"]')
  if (meta && meta.content && !meta.content.includes('__ADMIN')) return meta.content
  const fromSession = sessionStorage.getItem('llm-switch-token')
  if (fromSession) return fromSession
  const hash = location.hash.match(/token=([0-9a-f]{16,})/i)
  if (hash) {
    sessionStorage.setItem('llm-switch-token', hash[1])
    return hash[1]
  }
  return ''
}

async function request(path, { method = 'GET', body } = {}) {
  const res = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Admin-Token': readToken()
    },
    body: body === undefined ? undefined : JSON.stringify(body)
  })
  let data = null
  const text = await res.text()
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: text }
    }
  }
  if (res.status === 401 && !sessionStorage.getItem('llm-switch-token-retry')) {
    sessionStorage.setItem('llm-switch-token-retry', '1')
    window.location.reload()
    throw new Error(data?.error || '管理令牌已更新，页面正在重新加载')
  }
  if (!res.ok) {
    const msg = data?.error || `请求失败（HTTP ${res.status}）`
    throw new Error(msg)
  }
  sessionStorage.removeItem('llm-switch-token-retry')
  return data
}

async function requestText(path) {
  const res = await fetch(path, { headers: { 'X-Admin-Token': readToken() } })
  if (!res.ok) {
    const text = await res.text()
    let msg = `请求失败（HTTP ${res.status}）`
    try {
      msg = JSON.parse(text)?.error || msg
    } catch {
      if (text) msg = text
    }
    if (res.status === 401 && !sessionStorage.getItem('llm-switch-token-retry')) {
      sessionStorage.setItem('llm-switch-token-retry', '1')
      window.location.reload()
      throw new Error(msg || '管理令牌已更新，页面正在重新加载')
    }
    throw new Error(msg)
  }
  sessionStorage.removeItem('llm-switch-token-retry')
  return res.text()
}

export const Api = {
  overview: () => request('/api/overview'),
  usage: {
    summary: () => request('/api/usage'),
    report: (params = {}) => request(`/api/usage/report?${new URLSearchParams(params)}`)
  },
  metrics: {
    report: (params = {}) => request(`/api/metrics/report?${new URLSearchParams(params)}`)
  },

  trajectory: {
    list: () => request('/api/trajectory'),
    detail: (id) => request(`/api/trajectory/${id}`),
    raw: (id) => request(`/api/trajectory/${id}/raw?body=0`),
    body: (id, part) => requestText(`/api/trajectory/${id}/body/${part}`)
  },

  providers: {
    list: () => request('/api/providers'),
    presets: () => request('/api/providers/presets'),
    create: (body) => request('/api/providers', { method: 'POST', body }),
    update: (id, body) => request(`/api/providers/${id}`, { method: 'PUT', body }),
    remove: (id) => request(`/api/providers/${id}`, { method: 'DELETE' }),
    test: (id) => request(`/api/providers/${id}/test`, { method: 'POST' }),
    secret: (id) => request(`/api/providers/${id}/secret`),
    fetchModels: (id) => request(`/api/providers/${id}/fetch-models`, { method: 'POST' })
  },

  models: {
    list: (providerId) => request(`/api/providers/${providerId}/models`),
    create: (providerId, body) => request(`/api/providers/${providerId}/models`, { method: 'POST', body }),
    update: (providerId, modelId, body) => request(`/api/providers/${providerId}/models/${modelId}`, { method: 'PUT', body }),
    remove: (providerId, modelId) => request(`/api/providers/${providerId}/models/${modelId}`, { method: 'DELETE' }),
    test: (providerId, modelId) => request(`/api/providers/${providerId}/models/${modelId}/test`, { method: 'POST' })
  },

  proxy: {
    status: () => request('/api/proxy/status'),
    start: () => request('/api/proxy/start', { method: 'POST' }),
    stop: () => request('/api/proxy/stop', { method: 'POST' }),
    restart: () => request('/api/proxy/restart', { method: 'POST' })
  },

  codex: {
    status: () => request('/api/codex'),
    setAuto: (enabled) => request('/api/codex/auto', { method: 'PUT', body: { enabled } }),
    sync: (body = {}) => request('/api/codex/sync', { method: 'POST', body }),
    disconnect: () => request('/api/codex/disconnect', { method: 'POST' })
  },

  opencode: {
    status: () => request('/api/opencode'),
    setAuto: (enabled) => request('/api/opencode/auto', { method: 'PUT', body: { enabled } }),
    setShape: (shape) => request('/api/opencode/shape', { method: 'PUT', body: { shape } }),
    sync: (body = {}) => request('/api/opencode/sync', { method: 'POST', body }),
    disconnect: () => request('/api/opencode/disconnect', { method: 'POST' })
  },

  trae: {
    status: () => request('/api/trae'),
    progress: () => request('/api/trae/progress'),
    sync: () => request('/api/trae/sync', { method: 'POST' }),
    reorder: () => request('/api/trae/reorder', { method: 'POST' }),
    restart: () => request('/api/trae/restart', { method: 'POST' })
  },

  dsh: {
    status: () => request('/api/dsh'),
    setAuto: (enabled) => request('/api/dsh/auto', { method: 'PUT', body: { enabled } }),
    setProfile: (profile) => request('/api/dsh/profile', { method: 'PUT', body: { profile } }),
    sync: (body = {}) => request('/api/dsh/sync', { method: 'POST', body }),
    disconnect: () => request('/api/dsh/disconnect', { method: 'POST' })
  },

  workbuddy: {
    status: () => request('/api/workbuddy'),
    setAuto: (enabled) => request('/api/workbuddy/auto', { method: 'PUT', body: { enabled } }),
    sync: (body = {}) => request('/api/workbuddy/sync', { method: 'POST', body }),
    disconnect: () => request('/api/workbuddy/disconnect', { method: 'POST' })
  },

  agents: {
    test: (agent) => request(`/api/agents/${agent}/test`, { method: 'POST' })
  },

  settings: {
    get: () => request('/api/settings'),
    update: (body) => request('/api/settings', { method: 'PUT', body })
  },

  system: {
    openConfigDir: () => request('/api/system/open-config-dir', { method: 'POST' }),
    openLogDir: () => request('/api/system/open-log-dir', { method: 'POST' })
  }
}
