import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Popconfirm,
  Progress,
  Row,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography
} from 'antd'
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  PoweroffOutlined,
  RocketOutlined,
  SortAscendingOutlined,
  SyncOutlined
} from '@ant-design/icons'
import { useApp } from '../store'

const { Text, Title, Paragraph } = Typography

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

  // 任务进行中轮询进度；结束后拉一次状态
  useEffect(() => {
    if (!running) return undefined
    const timer = setInterval(() => {
      app
        .pollTrae()
        .then((j) => {
          if (!j || j.state !== 'running') app.refreshTrae().catch(() => {})
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
    } catch (e) {
      message.error(e.message)
    } finally {
      setBusy(false)
    }
  }

  const restart = async () => {
    setRestarting(true)
    try {
      await app.restartTrae()
    } catch (e) {
      message.error(e.message)
    } finally {
      setRestarting(false)
    }
  }

  const reorder = async () => {
    try {
      await app.reorderTrae()
    } catch (e) {
      message.error(e.message)
    }
  }

  const pct = useMemo(() => {
    if (!job || !job.total) return job?.state === 'done' ? 100 : 0
    return Math.min(100, Math.round((job.done / job.total) * 100))
  }, [job])

  const failedItems = (job?.items || []).filter((i) => i.result === 'failed')
  const changedItems = (job?.items || []).filter((i) => ['added', 'saved_directly', 'deleted'].includes(i.result))

  if (!trae.installed) {
    return (
      <Alert
        type="warning"
        showIcon
        message="没有检测到 Trae SOLO"
        description={
          <Space direction="vertical" size={4}>
            <span>请确认已安装 TRAE SOLO CN（默认位置：%LOCALAPPDATA%\Programs\TRAE SOLO CN）。</span>
            <Text type="secondary" style={{ fontSize: 12 }}>
              当前探测到的可执行文件：{trae.exe || '（无）'}
            </Text>
          </Space>
        }
      />
    )
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {trae.needRestart ? (
        <Alert
          type="warning"
          showIcon
          icon={<ExclamationCircleFilled />}
          message="需要重启一次 Trae"
          description={
            <Space direction="vertical" size={8}>
              <span>
                Trae 正在运行，但还没有开启调试端口（这是自动化读取模型列表所必需的，仅监听本机 127.0.0.1）。
                重启后登录态与工作区都会保留。
              </span>
              <Button size="small" type="primary" icon={<PoweroffOutlined />} loading={restarting} onClick={restart}>
                重启 Trae 并继续
              </Button>
            </Space>
          }
        />
      ) : null}

      <div className="codex-hero">
        <Space direction="vertical" size={14} style={{ width: '100%' }}>
          <div>
            <Title level={3} style={{ marginBottom: 4 }}>
              让 Trae SOLO 用上你的模型
            </Title>
            <Paragraph type="secondary" style={{ marginBottom: 0 }}>
              点一下「一键接入」，Trae 的自定义模型就会和这里的 {trae.models.length} 个完全一致：
              缺少的补齐、不一致的移除，并按供应商顺序排列（已一致时不改动任何东西）。
            </Paragraph>
          </div>
          <Space size={20} wrap>
            <Space size={6}>
              <CheckCircleFilled style={{ color: '#52c41a' }} />
              <Text>走 Trae 官方界面写入，不改它的数据库与配置</Text>
            </Space>
            <Space size={6}>
              <CheckCircleFilled style={{ color: '#52c41a' }} />
              <Text>
                真实密钥留在 LLM Switch，Trae 里只放占位值 <Text code>llm-switch-local</Text>
              </Text>
            </Space>
            <Space size={6}>
              <CheckCircleFilled style={{ color: '#52c41a' }} />
              <Text>重复点击安全：已经一致时不会做任何改动</Text>
            </Space>
          </Space>
          <Space>
            <Button
              type="primary"
              size="large"
              icon={<RocketOutlined />}
              loading={busy || running}
              disabled={running || !trae.models.length}
              onClick={startSync}
            >
              {running ? '正在同步…' : '一键接入 Trae'}
            </Button>
            <Tooltip title="删除 Trae 里全部代理模型后按配置顺序重新添加；每个模型都会重跑一次连通性测试，可能需要几分钟">
              <Popconfirm
                title="按顺序重建 Trae 里的模型？"
                description={
                  <span style={{ display: 'inline-block', maxWidth: 360 }}>
                    会先删除 Trae 里所有代理模型（含本次接入的 {trae.models.length} 个），再按「
                    {trae.models.slice(0, 2).join('、')}
                    等」的顺序重新添加。每个模型都会重跑一次连通性测试，慢上游可能需要几分钟。
                  </span>
                }
                okText="重新排序"
                cancelText="取消"
                onConfirm={reorder}
              >
                <Button size="large" icon={<SortAscendingOutlined />} disabled={running}>
                  重新排序
                </Button>
              </Popconfirm>
            </Tooltip>
          </Space>
          {!trae.models.length ? (
            <Text type="danger" style={{ fontSize: 12 }}>
              当前没有可用的模型，请先在「供应商与模型」里添加并启用。
            </Text>
          ) : (
            <Text type="secondary" style={{ fontSize: 12 }}>
              注意：Trae 里不在上面这份清单中的自定义模型（包括手工添加的）会在接入时被移除。
            </Text>
          )}
        </Space>
      </div>

      {job ? (
        <Card
          className="soft-card"
          variant="borderless"
          title={
            <Space>
              <SyncOutlined spin={running} />
              <span>{job.kind === 'reorder' ? '重新排序进度' : '同步进度'}</span>
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
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <Alert
              type={job.state === 'failed' ? 'error' : job.state === 'done' ? 'success' : 'info'}
              showIcon
              message={job.message || (running ? '正在处理…' : '')}
            />
            {running ? (
              <>
                <Progress percent={pct} status="active" />
                <Space size={8}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {job.total ? `${job.done}/${job.total}` : ''} {job.current ? `当前：${job.current}` : ''}
                  </Text>
                </Space>
              </>
            ) : null}
            {job.items.length ? (
              <Table
                size="small"
                rowKey={(r) => `${r.model}-${r.result}`}
                pagination={job.items.length > 8 ? { pageSize: 8 } : false}
                dataSource={job.items}
                columns={[
                  { title: '模型', dataIndex: 'model', render: (v) => <Text code>{v}</Text> },
                  { title: '结果', dataIndex: 'result', width: 170, render: (v) => <ResultTag result={v} /> },
                  {
                    title: '说明',
                    dataIndex: 'detail',
                    render: (v) =>
                      v ? (
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {v}
                        </Text>
                      ) : (
                        ''
                      )
                  }
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
        </Card>
      ) : null}

      <Card className="soft-card" variant="borderless" title="接入信息">
        <Descriptions
          column={2}
          size="small"
          items={[
            {
              key: 'base',
              label: 'Trae 里的请求地址',
              children: <Text code>{trae.baseUrl}</Text>
            },
            {
              key: 'models',
              label: '本地代理模型数',
              children: <Text>{trae.models.length} 个</Text>
            },
            {
              key: 'state',
              label: '调试端口',
              children: trae.ready ? (
                <Space size={6}>
                  <CheckCircleFilled style={{ color: '#52c41a' }} />
                  <Text>可用（{trae.debugPort}）</Text>
                </Space>
              ) : trae.needRestart ? (
                <Space size={6}>
                  <ExclamationCircleFilled style={{ color: '#faad14' }} />
                  <Text>已配置但未生效，需重启 Trae</Text>
                </Space>
              ) : (
                <Space size={6}>
                  <CloseCircleFilled style={{ color: '#bfbfbf' }} />
                  <Text type="secondary">未开启（接入时会自动开启）</Text>
                </Space>
              )
            },
            {
              key: 'exe',
              label: 'Trae 位置',
              children: (
                <Text className="mono" ellipsis={{ tooltip: trae.exe }} style={{ maxWidth: 380 }}>
                  {trae.exe}
                </Text>
              )
            }
          ]}
        />
        <Divider style={{ margin: '12px 0' }} />
        <Space direction="vertical" size={6}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            写入的是 Trae 的「自定义模型」，模型 ID 与本地代理的模型键完全一致（例如 <Text code>km/gpt-5.6-sol</Text>）；
            Trae 会在你填的地址后自动补 <Text code>/chat/completions</Text>。
          </Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            同时会按模型元数据填好 Trae 的「高级配置」：上下文窗口（
            {trae.modelDetails.filter((m) => m.context_window > 0).length}/{trae.models.length} 个有值）、思考模式（
            {trae.modelDetails.filter((m) => m.thinking).length} 个开启）、图片输入
            （纯文本模型会在连通性测试后自动标记为不支持）。
          </Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            自定义模型只在 Trae 的本地环境生效（官方限制），并且会自动跟随本机代理，不会再走 Trae 的云端。
          </Text>
        </Space>
      </Card>
    </Space>
  )
}
