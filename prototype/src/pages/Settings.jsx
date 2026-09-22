import { App, Button, Card, Col, InputNumber, Row, Segmented, Select, Space, Switch, Tag, Typography } from 'antd'
import { FolderOpenOutlined, ReloadOutlined, RocketOutlined, UndoOutlined } from '@ant-design/icons'
import { useApp } from '../mock'

const { Text, Paragraph } = Typography

function RowItem({ title, desc, control }) {
  return (
    <Row align="middle" style={{ padding: '10px 0' }}>
      <Col flex="auto">
        <div style={{ fontWeight: 500 }}>{title}</div>
        <div style={{ fontSize: 12, color: '#8c93a4' }}>{desc}</div>
      </Col>
      <Col>{control}</Col>
    </Row>
  )
}

export default function SettingsPage() {
  const app = useApp()
  const { message } = App.useApp()
  const s = app.settings

  const update = (patch) => app.setSettings((prev) => ({ ...prev, ...patch }))

  const openDir = async (fn, label) => {
    try {
      await fn()
      message.success(`已打开${label}`)
    } catch (e) {
      message.error(e.message)
    }
  }
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card className="soft-card" variant="borderless" title="使用方式">
        <RowItem
          title="开机自动运行"
          desc="登录 Windows 后静默启动并常驻托盘，Codex 随时可用"
          control={<Switch checked={s.autoStart} onChange={(v) => update({ autoStart: v })} />}
        />
        <RowItem
          title="启动时打开控制台"
          desc="关闭后仅托盘常驻，需要时从托盘菜单打开"
          control={<Switch checked={s.openPanelOnStart} onChange={(v) => update({ openPanelOnStart: v })} />}
        />
      </Card>

      <Card className="soft-card" variant="borderless" title="本机代理">
        <RowItem
          title="服务状态"
          desc={`Codex 通过 ${app.proxy.host}:${app.proxy.port} 访问模型，仅监听本机`}
          control={
            <Space>
              <Tag color={app.proxy.running ? 'success' : 'default'} bordered={false}>
                {app.proxy.running ? '运行中' : '已停止'}
              </Tag>
              <Button size="small" icon={<ReloadOutlined />} onClick={() => app.restartProxy()}>
                重启
              </Button>
            </Space>
          }
        />
        <RowItem
          title="端口"
          desc="一般无需修改；修改后会自动更新 Codex 配置"
          control={
            <InputNumber
              min={1024}
              max={65535}
              value={app.proxy.port}
              onChange={(v) => app.setProxy((p) => ({ ...p, port: v }))}
              style={{ width: 120 }}
            />
          }
        />
        <RowItem
          title="允许局域网访问"
          desc="当前版本仅监听本机地址，其他设备无法访问"
          control={<Tag bordered={false}>暂不支持</Tag>}
        />
      </Card>

      <Card className="soft-card" variant="borderless" title="日志与排查">
        <RowItem
          title="日志级别"
          desc="默认 info；排查问题时可以切到 debug"
          control={
            <Segmented
              size="small"
              value={s.logLevel}
              onChange={(v) => update({ logLevel: v })}
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
          desc="按天滚动，超期自动清理"
          control={
            <Select
              size="small"
              style={{ width: 120 }}
              value={s.keepLogDays}
              onChange={(v) => update({ keepLogDays: v })}
              options={[3, 7, 15, 30].map((d) => ({ value: d, label: `${d} 天` }))}
            />
          }
        />
        <RowItem
          title="日志目录"
          desc="包含运行日志与请求记录（已脱敏，不写入密钥）"
          control={
            <Button size="small" icon={<FolderOpenOutlined />} onClick={() => openDir(app.openLogDir, '日志目录')}>
              打开
            </Button>
          }
        />
      </Card>

      <Card className="soft-card" variant="borderless" title="配置与数据">
        <RowItem
          title="配置目录"
          desc={<Text className="mono">{s.configDir}</Text>}
          control={
            <Space>
              <Button size="small" icon={<FolderOpenOutlined />} onClick={() => openDir(app.openConfigDir, '配置目录')}>
                打开
              </Button>
            </Space>
          }
        />
        <RowItem
          title="恢复默认设置"
          desc="只重置界面选项，不会删除供应商、模型和 Codex 配置"
          control={
            <Button
              size="small"
              danger
              icon={<UndoOutlined />}
              onClick={() =>
                app.setSettings((prev) => ({ ...prev, autoStart: true, openPanelOnStart: true, logLevel: 'info', keepLogDays: 7 }))
              }
            >
              恢复默认
            </Button>
          }
        />
        <Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}>
          <RocketOutlined /> 所有设置都会保存在本机配置文件中，随时可以手动备份或迁移。
        </Paragraph>
      </Card>
    </Space>
  )
}


