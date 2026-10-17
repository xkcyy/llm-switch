export default function PageHeader({ title, description, actions }) {
  return (
    <header className="page-header">
      <div className="page-header-main">
        <h1 className="page-title">{title}</h1>
        {description ? <div className="page-sub">{description}</div> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  )
}
