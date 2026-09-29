import { useEffect, useMemo, useState } from 'react'
import { Card, Col, DatePicker, Empty, Row, Segmented, Space, Table, Typography } from 'antd'
import { Api } from '../api'
import TrendChart, { PALETTE } from '../components/TrendChart'
import { fmtTokens } from '../format'

const { Text } = Typography
const { RangePicker } = DatePicker

const RANGES = [
  { label: '当天', value: 'today' },
  { label: '近7天', value: '7d' },
  { label: '本月', value: 'month' },
  { label: '近30天', value: '30d' },
  { label: '自定义', value: 'custom' }
]

const GROUPS = [
  { label: '汇总', value: 'all' },
  { label: '供应商', value: 'provider' },
  { label: '模型', value: 'model' }
]

const METRICS = [
  { label: 'Token', value: 'tokens' },
  { label: '调用次数', value: 'requests' }
]

const MAX_SERIES = 8

function Stat({ label, value, extra, accent }) {
  return (
    <Card className="soft-card stat-card" variant="borderless">
      <div className="stat-label" style={{ color: accent }}>{label}</div>
      <div className="stat-value">{value}</div>
      <div className="stat-extra">{extra}</div>
    </Card>
  )
}

// periodLabel 把桶名缩成横轴上的短标签。
const periodLabel = (p) => {
  if (p.length === 7) return p
  if (p.includes('T')) return `${p.slice(11)}:00`
  return p.slice(5)
}

// groupLabel 按分组维度挑出主标题与副标题。
const groupLabel = (row) => {
  if (!row.key) return { main: '合计', sub: '' }
  if (row.model_id) return { main: row.model_name || row.model_id, sub: row.provider_name || row.provider_id }
  if (row.provider_id) return { main: row.provider_name || row.provider_id, sub: '' }
  return { main: row.caller || row.key, sub: '' }
}

export default function Usage() {
  const [range, setRange] = useState('30d')
  const [group, setGroup] = useState('all')
  const [metric, setMetric] = useState('tokens')
  const [dates, setDates] = useState(null)
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const params = { range, group }
    if (range === 'custom') {
      if (!dates || dates.length !== 2) return
      params.from = dates[0].format('YYYY-MM-DD')
      params.to = dates[1].format('YYYY-MM-DD')
    }
    setLoading(true)
    Api.usage
      .report(params)
      .then(setData)
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [range, group, dates])

  const totals = data?.totals || {}
  const groups = data?.groups || []
  const valueOf = (t) => (metric === 'requests' ? t.requests || 0 : t.total_tokens || 0)
  const formatValue = (v) => (metric === 'requests' ? `${v} 次` : fmtTokens(v))

  const chart = useMemo(() => {
    if (!data?.series?.length) return null
    const periods = []
    const seen = new Set()
    for (const p of data.series) {
      if (!seen.has(p.period)) {
        seen.add(p.period)
        periods.push(p.period)
      }
    }
    // 汇总分组在 JSON 里 group 字段会被省略，统一按空串归位，避免取不到数。
    const lookup = new Map(data.series.map((p) => [`${p.period}\u0000${p.group || ''}`, p]))
    const at = (period, key) => {
      const bucket = lookup.get(`${period}\u0000${key || ''}`)
      return bucket ? valueOf(bucket) : 0
    }
    const ranked = [...groups].sort((a, b) => valueOf(b) - valueOf(a))
    const head = ranked.slice(0, MAX_SERIES)
    const tail = ranked.slice(MAX_SERIES)
    const series = head.map((g, i) => ({
      key: g.key || 'all',
      name: groupLabel(g).main,
      color: PALETTE[i % PALETTE.length],
      points: periods.map((p) => ({ label: periodLabel(p), period: p, value: at(p, g.key) }))
    }))
    if (tail.length) {
      series.push({
        key: '__other__',
        name: `其它 ${tail.length} 项`,
        color: '#8c93a4',
        points: periods.map((p) => ({
          label: periodLabel(p),
          period: p,
          value: tail.reduce((sum, g) => sum + at(p, g.key), 0)
        }))
      })
    }
    return { periods, series }
  }, [data, metric])

  const peak = groups.reduce((m, g) => Math.max(m, valueOf(g)), 0) || 1
  const empty = !totals.requests

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card className="soft-card" variant="borderless" size="small">
        <Space wrap size={20}>
          <Space size={8}>
            <Text type="secondary">时间</Text>
            <Segmented options={RANGES} value={range} onChange={setRange} />
          </Space>
          {range === 'custom' && <RangePicker value={dates} onChange={setDates} allowClear={false} />}
          <Space size={8}>
            <Text type="secondary">分组</Text>
            <Segmented options={GROUPS} value={group} onChange={setGroup} />
          </Space>
          <Space size={8}>
            <Text type="secondary">指标</Text>
            <Segmented options={METRICS} value={metric} onChange={setMetric} />
          </Space>
          {data && (
            <Text type="secondary" style={{ fontSize: 12 }}>
              {data.from} ~ {data.to} · {data.granularity === 'hour' ? '按小时' : data.granularity === 'month' ? '按月' : '按天'}
            </Text>
          )}
        </Space>
      </Card>

      <Row gutter={16}>
        <Col span={6}>
          <Stat label="调用次数" value={totals.requests ?? 0} extra={totals.err ? `${totals.err} 次失败` : '全部成功'} accent="#2f6bff" />
        </Col>
        <Col span={6}>
          <Stat label="输入 Token" value={fmtTokens(totals.input_tokens)} extra={`缓存命中 ${fmtTokens(totals.cached_tokens)}`} accent="#13a8a8" />
        </Col>
        <Col span={6}>
          <Stat label="输出 Token" value={fmtTokens(totals.output_tokens)} extra={`推理 ${fmtTokens(totals.reasoning_tokens)}`} accent="#fa8c16" />
        </Col>
        <Col span={6}>
          <Stat
            label="合计 Token"
            value={fmtTokens(totals.total_tokens)}
            extra={totals.missing_usage ? `${totals.missing_usage} 次无用量` : '用量完整'}
            accent="#722ed1"
          />
        </Col>
      </Row>

      <Card className="soft-card" variant="borderless" title="趋势">
        {empty || !chart ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="这段时间还没有调用" />
        ) : (
          <TrendChart series={chart.series} formatValue={formatValue} />
        )}
      </Card>

      {group !== 'all' && (
        <Card className="soft-card" variant="borderless" title="分组占比">
          <Row gutter={[24, 12]}>
            {empty ? (
              <Col span={24}>
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />
              </Col>
            ) : (
              groups.map((g, i) => (
                <Col span={12} key={g.key || 'all'}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                    <Text style={{ fontSize: 12 }}>{groupLabel(g).main}</Text>
                    <Text className="mono" style={{ fontSize: 12 }}>{formatValue(valueOf(g))}</Text>
                  </div>
                  <div style={{ background: '#f0f2f7', borderRadius: 4, height: 8 }}>
                    <div
                      style={{
                        width: `${Math.round((valueOf(g) / peak) * 100)}%`,
                        height: '100%',
                        borderRadius: 4,
                        background: PALETTE[i % PALETTE.length]
                      }}
                    />
                  </div>
                </Col>
              ))
            )}
          </Row>
        </Card>
      )}

      <Card className="soft-card" variant="borderless" title="明细">
        <Table
          size="small"
          rowKey={(r) => r.key || 'all'}
          loading={loading}
          pagination={groups.length > 10 ? { pageSize: 10 } : false}
          dataSource={groups}
          scroll={{ x: 860 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" /> }}
          columns={[
            {
              title: '分组',
              dataIndex: 'key',
              render: (_, r) => {
                const g = groupLabel(r)
                return (
                  <Space direction="vertical" size={0}>
                    <Text strong>{g.main}</Text>
                    {g.sub && <Text type="secondary" style={{ fontSize: 12 }}>{g.sub}</Text>}
                  </Space>
                )
              }
            },
            { title: '调用', dataIndex: 'requests', width: 80 },
            { title: '失败', dataIndex: 'err', width: 72, render: (v) => (v ? <Text type="danger">{v}</Text> : '0') },
            { title: '输入', dataIndex: 'input_tokens', width: 96, render: fmtTokens },
            { title: '输出', dataIndex: 'output_tokens', width: 96, render: fmtTokens },
            { title: '合计', dataIndex: 'total_tokens', width: 96, render: (v) => <Text strong>{fmtTokens(v)}</Text> },
            { title: '缓存', dataIndex: 'cached_tokens', width: 88, render: fmtTokens },
            { title: '推理', dataIndex: 'reasoning_tokens', width: 88, render: fmtTokens },
            { title: '无用量', dataIndex: 'missing_usage', width: 84, render: (v) => (v ? <Text type="warning">{v}</Text> : '—') }
          ]}
        />
      </Card>
    </Space>
  )
}
