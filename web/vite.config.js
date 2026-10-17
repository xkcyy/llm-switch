import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const ADMIN_ORIGIN = 'http://127.0.0.1:8318'

// 开发态从正在运行的管理面读取令牌；生产构建仍保留占位符，由 Go 服务端注入。
function devAdminToken() {
  return {
    name: 'llm-switch-dev-admin-token',
    apply: 'serve',
    enforce: 'pre',
    async transformIndexHtml(html) {
      try {
        const res = await fetch(ADMIN_ORIGIN, { headers: { 'Cache-Control': 'no-store' } })
        if (!res.ok) throw new Error(`管理面返回 HTTP ${res.status}`)
        const page = await res.text()
        const match = page.match(
          /<meta\b[^>]*\bname\s*=\s*["']llm-switch-token["'][^>]*\bcontent\s*=\s*["']([^"']+)["']/i
        )
        if (!match || match[1].includes('__ADMIN')) throw new Error('未解析到管理令牌')
        return html.replace('__ADMIN_TOKEN__', match[1])
      } catch (error) {
        console.warn(`[llm-switch] 无法注入管理令牌：${error.message}。先启动 LLM-Switch.exe。`)
        return html
      }
    }
  }
}

export default defineConfig({
  plugins: [react(), devAdminToken()],
  base: './',
  server: {
    host: '127.0.0.1',
    port: 5199,
    strictPort: true,
    proxy: {
      '/api': {
        target: ADMIN_ORIGIN,
        changeOrigin: true
      }
    }
  },
  build: { outDir: 'dist', assetsDir: 'assets', emptyOutDir: true }
})
