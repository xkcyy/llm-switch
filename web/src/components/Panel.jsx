export default function Panel({ title, subtitle, extra, children, className = '', bodyClassName = '' }) {
  return (
    <section className={`panel ${className}`.trim()}>
      {title || extra ? (
        <div className="panel-header">
          <div>
            {title ? <div className="panel-title">{title}</div> : null}
            {subtitle ? <div className="panel-sub">{subtitle}</div> : null}
          </div>
          {extra ? <div>{extra}</div> : null}
        </div>
      ) : null}
      <div className={`panel-body ${bodyClassName}`.trim()}>{children}</div>
    </section>
  )
}
