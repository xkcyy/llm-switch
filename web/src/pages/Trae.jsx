import { useEffect, useMemo, useState } from 'react'
import { Alert, App, Button, Popconfirm, Progress, Space, Table, Tag, Tooltip, Typography } from 'antd'
import {
  ExclamationCircleFilled,
  PoweroffOutlined,
  SortAscendingOutlined,
  SyncOutlined
} from '@ant-design/icons'
import { useApp } from '../store'
import PageHeader from '../components/PageHeader'
import Panel from '../components/Panel'
import CopyText from '../components/CopyText'
import ConnectAgent from '../components/ConnectAgent'

const { Text } = Typography

const RESULT_META = {
  present: { color: 'default', text: '已存在' },
  kept: { color: 'default', text: '保留' },
  added: { color: 'success', text: '已添加' },
  saved_directly: { color: 'warning', text: '已添加（测试未通过）' },
  exists: { color: 'default', text: '已存在' },
  deleted: { color: 'success', text: '已删除' },
  failed: { color: 'error', text: '失败' }
}

function ResultTag({ result }) {
  const meta = RESULT_META[result] || { color: 'default', text: result }
  return <Tag color={meta.color}>{meta.text}</Tag>
}

export default function TraePage() {
  const app = useApp()
  const { message } = App.useApp()
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const trae = app.trae
  const job = trae.job
  const running = job?.state === 'running'

  useEffect(() => {
    if (!running) return undefined
    const timer = setInterval(() => {
      app
        .pollTrae()
        .then((next) => {
          if (!next || next.state !== 'running') app.refreshTrae().catch(() => {})
        })
        .catch(() => {})
    }, 1500)
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running])

  const startSync = async () => {
    setBusy(true)
    try {
      await app.syncTrae()
    } catch (error) {
      message.error(error.message)
    } finally {
      setBusy(false)
    }
  }

  const restart = async () => {
    setRestarting(true)
    try {
      await app.restartTrae()
    } catch (error) {
      message.error(error.message)
    } finally {
      setRestarting(false)
    }
  }

  const reorder = async () => {
    try {
      await app.reorderTrae()
    } catch (error) {
      message.error(error.message)
    }
  }

  const pct = useMemo(() => {
    if (!job || !job.total) return job?.state === 'done' ? 100 : 0
    return Math.min(100, Math.round((job.done / job.total) * 100))
  }, [job])

  const failedItems = (job?.items || []).filter((item) => item.result === 'failed')
  const changedItems = (job?.items || []).filter((item) => ['added', 'saved_directly', 'deleted'].includes(item.result))

  if (!trae.installed) {
    return (
      <div className="stack">
        <PageHeader
          title="接入 Trae SOLO"
          description="通过 Trae 官方界面同步自定义模型，不改 Trae 数据库与配置。"
        />
        <Alert
          type="warning"
          showIcon
          message="没有检测到 Trae SOLO"
          description={
            <Space direction="vertical" size={4}>
              <span>请确认已安装 TRAE SOLO CN（默认位置：%LOCALAPPDATA%\Programs\TRAE SOLO CN）。</span>
              <Text type="secondary">当前探测到的可执行文件：{trae.exe || '（无）'}</Text>
            </Space>
          }
        />
      </div>
    )
  }

  const debugPortValue = trae.ready ? (
    <Space size={6}>
      <span className="status-dot is-running" />
      <Text>可用（{trae.debugPort}）</Text>
    </Space>
  ) : trae.needRestart ? (
    <Space size={6}>
      <ExclamationCircleFilled style={{ color: 'var(--ls-amber)' }} />
      <Text>已配置但未生效，需重启 Trae</Text>
    </Space>
  ) : (
    <Text type="secondary">未开启（接入时会自动开启）</Text>
  )

  const disconnectedInspection = [
    {
      label: '本地可用模型',
      value: trae.models.length ? `${trae.models.length} 个` : <Text type="danger">请先在供应商与模型中启用模型</Text>
    },
    { label: '调试端口', value: debugPortValue },
    { label: 'Trae 位置', value: <CopyText value={trae.exe} /> },
    { label: '请求地址', value: <CopyText value={trae.baseUrl || '接入后确定'} /> }
  ]

  const connectedInspection = [
    { label: '本地模型', value: `${trae.models.length} 个` },
    { label: '请求地址', value: <CopyText value={trae.baseUrl} /> },
    { label: '调试端口', value: debugPortValue },
    { label: 'Trae 位置', value: <CopyText value={trae.exe} /> }
  ]

  const jobPanel = job ? (
    <Panel
      title={job.kind === 'reorder' ? '重新排序进度' : '同步进度'}
      extra={
        <Space>
          <SyncOutlined spin={running} />
          {job.state === 'done' ? (
            <Tag color="success">完成</Tag>
          ) : job.state === 'failed' ? (
            <Tag color="error">失败</Tag>
          ) : (
            <Tag color="processing">
              {job.phase === 'scanning'
                ? '读取中'
                : job.phase === 'preparing'
                  ? '准备中'
                  : job.phase === 'verifying'
                    ? '复核中'
                    : '写入中'}
            </Tag>
          )}
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert
          type={job.state === 'failed' ? 'error' : job.state === 'done' ? 'success' : 'info'}
          showIcon
          message={job.message || (running ? '正在处理…' : '')}
        />
        {running ? (
          <>
            <Progress percent={pct} status="active" />
            <Text type="secondary">
              {job.total ? `${job.done}/${job.total}` : ''} {job.current ? `当前：${job.current}` : ''}
            </Text>
          </>
        ) : null}
        {job.items.length ? (
          <Table
            size="small"
            rowKey={(row) => `${row.model}-${row.result}`}
            pagination={job.items.length > 8 ? { pageSize: 8 } : false}
            dataSource={job.items}
            columns={[
              { title: '模型', dataIndex: 'model', render: (value) => <Text className="mono">{value}</Text> },
              { title: '结果', dataIndex: 'result', width: 170, render: (value) => <ResultTag result={value} /> },
              { title: '说明', dataIndex: 'detail', render: (value) => <Text type="secondary">{value || ''}</Text> }
            ]}
          />
        ) : null}
        {!running && failedItems.length ? (
          <Alert
            type="warning"
            showIcon
            message={`${failedItems.length} 个模型没有处理成功`}
            description="可以直接再点一次「一键接入」重试；若提示连通性测试失败且上游确实不可用，请先在「供应商与模型」里修好该供应商。"
          />
        ) : null}
        {!running && job.state === 'done' && !changedItems.length && job.kind === 'sync' ? (
          <Alert type="success" showIcon message="Trae 上的模型已经是本地代理的模型，无需改动" />
        ) : null}
      </Space>
    </Panel>
  ) : null

  const infoPanel = (
    <Panel title="接入信息" subtitle="Trae 的自定义模型会在每次接入时与本地代理完全对齐。">
      <div className="inspection">
        <div className="inspection-row">
          <div className="inspection-label">高级配置覆盖</div>
          <div className="inspection-value">
            上下文窗口 {trae.modelDetails.filter((model) => model.context_window > 0).length}/{trae.models.length} 个有值；
            思考模式 {trae.modelDetails.filter((model) => model.thinking).length} 个开启；图片输入由连通性测试结果决定。
          </div>
        </div>
        <div className="inspection-row">
          <div className="inspection-label">模型命名</div>
          <div className="inspection-value">
            与本地代理的模型键完全一致，例如 <span className="mono">km/gpt-5.6-sol</span>；Trae 会在地址后自动补
            <span className="mono">/chat/completions</span>。
          </div>
        </div>
        <div className="inspection-row">
          <div className="inspection-label">生效范围</div>
          <div className="inspection-value">
            自定义模型只在 Trae 的本地环境生效，并跟随本机代理，不会再走 Trae 云端。
          </div>
        </div>
      </div>
    </Panel>
  )

  const restartAlert = trae.needRestart ? (
    <Alert
      type="warning"
      showIcon
      icon={<ExclamationCircleFilled />}
      message="需要重启一次 Trae"
      description={
        <Space direction="vertical" size={8}>
          <span>
            Trae 正在运行，但还没有开启调试端口；该端口仅监听本机 127.0.0.1。重启后登录态与工作区都会保留。
          </span>
          <Button size="small" type="primary" icon={<PoweroffOutlined />} loading={restarting} onClick={restart}>
            重启 Trae 并继续
          </Button>
        </Space>
      }
    />
  ) : null

  return (
    <div className="stack">
      <PageHeader
        title="接入 Trae SOLO"
        description="通过 Trae 官方界面同步自定义模型，不改 Trae 数据库与配置。"
      />

      <ConnectAgent
        connected={trae.ready}
        heroTitle="让 Trae SOLO 用上你的模型"
        heroDescription={`点一下一键接入，Trae 的自定义模型就会和这里的 ${trae.models.length} 个完全一致：缺少的补齐、不一致的移除，并按供应商顺序排列。`}
        points={[
          '走 Trae 官方界面写入，不改它的数据库与配置',
          '真实密钥留在 LLM Switch，Trae 里只放占位值',
          '重复点击安全：已经一致时不会做任何改动'
        ]}
        connectLabel="一键接入 Trae"
        connecting={busy || running}
        connectDisabled={running || !trae.models.length}
        onConnect={startSync}
        previewPlaceholder={null}
        disconnectedInspection={disconnectedInspection}
        disconnectedExtra={restartAlert}
        connectedTitle="Trae SOLO 已就绪"
        connectedDescription="调试端口可用，可以随时把本地代理模型同步到 Trae。"
        onSync={startSync}
        syncing={busy || running}
        syncLabel="一键接入 Trae"
        onTest={() => app.verifyAgent('trae')}
        verification={app.agentVerifications?.trae}
        inspection={connectedInspection}
        actions={
          <Tooltip title="删除 Trae 里全部代理模型后按配置顺序重新添加；每个模型都会重跑一次连通性测试，可能需要几分钟">
            <Popconfirm
              title="按顺序重建 Trae 里的模型？"
              description={`会先删除 Trae 里所有代理模型（含本次接入的 ${trae.models.length} 个），再按配置顺序重新添加。`}
              okText="重新排序"
              cancelText="取消"
              onConfirm={reorder}
            >
              <Button icon={<SortAscendingOutlined />} disabled={running}>
                重新排序
              </Button>
            </Popconfirm>
          </Tooltip>
        }
        footnote={
          trae.models.length
            ? 'Trae 里不在当前清单中的自定义模型（包括手工添加的）会在接入时被移除。'
            : '当前没有可用模型，请先在「供应商与模型」里添加并启用。'
        }
      >
        {jobPanel}
        {infoPanel}
      </ConnectAgent>
    </div>
  )
}
