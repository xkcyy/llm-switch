import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Divider,
  Dropdown,
  Empty,
  Form,
  Input,
  InputNumber,
  List,
  Modal,
  Popconfirm,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import {
  ApiOutlined,
  CheckCircleTwoTone,
  CloudDownloadOutlined,
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  EllipsisOutlined,
  EyeOutlined,
  InfoCircleOutlined,
  KeyOutlined,
  PlusOutlined,
  SyncOutlined,
  ThunderboltOutlined,
  WarningOutlined
} from '@ant-design/icons'
import { PRESETS, useApp } from '../store'

const { Text, Paragraph } = Typography

const PROTOCOL_LABEL = {
  chat: 'OpenAI Chat',
  responses: 'OpenAI Responses',
  messages: 'Anthropic Messages'
}

const PROTOCOL_OPTIONS = [
  { value: 'responses', label: 'OpenAI Responses' },
  { value: 'chat', label: 'OpenAI Chat' },
  { value: 'messages', label: 'Anthropic Messages' }
]

// protocolsOf 返回供应商声明的协议列表（兼容旧单值字段）
const protocolsOf = (p) => p?.protocols?.length ? p.protocols : p?.protocol ? [p.protocol] : []
const protocolLabels = (p) => protocolsOf(p).map((x) => PROTOCOL_LABEL[x] || x).join(' / ')

// 常见窗口档位（单位 K），一键选择，避免手敲数字
const CONTEXT_PRESETS = [128, 256, 512, 1024]
const OUTPUT_PRESETS = [8, 16, 32, 64, 128]

const formatK = (tokens) => {
  if (!tokens) return '—'
  return tokens >= 1024 * 1024 ? `${Math.round(tokens / 1024 / 1024)}M` : `${Math.round(tokens / 1024)}K`
}

// 从显示名推导一个可用的供应商 ID（请求名前缀）：优先英文，全中文时保留中文
const deriveId = (name) => {
  const base = (name || '')
    .toLowerCase()
    .trim()
    .replace(/[\s_.]+/g, '-')
  const ascii = base.replace(/[^a-z0-9-]/g, '').replace(/^-+|-+$/g, '')
  if (ascii) return ascii
  return base.replace(/[^a-z0-9\-\u4e00-\u9fa5]/g, '').replace(/^-+|-+$/g, '')
}

function PresetTags({ value, presets, onPick }) {
  return (
    <Space size={6} wrap style={{ marginTop: 6 }}>
      {presets.map((k) => {
        const tokens = k * 1024
        return (
          <Tag.CheckableTag key={k} checked={value === tokens} onChange={() => onPick(tokens)}>
            {k >= 1024 ? `${k / 1024}M` : `${k}K`}
          </Tag.CheckableTag>
        )
      })}
    </Space>
  )
}

export default function Providers() {
  const app = useApp()
  const { message } = App.useApp()
  const [selectedId, setSelectedId] = useState(app.providers[0]?.id)
  const [addOpen, setAddOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [importState, setImportState] = useState(null)
  const [importing, setImporting] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [syncToken, setSyncToken] = useState(0)
  const [search, setSearch] = useState('')
  const [form] = Form.useForm()
  const autoIdRef = useRef('')
  const [narrow, setNarrow] = useState(window.innerWidth < 1360)

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 1360)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  const [editProvider, setEditProvider] = useState(null)
  const [editForm] = Form.useForm()
  const [keyField, setKeyField] = useState({ value: '', masked: '', dirty: false, visible: false })

  const openEdit = (p) => {
    setEditProvider(p)
    editForm.setFieldsValue({ id: p.id, name: p.name, baseUrl: p.baseUrl, protocols: p.protocols || [p.protocol] })
    setKeyField({ value: p.keyMasked || '', masked: p.keyMasked || '', dirty: false, visible: false })
  }

  const toggleKeyVisible = async (visible) => {
    if (visible && !keyField.dirty) {
      try {
        const res = await app.revealProviderKey(editProvider.id)
        setKeyField((s) => ({ ...s, value: res.api_key || s.masked, visible: true }))
      } catch (e) {
        message.error(e.message)
      }
      return
    }
    if (!visible && !keyField.dirty) {
      setKeyField((s) => ({ ...s, value: s.masked, visible: false }))
      return
    }
    setKeyField((s) => ({ ...s, visible }))
  }
  const [modelModal, setModelModal] = useState(null)
  const [modelForm] = Form.useForm()
  const contextValue = Form.useWatch('contextWindow', modelForm)
  const outputValue = Form.useWatch('maxOutputTokens', modelForm)
  const modelIdValue = Form.useWatch('id', modelForm)

  const selected = app.providers.find((p) => p.id === selectedId) || null
  const presets = app.presets?.length ? app.presets : PRESETS
  const providerModels = useMemo(
    () => app.models.filter((m) => m.providerId === selected?.id && (!search || m.slug.toLowerCase().includes(search.toLowerCase()))),
    [app.models, selected, search]
  )

  const openAdd = () => {
    const fallback = presets.find((p) => p.key === 'deepseek') || presets[0]
    form.setFieldsValue({
      preset: fallback.key,
      name: fallback.label,
      id: deriveId(fallback.label),
      baseUrl: fallback.baseUrl,
      protocols: fallback.protocols || [fallback.protocol],
      apiKey: ''
    })
    autoIdRef.current = deriveId(fallback.label)
    setAddOpen(true)
  }

  const onPresetChange = (key) => {
    const preset = presets.find((p) => p.key === key)
    const name = preset.label === '自定义' ? '' : preset.label
    const id = deriveId(name)
    form.setFieldsValue({ name, baseUrl: preset.baseUrl, protocols: preset.protocols || [preset.protocol], id })
    autoIdRef.current = id
  }

  const onAddNameChange = (e) => {
    const derived = deriveId(e.target.value)
    if ((form.getFieldValue('id') || '') === autoIdRef.current) {
      form.setFieldValue('id', derived)
    }
    autoIdRef.current = derived
  }

  const saveAndFetch = async () => {
    const values = await form.validateFields()
    setSaving(true)
    try {
      const provider = await app.addProvider(values)
      setAddOpen(false)
      setSelectedId(provider.id)
      const test = await app.testProvider(provider.id)
      if (!test.ok) {
        message.warning('供应商已保存。连接测试未通过，请检查 API Key 或接口地址后重试。')
        return
      }
      message.loading({ content: '正在获取模型列表…', key: 'fetch', duration: 0 })
      const list = await app.fetchRemoteModels(provider.id)
      message.destroy('fetch')
      if (!list.length) {
        message.info('连接正常，但上游没有返回模型，可以稍后重试或手动添加')
        return
      }
      setImportState({ provider, list, keys: list.map((m) => m.id) })
    } catch (e) {
      message.destroy('fetch')
      message.error(e?.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const doImport = async () => {
    setImporting(true)
    try {
      const chosen = importState.list.filter((m) => importState.keys.includes(m.id))
      await app.importModels(importState.provider.id, chosen)
      setImportState(null)
    } catch (e) {
      message.error(e.message || '导入失败')
    } finally {
      setImporting(false)
    }
  }

  const syncUpstream = async () => {
    if (!selected || syncing) return
    setSyncing(true)
    message.loading({ content: '正在从上游获取…', key: 'sync', duration: 0 })
    try {
      const list = await app.fetchRemoteModels(selected.id)
      if (!list.length) {
        message.info('上游没有返回模型')
        return
      }
      setSyncToken((t) => t + 1) // 每次同步用新的弹窗实例，避免关闭动画期间重开导致遮罩卡住
      setImportState({ provider: selected, list, keys: list.map((m) => m.id) })
    } catch (e) {
      message.error(e.message)
    } finally {
      message.destroy('sync') // 无论成功失败都销毁 loading，避免提示常驻
      setSyncing(false)
    }
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert
        type="info"
        showIcon
        icon={<ThunderboltOutlined />}
        message="添加供应商只需要两步：选择预设、粘贴 API Key"
        description="接口地址、协议、模型信息都会自动填好；模型保留服务商原始 ID，对外请求名自动拼接为「供应商 ID/模型 ID」。"
        action={
          <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openAdd}>
            添加供应商
          </Button>
        }
      />

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: narrow ? '1fr' : '300px minmax(0, 1fr)',
          gap: 16,
          alignItems: 'start'
        }}
      >
        <Card
          className="soft-card"
          variant="borderless"
          title="供应商"
          styles={{ body: { padding: 8, maxHeight: 'calc(100vh - 300px)', overflowY: 'auto' } }}
          extra={
            <Button size="small" type="text" icon={<PlusOutlined />} onClick={openAdd}>
              添加
            </Button>
          }
        >
          {app.providers.length === 0 ? (
            <Empty description="还没有供应商" image={Empty.PRESENTED_IMAGE_SIMPLE}>
              <Button type="primary" onClick={openAdd}>
                添加第一个供应商
              </Button>
            </Empty>
          ) : (
            <List
              split={false}
              dataSource={app.providers}
              renderItem={(p) => {
                const count = app.models.filter((m) => m.providerId === p.id).length
                return (
                  <List.Item
                    className={'provider-item' + (p.id === selected?.id ? ' active' : '')}
                    onClick={() => setSelectedId(p.id)}
                    style={{ display: 'block' }}
                  >
                    <Space align="start" style={{ width: '100%', justifyContent: 'space-between' }}>
                      <Space align="start" size={10}>
                        <CheckCircleTwoTone twoToneColor="#52c41a" style={{ marginTop: 2, opacity: p.enabled ? 1 : 0.25 }} />
                        <div>
                          <div className="provider-name">
                            {p.name}
                            <Text code style={{ marginInlineStart: 6, fontSize: 11 }}>
                              {p.id}
                            </Text>
                          </div>
                          <div className="provider-meta">
                            {protocolLabels(p) || '—'} · {count} 个模型
                          </div>
                        </div>
                      </Space>
                      <Dropdown
                        trigger={['click']}
                        menu={{
                          items: [
                            { key: 'test', icon: <ApiOutlined />, label: '测试连接' },
                            { key: 'edit', icon: <EditOutlined />, label: '编辑' },
                            { type: 'divider' },
                            { key: 'del', icon: <DeleteOutlined />, label: '删除', danger: true }
                          ],
                          onClick: ({ key, domEvent }) => {
                            domEvent.stopPropagation()
                            if (key === 'test') app.testProvider(p.id)
                            if (key === 'edit') {
                              openEdit(p)
                            }
                            if (key === 'del') {
                              Modal.confirm({
                                title: `删除 ${p.name}？`,
                                content: `将同时删除该供应商下的 ${count} 个模型，Codex 配置会自动更新。`,
                                okText: '删除',
                                okButtonProps: { danger: true },
                                cancelText: '取消',
                                onOk: () => {
                                  app.removeProvider(p.id)
                                  if (selectedId === p.id) setSelectedId(null)
                                }
                              })
                            }
                          }
                        }}
                      >
                        <Button size="small" type="text" icon={<EllipsisOutlined />} onClick={(e) => e.stopPropagation()} />
                      </Dropdown>
                    </Space>
                  </List.Item>
                )
              }}
            />
          )}
        </Card>

        {selected ? (
          <Card
            className="soft-card"
            variant="borderless"
            style={{ minWidth: 0 }}
            title={
              <Space size={8}>
                {selected.name}
                <Tag bordered={false}>{protocolLabels(selected) || '—'}</Tag>
                {selected.testing && <Tag color="processing">测试中…</Tag>}
              </Space>
            }
            extra={
              <Space>
                <Button size="small" icon={<ApiOutlined />} loading={selected.testing} onClick={() => app.testProvider(selected.id)}>
                  测试连接
                </Button>
                <Button size="small" type="primary" ghost icon={<CloudDownloadOutlined />} loading={syncing} onClick={syncUpstream}>
                  从上游同步模型
                </Button>
              </Space>
            }
          >
            <Space direction="vertical" size={12} style={{ width: '100%', minWidth: 0 }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
                <Text type="secondary" className="mono provider-url" ellipsis={{ tooltip: selected.baseUrl }} style={{ minWidth: 0 }}>
                  {selected.baseUrl}
                </Text>
                <Button
                  type="link"
                  size="small"
                  icon={<EyeOutlined />}
                  style={{ flex: 'none', paddingInline: 0 }}
                  onClick={() => openEdit(selected)}
                >
                  密钥 {selected.keyMasked || '未设置'}
                </Button>
              </div>
              <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                <Space>
                  <Input.Search
                    allowClear
                    placeholder="搜索模型"
                    style={{ width: 220 }}
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {providerModels.length} 个模型
                  </Text>
                </Space>
                <Button
                  size="small"
                  icon={<PlusOutlined />}
                  onClick={() => {
                    modelForm.setFieldsValue({
                      id: '',
                      name: '',
                      contextWindow: 128000,
                      maxOutputTokens: 16384,
                      levels: ['low', 'medium', 'high'],
                      protocol: undefined,
                      vision: false,
                      enabled: true
                    })
                    setModelModal({ mode: 'add', provider: selected })
                  }}
                >
                  手动添加
                </Button>
              </Space>

              <Table
                size="small"
                rowKey="id"
                pagination={false}
                scroll={{ x: 'max-content' }}
                dataSource={providerModels}
                locale={{
                  emptyText: (
                    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有模型">
                      <Button type="primary" loading={syncing} onClick={syncUpstream}>
                        从上游同步模型
                      </Button>
                    </Empty>
                  )
                }}
                columns={[
                  {
                    title: '模型 ID（上游原始）',
                    dataIndex: 'id',
                    width: 190,
                    ellipsis: true,
                    render: (v, r) => (
                      <Tooltip title={v}>
                        <div style={{ minWidth: 0 }}>
                          <Space size={4}>
                            <Text className="mono">{v}</Text>
                            {(r.caps || []).includes('vision') && (
                              <Tooltip title="已声明图片输入：Codex 可粘贴截图">
                                <Tag bordered={false} color="blue" style={{ marginInlineEnd: 0 }}>
                                  图片
                                </Tag>
                              </Tooltip>
                            )}
                          </Space>
                          {r.name && r.name !== v && (
                            <div className="provider-meta" style={{ marginTop: 0 }}>
                              {r.name}
                            </div>
                          )}
                        </div>
                      </Tooltip>
                    )
                  },
                  {
                    title: '对外请求名（自动生成）',
                    dataIndex: 'slug',
                    width: 210,
                    ellipsis: true,
                    render: (v) => (
                      <Tooltip title="由「供应商 ID/模型 ID」自动拼接，Codex 请求时使用">
                        <Space size={4}>
                          <Text className="mono">{v}</Text>
                          <Button
                            type="text"
                            size="small"
                            icon={<CopyOutlined />}
                            onClick={() => {
                              navigator.clipboard?.writeText(v)
                              message.success('已复制 ' + v)
                            }}
                          />
                        </Space>
                      </Tooltip>
                    )
                  },
                  {
                    title: '输入窗口',
                    dataIndex: 'contextWindow',
                    width: 74,
                    render: (v) => (v ? <Text type="secondary">{formatK(v)}</Text> : '—')
                  },
                  {
                    title: '输出窗口',
                    dataIndex: 'maxOutputTokens',
                    width: 74,
                    render: (v) => (v ? <Text type="secondary">{formatK(v)}</Text> : '—')
                  },
                  {
                    title: '推理档位',
                    dataIndex: 'levels',
                    ellipsis: true,
                    render: (v) =>
                      v?.length ? (
                        <Tooltip title={v.join(' · ')}>
                          <Text type="secondary" ellipsis style={{ maxWidth: '100%' }}>
                            {v.join(' · ')}
                          </Text>
                        </Tooltip>
                      ) : (
                        <Text type="secondary">—</Text>
                      )
                  },
                  {
                    title: '启用',
                    dataIndex: 'enabled',
                    width: 56,
                    render: (v, r) => <Switch size="small" checked={v} onChange={(checked) => app.toggleModel(r.id, checked)} />
                  },
                  {
                    title: '操作',
                    width: 88,
                    render: (_, r) => (
                      <Space size={0}>
                        <Tooltip title="发送一次最小请求验证可用">
                          <Button size="small" type="link" icon={<ThunderboltOutlined />} onClick={() => app.testModel(r.id)} />
                        </Tooltip>
                        <Tooltip title="编辑">
                          <Button
                            size="small"
                            type="text"
                            icon={<EditOutlined />}
                            onClick={() => {
                              modelForm.setFieldsValue({
                                id: r.id,
                                name: r.name,
                                contextWindow: r.contextWindow,
                                maxOutputTokens: r.maxOutputTokens,
                                levels: r.levels,
                                protocol: r.protocol || undefined,
                                vision: (r.caps || []).includes('vision'),
                                enabled: r.enabled
                              })
                              setModelModal({ mode: 'edit', provider: selected, record: r })
                            }}
                          />
                        </Tooltip>
                        <Popconfirm
                          title="删除这个模型？"
                          description="Codex 的模型清单会自动更新。"
                          okText="删除"
                          cancelText="取消"
                          okButtonProps={{ danger: true }}
                          onConfirm={() => app.removeModel(r.id)}
                        >
                          <Button size="small" type="text" danger icon={<DeleteOutlined />} />
                        </Popconfirm>
                      </Space>
                    )
                  }
                ]}
              />
            </Space>
          </Card>
        ) : (
          <Card className="soft-card" variant="borderless">
            <Empty description="选择左侧供应商查看模型" />
          </Card>
        )}
      </div>

      <Modal
        title="添加供应商"
        open={addOpen}
        onCancel={() => setAddOpen(false)}
        onOk={saveAndFetch}
        okText="保存并测试"
        cancelText="取消"
        confirmLoading={saving}
        width={640}
        destroyOnClose
        styles={{ body: { maxHeight: '62vh', overflowY: 'auto' } }}
      >
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ preset: 'deepseek' }}>
          <Form.Item name="preset" label="选择服务商" rules={[{ required: true }]}>
            <Radio.Group onChange={(e) => onPresetChange(e.target.value)} optionType="button" buttonStyle="solid">
              {presets.map((p) => (
                <Radio.Button key={p.key} value={p.key}>
                  {p.label}
                </Radio.Button>
              ))}
            </Radio.Group>
          </Form.Item>
          <Form.Item name="name" label="显示名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如 DeepSeek" onChange={onAddNameChange} />
          </Form.Item>
          <Form.Item
            name="id"
            label="供应商 ID"
            rules={[
              { required: true, message: '请填写供应商 ID' },
              { pattern: /^[^/\s]+$/, message: '不能包含空格或斜杠' }
            ]}
            extra="请求名前缀：供应商 ID/模型 ID，建议使用英文小写，创建后可修改"
          >
            <Input className="mono" placeholder="例如 deepseek" onChange={(e) => form.setFieldValue('id', e.target.value)} />
          </Form.Item>
          <Form.Item
            name="apiKey"
            label="API Key"
            rules={[{ required: true, message: '粘贴服务商提供的 API Key' }]}
            extra="只保存在本机配置目录，界面默认脱敏显示"
          >
            <Input.Password prefix={<KeyOutlined />} placeholder="粘贴 API Key" autoComplete="off" />
          </Form.Item>
          <Form.Item name="baseUrl" label="接口地址" extra="已按服务商自动填好，一般无需修改">
            <Input placeholder="https://…/v1" />
          </Form.Item>
          <Form.Item
            name="protocols"
            label="接口协议"
            rules={[{ required: true, message: '请至少选择一个协议' }]}
            extra="可多选，按顺序排列：入口协议命中时直接直通（不转换）；未命中时按顺序选择第一个可转换的协议。多端点网关建议把常用协议放前面"
          >
            <Select mode="multiple" options={PROTOCOL_OPTIONS} placeholder="选择上游支持的协议" />
          </Form.Item>
          <Alert
            type="info"
            showIcon
            icon={<InfoCircleOutlined />}
            message="保存后会自动测试连接，并拉取模型列表让你勾选"
          />
        </Form>
      </Modal>

      <Modal
        key={syncToken}
        title={importState ? `已获取 ${importState.list.length} 个模型` : ''}
        open={!!importState}
        onCancel={() => setImportState(null)}
        afterClose={() => setImportState(null)}
        destroyOnClose
        onOk={doImport}
        okText={`添加选中的 ${importState?.keys.length || 0} 个模型`}
        cancelText="稍后再说"
        confirmLoading={importing}
        width={820}
        styles={{ body: { maxHeight: '58vh', overflowY: 'auto' } }}
      >
        {importState && (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            {(() => {
              const enriched = importState.list.filter((m) => m.context_window || m.levels?.length).length
              const vision = importState.list.filter((m) => (m.capabilities || []).includes('vision')).length
              return (
                <Alert
                  type={enriched ? 'success' : 'info'}
                  showIcon
                  message={enriched ? `已自动补全 ${enriched} 个模型的信息` : '这些模型暂时没有可补全的元数据'}
                  description={
                    enriched
                      ? `上下文窗口、推理档位与图片输入来自上游或公开模型元数据${vision ? `（其中 ${vision} 个识别为支持图片）` : ''}，导入后仍可随时修改。`
                      : '导入后可以手动补充上下文窗口和推理档位，或点击「从上游同步模型」重试。'
                  }
                />
              )
            })()}
            <Table
              size="small"
              rowKey="id"
              pagination={false}
              scroll={{ x: 'max-content' }}
              dataSource={importState.list}
              rowSelection={{
                selectedRowKeys: importState.keys,
                onChange: (keys) => setImportState((s) => ({ ...s, keys })),
                selections: [Table.SELECTION_ALL, Table.SELECTION_INVERT]
              }}
              columns={[
                { title: '模型 ID（上游原始）', dataIndex: 'id', width: 170, ellipsis: true, render: (v) => <Text className="mono">{v}</Text> },
                {
                  title: '对外请求名（自动生成）',
                  dataIndex: 'id',
                  width: 190,
                  ellipsis: true,
                  render: (v) => (
                    <Text className="mono" type="secondary">
                      {importState.provider.id}/{v}
                    </Text>
                  )
                },
                {
                  title: '接口协议',
                  dataIndex: 'protocol',
                  width: 100,
                  render: (v) =>
                    v ? (
                      <Tag bordered={false} color="purple">
                        {v}
                      </Tag>
                    ) : (
                      <Text type="secondary">跟随供应商</Text>
                    )
                },
                {
                  title: '输入窗口',
                  dataIndex: 'context_window',
                  width: 76,
                  render: (v) =>
                    v ? (
                      <Tag bordered={false} color="blue">
                        {formatK(v)} · 自动
                      </Tag>
                    ) : (
                      <Text type="secondary">—</Text>
                    )
                },
                {
                  title: '输出窗口',
                  dataIndex: 'max_output_tokens',
                  width: 76,
                  render: (v) =>
                    v ? (
                      <Tag bordered={false} color="blue">
                        {formatK(v)} · 自动
                      </Tag>
                    ) : (
                      <Text type="secondary">—</Text>
                    )
                },
                {
                  title: '图片输入',
                  dataIndex: 'capabilities',
                  width: 82,
                  render: (v) =>
                    (v || []).includes('vision') ? (
                      <Tag bordered={false} color="blue">
                        支持
                      </Tag>
                    ) : (
                      <Text type="secondary">—</Text>
                    )
                },
                {
                  title: '推理档位',
                  dataIndex: 'levels',
                  ellipsis: true,
                  render: (v) =>
                    v?.length ? (
                      <Tooltip title={v.join(' · ')}>
                        <Text type="secondary" ellipsis style={{ maxWidth: '100%' }}>
                          {v.join(' · ')}
                        </Text>
                      </Tooltip>
                    ) : (
                      <Text type="secondary">—</Text>
                    )
                }
              ]}
            />
          </Space>
        )}
      </Modal>

      <Modal
        title={`编辑供应商 · ${editProvider?.name || ''}`}
        open={!!editProvider}
        onCancel={() => setEditProvider(null)}
        onOk={async () => {
          const values = await editForm.validateFields()
          const updated = await app.updateProvider(editProvider.id, {
            id: values.id,
            name: values.name,
            baseUrl: values.baseUrl,
            protocols: values.protocols,
            apiKey: keyField.dirty ? keyField.value : ''
          })
          if (updated?.id && updated.id !== editProvider.id) {
            setSelectedId(updated.id) // 跟随重命名，避免右侧模型列表看起来被清空
          }
          message.success('已保存，Codex 配置会自动更新')
          setEditProvider(null)
        }}
        okText="保存"
        cancelText="取消"
        width={560}
        destroyOnClose
        styles={{ body: { maxHeight: '62vh', overflowY: 'auto' } }}
      >
        <Form form={editForm} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label="显示名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="id"
            label="供应商 ID"
            rules={[
              { required: true, message: '请填写供应商 ID' },
              { pattern: /^[^/\s]+$/, message: '不能包含空格或斜杠' }
            ]}
            extra="修改后请求名与 Codex 配置会自动更新"
          >
            <Input className="mono" />
          </Form.Item>
          <Form.Item
            label="API Key"
            extra="默认脱敏；点右侧眼睛查看完整密钥，直接输入表示替换，保持不变则不改动"
          >
            <Input.Password
              prefix={<KeyOutlined />}
              value={keyField.value}
              onChange={(e) => setKeyField((s) => ({ ...s, value: e.target.value, dirty: true }))}
              visibilityToggle={{ visible: keyField.visible, onVisibleChange: toggleKeyVisible }}
              placeholder="尚未设置"
              autoComplete="off"
            />
          </Form.Item>
          <Form.Item name="baseUrl" label="接口地址">
            <Input />
          </Form.Item>
          <Form.Item
            name="protocols"
            label="接口协议"
            rules={[{ required: true, message: '请至少选择一个协议' }]}
            extra="按顺序排列：入口协议命中即直通，未命中才转换"
          >
            <Select mode="multiple" options={PROTOCOL_OPTIONS} placeholder="选择上游支持的协议" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={modelModal?.mode === 'edit' ? `编辑模型 · ${modelModal?.record?.id || ''}` : '添加模型'}
        open={!!modelModal}
        onCancel={() => setModelModal(null)}
        onOk={async () => {
          const values = await modelForm.validateFields()
          await app.saveModel(modelModal.provider.id, values, modelModal.mode === 'edit' ? modelModal.record.id : null)
          setModelModal(null)
        }}
        okText="保存"
        cancelText="取消"
        width={600}
        destroyOnClose
        styles={{ body: { maxHeight: '60vh', overflowY: 'auto', paddingRight: 8 } }}
      >
        <Form form={modelForm} layout="vertical" requiredMark={false}>
          <Form.Item
            name="id"
            label="模型 ID（上游原始标识）"
            rules={[{ required: true, message: '请填写模型 ID' }]}
            extra={
              modelModal?.mode === 'edit'
                ? '修改后会同时更新对外请求名'
                : '填写服务商文档里的原始模型 ID，例如 deepseek-chat；含 / 的 ID 也可以直接填写'
            }
          >
            <Input className="mono" placeholder="例如 deepseek-chat" />
          </Form.Item>
          <Form.Item label="对外请求名" extra="由「供应商 ID/模型 ID」自动拼接；Codex 的模型列表与请求都使用这个名称">
            <Input className="mono" readOnly value={`${modelModal?.provider?.id || ''}/${modelIdValue || ''}`} />
          </Form.Item>
          <Form.Item name="name" label="模型名称（原始名称）" extra="留空默认与模型 ID 一致">
            <Input placeholder="例如 DeepSeek Chat" />
          </Form.Item>
          <Form.Item label="上下文窗口（输入）" extra="决定 Codex 一次能带多少上下文，可快捷选择或手动填写">
            <Form.Item name="contextWindow" noStyle>
              <InputNumber min={1024} step={1024} style={{ width: '100%' }} placeholder="例如 128000" />
            </Form.Item>
            <PresetTags
              value={contextValue}
              presets={CONTEXT_PRESETS}
              onPick={(v) => modelForm.setFieldValue('contextWindow', v)}
            />
          </Form.Item>
          <Form.Item label="最大输出（输出窗口）" extra="单次回复的最大 token 数，可快捷选择或手动填写">
            <Form.Item name="maxOutputTokens" noStyle>
              <InputNumber min={256} step={256} style={{ width: '100%' }} placeholder="例如 16384" />
            </Form.Item>
            <PresetTags
              value={outputValue}
              presets={OUTPUT_PRESETS}
              onPick={(v) => modelForm.setFieldValue('maxOutputTokens', v)}
            />
          </Form.Item>
          <Form.Item name="levels" label="推理档位" extra="可直接输入自定义档位后回车">
            <Select
              mode="tags"
              options={['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'].map((v) => ({ value: v, label: v }))}
            />
          </Form.Item>
          <Form.Item
            name="protocol"
            label="接口协议（模型级）"
            extra="默认跟随供应商；部分网关的不同模型使用不同端点时可单独固定（留空即可）"
          >
            <Select
              allowClear
              placeholder="跟随供应商"
              options={[
                { value: 'chat', label: 'OpenAI Chat' },
                { value: 'responses', label: 'OpenAI Responses' },
                { value: 'messages', label: 'Anthropic Messages' }
              ]}
            />
          </Form.Item>
          <Form.Item
            name="vision"
            label="图片输入（视觉）"
            valuePropName="checked"
            extra="勾选后 Codex 模型清单声明支持图片，可在对话里粘贴截图；上游模型与协议确实支持时再勾选"
          >
            <Switch />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  )
}

