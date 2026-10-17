import { useEffect, useMemo, useState } from 'react'
import { DatePicker, Empty, Segmented, Space, Table, Typography } from 'antd'
import { Api } from '../api'
import TrendChart, { PALETTE } from '../components/TrendChart'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import { EmptyState } from '../components/States'
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

function periodLabel(period) {
  if (period.length === 7) return period
  if (period.includes('T')) return `${period.slice(11)}:00`
  return period.slice(5)
}

function groupLabel(row) {
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
  const valueOf = (row) => (metric === 'requests' ? row.requests || 0 : row.total_tokens || 0)
  const formatValue = (value) => (metric === 'requests' ? `${value} 次` : fmtTokens(value))

  const chart = useMemo(() => {
    if (!data?.series?.length) return null
    const periods = []
    const seen = new Set()
    for (const point of data.series) {
      if (!seen.has(point.period)) {
        seen.add(point.period)
        periods.push(point.period)
      }
    }
    const lookup = new Map(data.series.map((point) => [`${point.period}\u0000${point.group || ''}`, point]))
    const at = (period, key) => {
      const bucket = lookup.get(`${period}\u0000${key || ''}`)
      return bucket ? valueOf(bucket) : 0
    }
    const ranked = [...groups].sort((a, b) => valueOf(b) - valueOf(a))
    const head = ranked.slice(0, MAX_SERIES)
    const tail = ranked.slice(MAX_SERIES)
    const series = head.map((item, index) => ({
      key: item.key || 'all',
      name: groupLabel(item).main,
      color: PALETTE[index % PALETTE.length],
      points: periods.map((period) => ({ label: periodLabel(period), period, value: at(period, item.key) }))
    }))
    if (tail.length) {
      series.push({
        key: '__other__',
        name: `其它 ${tail.length} 项`,
        color: 'var(--ls-ink-muted)',
        points: periods.map((period) => ({
          label: periodLabel(period),
          period,
          value: tail.reduce((sum, item) => sum + at(period, item.key), 0)
        }))
      })
    }
    return { periods, series }
  }, [data, metric])

  const peak = groups.reduce((max, item) => Math.max(max, valueOf(item)), 0) || 1
  const empty = !totals.requests

  return (
    <div className="stack">
      <PageHeader
        title="用量"
        description="按时间、供应商、模型与调用方查看 token 消耗和调用次数。"
      />

      <Panel title="筛选" subtitle="只采信上游上报的用量；缺失数据单独计数，不估算。">
        <div className="usage-toolbar">
          <Space size={8} wrap>
            <Text type="secondary">时间</Text>
            <Segmented options={RANGES} value={range} onChange={setRange} />
            {range === 'custom' ? <RangePicker value={dates} onChange={setDates} allowClear={false} /> : null}
          </Space>
          <Space size={8} wrap>
            <Text type="secondary">分组</Text>
            <Segmented options={GROUPS} value={group} onChange={setGroup} />
          </Space>
          <Space size={8} wrap>
            <Text type="secondary">指标</Text>
            <Segmented options={METRICS} value={metric} onChange={setMetric} />
          </Space>
          {data ? (
            <Text type="secondary">
              {data.from} ~ {data.to} · {data.granularity === 'hour' ? '按小时' : data.granularity === 'month' ? '按月' : '按天'}
            </Text>
          ) : null}
        </div>
      </Panel>

      <div className="metric-strip">
        <div className="metric-strip-item">
          <div className="metric-strip-label">调用次数</div>
          <div className="metric-strip-value">{totals.requests ?? 0}</div>
          <div className="metric-strip-extra">{totals.err ? `${totals.err} 次失败` : '全部成功'}</div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">输入 Token</div>
          <div className="metric-strip-value">{fmtTokens(totals.input_tokens)}</div>
          <div className="metric-strip-extra">缓存命中 {fmtTokens(totals.cached_tokens)}</div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">输出 Token</div>
          <div className="metric-strip-value">{fmtTokens(totals.output_tokens)}</div>
          <div className="metric-strip-extra">推理 {fmtTokens(totals.reasoning_tokens)}</div>
        </div>
        <div className="metric-strip-item">
          <div className="metric-strip-label">合计 Token</div>
          <div className="metric-strip-value">{fmtTokens(totals.total_tokens)}</div>
          <div className="metric-strip-extra">
            {totals.missing_usage ? `${totals.missing_usage} 次无用量` : '用量完整'}
          </div>
        </div>
      </div>

      <Panel
        title="趋势"
        extra={<Text type="secondary">{metric === 'requests' ? '调用次数' : 'Token 数'}</Text>}
      >
        {empty || !chart ? (
          <EmptyState title="这段时间还没有调用" description="接入 Agent 并完成一次模型调用后，这里会显示趋势。" />
        ) : (
          <TrendChart series={chart.series} formatValue={formatValue} />
        )}
      </Panel>

      {group !== 'all' ? (
        <Panel title="分组占比" subtitle={metric === 'requests' ? '按调用次数' : '按 Token 数'}>
          {empty ? (
            <EmptyState title="暂无分组数据" />
          ) : (
            <div className="usage-group-list">
              {groups.map((item, index) => (
                <div className="usage-group-row" key={item.key || 'all'}>
                  <div className="usage-group-head">
                    <span>{groupLabel(item).main}</span>
                    <span className="mono">{formatValue(valueOf(item))}</span>
                  </div>
                  <div className="usage-bar">
                    <div
                      className="usage-bar-fill"
                      style={{
                        width: `${Math.round((valueOf(item) / peak) * 100)}%`,
                        background: PALETTE[index % PALETTE.length]
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>
      ) : null}

      <Panel title="明细" bodyClassName="flush">
        <Table
          size="small"
          rowKey={(row) => row.key || 'all'}
          loading={loading}
          pagination={groups.length > 10 ? { pageSize: 10 } : false}
          dataSource={groups}
          scroll={{ x: 860 }}
          locale={{
            emptyText: (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />
            )
          }}
          columns={[
            {
              title: '分组',
              dataIndex: 'key',
              render: (_, row) => {
                const item = groupLabel(row)
                return (
                  <Space direction="vertical" size={0}>
                    <Text strong>{item.main}</Text>
                    {item.sub ? <Text type="secondary">{item.sub}</Text> : null}
                  </Space>
                )
              }
            },
            { title: '调用', dataIndex: 'requests', width: 80 },
            { title: '失败', dataIndex: 'err', width: 72, render: (value) => (value ? <Text type="danger">{value}</Text> : '0') },
            { title: '输入', dataIndex: 'input_tokens', width: 96, render: fmtTokens },
            { title: '输出', dataIndex: 'output_tokens', width: 96, render: fmtTokens },
            { title: '合计', dataIndex: 'total_tokens', width: 96, render: (value) => <Text strong>{fmtTokens(value)}</Text> },
            { title: '缓存', dataIndex: 'cached_tokens', width: 88, render: fmtTokens },
            { title: '推理', dataIndex: 'reasoning_tokens', width: 88, render: fmtTokens },
            { title: '无用量', dataIndex: 'missing_usage', width: 84, render: (value) => (value ? <Text type="warning">{value}</Text> : '—') }
          ]}
        />
      </Panel>
    </div>
  )
}
