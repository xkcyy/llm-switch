import { useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Drawer,
  Popconfirm,
  Row,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import {
  CheckCircleFilled,
  CopyOutlined,
  DisconnectOutlined,
  RocketOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text, Title, Paragraph } = Typography

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
  const [previewOpen, setPreviewOpen] = useState(false)

  const connect = async () => {
    setConnecting(true)
    try {
      await app.connectDsh()
    } catch (e) {
      message.error(e.message)
    } finally {
      setConnecting(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await app.syncDsh()
    } catch (e) {
      message.error(e.message)
    } finally {
      setSyncing(false)
    }
  }

  const profileOptions = (() => {
    const list = new Set(app.dsh.profiles || [])
    list.add('desktop')
    list.add('web')
    return [...list].sort().map((p) => ({
      value: p,
      label: p === 'desktop' ? 'desktop（桌面端）' : p === 'web' ? 'web（Web UI）' : p
    }))
  })()

  const warningsAlert = app.dsh.warnings?.length ? (
    <Alert
      type="warning"
      showIcon
      message="接入提示"
      description={
        <ul style={{ margin: 0, paddingInlineStart: 18 }}>
          {app.dsh.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      }
    />
  ) : null

  if (!app.dsh.connected) {
    return (
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {warningsAlert}
        <div className="codex-hero">
          <Space direction="vertical" size={14} style={{ width: '100%' }}>
            <div>
              <Title level={3} style={{ marginBottom: 4 }}>
                让 DeepSeek Harness 用上你的模型
              </Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>
                只改 <Text code>cordis.patch.yml</Text> 里的 <Text code>llm-pi-ai</Text> 一段，
                你已有的 provider、插件与注释一律不动。
              </Paragraph>
            </div>
            <Space size={20} wrap>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>模型清单直接写进 profile patch，模型选择器里可切换</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>dsh 在下一次请求时重读配置，无需重启</Text>
              </Space>
              <Space size={6}>
                <CheckCircleFilled style={{ color: '#52c41a' }} />
                <Text>真实密钥留在 LLM Switch，配置文件里只放占位值</Text>
              </Space>
            </Space>
            <Space>
              <Button type="primary" size="large" icon={<RocketOutlined />} loading={connecting} onClick={connect}>
                一键接入 DeepSeek Harness
              </Button>
              <Button size="large" onClick={() => setPreviewOpen(true)}>
                看看会写入什么
              </Button>
            </Space>
          </Space>
        </div>

        <Card className="soft-card" variant="borderless" title="接入前检查">
          <Descriptions
            column={1}
            size="small"
            items={[
              {
                key: 'provider',
                label: '供应商',
                children: app.providers.length ? `${app.providers.length} 个可用` : <Text type="danger">请先添加供应商</Text>
              },
              {
                key: 'model',
                label: '模型',
                children: app.enabledModels.length ? (
                  `${app.enabledModels.length} 个可用模型`
                ) : (
                  <Text type="danger">请先添加模型</Text>
                )
              },
              {
                key: 'proxy',
                label: '本机代理',
                children: app.proxy.running ? (
                  `运行中（${app.proxy.host}:${app.proxy.port}）`
                ) : (
                  <Text type="warning">已停止，接入时会自动启动</Text>
                )
              },
              {
                key: 'profile',
                label: '目标 profile',
                children: (
                  <Space size={8}>
                    <Select
                      size="small"
                      style={{ width: 220 }}
                      value={app.dsh.profileSetting}
                      onChange={(v) => app.setDshProfile(v).catch((e) => message.error(e.message))}
                      options={profileOptions}
                    />
                    {!app.dsh.profileExists && <Text type="warning">该 profile 目录尚不存在，请先启动一次 dsh</Text>}
                  </Space>
                )
              },
              {
                key: 'file',
                label: '配置文件',
                children: <Text className="mono">{app.dsh.configPath}</Text>
              }
            ]}
          />
        </Card>

        <Drawer title="将写入的配置" width={680} open={previewOpen} onClose={() => setPreviewOpen(false)}>
          <pre className="mono">{app.dsh.generated || PREVIEW}</pre>
        </Drawer>
      </Space>
    )
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {warningsAlert}
      <Card className="soft-card" variant="borderless">
        <Row align="middle" gutter={16} wrap={false}>
          <Col flex="none">
            <CheckCircleFilled style={{ fontSize: 32, color: '#52c41a' }} />
          </Col>
          <Col flex="auto" style={{ minWidth: 0 }}>
            <div style={{ fontSize: 15, fontWeight: 600, lineHeight: 1.3 }}>DeepSeek Harness 已接入</div>
            <div style={{ fontSize: 12, color: '#9aa1b1', marginTop: 2 }}>
              在 dsh 的模型选择器里选 <Text code>LLM Switch</Text> 下的模型即可，新增或删除模型会自动同步
            </div>
          </Col>
          <Col flex="none">
            <Space>
              <Button type="primary" icon={<SyncOutlined spin={syncing} />} loading={syncing} onClick={sync}>
                重新同步
              </Button>
              <Button
                icon={<ThunderboltOutlined />}
                onClick={() => {
                  const target = app.models.find((m) => m.slug === app.defaultModel) || app.enabledModels[0]
                  if (!target) return message.warning('还没有可用模型')
                  app.testModel(target.id)
                }}
              >
                测试一次调用
              </Button>
              <Popconfirm
                title="断开 DeepSeek Harness 接入？"
                description="只移除 LLM Switch 写入的 providers.llm-switch 段，不影响你其他的 dsh 设置。"
                okText="断开"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                onConfirm={() => app.disconnectDsh()}
              >
                <Button danger type="text" icon={<DisconnectOutlined />}>
                  断开接入
                </Button>
              </Popconfirm>
            </Space>
          </Col>
        </Row>
      </Card>

      <Card className="soft-card" variant="borderless" title="当前配置">
        <Descriptions
          column={2}
          size="small"
          items={[
            {
              key: 'models',
              label: '已写入模型',
              children: <Text>{app.dsh.models.length} 个</Text>
            },
            {
              key: 'base',
              label: '请求地址',
              children: <Text code>{app.dsh.baseUrl}</Text>
            },
            {
              key: 'profile',
              label: '目标 profile',
              children: (
                <Space size={8} align="center">
                  <Select
                    size="small"
                    style={{ width: 220 }}
                    value={app.dsh.profileSetting}
                    onChange={(v) => app.setDshProfile(v).catch((e) => message.error(e.message))}
                    options={profileOptions}
                  />
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    当前写入 {app.dsh.profile}
                  </Text>
                </Space>
              )
            },
            {
              key: 'last',
              label: '最近同步',
              children: app.dsh.lastSync ? app.dsh.lastSync.toLocaleTimeString('zh-CN') : '刚刚'
            },
            {
              key: 'file',
              label: '配置文件',
              children: (
                <Space size={4} align="center">
                  <Text className="mono" ellipsis={{ tooltip: app.dsh.configPath }} style={{ maxWidth: 380 }}>
                    {app.dsh.configPath}
                  </Text>
                  <Tooltip title="复制路径">
                    <Button
                      size="small"
                      type="text"
                      icon={<CopyOutlined />}
                      onClick={() => {
                        navigator.clipboard?.writeText(app.dsh.configPath)
                        message.success('已复制路径')
                      }}
                    />
                  </Tooltip>
                </Space>
              )
            }
          ]}
        />
        <Divider style={{ margin: '12px 0' }} />
        <Space align="start" size={12}>
          <Switch checked={app.dsh.autoSync} onChange={(v) => app.setDsh((c) => ({ ...c, autoSync: v }))} />
          <div>
            <Text strong>自动同步</Text>
            <div style={{ fontSize: 12, color: '#8c93a4' }}>
              推荐开启：新增或删除模型、修改代理端口时，自动更新 cordis.patch.yml。
            </div>
          </div>
        </Space>
        {app.dsh.otherProviders?.length ? (
          <>
            <Divider style={{ margin: '12px 0' }} />
            <Space size={8} wrap>
              <Text type="secondary" style={{ fontSize: 12 }}>
                llm-pi-ai 段里还有其它 provider：
              </Text>
              {app.dsh.otherProviders.map((p) => (
                <Tag key={p}>{p}</Tag>
              ))}
              <Text type="secondary" style={{ fontSize: 12 }}>
                它们同样由 dsh 模型页管理，本工具不会改动。
              </Text>
            </Space>
          </>
        ) : null}
      </Card>

      <Alert
        type="success"
        showIcon
        message="无需重启 DeepSeek Harness"
        description="dsh 会在下一次请求时重读 profile patch；如果模型选择器没有立刻刷新，重新打开一次设置页即可。"
      />
    </Space>
  )
}