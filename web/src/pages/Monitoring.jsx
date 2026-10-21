import { useEffect, useMemo, useState } from 'react'
import { Button, DatePicker, Empty, Segmented, Space, Table, Tag, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { Api } from '../api'
import TrendChart, { PALETTE } from '../components/TrendChart'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import { EmptyState, ErrorState } from '../components/States'

const { Text } = Typography
const { RangePicker } = DatePicker

const RANGES = [
  { label: '近90分钟', value: '90m' },
  { label: '近24小时', value: '24h' },
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '全部', value: 'all' },
  { label: '自定义', value: 'custom' }
]

const GROUPS = [
  { label: '供应商', value: 'provider' },
  { label: '模型', value: 'model' }
]

const CHART_METRICS = [
  { label: 'Token 速度', value: 'speed' },
  { label: '请求量', value: 'requests' },
  { label: '成功率', value: 'success' }
]

const MAX_SERIES = 8

function formatPeriod(period) {
  if (!period) return '—'
  if (period.length === 7) return period
  if (period.includes('T')) {
    const [date, time] = period.split('T')
    return `${date.slice(5)} ${time.length === 5 ? time : `${time}:00`}`
  }
  return period.slice(5)
}

function formatLatency(value) {
  if (value == null) return '—'
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`
}

function formatRate(value) {
  if (value == null) return '—'
  return `${(value * 100).toFixed(1)}%`
}

function formatSpeed(value) {
  if (value == null) return '—'
  return `${value.toFixed(1)} tok/s`
}

function rateStatus(value) {
  if (value == null) return 'default'
  if (value >= 0.99) return 'success'
  if (value >= 0.9) return 'warning'
  return 'error'
}

function rateClass(value) {
  if (value == null) return 'is-empty'
  if (value >= 0.99) return 'is-ok'
  if (value >= 0.9) return 'is-warn'
  return 'is-bad'
}

function groupLabel(row) {
  if (row.model_id) {
    return { main: row.model_name || row.model_id, sub: row.provider_name || row.provider_id }
  }
  if (row.provider_id) {
    return { main: row.provider_name || row.provider_id, sub: row.provider_id }
  }
  return { main: row.key || '合计', sub: '' }
}

function pointValue(point, metric) {
  const metrics = point?.metrics || {}
  if (metric === 'speed') return metrics.tps ?? null
  if (metric === 'success') return metrics.success_rate == null ? null : metrics.success_rate * 100
  return metrics.requests || 0
}

function chartFormat(metric) {
  if (metric === 'speed') return formatSpeed
  if (metric === 'success') return (value) => (value == null ? '—' : `${value.toFixed(1)}%`)
  return (value) => `${value ?? 0} 次`
}

function AvailabilityTrend({ points }) {
  if (!points?.length) return <Text type="secondary">—</Text>
  return (
    <div className="availability-strip" aria-label="可用性趋势">
      {points.map((point) => {
        const metrics = point.metrics || {}
        const title = `${formatPeriod(point.period)} · ${metrics.requests || 0} 次 · 成功率 ${formatRate(metrics.success_rate)}`
        return (
          <span
            key={point.period}
            className={`availability-segment ${rateClass(metrics.requests ? metrics.success_rate : null)}`}
            title={title}
          />
        )
      })}
    </div>
  )
}

export default function Monitoring() {
  const [range, setRange] = useState('90m')
  const [group, setGroup] = useState('provider')
  const [chartMetric, setChartMetric] = useState('speed')
  const [dates, setDates] = useState(null)
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    if (range === 'custom' && (!dates || dates.length !== 2)) return
    const params = { range, group }
    if (range === 'custom') {
      params.from = dates[0].format('YYYY-MM-DD')
      params.to = dates[1].format('YYYY-MM-DD')
    }
    let cancelled = false
    setLoading(true)
    setError('')
    Api.metrics
      .report(params)
      .then((next) => {
        if (!cancelled) setData(next)
      })
      .catch((err) => {
        if (!cancelled) {
          setData(null)
          setError(err.message || '加载失败')
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [range, group, dates, refreshKey])

  const summary = data?.summary || {}
  const groups = data?.groups || []
  const granularity = data?.granularity

  const seriesByGroup = useMemo(() => {
    const out = new Map()
    for (const point of data?.series || []) {
      const key = point.group || ''
      if (!out.has(key)) out.set(key, [])
      out.get(key).push(point)
    }
    return out
  }, [data])

  const chart = useMemo(() => {
    if (!data?.series?.length || !groups.length) return null
    const periods = []
    const seen = new Set()
    for (const point of data.series) {
      if (!seen.has(point.period)) {
        seen.add(point.period)
        periods.push(point.period)
      }
    }
    const lookup = new Map(data.series.map((point) => [`${point.period}\u0000${point.group || ''}`, point]))
    const ranked = [...groups].sort((a, b) => (b.metrics?.requests || 0) - (a.metrics?.requests || 0))
    const series = ranked.slice(0, MAX_SERIES).map((item, index) => ({
      key: item.key || 'all',
      name: groupLabel(item).main,
      color: PALETTE[index % PALETTE.length],
      points: periods.map((period) => ({
        label: formatPeriod(period),
        period,
        value: pointValue(lookup.get(`${period}\u0000${item.key || ''}`), chartMetric)
      }))
    }))
    return { periods, series }
  }, [data, groups, chartMetric])

  const hasChartData = Boolean(
    chart?.series?.some((series) => series.points.some((point) => point.value != null && point.value > 0))
  )
  const empty = !summary.requests

  return (
    <div className="stack">
      <PageHeader
        title="性能与可用性"
        description="按供应商和时间段查看 token 生成速度、首 token 延迟与请求成功率。"
        actions={
          <Button
            icon={<ReloadOutlined />}
            loading={loading}
            onClick={() => setRefreshKey((value) => value + 1)}
          >
            刷新
          </Button>
        }
      />

      <Panel
        title="筛选"
        subtitle="速度只统计上游上报 usage 的流式请求；成功率按到达供应商的请求计算，客户端取消单独排除。"
      >
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
            <Text type="secondary">图表</Text>
            <Segmented options={CHART_METRICS} value={chartMetric} onChange={setChartMetric} />
          </Space>
          {data ? (
            <Text type="secondary">
              {data.from} ~ {data.to} ·{' '}
              {granularity === 'minute' ? '按分钟' : granularity === 'hour' ? '按小时' : granularity === 'month' ? '按月' : '按天'}
            </Text>
          ) : null}
        </div>
      </Panel>

      {error ? (
        <ErrorState message={error} onRetry={() => setRefreshKey((value) => value + 1)} />
      ) : (
        <>
          <div className="metric-strip">
            <div className="metric-strip-item">
              <div className="metric-strip-label">请求数</div>
              <div className="metric-strip-value">{summary.requests ?? 0}</div>
              <div className="metric-strip-extra">
                {summary.failed ?? 0} 次供应商失败 · {summary.excluded ?? 0} 次排除
              </div>
            </div>
            <div className="metric-strip-item">
              <div className="metric-strip-label">请求成功率</div>
              <div className="metric-strip-value">{formatRate(summary.success_rate)}</div>
              <div className="metric-strip-extra">
                {summary.ok ?? 0} 成功 / {(summary.ok ?? 0) + (summary.failed ?? 0)} 有效请求
              </div>
            </div>
            <div className="metric-strip-item">
              <div className="metric-strip-label">加权生成速度</div>
              <div className="metric-strip-value">{formatSpeed(summary.tps)}</div>
              <div className="metric-strip-extra">
                {summary.speed_samples ?? 0} 个流式速度样本
              </div>
            </div>
            <div className="metric-strip-item">
              <div className="metric-strip-label">平均首 Token 延迟</div>
              <div className="metric-strip-value">{formatLatency(summary.ttft_ms)}</div>
              <div className="metric-strip-extra">TTFT，成功流式请求</div>
            </div>
          </div>

          <Panel
            title="趋势"
            subtitle={chartMetric === 'speed' ? '按供应商加权，单请求生成时长作为分母' : chartMetric === 'success' ? '按时间桶展示成功率' : '按时间桶展示请求量'}
            extra={<Text type="secondary">最多显示 {MAX_SERIES} 个分组</Text>}
          >
            {empty || !hasChartData ? (
              <EmptyState
                title={chartMetric === 'speed' ? '暂无可测速样本' : '这段时间还没有调用'}
                description={
                  chartMetric === 'speed'
                    ? '需要流式响应且上游上报 output_tokens，才能计算生成速度。'
                    : '接入 Agent 并完成一次模型调用后，这里会显示趋势。'
                }
              />
            ) : (
              <TrendChart series={chart.series} formatValue={chartFormat(chartMetric)} />
            )}
          </Panel>

          <Panel
            title={group === 'provider' ? '供应商明细' : '模型明细'}
            subtitle="成功率 = 成功 /（成功 + 供应商失败 + 认证失败）；客户端取消不计入分母。"
            extra={
              <Space size={12} wrap>
                <span className="availability-legend"><i className="is-ok" />正常</span>
                <span className="availability-legend"><i className="is-warn" />波动</span>
                <span className="availability-legend"><i className="is-bad" />失败</span>
                <span className="availability-legend"><i className="is-empty" />无数据</span>
              </Space>
            }
            bodyClassName="flush"
          >
            <Table
              size="small"
              className="monitor-table"
              rowKey={(row) => row.key || 'all'}
              loading={loading}
              pagination={groups.length > 10 ? { pageSize: 10 } : false}
              dataSource={groups}
              scroll={{ x: 1100 }}
              locale={{
                emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无可统计数据" />
              }}
              columns={[
                {
                  title: group === 'provider' ? '供应商' : '模型',
                  width: 200,
                  render: (_, row) => {
                    const label = groupLabel(row)
                    return (
                      <Space direction="vertical" size={0}>
                        <Text strong>{label.main}</Text>
                        {label.sub ? <Text type="secondary">{label.sub}</Text> : null}
                      </Space>
                    )
                  }
                },
                {
                  title: '成功率',
                  width: 110,
                  render: (_, row) => {
                    const value = row.metrics?.success_rate
                    return (
                      <Tooltip title={`${row.metrics?.ok ?? 0} 成功 / ${row.metrics?.failed ?? 0} 供应商失败`}>
                        <Tag color={rateStatus(value)}>{formatRate(value)}</Tag>
                      </Tooltip>
                    )
                  }
                },
                {
                  title: '请求',
                  width: 110,
                  render: (_, row) => (
                    <Space direction="vertical" size={0}>
                      <Text>{row.metrics?.requests ?? 0}</Text>
                      <Text type="secondary">
                        {row.metrics?.failed ?? 0} 失败 · {row.metrics?.excluded ?? 0} 排除
                      </Text>
                    </Space>
                  )
                },
                {
                  title: 'TPS',
                  width: 125,
                  render: (_, row) => (
                    <Space direction="vertical" size={0}>
                      <Text strong>{formatSpeed(row.metrics?.tps)}</Text>
                      <Text type="secondary">{row.metrics?.speed_samples ?? 0} 样本</Text>
                    </Space>
                  )
                },
                {
                  title: '首 Token',
                  width: 110,
                  render: (_, row) => (
                    <Space direction="vertical" size={0}>
                      <Text>{formatLatency(row.metrics?.ttft_ms)}</Text>
                      <Text type="secondary">TTFT</Text>
                    </Space>
                  )
                },
                {
                  title: '失败构成',
                  width: 200,
                  render: (_, row) => {
                    const parts = []
                    if (row.upstream_err) parts.push(<Tag key="upstream">上游 {row.upstream_err}</Tag>)
                    if (row.auth_err) parts.push(<Tag key="auth" color="warning">认证 {row.auth_err}</Tag>)
                    if (row.client_cancel) parts.push(<Tag key="cancel">取消 {row.client_cancel}</Tag>)
                    if (row.client_err) parts.push(<Tag key="client">本地 {row.client_err}</Tag>)
                    const legacy = Math.max(
                      0,
                      (row.err || 0) -
                        (row.upstream_err || 0) -
                        (row.auth_err || 0) -
                        (row.client_cancel || 0) -
                        (row.client_err || 0)
                    )
                    if (legacy) parts.push(<Tag key="legacy">未分类 {legacy}</Tag>)
                    return parts.length ? <Space size={4} wrap>{parts}</Space> : <Text type="secondary">—</Text>
                  }
                },
                {
                  title: '最后监测',
                  width: 125,
                  render: (_, row) => <Text type="secondary">{formatPeriod(row.last_period)}</Text>
                },
                {
                  title: '可用性趋势',
                  width: 250,
                  render: (_, row) => <AvailabilityTrend points={seriesByGroup.get(row.key || '')} />
                }
              ]}
            />
          </Panel>
        </>
      )}
    </div>
  )
}
