import { useState } from 'react'
import { App as AntApp, Badge, Button, Card, Layout, Menu, Skeleton, Space, Tag, Tooltip, Typography } from 'antd'
import {
  ApiOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
  SettingOutlined
} from '@ant-design/icons'
import { AppStoreProvider, useApp } from './mock'
import Overview from './pages/Overview'
import Providers from './pages/Providers'
import CodexPage from './pages/Codex'
import SettingsPage from './pages/Settings'

const { Sider, Header, Content } = Layout

const NAV = [
  { key: 'overview', icon: <DashboardOutlined />, label: '总览' },
  { key: 'providers', icon: <CloudServerOutlined />, label: '供应商与模型' },
  { key: 'codex', icon: <ApiOutlined />, label: '接入 Codex' },
  { key: 'settings', icon: <SettingOutlined />, label: '设置' }
]

const TITLES = {
  overview: { title: '总览', sub: '一眼确认服务状态与接入进度' },
  providers: { title: '供应商与模型', sub: '选预设、粘贴 Key，模型信息自动补全' },
  codex: { title: '接入 Codex', sub: '一键写入配置，之后模型变化自动同步' },
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
          <div className="sider-version">v0.2.0 · 本地运行</div>
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
            pageNode
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

