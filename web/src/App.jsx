import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { App as AntApp, Button, Drawer, Layout, Menu, Tooltip } from 'antd'
import {
  ApiOutlined,
  BarChartOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  FolderOpenOutlined,
  MenuOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
  SettingOutlined
} from '@ant-design/icons'
import { AppStoreProvider, useApp } from './store'
import { LoadingState } from './components/States'

const Overview = lazy(() => import('./pages/Overview'))
const Providers = lazy(() => import('./pages/Providers'))
const CodexPage = lazy(() => import('./pages/Codex'))
const OpenCodePage = lazy(() => import('./pages/OpenCode'))
const TraePage = lazy(() => import('./pages/Trae'))
const DshPage = lazy(() => import('./pages/Dsh'))
const WorkBuddyPage = lazy(() => import('./pages/WorkBuddy'))
const SettingsPage = lazy(() => import('./pages/Settings'))
const Usage = lazy(() => import('./pages/Usage'))
const Trajectory = lazy(() => import('./pages/Trajectory'))

const { Sider, Header, Content } = Layout

const NAV_GROUPS = [
  {
    label: '概览',
    items: [{ key: 'overview', icon: <DashboardOutlined />, label: '总览' }]
  },
  {
    label: '配置',
    items: [
      { key: 'providers', icon: <CloudServerOutlined />, label: '供应商与模型' },
      { key: 'settings', icon: <SettingOutlined />, label: '设置' }
    ]
  },
  {
    label: '接入',
    items: [{ key: 'connect', icon: <ApiOutlined />, label: '接入' }]
  },
  {
    label: '观测',
    items: [
      { key: 'usage', icon: <BarChartOutlined />, label: '用量' },
      { key: 'trajectory', icon: <NodeIndexOutlined />, label: '请求轨迹' }
    ]
  }
]

const AGENTS = {
  codex: { label: 'Codex', page: CodexPage },
  opencode: { label: 'OpenCode', page: OpenCodePage },
  trae: { label: 'Trae SOLO', page: TraePage },
  dsh: { label: 'DeepSeek Harness', page: DshPage },
  workbuddy: { label: 'WorkBuddy', page: WorkBuddyPage }
}

function parseRoute() {
  const raw = window.location.hash.replace(/^#\/?/, '').replace(/\/$/, '')
  if (!raw) return { page: 'overview', agent: 'codex' }
  const [page, agent] = raw.split('/')
  if (page === 'connect') return { page: 'connect', agent: AGENTS[agent] ? agent : 'codex' }
  return { page, agent: 'codex' }
}

function routeTitle(route) {
  if (route.page === 'connect') return `接入 / ${AGENTS[route.agent]?.label || 'Agent'}`
  const all = NAV_GROUPS.flatMap((group) => group.items)
  return all.find((item) => item.key === route.page)?.label || '总览'
}

function navKey(route) {
  return route.page === 'connect' ? 'connect' : route.page
}

function RailNav({ route, navigate, collapsed = false }) {
  return (
    <div className="rail-nav">
      {NAV_GROUPS.map((group) => (
        <div className="nav-group" key={group.label}>
          {!collapsed ? <div className="nav-group-label">{group.label}</div> : null}
          <Menu
            mode="inline"
            theme="dark"
            inlineCollapsed={collapsed}
            selectedKeys={[navKey(route)]}
            items={group.items}
            onClick={({ key }) => navigate(key)}
          />
        </div>
      ))}
    </div>
  )
}

function Shell() {
  const app = useApp()
  const { message } = AntApp.useApp()
  const [route, setRoute] = useState(parseRoute)
  const [restarting, setRestarting] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [mobile, setMobile] = useState(() => window.innerWidth < 900)
  const [navOpen, setNavOpen] = useState(false)

  useEffect(() => {
    const onHash = () => setRoute(parseRoute())
    const onResize = () => {
      const width = window.innerWidth
      setMobile(width < 900)
      setCollapsed(width < 1120)
    }
    if (!window.location.hash) window.location.hash = '#/overview'
    window.addEventListener('hashchange', onHash)
    window.addEventListener('resize', onResize)
    onResize()
    return () => {
      window.removeEventListener('hashchange', onHash)
      window.removeEventListener('resize', onResize)
    }
  }, [])

  const navigate = (key) => {
    setNavOpen(false)
    if (key === 'connect') {
      const [page, agent] = window.location.hash.replace(/^#\/?/, '').split('/')
      window.location.hash = page === 'connect' && AGENTS[agent] ? window.location.hash : '#/connect/codex'
      return
    }
    window.location.hash = `#/${key}`
  }

  const Page = useMemo(() => {
    if (route.page === 'connect') return AGENTS[route.agent]?.page || CodexPage
    return (
      {
        overview: Overview,
        providers: Providers,
        usage: Usage,
        trajectory: Trajectory,
        settings: SettingsPage
      }[route.page] || Overview
    )
  }, [route.page, route.agent])

  const restartProxy = async () => {
    setRestarting(true)
    try {
      await app.restartProxy()
    } catch (error) {
      message.error(error.message)
    } finally {
      setRestarting(false)
    }
  }

  return (
    <Layout className="app-shell">
      {!mobile ? (
        <Sider
          width={248}
          collapsedWidth={72}
          collapsed={collapsed}
          className="app-sider"
          theme="dark"
        >
          <div className="brand">
            <div className="brand-mark">LS</div>
            <div className="brand-copy">
              <div className="brand-name">LLM Switch</div>
              <div className="brand-sub">本机模型代理</div>
            </div>
          </div>
          <RailNav route={route} navigate={navigate} collapsed={collapsed} />
          <div className="sider-foot">
            <button
              className="sider-action"
              onClick={async () => {
                try {
                  await app.openConfigDir()
                  message.success('已打开配置目录')
                } catch (error) {
                  message.error(error.message)
                }
              }}
            >
              <FolderOpenOutlined />
              <span className="sider-action-label">配置目录</span>
            </button>
            <button className="sider-action" onClick={() => setCollapsed((value) => !value)}>
              <ApiOutlined />
              <span className="sider-action-label">{collapsed ? '展开导航' : '收起导航'}</span>
            </button>
            <div className="sider-version">v{app.settings.version || 'dev'} · 本地运行</div>
          </div>
        </Sider>
      ) : null}

      <Layout className="app-main">
        <Header className="app-header">
          <div className="topbar-left">
            {mobile ? (
              <Button
                className="topbar-menu"
                type="text"
                icon={<MenuOutlined />}
                onClick={() => setNavOpen(true)}
                aria-label="打开导航"
              />
            ) : null}
            <span className="breadcrumb-text">LLM Switch / {routeTitle(route)}</span>
          </div>
          <div className="topbar-right">
            <Tooltip title={`代理监听 ${app.proxy.host}:${app.proxy.port}`}>
              <span className="proxy-status">
                <span className={`status-dot ${app.proxy.running ? 'is-running' : 'is-stopped'}`} />
                <span className="proxy-status-text">
                  {app.proxy.running ? `代理运行中 · ${app.proxy.port}` : '代理已停止'}
                </span>
              </span>
            </Tooltip>
            <Tooltip title="重启代理">
              <Button
                type="text"
                icon={<ReloadOutlined />}
                loading={restarting}
                onClick={restartProxy}
                aria-label="重启代理"
              />
            </Tooltip>
          </div>
        </Header>

        <Content className="app-content">
          <div className="page-wrap">
            {app.ready ? (
              <Suspense fallback={<LoadingState rows={8} />}>
                <Page route={route} navigate={navigate} />
              </Suspense>
            ) : (
              <LoadingState rows={8} />
            )}
          </div>
        </Content>
      </Layout>

      <Drawer
        className="mobile-nav"
        placement="left"
        width={280}
        open={navOpen}
        onClose={() => setNavOpen(false)}
        closable={false}
        styles={{ body: { padding: 8 } }}
      >
        <div className="brand">
          <div className="brand-mark">LS</div>
          <div className="brand-copy">
            <div className="brand-name">LLM Switch</div>
            <div className="brand-sub">本机模型代理</div>
          </div>
        </div>
        <RailNav route={route} navigate={navigate} />
      </Drawer>
    </Layout>
  )
}

export default function App() {
  return (
    <AppStoreProvider>
      <Shell />
    </AppStoreProvider>
  )
}
