import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as Dialog from '@radix-ui/react-dialog'
import ReactMarkdown from 'react-markdown'
import {
  AlertTriangle, Ban, CheckCircle2, ChevronDown, CircleAlert, Clock3, Code2, Download,
  FileCode2, FileJson, FileText, FolderSearch2, GitCompare, Layers3, LoaderCircle, Network,
  Pencil, Play, Plus, RefreshCw, Save, ScanLine, Settings2, ShieldAlert, Trash2, X
} from 'lucide-react'
import { ApiError, client } from './api'
import type {
  ArchitectureAnalysis, ArchitectureArtifact, ArchitectureDiff, ArchitectureExecutor, ArchitectureProject,
  ArchitectureSnapshot, ArchitectureTask, Library, ProjectExecutionConfig
} from './types'

type ArchitectureTab = 'overview' | 'modules' | 'evidence' | 'versions' | 'tasks' | 'artifacts'

const executorLabels: Record<ArchitectureExecutor, string> = {
  auto: '自动选择', codex: 'Codex CLI', mcp: 'MCP Agent', provider: '配置的生成模型', none: '仅扫描'
}

const taskStatusLabels: Record<string, string> = {
  queued: '排队中', running: '进行中', completed: '已完成', failed: '失败', cancelled: '已取消', cancellation_requested: '正在取消'
}

const languageLabels: Record<string, string> = { typescript: 'TypeScript', javascript: 'JavaScript', go: 'Go', python: 'Python', csharp: 'C#', unknown: '未知' }

const artifactLabels: Record<string, string> = { document: 'Markdown 文档', diagram_json: '图描述 JSON', html: '自包含 HTML', svg: 'SVG 图', png: 'PNG 图' }

function formatDate(value?: string) {
  if (!value) return '暂无'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

function taskProgress(task?: ArchitectureTask) {
  if (!task) return 0
  if (task.status === 'completed') return 100
  if (task.status === 'failed' || task.status === 'cancelled') return 100
  return task.status === 'queued' ? 12 : 52
}

function taskStage(task?: ArchitectureTask) {
  if (!task) return ''
  if (task.status === 'queued') return '等待执行器'
  if (task.status === 'running') return task.kind === 'scan' ? '读取文件、计算哈希与识别模块' : '分析证据并生成架构产物'
  return taskStatusLabels[task.status] ?? task.status
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return `该项目已有任务在运行。请等待当前任务结束，或先取消它。${error.message ? `（${error.message}）` : ''}`
  return error instanceof Error ? error.message : String(error)
}

function queryErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function projectFormValues(project?: ArchitectureProject) {
  return {
    name: project?.name ?? '',
    description: project?.description ?? '',
    libraryId: project?.libraryId ?? '',
    rootPath: project?.rootPath ?? '',
    excludePatterns: project?.excludePatterns.join('\n') ?? '',
    executor: project?.execution.executor ?? 'auto' as ArchitectureExecutor,
    providerId: project?.execution.providerId ?? '',
    codexPath: project?.execution.codexPath ?? '',
    codexModel: project?.execution.codexModel ?? '',
    timeoutSeconds: project?.execution.timeoutSeconds ? String(project.execution.timeoutSeconds) : '',
    allowRemoteCode: project?.allowRemoteCode ?? false,
    scheduleKind: project?.scheduleKind ?? 'manual',
    scheduleAnchor: project?.scheduleAnchor ? project.scheduleAnchor.slice(0, 16) : '',
    scanPeriodSeconds: project?.scanPeriodSeconds ? String(project.scanPeriodSeconds) : '3600'
  }
}

function normalizeAnchor(value: string) {
  if (!value.trim()) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toISOString()
}

function ProjectDialog({ open, onOpenChange, project, libraries, providers, onSaved }: { open: boolean; onOpenChange: (value: boolean) => void; project?: ArchitectureProject; libraries: Library[]; providers: { id: string; name: string; local: boolean }[]; onSaved: (project: ArchitectureProject) => void }) {
  const editing = Boolean(project)
  const [values, setValues] = useState(() => projectFormValues(project))
  const [error, setError] = useState('')
  const [choosing, setChoosing] = useState(false)

  useEffect(() => { if (open) { setValues(projectFormValues(project)); setError('') } }, [open, project])

  const update = <K extends keyof ReturnType<typeof projectFormValues>>(key: K, value: ReturnType<typeof projectFormValues>[K]) => setValues((current) => ({ ...current, [key]: value }))

  const save = useMutation({
    mutationFn: async () => {
      const name = values.name.trim()
      if (!name) throw new Error('项目名称不能为空。')
      if (!values.libraryId) throw new Error('请选择关联知识库。')
      if (!values.rootPath.trim() || !/^(?:[A-Za-z]:[\\/]|[\\/]{2}|\/)/.test(values.rootPath.trim())) throw new Error('请选择一个本地绝对目录。')
      if (values.scheduleKind === 'interval' && Number(values.scanPeriodSeconds) < 60) throw new Error('固定间隔必须至少为 60 秒。')
      if ((values.scheduleKind === 'daily' || values.scheduleKind === 'weekly') && !values.scheduleAnchor) throw new Error('每日或每周更新需要设置首次运行时间。')
      const execution: ProjectExecutionConfig = { executor: values.executor, providerId: values.providerId.trim() || undefined, codexPath: values.codexPath.trim() || undefined, codexModel: values.codexModel.trim() || undefined, allowExternal: values.allowRemoteCode, timeoutSeconds: Number(values.timeoutSeconds) || 0 }
      const input = { name, description: values.description.trim(), libraryId: values.libraryId, rootPath: values.rootPath.trim(), excludePatterns: values.excludePatterns.split(/[\r\n,，]/).map((item) => item.trim()).filter(Boolean), scanPeriodSeconds: values.scheduleKind === 'interval' ? Number(values.scanPeriodSeconds) || 0 : 0, scheduleKind: values.scheduleKind, scheduleAnchor: normalizeAnchor(values.scheduleAnchor), allowRemoteCode: values.allowRemoteCode, execution }
      return editing ? client.updateArchitectureProject(project!.id, input) : client.createArchitectureProject(input)
    },
    onSuccess: (saved) => { onSaved(saved); onOpenChange(false) },
    onError: (cause) => setError(errorMessage(cause))
  })

  async function chooseDirectory() {
    setChoosing(true)
    try { const path = await window.kah.selectDirectory(); if (path) update('rootPath', path) } catch (cause) { setError(errorMessage(cause)) } finally { setChoosing(false) }
  }

  return <Dialog.Root open={open} onOpenChange={(value) => { if (!save.isPending) onOpenChange(value) }}>
    <Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="confirm-dialog architecture-project-dialog" aria-describedby="architecture-project-description">
      <Dialog.Title>{editing ? '编辑项目架构配置' : '添加项目架构'}</Dialog.Title>
      <Dialog.Description id="architecture-project-description">选择本地项目目录后，扫描结果和生成产物会保存在应用数据目录，不会写回源项目。</Dialog.Description>
      <form onSubmit={(event) => { event.preventDefault(); save.mutate() }}>
        <div className="architecture-form-grid">
          <label>项目名称<input aria-label="项目名称" value={values.name} maxLength={160} onChange={(event) => update('name', event.target.value)} autoFocus/></label>
          <label>关联知识库<select aria-label="关联知识库" value={values.libraryId} onChange={(event) => update('libraryId', event.target.value)}><option value="">选择知识库</option>{libraries.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
          <label className="span-2">项目说明<textarea aria-label="项目说明" value={values.description} rows={2} maxLength={1000} onChange={(event) => update('description', event.target.value)} placeholder="可选：项目用途或团队约定"/></label>
          <label className="span-2">项目目录<div className="architecture-path-control"><input aria-label="项目目录" value={values.rootPath} readOnly placeholder="请选择一个本地绝对目录"/><button type="button" className="button secondary" onClick={() => void chooseDirectory()} disabled={choosing}>{choosing ? <LoaderCircle className="spin" size={15}/> : <FolderSearch2 size={15}/>}选择目录</button></div></label>
          <label className="span-2">排除规则<small>每行一个规则；默认扫描器还会排除依赖、构建产物、二进制和秘密文件。</small><textarea aria-label="排除规则" value={values.excludePatterns} rows={3} onChange={(event) => update('excludePatterns', event.target.value)} placeholder="例如：vendor\n.tmp\nfixtures/generated"/></label>
          <label>执行方式<select aria-label="执行方式" value={values.executor} onChange={(event) => update('executor', event.target.value as ArchitectureExecutor)}>{Object.entries(executorLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
          <label>生成模型提供方<select aria-label="生成模型提供方" value={values.providerId} onChange={(event) => update('providerId', event.target.value)} disabled={values.executor !== 'provider' && values.executor !== 'auto'}><option value="">不指定</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}{provider.local ? ' · 本地' : ' · 远程'}</option>)}</select></label>
          {(values.executor === 'auto' || values.executor === 'codex') && <label>Codex 可执行文件<input aria-label="Codex 可执行文件" value={values.codexPath} onChange={(event) => update('codexPath', event.target.value)} placeholder="留空使用 PATH 中的 codex"/></label>}
          {(values.executor === 'auto' || values.executor === 'codex') && <label>Codex 模型<input aria-label="Codex 模型" value={values.codexModel} onChange={(event) => update('codexModel', event.target.value)} placeholder="可选，由 Codex 能力探测决定"/></label>}
          <label>超时（秒）<input aria-label="超时秒数" type="number" min={0} max={86400} value={values.timeoutSeconds} onChange={(event) => update('timeoutSeconds', event.target.value)} placeholder="默认 1800"/></label>
          <label>更新周期<select aria-label="更新周期" value={values.scheduleKind} onChange={(event) => update('scheduleKind', event.target.value as typeof values.scheduleKind)}><option value="manual">手动</option><option value="daily">每天</option><option value="weekly">每周</option><option value="interval">固定间隔</option></select></label>
          {(values.scheduleKind === 'daily' || values.scheduleKind === 'weekly') && <label>首次运行时间<input aria-label="首次运行时间" type="datetime-local" value={values.scheduleAnchor} onChange={(event) => update('scheduleAnchor', event.target.value)}/></label>}
          {values.scheduleKind === 'interval' && <label>间隔秒数<input aria-label="间隔秒数" type="number" min={60} max={31536000} value={values.scanPeriodSeconds} onChange={(event) => update('scanPeriodSeconds', event.target.value)}/></label>}
        </div>
        <label className="architecture-switch-row"><input type="checkbox" aria-label="允许远程处理项目代码" checked={values.allowRemoteCode} onChange={(event) => update('allowRemoteCode', event.target.checked)}/><span><strong>允许远程处理项目代码</strong><small>关闭时，外部 MCP Agent 和远程生成模型不得读取此项目；本地扫描仍可执行。</small></span></label>
        {(values.executor === 'mcp' || values.executor === 'provider') && !values.allowRemoteCode && <div className="architecture-notice warning" role="status"><ShieldAlert size={16}/>当前关闭远程代码许可，外部执行器会被阻止或回退；请确认这是预期设置。</div>}
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="dialog-actions"><button type="button" className="button ghost" onClick={() => onOpenChange(false)} disabled={save.isPending}>取消</button><button type="submit" className="button primary" disabled={save.isPending || !values.name.trim() || !values.libraryId || !values.rootPath.trim()}>{save.isPending && <LoaderCircle className="spin" size={15}/>} {editing ? '保存配置' : '创建项目'}</button></div>
      </form>
    </Dialog.Content></Dialog.Portal>
  </Dialog.Root>
}

function ProjectList({ projects, selectedId, onSelect, onCreate, onEdit, onDelete, loading, error }: { projects: ArchitectureProject[]; selectedId: string; onSelect: (id: string) => void; onCreate: () => void; onEdit: (project: ArchitectureProject) => void; onDelete: (project: ArchitectureProject) => void; loading: boolean; error?: unknown }) {
  return <aside className="architecture-project-list" aria-label="项目架构项目列表">
    <div className="architecture-list-heading"><div><span className="eyebrow">代码范围</span><h2>项目</h2></div><button type="button" className="icon-button" aria-label="添加项目架构" title="添加项目架构" onClick={onCreate}><Plus size={17}/></button></div>
    {loading && <div className="architecture-loading" role="status"><LoaderCircle className="spin" size={17}/>正在读取项目…</div>}
    {error ? <div className="inline-error" role="alert"><CircleAlert size={16}/>{String(errorMessage(error))}</div> : null}
    {!loading && !projects.length && <div className="architecture-empty small"><Layers3 size={24}/><strong>还没有项目</strong><span>添加一个本地项目目录，开始生成有证据的技术架构。</span><button type="button" className="button primary" onClick={onCreate}><Plus size={15}/>添加项目</button></div>}
    <div className="architecture-project-items">{projects.map((project) => <article key={project.id} className={`architecture-project-item ${project.id === selectedId ? 'active' : ''}`}>
      <button type="button" className="architecture-project-select" aria-current={project.id === selectedId ? 'page' : undefined} onClick={() => onSelect(project.id)}><span className="architecture-project-icon"><Code2 size={17}/></span><span><strong>{project.name}</strong><small>{project.rootPath}</small><em>{project.analysisStatus === 'stale' ? '需要更新' : project.analysisStatus === 'failed' ? '生成失败' : executorLabels[project.execution.executor] ?? project.execution.executor}</em></span></button>
      <div className="architecture-project-actions"><button type="button" className="icon-button" aria-label={`编辑项目 ${project.name}`} title="编辑项目" onClick={() => onEdit(project)}><Pencil size={14}/></button><button type="button" className="icon-button danger-icon" aria-label={`删除项目 ${project.name}`} title="删除项目" onClick={() => onDelete(project)}><Trash2 size={14}/></button></div>
    </article>)}</div>
  </aside>
}

function ArchitectureProgress({ task, project, onCancel, onRetry }: { task?: ArchitectureTask; project: ArchitectureProject; onCancel: () => void; onRetry: () => void }) {
  if (!task && !project.analysisError && project.analysisStatus !== 'stale') return null
  const progress = taskProgress(task)
  const terminalFailure = task?.status === 'failed' || project.analysisStatus === 'failed'
  return <div className={`architecture-progress ${terminalFailure ? 'error' : project.analysisStatus === 'stale' ? 'stale' : ''}`} role="region" aria-label="项目架构任务状态">
    <div className="architecture-progress-heading"><div><strong>{task ? `${task.kind === 'scan' ? '扫描' : '生成'} · ${taskStatusLabels[task.status] ?? task.status}` : project.analysisStatus === 'stale' ? '架构内容已过期' : '上次生成失败'}</strong><span>{taskStage(task) || project.analysisError || '源项目发生变化，需要重新生成架构。'}</span></div><div className="architecture-progress-actions">{task && ['queued', 'running', 'cancellation_requested'].includes(task.status) && <button type="button" className="button ghost" onClick={onCancel}><Ban size={14}/>取消</button>}{(!task || terminalFailure || project.analysisStatus === 'stale') && <button type="button" className="button secondary" onClick={onRetry}><RefreshCw size={14}/>重试</button>}</div></div>
    {task && <><div className="architecture-progress-track" role="progressbar" aria-label="架构任务进度" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress}><span style={{ width: `${progress}%` }}/></div><div className="architecture-progress-meta"><span>{progress}% · 执行器：{executorLabels[project.execution.executor] ?? project.execution.executor}</span><span>{task.message || `任务 ${task.id.slice(0, 8)}`}</span></div></>}
    {project.analysisError && <p role="alert" className="architecture-error-detail"><CircleAlert size={15}/>{project.analysisError}</p>}
  </div>
}

function SnapshotOverview({ snapshot, project }: { snapshot?: ArchitectureSnapshot; project: ArchitectureProject }) {
  if (!snapshot) return <div className="architecture-empty"><ScanLine size={30}/><strong>还没有扫描版本</strong><span>先执行“扫描”，系统会记录文件哈希、Git 修订、模块识别和跳过原因。</span></div>
  return <div className="architecture-overview"><div className="architecture-metric-grid"><div><span>文件</span><strong>{snapshot.fileCount}</strong><small>纳入 {snapshot.coverage.includedFiles} · 跳过 {snapshot.coverage.ignoredFiles}</small></div><div><span>模块</span><strong>{snapshot.moduleCount}</strong><small>未知关系 {snapshot.unknownCount}</small></div><div><span>Git</span><strong>{snapshot.gitDirty ? '有变更' : '干净'}</strong><small>{snapshot.gitRevision || '没有可用修订'}</small></div><div><span>快照</span><strong>{snapshot.status === 'ready' ? '有效' : snapshot.status}</strong><small>{formatDate(snapshot.createdAt)}</small></div></div><section className="architecture-card"><div className="architecture-card-heading"><div><span className="eyebrow">扫描范围</span><h3>{project.rootPath}</h3></div><code>{snapshot.contentHash.slice(0, 16)}…</code></div><dl className="architecture-stats-list"><div><dt>语言</dt><dd>{Object.entries(snapshot.coverage.byLanguage).map(([language, count]) => <span key={language} className="tag">{languageLabels[language] ?? language} · {count}</span>)}</dd></div><div><dt>安全过滤</dt><dd>{snapshot.coverage.secretFiles} 秘密文件 · {snapshot.coverage.binaryFiles} 二进制 · {snapshot.coverage.outsideLinks} 越界链接</dd></div><div><dt>Git 变更</dt><dd>{snapshot.gitChangedPaths.length ? snapshot.gitChangedPaths.slice(0, 8).join('、') : '没有记录到变更路径'}</dd></div></dl></section><section className="architecture-card"><div className="architecture-card-heading"><div><span className="eyebrow">覆盖说明</span><h3>识别边界</h3></div></div><p className="muted">静态扫描识别 TS/JS、Go、Python、C# 以及常见构建与容器配置。未知文件与未确认的部署关系会保留在覆盖信息中，生成结果不会把抽样分析宣称为完整调用图。</p><div className="architecture-skip-grid">{Object.entries(snapshot.coverage.ignoredReasons).map(([reason, count]) => <span key={reason}><strong>{count}</strong>{reason}</span>)}</div></section></div>
}

function ModulesPanel({ snapshot }: { snapshot?: ArchitectureSnapshot }) {
  if (!snapshot) return <div className="architecture-empty"><Layers3 size={28}/><strong>暂无模块</strong><span>扫描后会列出语言、版本、入口、依赖文件和证据路径。</span></div>
  return <div className="architecture-module-list">{snapshot.modules.map((module) => <article className="architecture-module-card" key={`${module.language}:${module.rootPath}:${module.name}`}><div className="architecture-module-heading"><div><span className="tag">{languageLabels[module.language] ?? module.language}</span><h3>{module.name}</h3></div>{module.version && <code>{module.version}</code>}</div><p>{module.rootPath}</p>{module.unknown && <div className="architecture-notice warning"><AlertTriangle size={15}/>部分关系由 Agent 推断，仍需检查证据。</div>}<dl><div><dt>入口</dt><dd>{module.entryPoints.length ? module.entryPoints.map((item) => <code key={item}>{item}</code>) : '未识别'}</dd></div><div><dt>依赖</dt><dd>{module.dependencies.length ? module.dependencies.slice(0, 18).join('、') : '未识别'}</dd></div><div><dt>证据</dt><dd>{module.evidencePaths.length ? module.evidencePaths.map((item) => <code key={item}>{item}</code>) : '暂无'}</dd></div></dl></article>)}</div>
}

function EvidencePanel({ analysis, snapshot }: { analysis?: ArchitectureAnalysis; snapshot?: ArchitectureSnapshot }) {
  const evidence = analysis?.evidence ?? []
  return <div className="architecture-evidence-panel"><section className="architecture-card"><div className="architecture-card-heading"><div><span className="eyebrow">代码事实</span><h3>Facts</h3></div><span>{analysis?.facts.length ?? 0} 条</span></div>{analysis?.facts.length ? <div className="architecture-fact-list">{analysis.facts.map((fact) => <article key={fact.id}><strong>{fact.subject}</strong><span>{fact.predicate}：{fact.object}</span><small>证据：{fact.evidence.join('、') || '缺少证据'}</small></article>)}</div> : <p className="muted">当前版本还没有经过分析的代码事实。扫描结果仍可在“文件证据”查看。</p>}</section><section className="architecture-card"><div className="architecture-card-heading"><div><span className="eyebrow">明确标注</span><h3>Inferences</h3></div><span>{analysis?.inferences.length ?? 0} 条</span></div>{analysis?.inferences.length ? <div className="architecture-fact-list">{analysis.inferences.map((item) => <article key={item.id}><strong>{item.statement}</strong><span>置信度 {Math.round(item.confidence * 100)}%</span><small>依据：{item.evidence.join('、') || '缺少证据'}</small></article>)}</div> : <p className="muted">暂无推断。没有证据的部署关系不会在此被伪装成事实。</p>}</section><section className="architecture-card"><div className="architecture-card-heading"><div><span className="eyebrow">可定位引用</span><h3>Evidence</h3></div><span>{evidence.length} 条</span></div>{evidence.length ? <div className="architecture-evidence-list">{evidence.map((item) => <article key={item.id}><div><strong>{item.path}</strong><span>第 {item.lineStart}-{item.lineEnd} 行 · {item.kind}</span></div><code>{item.contentHash}</code></article>)}</div> : <FileEvidenceList snapshot={snapshot}/>}</section></div>
}

function FileEvidenceList({ snapshot }: { snapshot?: ArchitectureSnapshot }) {
  if (!snapshot?.files.length) return <p className="muted">没有可展示的文件证据。</p>
  return <div className="architecture-evidence-list">{snapshot.files.slice(0, 120).map((file) => <article key={file.path}><div><strong>{file.path}</strong><span>第 {file.lineStart}-{file.lineEnd} 行 · {languageLabels[file.language] ?? file.language}{file.entryPoint ? ' · 入口' : ''}</span></div><code>{file.contentHash}</code></article>)}</div>
}

function DiffPanel({ diff, snapshots, selectedId, onSelectBase }: { diff?: ArchitectureDiff; snapshots: ArchitectureSnapshot[]; selectedId: string; onSelectBase: (id: string) => void }) {
  return <div className="architecture-diff-panel"><div className="architecture-diff-controls"><label>比较基线<select aria-label="比较基线" value={diff?.fromSnapshotId ?? ''} onChange={(event) => onSelectBase(event.target.value)}><option value="">自动选择上一版本</option>{snapshots.filter((item) => item.id !== selectedId).map((item) => <option key={item.id} value={item.id}>{formatDate(item.createdAt)} · {item.id.slice(0, 8)}</option>)}</select></label>{diff && <span>新增 {diff.added.length} · 修改 {diff.changed.length} · 删除 {diff.deleted.length}</span>}</div>{!diff || (!diff.added.length && !diff.changed.length && !diff.deleted.length) ? <div className="architecture-empty small"><GitCompare size={25}/><strong>没有文件差异</strong><span>选择另一版本后，这里会列出新增、修改和删除的路径。</span></div> : <div className="architecture-diff-grid"><DiffList title="新增" items={diff.added} tone="added"/><DiffList title="修改" items={diff.changed} tone="changed"/><DiffList title="删除" items={diff.deleted} tone="deleted"/></div>}</div>
}

function DiffList({ title, items, tone }: { title: string; items: string[]; tone: string }) {
  return <section className={`architecture-diff-list ${tone}`}><h3>{title}<span>{items.length}</span></h3>{items.length ? <ul>{items.map((item) => <li key={item}><code>{item}</code></li>)}</ul> : <p>无</p>}</section>
}

function TaskPanel({ tasks, project }: { tasks: ArchitectureTask[]; project: ArchitectureProject }) {
  if (!tasks.length) return <div className="architecture-empty"><Clock3 size={28}/><strong>还没有任务记录</strong><span>扫描、生成和取消操作会保留在这里。</span></div>
  return <div className="architecture-task-list">{tasks.map((task) => <article key={task.id} className={`architecture-task-card ${task.status}`}><div className="architecture-task-icon">{task.status === 'running' || task.status === 'queued' ? <LoaderCircle className="spin" size={17}/> : task.status === 'completed' ? <CheckCircle2 size={17}/> : <CircleAlert size={17}/>}</div><div><strong>{task.kind === 'scan' ? '扫描' : task.kind === 'analysis' ? '架构生成' : task.kind}</strong><span>{taskStatusLabels[task.status] ?? task.status} · {formatDate(task.updatedAt)}</span><small>{task.message || task.errorCode || `代次 ${task.generation}`} · 执行器 {executorLabels[project.execution.executor] ?? project.execution.executor}</small></div><code>{task.id.slice(0, 8)}</code></article>)}</div>
}

function safeFileName(name: string) { return name.replace(/[^\w\-.]+/g, '-') }

function ArtifactsPanel({ project, snapshotId, artifacts }: { project: ArchitectureProject; snapshotId: string; artifacts: ArchitectureArtifact[] }) {
  const [selectedKind, setSelectedKind] = useState('document')
  const [preview, setPreview] = useState<{ kind: string; blob: Blob }>()
  const [previewUrl, setPreviewUrl] = useState('')
  const [error, setError] = useState('')
  const [exportMessage, setExportMessage] = useState('')
  const artifact = artifacts.find((item) => item.kind === selectedKind) ?? artifacts[0]

  useEffect(() => {
    let disposed = false
    setPreview(undefined); setPreviewUrl(''); setError(''); setExportMessage('')
    if (!artifact) return () => { disposed = true }
    void client.downloadArchitectureArtifact(project.id, snapshotId, artifact.kind).then((blob) => { if (!disposed) setPreview({ kind: artifact.kind, blob }) }).catch((cause) => { if (!disposed) setError(errorMessage(cause)) })
    return () => { disposed = true }
  }, [artifact?.kind, project.id, snapshotId])

  useEffect(() => {
    if (!preview || (preview.kind !== 'svg' && preview.kind !== 'png')) return
    const url = URL.createObjectURL(preview.blob); setPreviewUrl(url); return () => URL.revokeObjectURL(url)
  }, [preview])

  async function exportArtifact() {
    if (!artifact || !preview) return
    try {
      const buffer = typeof preview.blob.arrayBuffer === 'function' ? await preview.blob.arrayBuffer() : await new Response(preview.blob).arrayBuffer()
      const data = new Uint8Array(buffer)
      if (typeof window.kah.saveFile === 'function') await window.kah.saveFile(safeFileName(artifact.fileName), data)
      else { const url = URL.createObjectURL(preview.blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = artifact.fileName; anchor.click(); URL.revokeObjectURL(url) }
      setExportMessage(`已准备导出：${artifact.fileName}`)
    } catch (cause) { setError(`导出失败：${errorMessage(cause)}`) }
  }

  return <div className="architecture-artifacts-panel"><div className="architecture-artifact-tabs" role="tablist" aria-label="架构产物类型">{artifacts.map((item) => <button type="button" role="tab" aria-selected={item.kind === selectedKind} className={item.kind === selectedKind ? 'active' : ''} key={item.kind} onClick={() => setSelectedKind(item.kind)}>{item.kind === 'document' ? <FileText size={15}/> : item.kind === 'diagram_json' ? <FileJson size={15}/> : item.kind === 'svg' || item.kind === 'png' ? <Network size={15}/> : <Code2 size={15}/>}<span>{artifactLabels[item.kind] ?? item.kind}</span></button>)}</div>{artifact && <div className="architecture-artifact-heading"><div><strong>{artifact.fileName}</strong><span>{formatBytes(artifact.size)} · {artifact.renderer || '未知渲染器'}</span></div><button type="button" className="button secondary" onClick={() => void exportArtifact()} disabled={!preview}><Download size={15}/>导出 {artifactLabels[artifact.kind] ?? '文件'}</button></div>}{error && <div className="inline-error" role="alert"><CircleAlert size={16}/>{error}</div>}{exportMessage && <div className="architecture-export-status" role="status" aria-live="polite"><CheckCircle2 size={15}/>{exportMessage}</div>}{artifact?.rendererError && <div className="architecture-notice warning" role="status"><AlertTriangle size={16}/><span>PNG 渲染器报告：{artifact.rendererError}。当前文件不会被冒充为 archify PNG。</span></div>}{preview?.kind === 'document' && <div className="architecture-markdown-preview"><ArtifactText blob={preview.blob}/></div>}{preview?.kind === 'diagram_json' && <ArtifactText blob={preview.blob} code/>}{preview?.kind === 'html' && <ArtifactText blob={preview.blob} html/>}{preview?.kind === 'svg' && previewUrl && <div className="architecture-image-preview"><img src={previewUrl} alt="系统架构 SVG 图"/></div>}{preview?.kind === 'png' && previewUrl && <div className="architecture-image-preview"><img src={previewUrl} alt="系统架构 PNG 图"/></div>}{!preview && !error && artifact && <div className="architecture-loading" role="status"><LoaderCircle className="spin" size={17}/>正在读取产物…</div>}{!artifacts.length && <div className="architecture-empty small"><FileText size={25}/><strong>暂无生成产物</strong><span>完成一次架构生成后，Markdown、图描述和图形导出会出现在这里。</span></div>}</div>
}

function ArtifactText({ blob, code = false, html = false }: { blob: Blob; code?: boolean; html?: boolean }) {
  const [text, setText] = useState('')
  useEffect(() => { let mounted = true; const read = typeof blob.text === 'function' ? blob.text() : new Response(blob).text(); void read.then((value) => { if (mounted) setText(value) }); return () => { mounted = false } }, [blob])
  if (html) return <iframe className="architecture-html-preview" title="自包含架构 HTML 预览" sandbox="" srcDoc={text}/>
  if (code) return <pre className="architecture-code-preview"><code>{text}</code></pre>
  return <div className="architecture-markdown-body"><ReactMarkdown>{text}</ReactMarkdown></div>
}

function ArchitectureDetail({ project, selectedSnapshot, snapshots, analysis, diff, artifacts, tab, setTab, onSelectSnapshot, onSelectBase, tasks }: { project: ArchitectureProject; selectedSnapshot?: ArchitectureSnapshot; snapshots: ArchitectureSnapshot[]; analysis?: ArchitectureAnalysis; diff?: ArchitectureDiff; artifacts: ArchitectureArtifact[]; tab: ArchitectureTab; setTab: (tab: ArchitectureTab) => void; onSelectSnapshot: (id: string) => void; onSelectBase: (id: string) => void; tasks: ArchitectureTask[] }) {
  const snapshot = selectedSnapshot
  return <div className="architecture-detail"><div className="architecture-detail-toolbar"><div><span className="eyebrow">当前版本</span><select aria-label="选择架构版本" value={snapshot?.id ?? ''} onChange={(event) => onSelectSnapshot(event.target.value)}><option value="">选择快照</option>{snapshots.map((item) => <option key={item.id} value={item.id}>{formatDate(item.createdAt)} · {item.contentHash.slice(0, 10)}</option>)}</select></div>{snapshot && <span className="status-pill ready">{project.currentAnalysisSnapshotId === snapshot.id ? '当前生成版本' : '扫描版本'}</span>}</div><nav className="architecture-tabs" role="tablist" aria-label="项目架构内容"><button type="button" role="tab" aria-selected={tab === 'overview'} className={tab === 'overview' ? 'active' : ''} onClick={() => setTab('overview')}><Layers3 size={15}/>总览</button><button type="button" role="tab" aria-selected={tab === 'modules'} className={tab === 'modules' ? 'active' : ''} onClick={() => setTab('modules')}><Network size={15}/>模块与依赖</button><button type="button" role="tab" aria-selected={tab === 'evidence'} className={tab === 'evidence' ? 'active' : ''} onClick={() => setTab('evidence')}><FileCode2 size={15}/>代码证据</button><button type="button" role="tab" aria-selected={tab === 'versions'} className={tab === 'versions' ? 'active' : ''} onClick={() => setTab('versions')}><GitCompare size={15}/>版本差异</button><button type="button" role="tab" aria-selected={tab === 'tasks'} className={tab === 'tasks' ? 'active' : ''} onClick={() => setTab('tasks')}><Clock3 size={15}/>任务记录</button><button type="button" role="tab" aria-selected={tab === 'artifacts'} className={tab === 'artifacts' ? 'active' : ''} onClick={() => setTab('artifacts')}><Download size={15}/>文档与图</button></nav><section className="architecture-detail-content" role="tabpanel">{tab === 'overview' && <SnapshotOverview snapshot={snapshot} project={project}/>} {tab === 'modules' && <ModulesPanel snapshot={snapshot}/>} {tab === 'evidence' && <EvidencePanel analysis={analysis} snapshot={snapshot}/>} {tab === 'versions' && <DiffPanel diff={diff} snapshots={snapshots} selectedId={snapshot?.id ?? ''} onSelectBase={onSelectBase}/>} {tab === 'tasks' && <TaskPanel tasks={tasks} project={project}/>} {tab === 'artifacts' && snapshot && <ArtifactsPanel project={project} snapshotId={snapshot.id} artifacts={artifacts}/>} {tab === 'artifacts' && !snapshot && <div className="architecture-empty"><Download size={28}/><strong>请选择一个版本</strong><span>架构产物按快照保存，选择版本后可以预览和导出。</span></div>}</section></div>
}

export function ArchitectureWorkspace({ libraries }: { libraries: Library[] }) {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState('')
  const [selectedSnapshotId, setSelectedSnapshotId] = useState('')
  const [baseSnapshotId, setBaseSnapshotId] = useState('')
  const [tab, setTab] = useState<ArchitectureTab>('overview')
  const [dialog, setDialog] = useState<{ open: boolean; project?: ArchitectureProject }>({ open: false })
  const projects = useQuery({ queryKey: ['architecture-projects'], queryFn: () => client.architectureProjects(), refetchInterval: 5000 })
  const providers = useQuery({ queryKey: ['providers'], queryFn: client.providers, staleTime: 30_000 })
  const project = projects.data?.find((item) => item.id === selectedId) ?? projects.data?.[0]
  const tasks = useQuery({ queryKey: ['architecture-tasks', project?.id], queryFn: () => client.architectureTasks(project!.id), enabled: Boolean(project), refetchInterval: project ? 1500 : false })
  const activeTask = tasks.data?.find((item) => ['queued', 'running', 'cancellation_requested'].includes(item.status))
  const snapshots = useQuery({ queryKey: ['architecture-snapshots', project?.id], queryFn: () => client.architectureSnapshots(project!.id), enabled: Boolean(project), refetchInterval: project?.analysisStatus === 'stale' ? 3000 : false })
  const snapshotId = selectedSnapshotId || project?.lastSnapshotId || snapshots.data?.[0]?.id || ''
  const snapshot = useQuery({ queryKey: ['architecture-snapshot', project?.id, snapshotId], queryFn: () => client.architectureSnapshot(project!.id, snapshotId), enabled: Boolean(project && snapshotId) })
  const analysis = useQuery({ queryKey: ['architecture-analysis', project?.id, snapshotId], queryFn: async () => {
    try { return await client.architectureAnalysis(project!.id, snapshotId) }
    catch (cause) {
      // A scan-only snapshot is still useful for file evidence and version diffs.
      // Treat its missing analysis as an empty state instead of blocking the rest
      // of the snapshot view with a top-level query error.
      if (cause instanceof ApiError && cause.status === 404) return null
      throw cause
    }
  }, enabled: Boolean(project && snapshotId) })
  const artifacts = useQuery({ queryKey: ['architecture-artifacts', project?.id, snapshotId], queryFn: () => client.architectureArtifacts(project!.id, snapshotId), enabled: Boolean(project && snapshotId) })
  const diff = useQuery({ queryKey: ['architecture-diff', project?.id, snapshotId, baseSnapshotId], queryFn: () => client.architectureDiff(project!.id, snapshotId, baseSnapshotId || undefined), enabled: Boolean(project && snapshotId && tab === 'versions') })
  const scan = useMutation({ mutationFn: () => client.scanArchitectureProject(project!.id), onSuccess: () => invalidateArchitecture() })
  const generate = useMutation({ mutationFn: () => client.generateArchitectureProject(project!.id), onSuccess: () => invalidateArchitecture() })
  const cancel = useMutation({ mutationFn: (taskId: string) => client.cancelArchitectureTask(taskId), onSuccess: () => invalidateArchitecture() })
  const chain = useMutation({ mutationFn: async () => { const scanTask = await client.scanArchitectureProject(project!.id); await waitForArchitectureTask(project!.id, scanTask.id); return client.generateArchitectureProject(project!.id) }, onSuccess: () => invalidateArchitecture() })
  const remove = useMutation({ mutationFn: (id: string) => client.deleteArchitectureProject(id), onSuccess: (_result, id) => { if (selectedId === id) setSelectedId(''); void invalidateArchitecture() } })
  const busy = Boolean(activeTask) || scan.isPending || generate.isPending || chain.isPending || cancel.isPending
  const actionError = scan.error || generate.error || chain.error || cancel.error || remove.error

  useEffect(() => { if (!selectedId && projects.data?.[0]) setSelectedId(projects.data[0].id) }, [projects.data, selectedId])
  useEffect(() => { if (project?.lastSnapshotId) setSelectedSnapshotId((current) => current || project.lastSnapshotId || '') }, [project?.lastSnapshotId])
  useEffect(() => { if (snapshotId && !snapshots.data?.some((item) => item.id === snapshotId) && snapshots.data?.length) setSelectedSnapshotId(snapshots.data[0].id) }, [snapshotId, snapshots.data])

  async function invalidateArchitecture() {
    await Promise.all([queryClient.invalidateQueries({ queryKey: ['architecture-projects'] }), queryClient.invalidateQueries({ queryKey: ['architecture-tasks', project?.id] }), queryClient.invalidateQueries({ queryKey: ['architecture-snapshots', project?.id] }), queryClient.invalidateQueries({ queryKey: ['architecture-snapshot', project?.id] }), queryClient.invalidateQueries({ queryKey: ['architecture-analysis', project?.id] }), queryClient.invalidateQueries({ queryKey: ['architecture-artifacts', project?.id] })])
  }

  function onDelete(item: ArchitectureProject) { if (window.confirm(`确定从项目架构列表移除“${item.name}”？历史架构版本和产物会保留在应用数据中。`)) remove.mutate(item.id) }
  function onRetry() { if (!project || busy) return; if (project.analysisStatus === 'stale' || !project.lastSnapshotId) scan.mutate(); else generate.mutate() }
  function onSaved(saved: ArchitectureProject) { queryClient.setQueryData<ArchitectureProject[]>(['architecture-projects'], (current) => current ? current.some((item) => item.id === saved.id) ? current.map((item) => item.id === saved.id ? saved : item) : [saved, ...current] : [saved]); setSelectedId(saved.id) }

  const projectsError = projects.error
  const dataError = snapshots.error || snapshot.error || analysis.error || artifacts.error || diff.error || tasks.error
  const lastSuccessfulSnapshot = snapshots.data?.find((item) => item.id === project?.currentAnalysisSnapshotId) ?? snapshots.data?.[0]
  return <section className="architecture-workspace" aria-label="项目架构">
    <header className="workspace-header architecture-workspace-header"><div className="workspace-title-block"><span className="eyebrow">代码理解与版本化证据</span><div className="workspace-title-line"><h1>项目架构</h1><Layers3 size={17}/></div><p className="architecture-header-subtitle">选择一个本地项目，生成技术栈、模块关系、数据流和可回溯代码证据。</p></div><div className="header-actions"><button type="button" className="button secondary" onClick={() => void projects.refetch()} disabled={projects.isFetching}><RefreshCw className={projects.isFetching ? 'spin' : ''} size={15}/>刷新</button><button type="button" className="button primary" onClick={() => setDialog({ open: true })}><Plus size={15}/>添加项目</button></div></header>
    {actionError && <div className="architecture-top-error" role="alert"><CircleAlert size={17}/>{errorMessage(actionError)}<button type="button" className="text-button" onClick={() => { scan.reset(); generate.reset(); chain.reset(); cancel.reset(); remove.reset() }}><X size={14}/>关闭提示</button></div>}
    <div className="architecture-main-grid"><ProjectList projects={projects.data ?? []} selectedId={project?.id ?? ''} onSelect={(id) => { setSelectedId(id); setSelectedSnapshotId(''); setTab('overview') }} onCreate={() => setDialog({ open: true })} onEdit={(item) => setDialog({ open: true, project: item })} onDelete={onDelete} loading={projects.isLoading} error={projectsError}/>{project ? <main className="architecture-project-main"><div className="architecture-project-header"><div><span className="eyebrow">{project.rootPath}</span><h2>{project.name}</h2><p>{project.description || '没有项目说明'} · 关联知识库 {libraries.find((item) => item.id === project.libraryId)?.name ?? project.libraryId} · 最后成功：{lastSuccessfulSnapshot ? formatDate(lastSuccessfulSnapshot.createdAt) : '尚未生成'}</p></div><div className="architecture-action-row"><button type="button" className="button secondary" onClick={() => scan.mutate()} disabled={busy}><ScanLine size={15}/>{scan.isPending ? '正在排队…' : '扫描'}</button><button type="button" className="button secondary" onClick={() => generate.mutate()} disabled={busy || !project.lastSnapshotId}><Play size={15}/>{generate.isPending ? '正在排队…' : '生成/更新'}</button><button type="button" className="button primary" onClick={() => chain.mutate()} disabled={busy}><Play size={15}/>{chain.isPending ? '扫描并生成中…' : '扫描并生成'}</button><button type="button" className="icon-button" aria-label={`编辑项目 ${project.name}`} title="编辑项目配置" onClick={() => setDialog({ open: true, project })}><Settings2 size={17}/></button></div></div><ArchitectureProgress task={activeTask} project={project} onCancel={() => activeTask && cancel.mutate(activeTask.id)} onRetry={onRetry}/>{project.analysisStatus === 'stale' && <div className="architecture-notice stale" role="status"><AlertTriangle size={16}/>检测到源项目变化；上一个成功版本仍可查看，生成成功前不会覆盖它。</div>}{dataError && <div className="architecture-top-error" role="alert"><CircleAlert size={16}/><span>{queryErrorMessage(dataError)}</span><button type="button" className="text-button" onClick={() => void invalidateArchitecture()}><RefreshCw size={14}/>重试读取</button></div>}{snapshot.isLoading && snapshotId && <div className="architecture-loading" role="status"><LoaderCircle className="spin" size={17}/>正在读取架构版本…</div>}{!snapshot.isLoading && <ArchitectureDetail project={project} selectedSnapshot={snapshot.data} snapshots={snapshots.data ?? []} analysis={analysis.data ?? undefined} diff={diff.data} artifacts={artifacts.data ?? []} tab={tab} setTab={setTab} onSelectSnapshot={(id) => { setSelectedSnapshotId(id); setBaseSnapshotId('') }} onSelectBase={setBaseSnapshotId} tasks={tasks.data ?? []}/>}</main> : <div className="architecture-empty large"><Layers3 size={34}/><strong>选择或添加一个项目</strong><span>项目架构与知识库保持独立版本，生成内容通过现有审核流程后再进入正式知识。</span><button type="button" className="button primary" onClick={() => setDialog({ open: true })}><Plus size={15}/>添加项目架构</button></div>}</div>
    <ProjectDialog open={dialog.open} onOpenChange={(open) => setDialog((current) => ({ ...current, open }))} project={dialog.project} libraries={libraries} providers={(providers.data ?? []).map((provider) => ({ id: provider.id, name: provider.name, local: provider.local }))} onSaved={onSaved}/>
  </section>
}

async function waitForArchitectureTask(projectId: string, taskId: string) {
  for (let attempt = 0; attempt < 1200; attempt += 1) {
    const tasks = await client.architectureTasks(projectId)
    const task = tasks.find((item) => item.id === taskId)
    if (task?.status === 'completed') return task
    if (task?.status === 'failed' || task?.status === 'cancelled') throw new Error(task.message || '扫描任务未完成，生成已停止。')
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('扫描任务等待超时，生成已停止。')
}
