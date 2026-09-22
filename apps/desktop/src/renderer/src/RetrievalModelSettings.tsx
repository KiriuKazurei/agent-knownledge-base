import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, CheckCircle2, CircleAlert, Cpu, Download, HardDrive, LoaderCircle, RefreshCw, Search, Trash2, X } from 'lucide-react'
import { client } from './api'
import type { EmbeddingModel, EmbeddingModelHealth, Library, RetrievalGeneration, RetrievalProgress } from './types'

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value ?? '未知错误')
}

function formatBytes(value?: number) {
  if (!value || value < 1) return '未提供'
  if (value < 1024 * 1024) return `${Math.max(1, Math.round(value / 1024))} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function modelHealthLabel(item: EmbeddingModel, health?: EmbeddingModelHealth | null) {
  const current = health ?? item.health
  if (!item.installed) return '未安装'
  if (!current) return '尚未检查'
  return current.available ? '健康' : '不可用'
}

function indexStatusLabel(status?: string) {
  switch (status) {
    case 'current': return '当前'
    case 'stale': return '待重建'
    case 'queued': return '排队中'
    case 'building': return '构建中'
    case 'failed': return '失败'
    case 'cancelled': return '已取消'
    default: return status || '尚未建立'
  }
}

function progressLabel(progress?: RetrievalProgress, status?: RetrievalGeneration) {
  if (!progress || progress.total < 1) return status?.count ? `${status.count.toLocaleString()} 个片段` : '等待语料统计'
  return `${Math.min(progress.completed, progress.total).toLocaleString()} / ${progress.total.toLocaleString()} 个片段`
}

function ModelCard({
  item,
  onOnlineInstall,
  onlineBusy,
}: {
  item: EmbeddingModel
  onOnlineInstall: (id: string) => void
  onlineBusy: boolean
}) {
  const queryClient = useQueryClient()
  const [health, setHealth] = useState<EmbeddingModelHealth | null | undefined>(item.health)
  const healthMutation = useMutation({
    mutationFn: () => client.embeddingModelHealth(item.id),
    onSuccess: (value) => setHealth(value),
  })
  const toggleTargetRef = useRef<boolean | null>(null)
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => client.setEmbeddingModelEnabled(item.id, enabled),
    onSuccess: () => { toggleTargetRef.current = null; void queryClient.invalidateQueries({ queryKey: ['embedding-models'] }) },
  })
  const remove = useMutation({
    mutationFn: () => client.deleteEmbeddingModel(item.id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['embedding-models'] }),
  })
  const currentHealth = healthMutation.data ?? health ?? item.health
  const canEnable = item.installed && Boolean(currentHealth?.available)
  const actionError = healthMutation.error || toggle.error || remove.error

  useEffect(() => {
    setHealth(item.health)
  }, [item.id, item.health])

  return <article className={`retrieval-model-card ${item.enabled ? 'active' : ''}`} aria-label={`检索模型 ${item.id}`}>
    <div className="retrieval-model-heading">
      <div className="retrieval-model-icon" aria-hidden="true"><Search size={18}/></div>
      <div className="retrieval-model-title">
        <strong>{item.id}</strong>
        <span>{item.version ? `版本 ${item.version}` : '固定目录模型'} · {item.license || '许可证未说明'}</span>
      </div>
      <span className={`status-pill ${item.enabled ? 'ready' : item.installed ? 'pending' : 'muted'}`}>{item.enabled ? '已启用' : modelHealthLabel(item, currentHealth)}</span>
    </div>
    <div className="retrieval-model-facts">
      <span><Cpu size={13}/>维度 {item.dimension ?? '未知'}</span>
      <span><HardDrive size={13}/>模型 {formatBytes(item.sizeBytes)}</span>
      <span><HardDrive size={13}/>运行时 {formatBytes(item.runtimeBytes)}</span>
      {item.maxTokens && <span>上限 {item.maxTokens.toLocaleString()} tokens</span>}
      {item.pooling?.type && <span>池化 {item.pooling.type.toUpperCase()} {item.pooling.normalize ? '· 已归一化' : ''}</span>}
    </div>
    <p className="retrieval-model-source">来源提交：<code>{item.upstreamCommit || '固定目录记录'}</code>{item.upstream && <> · <span>{item.upstream}</span></>}</p>
    {!item.installed && <p className="retrieval-model-note">模型尚未安装。未安装时桌面搜索仍可用，会明确显示关键词降级。</p>}
    {item.installed && currentHealth && <div className={`retrieval-health ${currentHealth.available ? 'ok' : 'bad'}`} role={currentHealth.available ? 'status' : 'alert'}>
      {currentHealth.available ? <CheckCircle2 size={15}/> : <CircleAlert size={15}/>}<span>{currentHealth.available ? `CPU ONNX Runtime ${currentHealth.runtimeVersion || '已加载'} 可用` : currentHealth.error || '模型健康检查失败'}</span>
    </div>}
    {!item.installed && item.onlineInstall === false && <p className="retrieval-model-note warning">在线安装不可用：{item.onlineDisabledReason || '固定资源清单尚未发布，请导入离线包。'}</p>}
    {actionError && <div className="inline-error retrieval-action-error" role="alert"><CircleAlert size={15}/><span>{errorMessage(actionError)}</span>{remove.error && <button type="button" className="text-button" onClick={() => remove.mutate()} disabled={remove.isPending}>重试卸载</button>}{toggle.error && <button type="button" className="text-button" onClick={() => toggle.mutate(toggleTargetRef.current ?? !item.enabled)} disabled={toggle.isPending}>重试</button>}</div>}
    <div className="retrieval-model-actions">
      <button type="button" className="button secondary" onClick={() => healthMutation.mutate()} disabled={!item.installed || healthMutation.isPending}>
        {healthMutation.isPending ? <LoaderCircle size={15} className="spin"/> : <RefreshCw size={15}/>} {healthMutation.isPending ? '检查中…' : '健康检查'}
      </button>
      {!item.installed && <button type="button" className="button secondary" onClick={() => onOnlineInstall(item.id)} disabled={onlineBusy || item.onlineInstall === false} title={item.onlineInstall === false ? item.onlineDisabledReason : undefined}>
        {onlineBusy ? <LoaderCircle size={15} className="spin"/> : <Download size={15}/>} {onlineBusy ? '安装中…' : '在线安装'}
      </button>}
      {item.installed && <button type="button" className="button secondary" onClick={() => { const target = !item.enabled; toggleTargetRef.current = target; toggle.mutate(target) }} disabled={toggle.isPending || (!item.enabled && !canEnable)} title={!item.enabled && !canEnable ? '健康检查通过后才能启用' : undefined}>
        {toggle.isPending ? <LoaderCircle size={15} className="spin"/> : <Activity size={15}/>} {toggle.isPending ? '保存中…' : item.enabled ? '停用' : '启用'}
      </button>}
      {item.installed && <button type="button" className="button danger" onClick={() => { if (item.enabled) return; if (window.confirm(`卸载检索模型“${item.id}”？已生成的索引不会自动删除，但语义检索会降级。`)) remove.mutate() }} disabled={item.enabled || remove.isPending} title={item.enabled ? '请先停用模型' : undefined}>
        {remove.isPending ? <LoaderCircle size={15} className="spin"/> : <Trash2 size={15}/>} {remove.isPending ? '卸载中…' : '卸载'}
      </button>}
    </div>
  </article>
}

function RetrievalIndexCard({ library, models }: { library: Library; models: EmbeddingModel[] }) {
  const queryClient = useQueryClient()
  const [selectedModel, setSelectedModel] = useState('')
  const status = useQuery({
    queryKey: ['retrieval-index', library.id],
    queryFn: () => client.retrievalIndexStatus(library.id),
    retry: false,
    refetchInterval: 3000,
  })
  const progress = useQuery({
    queryKey: ['retrieval-index-progress', library.id],
    queryFn: () => client.retrievalIndexProgress(library.id),
    retry: false,
    refetchInterval: 2500,
  })
  const rebuild = useMutation({
    mutationFn: () => client.rebuildRetrievalIndex([library.id], selectedModel || undefined),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['retrieval-index', library.id] })
      void queryClient.invalidateQueries({ queryKey: ['retrieval-index-progress', library.id] })
    },
  })
  const cancel = useMutation({
    mutationFn: () => client.cancelRetrievalIndex(library.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['retrieval-index', library.id] })
      void queryClient.invalidateQueries({ queryKey: ['retrieval-index-progress', library.id] })
    },
  })
  const current = status.data
  const currentStatus = current?.status || progress.data?.status
  const busy = rebuild.isPending || cancel.isPending || currentStatus === 'queued' || currentStatus === 'building' || progress.data?.status === 'running' || progress.data?.status === 'cancellation_requested'
  const enabledModels = useMemo(() => models.filter((item) => item.installed && item.enabled), [models])

  useEffect(() => {
    if (!selectedModel && current?.modelId) setSelectedModel(current.modelId)
    if (!selectedModel && enabledModels[0]?.id) setSelectedModel(enabledModels[0].id)
  }, [current?.modelId, enabledModels, selectedModel])

  const modelChanged = Boolean(current?.modelId && selectedModel && current.modelId !== selectedModel)
  const indexError = status.error || progress.error || rebuild.error || cancel.error
  return <article className="retrieval-index-card" aria-label={`${library.name} 检索索引`}>
    <div className="retrieval-index-heading">
      <div><strong>{library.name}</strong><span>统一索引状态</span></div>
      <span className={`status-pill ${currentStatus === 'current' ? 'ready' : currentStatus === 'failed' ? 'failed' : 'pending'}`}>{indexStatusLabel(currentStatus)}</span>
    </div>
    <div className="retrieval-index-meta" role="status" aria-live="polite">
      <span>代次：<code>{current?.generation || progress.data?.generation || '—'}</code></span>
      <span>模型：<code>{current?.modelId || '关键词检索'}</code></span>
      <span>{progressLabel(progress.data, current)}</span>
    </div>
    {(currentStatus === 'building' || currentStatus === 'queued' || progress.data?.status === 'running') && <div className="retrieval-progress-wrap" aria-label="索引构建进度">
      <div className="retrieval-progress-track"><span style={{ width: `${progress.data?.total ? Math.min(100, (progress.data.completed / progress.data.total) * 100) : 0}%` }}/></div>
      <small>{progress.data?.total ? `${Math.round((progress.data.completed / progress.data.total) * 100)}%` : '准备中…'}</small>
    </div>}
    {modelChanged && <div className="retrieval-index-note" role="status"><RefreshCw size={15}/><span>切换模型后会在后台建立新索引；当前代次继续提供检索，验证成功后才切换。</span></div>}
    {current?.degraded && <div className="retrieval-index-note warning" role="status"><CircleAlert size={15}/><span>{current.degradedReason || '当前索引处于关键词降级状态。'}</span></div>}
    {indexError && <div className="inline-error retrieval-action-error" role="alert"><CircleAlert size={15}/><span>{errorMessage(indexError)}</span><button type="button" className="text-button" onClick={() => { void status.refetch(); void progress.refetch() }}>重试读取</button>{rebuild.error && <button type="button" className="text-button" onClick={() => rebuild.mutate()} disabled={rebuild.isPending}>重试重建</button>}</div>}
    <div className="retrieval-index-actions">
      <label><span>用于下一代索引的模型</span><select aria-label={`${library.name} 索引模型`} value={selectedModel} onChange={(event) => setSelectedModel(event.target.value)} disabled={busy}>
        <option value="">关键词检索（无语义模型）</option>
        {enabledModels.map((item) => <option key={item.id} value={item.id}>{item.id} · {item.version || '本地'}</option>)}
      </select></label>
      <div className="settings-actions">
        <button type="button" className="button primary" onClick={() => rebuild.mutate()} disabled={busy || rebuild.isPending}>
          {rebuild.isPending ? <LoaderCircle size={15} className="spin"/> : <RefreshCw size={15}/>} {rebuild.isPending ? '重建中…' : currentStatus === 'failed' || currentStatus === 'stale' ? '重试重建' : '重建索引'}
        </button>
        {busy && <button type="button" className="button secondary" onClick={() => cancel.mutate()} disabled={cancel.isPending || progress.data?.status === 'cancellation_requested'}>{cancel.isPending ? '取消中…' : progress.data?.status === 'cancellation_requested' ? '已请求取消' : '取消'}</button>}
      </div>
    </div>
    {rebuild.isSuccess && !rebuild.isPending && <p className="settings-success" role="status">索引代次已验证，旧代次已安全切换。</p>}
  </article>
}

export function RetrievalModelSettings({ libraries }: { libraries: Library[] }) {
  const queryClient = useQueryClient()
  const models = useQuery({ queryKey: ['embedding-models'], queryFn: client.embeddingModels, retry: false })
  const [importError, setImportError] = useState('')
  const [importMessage, setImportMessage] = useState('')
  const install = useMutation({
    mutationFn: (path: string) => client.installEmbeddingModel(path),
    onSuccess: () => { setImportMessage('离线模型包已校验并安装；默认保持停用，请先健康检查。'); setImportError(''); void queryClient.invalidateQueries({ queryKey: ['embedding-models'] }) },
    onError: (error) => setImportError(errorMessage(error)),
  })
  const onlineInstall = useMutation({
    mutationFn: (modelId: string) => client.installEmbeddingModelOnline(modelId),
    onSuccess: () => { setImportMessage('固定目录模型已安装；下一步可健康检查并启用。'); setImportError(''); void queryClient.invalidateQueries({ queryKey: ['embedding-models'] }) },
    onError: (error) => setImportError(errorMessage(error)),
  })
  async function choosePackage() {
    setImportError(''); setImportMessage('')
    try {
      const paths = await window.kah.selectFiles('embedding-model')
      if (paths[0]) install.mutate(paths[0])
    } catch (error) { setImportError(errorMessage(error)) }
  }
  const modelList = models.data ?? []
  return <section aria-labelledby="retrieval-models-heading">
    <div className="settings-section-heading"><div><h3 id="retrieval-models-heading">检索模型</h3><p className="settings-lead">本地 CPU embedding 只从固定资源清单加载。安装包必须包含模型、分词器和许可证文件，应用不会隐式下载模型。</p></div><Search size={22} aria-hidden="true"/></div>
    <div className="retrieval-install-toolbar">
      <div><strong>离线模型包</strong><span>导入已签名 ZIP；安装在应用数据目录，模型运行时大小单独显示。</span></div>
      <button type="button" className="button primary" onClick={() => void choosePackage()} disabled={install.isPending || onlineInstall.isPending}>{install.isPending ? <><LoaderCircle size={15} className="spin"/>校验并安装中…</> : <><Download size={15}/>导入 ZIP</>}</button>
    </div>
    {models.isLoading && <div className="preview-loading" role="status" aria-live="polite"><LoaderCircle className="spin" size={16}/>正在读取检索模型目录…</div>}
    {models.error && <div className="inline-error" role="alert"><CircleAlert size={16}/><span>{errorMessage(models.error)}</span><button type="button" className="text-button" onClick={() => void models.refetch()}>重试</button></div>}
    {(importError || install.error || onlineInstall.error) && <div className="inline-error" role="alert"><CircleAlert size={16}/><span>{importError || errorMessage(install.error || onlineInstall.error)}</span><button type="button" className="text-button" onClick={() => setImportError('')}>关闭</button></div>}
    {importMessage && <p className="settings-success" role="status" aria-live="polite">{importMessage}</p>}
    <div className="retrieval-model-list" aria-live="polite">
      {modelList.map((item) => <ModelCard key={item.id} item={item} onOnlineInstall={(id) => { setImportError(''); onlineInstall.mutate(id) }} onlineBusy={onlineInstall.isPending && onlineInstall.variables === item.id}/>) }
      {!models.isLoading && !modelList.length && <div className="retrieval-empty"><Search size={22}/><strong>还没有可用的检索模型</strong><span>导入固定格式的离线 ZIP 后，模型会显示在这里。</span></div>}
    </div>
    <div className="retrieval-index-section">
      <div className="settings-section-heading"><div><h4>索引管理</h4><p className="settings-lead">每个知识库独立记录当前代次、模型和构建进度。重建期间继续使用上一代索引。</p></div><RefreshCw size={19} aria-hidden="true"/></div>
      {libraries.length ? <div className="retrieval-index-list">{libraries.map((library) => <RetrievalIndexCard key={library.id} library={library} models={modelList}/>)}</div> : <p className="muted">先创建知识库，再为它建立统一检索索引。</p>}
    </div>
  </section>
}
