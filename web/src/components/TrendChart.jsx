import { useState } from 'react'
import { uiTokens } from '../theme'

export const PALETTE = [
  uiTokens.blue,
  uiTokens.cyan,
  uiTokens.amber,
  uiTokens.violet,
  '#db2777',
  uiTokens.green,
  uiTokens.red,
  '#0891b2',
  '#65a30d',
  uiTokens.inkMuted
]

const NICE = [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]

// niceScale 选一个「好看」的刻度步长，让纵轴落在 2–5 段、标签是整十整百，
// 同时把柱子撑到接近满高（朴素的向上取到 10 的幂会让柱子只有一半高）。
const niceScale = (max) => {
  const safe = Math.max(1, max)
  const exp = Math.floor(Math.log10(safe))
  for (let k = exp - 1; k <= exp + 1; k += 1) {
    for (const m of NICE) {
      const step = m * 10 ** k
      const count = Math.ceil(safe / step)
      if (count >= 2 && count <= 5) return { step, count, top: count * step }
    }
  }
  return { step: safe, count: 1, top: safe }
}

const fmtAxis = (v) => {
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`
  return String(Math.round(v))
}

// TrendChart 是无依赖的 SVG 分组柱状图：每个时间桶内每个分组各一根柱并排，
// 悬停显示该时刻各分组的取值与合计。
export default function TrendChart({ series, height = 230, formatValue = fmtAxis }) {
  const [hover, setHover] = useState(null)
  const n = series?.[0]?.points?.length ?? 0
  if (!n || !series?.length) return null

  const W = 1000
  const padL = 54
  const padR = 20
  const padT = 14
  const padB = 26
  const innerW = W - padL - padR
  const innerH = height - padT - padB
  const band = innerW / n
  const groupW = band * 0.74
  const slotW = groupW / series.length
  const barW = Math.max(1, slotW > 3 ? slotW - 1 : slotW)

  const at = (si, i) => series[si]?.points[i]?.value ?? null
  const totals = Array.from({ length: n }, (_, i) => series.reduce((sum, _, si) => sum + at(si, i), 0))
  const scale = niceScale(Math.max(...series.flatMap((s) => s.points.map((p) => p.value || 0))))
  const cx = (i) => padL + band * (i + 0.5)
  const bx = (i, si) => padL + band * i + (band - groupW) / 2 + si * slotW
  const py = (v) => padT + innerH - (v / scale.top) * innerH
  const baseY = padT + innerH

  const labelStep = Math.max(1, Math.ceil(n / 8))
  const labelIdx = []
  for (let i = n - 1; i >= 0; i -= labelStep) labelIdx.push(i)
  const tipLeft = Math.min(88, Math.max(12, (cx(hover ?? 0) / W) * 100))

  return (
    <div style={{ position: 'relative' }}>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginBottom: 10 }}>
        {series.map((s, i) => (
          <span key={s.key || i} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--ls-ink-soft)' }}>
            <i style={{ width: 10, height: 10, borderRadius: 2, background: s.color || PALETTE[i % PALETTE.length], display: 'inline-block' }} />
            {s.name}
          </span>
        ))}
      </div>

      <svg viewBox={`0 0 ${W} ${height}`} width="100%" height={height} onMouseLeave={() => setHover(null)}>
        {Array.from({ length: scale.count + 1 }, (_, i) => i * scale.step).map((v) => (
          <g key={v}>
            <line x1={padL} x2={W - padR} y1={py(v)} y2={py(v)} stroke="var(--ls-line)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
            <text x={padL - 8} y={py(v) + 4} textAnchor="end" fontSize="11" fill="var(--ls-ink-muted)">
              {fmtAxis(v)}
            </text>
          </g>
        ))}

        {hover != null && (
          <rect x={padL + band * hover} y={padT} width={band} height={innerH} fill="var(--ls-surface-soft)" />
        )}

        {Array.from({ length: n }, (_, i) => (
          <g key={i}>
            {series.map((s, si) => {
              const v = at(si, i)
              if (v == null || v === 0) return null
              const y = py(v)
              return (
                <rect
                  key={s.key || si}
                  x={bx(i, si)}
                  y={y}
                  width={barW}
                  height={Math.max(1, baseY - y)}
                  rx={barW > 4 ? 2 : 0}
                  fill={s.color || PALETTE[si % PALETTE.length]}
                  opacity={hover == null || hover === i ? 1 : 0.4}
                />
              )
            })}
          </g>
        ))}

        {/* 横轴标签从末端倒排：保证最后一个时段一定有标签，且不会与前一个挤在一起 */}
        {labelIdx.map((i) => (
          <text
            key={i}
            x={cx(i)}
            y={height - 8}
            textAnchor={i === n - 1 ? 'end' : 'middle'}
            fontSize="11"
            fill="var(--ls-ink-muted)"
          >
            {series[0].points[i].label}
          </text>
        ))}

        {Array.from({ length: n }, (_, i) => (
          <rect
            key={i}
            x={padL + band * i}
            y={padT}
            width={band}
            height={innerH}
            fill="transparent"
            onMouseEnter={() => setHover(i)}
          />
        ))}
      </svg>

      {hover != null && (
        <div
          style={{
            position: 'absolute',
            top: 22,
            left: `${tipLeft}%`,
            transform: 'translateX(-50%)',
            background: 'var(--ls-surface)',
            border: '1px solid var(--ls-line)',
            boxShadow: '0 4px 16px rgba(15,23,42,0.08)',
            borderRadius: 8,
            padding: '8px 10px',
            pointerEvents: 'none',
            fontSize: 12,
            minWidth: 140
          }}
        >
          <div style={{ color: 'var(--ls-ink-muted)', marginBottom: 4 }}>{series[0].points[hover].period}</div>
          {series.map((s, i) => (
            <div key={s.key || i} style={{ display: 'flex', justifyContent: 'space-between', gap: 14 }}>
              <span style={{ color: 'var(--ls-ink-soft)' }}>{s.name}</span>
              <span className="mono">{at(i, hover) == null ? '—' : formatValue(at(i, hover))}</span>
            </div>
          ))}
          {series.length > 1 && (
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 14, marginTop: 4, paddingTop: 4, borderTop: '1px solid var(--ls-line)' }}>
              <span style={{ color: 'var(--ls-ink-soft)' }}>合计</span>
              <span className="mono">{formatValue(totals[hover])}</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
