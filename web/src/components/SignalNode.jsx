import { Badge } from 'antd'

export default function SignalNode({ title, meta, icon, active = false, muted = false, status }) {
  return (
    <div className={`signal-node${active ? ' is-active' : ''}${muted ? ' is-muted' : ''}`}>
      <div className="signal-node-mark">{icon}</div>
      <div className="signal-node-main">
        <div className="signal-node-title">{title}</div>
        {meta ? <div className="signal-node-meta">{meta}</div> : null}
      </div>
      {status ? <Badge status={status} /> : null}
    </div>
  )
}
