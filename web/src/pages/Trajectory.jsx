import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  App,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Modal,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import { ReloadOutlined, RightOutlined } from '@ant-design/icons'
import { Api } from '../api'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import { uiTokens } from '../theme'

const { Text } = Typography

// KIND 把轨迹行类型映射成中文标签与颜色。
const KIND = {
  system: { label: '系统', color: 'blue', border: uiTokens.blue },
  context: { label: '上下文', color: 'geekblue', border: uiTokens.violet },
  user: { label: '用户', color: 'green', border: uiTokens.green },
  assistant: { label: '助手', color: 'purple', border: uiTokens.violet },
  reasoning: { label: '思考', color: 'gold', border: uiTokens.amber },
  'tool-call': { label: '工具', color: 'orange', border: uiTokens.amber },
  'tool-result': { label: '结果', color: 'cyan', border: uiTokens.cyan },
  other: { label: '其它', color: 'default', border: uiTokens.inkMuted }
}

const kindOf = (k) => KIND[k] || { label: k || '其它', color: 'default', border: uiTokens.inkMuted }

const fmtBytes = (n) => {
  if (!n) return '0 B'
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${n} B`
}

const fmtTime = (iso) => (iso ? new Date(iso).toLocaleString('zh-CN', { hour12: false }) : '—')

// prettyJSON 尝试把工具参数格式化成缩进 JSON，失败则原样返回。
const prettyJSON = (v) => {
  if (v === undefined || v === null || v === '') return ''
  if (typeof v !== 'string') {
    try {
      return JSON.stringify(v, null, 2)
    } catch {
      return String(v)
    }
  }
  try {
    return JSON.stringify(JSON.parse(v), null, 2)
  } catch {
    return v
  }
}

// Pre 渲染长文本：max > 0 时默认截断并可展开；max <= 0 表示完整显示。
function Pre({ text, max = 600 }) {
  const [open, setOpen] = useState(false)
  if (!text) return null
  const long = max > 0 && text.length > max
  const shown = long && !open ? `${text.slice(0, max)}…` : text
  return (
    <div>
      <pre className="trace-pre">{shown}</pre>
      {long && (
        <Button size="small" type="link" className="button-link-flush" onClick={() => setOpen((v) => !v)}>
          {open ? '收起' : `展开全部（${text.length} 字）`}
        </Button>
      )}
    </div>
  )
}

// RowDetail 在右侧检查器里完整展示选中行。
function RowDetail({ row }) {
  const k = kindOf(row.kind)
  return (
    <div>
      <div className="trace-row-head">
        <Tag color={k.color} bordered={false}>
          {k.label}
        </Tag>
        <Text type="secondary" className="text-xs">
          #{row.index}
        </Text>
        {row.name && <Text strong>{row.name}</Text>}
        {row.call_id && (
          <Text type="secondary" className="mono text-xs">
            {row.call_id}
          </Text>
        )}
        {row.images > 0 && <Tag bordered={false}>图片 × {row.images}</Tag>}
        {row.label && !row.name && (
          <Text type="secondary" className="text-xs">
            {row.label}
          </Text>
        )}
      </div>
      {row.role && (
        <Text type="secondary" className="text-xs">
          role: {row.role}
        </Text>
      )}
      {row.text && <Pre text={row.text} max={0} />}
      {row.arguments && (
        <div className="trace-sub">
          <Text type="secondary" className="text-xs">
            参数
          </Text>
          <Pre text={prettyJSON(row.arguments)} max={0} />
        </div>
      )}
      {row.output && (
        <div className="trace-sub">
          <Text type="secondary" className="text-xs">
            结果
          </Text>
          <Pre text={row.output} max={0} />
        </div>
      )}
    </div>
  )
}

// previewOf 生成密集列表里的一行摘要。
const previewOf = (row) => {
  if (row.kind === 'tool-call' && row.arguments) return `${row.name || ''} ${prettyJSON(row.arguments)}`
  return row.text || row.output || ''
}

// ToolItem 默认折叠：一行显示名称 + 单行描述，点开后才是完整描述与 schema。
function ToolItem({ tool }) {
  const [open, setOpen] = useState(false)
  const desc = (tool.description || '').replace(/\s+/g, ' ').trim()
  return (
    <div className="trace-tool">
      <div className="trace-tool-head" onClick={() => setOpen((v) => !v)}>
        <RightOutlined className="trace-tool-caret" rotate={open ? 90 : 0} />
        <Text strong>{tool.name}</Text>
        {desc && <span className="trace-tool-desc">{desc}</span>}
      </div>
      {open && (
        <div className="trace-tool-body">
          {tool.description && <Pre text={tool.description} max={0} />}
          {tool.parameters && <Pre text={prettyJSON(tool.parameters)} max={0} />}
        </div>
      )}
    </div>
  )
}

export default function Trajectory() {
  const { message } = App.useApp()
  const [list, setList] = useState([])
  const [capacity, setCapacity] = useState(50)
  const [loading, setLoading] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [selected, setSelected] = useState(null)
  const [doc, setDoc] = useState(null)
  const [detailError, setDetailError] = useState('')
  const [detailLoading, setDetailLoading] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(null)
  const [rawOpen, setRawOpen] = useState(false)
  const [rawData, setRawData] = useState(null)
  const loadingRef = useRef(false)

  const load = useCallback(async (silent = false) => {
    if (loadingRef.current) return
    loadingRef.current = true
    if (!silent) setLoading(true)
    try {
      const res = await Api.trajectory.list()
      setList(res.requests || [])
      setCapacity(res.capacity || 50)
    } catch (e) {
      if (!silent) message.error(e.message)
    } finally {
      loadingRef.current = false
      if (!silent) setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  useEffect(() => {
    if (!autoRefresh) return undefined
    const timer = setInterval(() => load(true), 5000)
    return () => clearInterval(timer)
  }, [autoRefresh, load])

  const open = (row) => {
    setSelected(row)
    setDoc(null)
    setDetailError('')
    setDetailLoading(true)
    setQuery('')
    setActiveIndex(null)
    Api.trajectory
      .detail(row.id)
      .then((res) => {
        setSelected(res.entry || row)
        setDoc(res.document || null)
        setDetailError(res.error || '')
        setActiveIndex(res.document?.rows?.[0]?.index ?? null)
      })
      .catch((e) => setDetailError(e.message))
      .finally(() => setDetailLoading(false))
  }

  const openRaw = async () => {
    try {
      const res = await Api.trajectory.raw(selected.id)
      setRawData({ entry: res.entry || selected, headers: res.headers || [], body: res.body || '' })
      setRawOpen(true)
    } catch (e) {
      message.error(e.message)
    }
  }

  const rows = doc?.rows || []
  const filtered = useMemo(() => {
    if (!query.trim()) return rows
    const q = query.trim().toLowerCase()
    return rows.filter((r) =>
      [r.text, r.name, r.call_id, r.arguments, r.output, r.label]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q))
    )
  }, [rows, query])

  const activeRow = rows.find((r) => r.index === activeIndex) || filtered[0] || null

  const columns = [
    { title: '#', dataIndex: 'id', width: 60, render: (v) => <Text type="secondary">#{v}</Text> },
    { title: '时间', dataIndex: 'time', width: 170, render: (v) => <Text type="secondary">{fmtTime(v)}</Text> },
    { title: '来源', dataIndex: 'source', width: 90, render: (v) => v || '未知' },
    { title: '入口', dataIndex: 'protocol', width: 110, render: (v) => <Tag bordered={false}>{v}</Tag> },
    { title: '请求模型', dataIndex: 'request_model', render: (v) => <Text className="mono">{v}</Text> },
    {
      title: '会话',
      dataIndex: 'session',
      width: 160,
      ellipsis: true,
      render: (v) =>
        v ? (
          <Tooltip title={v}>
            <Text className="mono text-xs">
              {v}
            </Text>
          </Tooltip>
        ) : (
          <Text type="secondary">—</Text>
        )
    },
    {
      title: '结果',
      dataIndex: 'status',
      width: 80,
      render: (v) =>
        v >= 200 && v < 300 ? (
          <Tag color="success" bordered={false}>
            {v}
          </Tag>
        ) : (
          <Tag color="error" bordered={false}>
            {v || '—'}
          </Tag>
        )
    },
    { title: '耗时', dataIndex: 'duration_ms', width: 90, render: (v) => (v ? `${(v / 1000).toFixed(1)}s` : '—') },
    { title: '大小', dataIndex: 'bytes', width: 90, render: fmtBytes }
  ]

  const detailTabs = [
    {
      key: 'system',
      label: '系统提示词',
      children: doc?.system ? (
        <div className="trace-scroll">
          <Pre text={doc.system} max={0} />
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="本请求没有系统提示词" />
      )
    },
    {
      key: 'tools',
      label: `工具 ${doc?.tools?.length || 0}`,
      children:
        doc?.tools?.length > 0 ? (
          <div className="trace-scroll">
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              {doc.tools.map((t, i) => (
                <ToolItem key={`${t.name}-${i}`} tool={t} />
              ))}
            </Space>
          </div>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="本请求没有声明工具" />
        )
    },
    {
      key: 'chain',
      label: `执行链路 ${rows.length}`,
      children: (
        <div className="trace-split">
          <div className="trace-list-pane">
            <Input.Search
              size="small"
              placeholder="搜索消息、工具与结果"
              allowClear
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              style={{ marginBottom: 8 }}
            />
            <div className="trace-list">
              {filtered.length === 0 ? (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={rows.length ? '没有匹配的行' : '本请求没有历史消息'}
                />
              ) : (
                filtered.map((r) => {
                  const k = kindOf(r.kind)
                  return (
                    <div
                      key={r.index}
                      className={`trace-item${activeRow?.index === r.index ? ' active' : ''}`}
                      style={{ borderLeftColor: k.border }}
                      onClick={() => setActiveIndex(r.index)}
                    >
                      <Tag color={k.color} bordered={false}>
                        {k.label}
                      </Tag>
                      <span className="trace-item-title">{r.name || r.role || r.label || ''}</span>
                      <span className="trace-item-text">{previewOf(r).replace(/\s+/g, ' ')}</span>
                    </div>
                  )
                })
              )}
            </div>
          </div>
          <div className="trace-detail">
            {activeRow ? (
              <RowDetail row={activeRow} />
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择左侧一行查看详情" />
            )}
          </div>
        </div>
      )
    }
  ]

  return (
    <div className="stack">
      <PageHeader
        title="请求轨迹"
        description="回看最近入站请求的系统提示词、工具与执行链路；仅存内存，不落盘。"
      />
      <Panel
        title="入站请求"
        subtitle={`仅保留最近 ${capacity} 条 · 点开单条查看完整链路`}
        extra={
          <Space>
            <Space size={6}>
              <Text type="secondary">自动刷新（5 秒）</Text>
              <Switch size="small" checked={autoRefresh} onChange={setAutoRefresh} />
            </Space>
            <Button size="small" icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
          </Space>
        }
        bodyClassName="flush"
      >
        <Table
          size="small"
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: 20, size: 'small' }}
          dataSource={list}
          scroll={{ x: 900 }}
          onRow={(record) => ({ onClick: () => open(record), style: { cursor: 'pointer' } })}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="还没有入站请求；接入 Codex 等 Agent 并发起对话后这里会显示"
              />
            )
          }}
          columns={columns}
        />
      </Panel>

      <Drawer
        width={960}
        open={!!selected}
        onClose={() => setSelected(null)}
        title={selected ? `#${selected.id} · ${selected.request_model}` : ''}
        extra={
          <Button size="small" onClick={openRaw} disabled={!selected}>
            原始请求
          </Button>
        }
      >
        {selected && (
          <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="时间">{fmtTime(selected.time)}</Descriptions.Item>
            <Descriptions.Item label="来源">{selected.source || '未知'}</Descriptions.Item>
            <Descriptions.Item label="入口">{selected.protocol}</Descriptions.Item>
            <Descriptions.Item label="目标模型">
              <Text className="mono">
                {selected.provider ? `${selected.provider} · ` : ''}
                {selected.model || '—'}
              </Text>
            </Descriptions.Item>
            <Descriptions.Item label="会话">
              <Text className="mono">{selected.session || '—'}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="状态 / 耗时 / 大小">
              {selected.status} · {selected.duration_ms ? `${(selected.duration_ms / 1000).toFixed(1)}s` : '—'} ·{' '}
              {fmtBytes(selected.bytes)}
            </Descriptions.Item>
          </Descriptions>
        )}
        {detailError && (
          <Card className="soft-card" variant="borderless" size="small" style={{ marginBottom: 12 }}>
            <Text type="danger">解析失败：{detailError}（原始 JSON 仍可查看）</Text>
          </Card>
        )}
        <Tabs items={detailTabs} destroyInactiveTabPane />
      </Drawer>

      <Modal
        open={rawOpen}
        onCancel={() => setRawOpen(false)}
        footer={null}
        width={960}
        title={selected ? `#${selected.id} 原始请求` : '原始请求'}
      >
        <div className="trace-raw">
          {rawData?.entry && (
            <>
              <div className="trace-raw-line">
                <Tag color="blue" bordered={false}>
                  {rawData.entry.method}
                </Tag>
                <span className="mono trace-raw-url">{rawData.entry.url}</span>
                <Text type="secondary" className="text-xs">
                  {rawData.entry.proto}
                </Text>
              </div>
              <div className="trace-raw-headers">
                {rawData.headers.length === 0 ? (
                  <Text type="secondary" className="text-xs">
                    没有记录到请求头
                  </Text>
                ) : (
                  rawData.headers.map((h, i) => (
                    <div key={`${h.name}-${i}`} className="mono">
                      <span className="trace-raw-hname">{h.name}</span>: {h.value}
                    </div>
                  ))
                )}
              </div>
            </>
          )}
          <pre className="trace-pre">{rawData?.body}</pre>
        </div>
      </Modal>
    </div>
  )
}
