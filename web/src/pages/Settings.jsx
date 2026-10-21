import { App, Button, InputNumber, Segmented, Select, Space, Switch, Tag, Typography } from 'antd'
import { FolderOpenOutlined, ReloadOutlined, RocketOutlined, UndoOutlined } from '@ant-design/icons'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'

const { Text } = Typography

function RowItem({ title, description, control }) {
  return (
    <div className="settings-row">
      <div className="settings-row-main">
        <div className="settings-row-title">{title}</div>
        <div className="settings-row-desc">{description}</div>
      </div>
      <div className="settings-row-control">{control}</div>
    </div>
  )
}

export default function SettingsPage() {
  const app = useApp()
  const { message } = App.useApp()
  const settings = app.settings

  const update = (patch) => app.setSettings((previous) => ({ ...previous, ...patch }))

  const openDir = async (fn, label) => {
    try {
      await fn()
      message.success(`已打开${label}`)
    } catch (error) {
      message.error(error.message)
    }
  }

  return (
    <div className="stack">
      <PageHeader
        title="设置"
        description="启动、代理、日志和本机数据选项；推荐默认值通常无需修改。"
      />

      <div className="settings-grid">
        <Panel title="启动与运行" subtitle="控制开机行为和管理页打开方式">
          <RowItem
            title="开机自动运行"
            description="登录 Windows 后静默启动并常驻托盘，Agent 随时可用。"
            control={<Switch checked={settings.autoStart} onChange={(value) => update({ autoStart: value })} />}
          />
          <RowItem
            title="启动时打开控制台"
            description="关闭后仅托盘常驻，需要时从托盘菜单打开。"
            control={
              <Switch
                checked={settings.openPanelOnStart}
                onChange={(value) => update({ openPanelOnStart: value })}
              />
            }
          />
        </Panel>

        <Panel title="本机代理" subtitle="Agent 通过这里访问模型，仅监听本机">
          <RowItem
            title="服务状态"
            description={`Codex、OpenCode 等 Agent 通过 ${app.proxy.host}:${app.proxy.port} 访问模型。`}
            control={
              <Space>
                <Tag color={app.proxy.running ? 'success' : 'default'} bordered={false}>
                  {app.proxy.running ? '运行中' : '已停止'}
                </Tag>
                <Button icon={<ReloadOutlined />} onClick={() => app.restartProxy()}>
                  重启
                </Button>
              </Space>
            }
          />
          <RowItem
            title="端口"
            description="一般无需修改；修改后会自动更新已接入 Agent 的配置。"
            control={
              <InputNumber
                min={1024}
                max={65535}
                value={app.proxy.port}
                onChange={(value) => app.setProxy((proxy) => ({ ...proxy, port: value }))}
                className="settings-port"
              />
            }
          />
          <RowItem
            title="允许局域网访问"
            description="当前版本仅监听本机地址，其他设备无法访问。"
            control={<Tag bordered={false}>暂不支持</Tag>}
          />
        </Panel>

        <Panel title="日志与排查" subtitle="按天滚动，超期自动清理">
          <RowItem
            title="日志级别"
            description="默认 info；排查问题时可以切到 debug。"
            control={
              <Segmented
                value={settings.logLevel}
                onChange={(value) => update({ logLevel: value })}
                options={[
                  { label: 'info', value: 'info' },
                  { label: 'debug', value: 'debug' },
                  { label: 'warn', value: 'warn' }
                ]}
              />
            }
          />
          <RowItem
            title="日志保留"
            description="超过保留天数的日志会自动清理。"
            control={
              <Select
                className="settings-select"
                value={settings.keepLogDays}
                onChange={(value) => update({ keepLogDays: value })}
                options={[3, 7, 15, 30].map((days) => ({ value: days, label: `${days} 天` }))}
              />
            }
          />
          <RowItem
            title="日志目录"
            description="包含运行日志与请求记录；密钥不会写入日志。"
            control={
              <Button icon={<FolderOpenOutlined />} onClick={() => openDir(app.openLogDir, '日志目录')}>
                打开
              </Button>
            }
          />
          <RowItem
            title="记录请求轨迹"
            description="在「请求轨迹」页回看最近 50 条入站请求与上游原始响应；仅存内存，重启即丢。"
            control={
              <Switch
                checked={settings.traceEnabled !== false}
                onChange={(value) => update({ traceEnabled: value })}
              />
            }
          />
        </Panel>

        <Panel title="配置与数据" subtitle="所有设置保存在本机配置文件中">
          <RowItem
            title="配置目录"
            description={<Text className="mono">{settings.configDir}</Text>}
            control={
              <Button icon={<FolderOpenOutlined />} onClick={() => openDir(app.openConfigDir, '配置目录')}>
                打开
              </Button>
            }
          />
          <RowItem
            title="恢复默认设置"
            description="只重置界面选项，不会删除供应商、模型和 Agent 配置。"
            control={
              <Button
                danger
                icon={<UndoOutlined />}
                onClick={() =>
                  app.setSettings((previous) => ({
                    ...previous,
                    autoStart: true,
                    openPanelOnStart: true,
                    logLevel: 'info',
                    keepLogDays: 7
                  }))
                }
              >
                恢复默认
              </Button>
            }
          />
          <div className="settings-footnote">
            <RocketOutlined /> 设置会写入本机配置文件，可随时手动备份或迁移。
          </div>
        </Panel>
      </div>
    </div>
  )
}
