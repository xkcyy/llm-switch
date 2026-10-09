import { lazy, Suspense, useState } from 'react'
import { App as AntApp, Badge, Button, Card, Layout, Menu, Skeleton, Space, Tag, Tooltip, Typography } from 'antd'
import {
  ApiOutlined,
  BarChartOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DashboardOutlined,
  DeploymentUnitOutlined,
  FolderOpenOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
  RobotOutlined,
  SettingOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { AppStoreProvider, useApp } from './store'

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

const NAV = [
  { key: 'overview', icon: <DashboardOutlined />, label: '总览' },
  { key: 'providers', icon: <CloudServerOutlined />, label: '供应商与模型' },
  { key: 'codex', icon: <ApiOutlined />, label: '接入 Codex' },
  { key: 'opencode', icon: <CodeOutlined />, label: '接入 OpenCode' },
  { key: 'trae', icon: <RobotOutlined />, label: '接入 Trae SOLO' },
  { key: 'dsh', icon: <DeploymentUnitOutlined />, label: '接入 DeepSeek Harness' },
  { key: 'workbuddy', icon: <ThunderboltOutlined />, label: '接入 WorkBuddy' },
  { key: 'usage', icon: <BarChartOutlined />, label: '用量' },
  { key: 'trajectory', icon: <NodeIndexOutlined />, label: '请求轨迹' },
  { key: 'settings', icon: <SettingOutlined />, label: '设置' }
]

const TITLES = {
  overview: { title: '总览', sub: '一眼确认服务状态与接入进度' },
  providers: { title: '供应商与模型', sub: '选预设、粘贴 Key，模型信息自动补全' },
  codex: { title: '接入 Codex', sub: '一键写入配置，之后模型变化自动同步' },
  opencode: { title: '接入 OpenCode', sub: '写入 opencode.json，模型变化自动同步且无需重启' },
  trae: { title: '接入 Trae SOLO', sub: '点一次，让 Trae 的自定义模型与这里保持一致' },
  dsh: { title: '接入 DeepSeek Harness', sub: '写入 cordis.patch.yml，模型变化自动同步且无需重启' },
  workbuddy: { title: '接入 WorkBuddy', sub: '写入 ~/.workbuddy/models.json，模型变化自动同步且无需重启' },
  usage: { title: '用量', sub: '各供应商、模型与调用方的 token 消耗' },
  trajectory: { title: '请求轨迹', sub: '回看最近入站请求的系统提示词、工具与执行链路' },
  settings: { title: '设置', sub: '都有推荐默认值，通常无需改动' }
}

function Shell() {
  const app = useApp()
  const { message } = AntApp.useApp()
  const [page, setPage] = useState('overview')
  const [restarting, setRestarting] = useState(false)

  const pageNode = {
    overview: <Overview onNavigate={setPage} />,
    providers: <Providers />,
    codex: <CodexPage />,
    opencode: <OpenCodePage />,
    trae: <TraePage />,
    dsh: <DshPage />,
    workbuddy: <WorkBuddyPage />,
    usage: <Usage />,
    trajectory: <Trajectory />,
    settings: <SettingsPage />
  }[page]

  const restartProxy = async () => {
    setRestarting(true)
    try {
      await app.restartProxy()
    } catch (e) {
      message.error(e.message)
    } finally {
      setRestarting(false)
    }
  }

  return (
    <Layout className="app-shell">
      <Sider width={216} className="app-sider" theme="light">
        <div className="brand">
          <div className="brand-mark">LS</div>
          <div>
            <div className="brand-name">LLM Switch</div>
            <div className="brand-sub">本机模型代理</div>
          </div>
        </div>
        <Menu
          mode="inline"
          selectedKeys={[page]}
          items={NAV}
          onClick={(e) => setPage(e.key)}
          style={{ borderInlineEnd: 'none', padding: '8px 8px' }}
        />
        <div className="sider-foot">
          <Button
            type="text"
            block
            icon={<FolderOpenOutlined />}
            onClick={async () => {
              try {
                await app.openConfigDir()
                message.success('已打开配置目录')
              } catch (e) {
                message.error(e.message)
              }
            }}
          >
            配置目录
          </Button>
          <div className="sider-version">v{app.settings.version || 'dev'} · 本地运行</div>
        </div>
      </Sider>

      <Layout>
        <Header className="app-header">
          <div className="page-heading">
            <span className="page-title">{TITLES[page].title}</span>
            <span className="page-sub">{TITLES[page].sub}</span>
          </div>
          <Space size={8}>
            <Tooltip title={`代理监听 ${app.proxy.host}:${app.proxy.port}，Codex 请求都从这里转发`}>
              <Tag className="status-tag" color={app.proxy.running ? 'success' : 'default'}>
                <Badge status={app.proxy.running ? 'processing' : 'default'} />
                {app.proxy.running ? `代理运行中 · ${app.proxy.port}` : '代理已停止'}
              </Tag>
            </Tooltip>
            <Tooltip title="重启代理">
              <Button type="text" icon={<ReloadOutlined />} loading={restarting} onClick={restartProxy} />
            </Tooltip>
          </Space>
        </Header>
        <Content className="app-content">
          {app.ready ? (
            <Suspense
              fallback={
                <Card className="soft-card" variant="borderless">
                  <Skeleton active paragraph={{ rows: 6 }} />
                </Card>
              }
            >
              {pageNode}
            </Suspense>
          ) : (
            <Card className="soft-card" variant="borderless">
              <Skeleton active paragraph={{ rows: 6 }} />
            </Card>
          )}
        </Content>
      </Layout>
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
