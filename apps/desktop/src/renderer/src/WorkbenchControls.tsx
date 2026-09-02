import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Activity, Ban, CircleAlert, LoaderCircle, MoreHorizontal, RefreshCw, RotateCcw, Search, X } from 'lucide-react'
import type { Job } from './types'

export interface WorkbenchAction {
  id: string
  label: string
  keywords?: string
  shortcut?: string
  danger?: boolean
  hidden?: boolean
  disabledReason?: string
  run: () => void | Promise<void>
}

export function MoreActions({ actions, label = '更多操作' }: { actions: WorkbenchAction[]; label?: string }) {
  const visible = actions.filter((action) => !action.hidden)
  if (!visible.length) return null
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" className="icon-button" aria-label={label} title={label}><MoreHorizontal size={17}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="action-menu" sideOffset={6} align="end" collisionPadding={10}>{visible.map((action) => <DropdownMenu.Item key={action.id} className={`action-menu-item ${action.danger ? 'danger' : ''}`} disabled={Boolean(action.disabledReason)} title={action.disabledReason} onSelect={() => void action.run()}><span>{action.label}</span>{action.disabledReason ? <small>{action.disabledReason}</small> : action.shortcut ? <kbd>{action.shortcut}</kbd> : null}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

/**
 * The same action definitions can be attached to a row's native context menu.
 * Keeping the menu in the renderer (instead of using a browser alert) preserves
 * keyboard focus and lets disabled reasons remain visible for mouse users.
 */
export function ContextActions({ actions, children }: { actions: WorkbenchAction[]; children: ReactNode }) {
  const [point, setPoint] = useState<{ x: number; y: number }>()
  useEffect(() => {
    const close = () => setPoint(undefined)
    window.addEventListener('pointerdown', close)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
    return () => {
      window.removeEventListener('pointerdown', close)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', close, true)
    }
  }, [])
  function open(event: React.MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    const width = 230
    const height = Math.min(340, actions.length * 44 + 12)
    setPoint({ x: Math.max(8, Math.min(event.clientX, window.innerWidth - width - 8)), y: Math.max(8, Math.min(event.clientY, window.innerHeight - height - 8)) })
  }
  return <div className="context-action-host" onContextMenu={open}>{children}{point && <div className="context-action-menu" role="menu" style={{ left: point.x, top: point.y }} onPointerDown={(event) => event.stopPropagation()} onContextMenu={(event) => { event.preventDefault(); event.stopPropagation() }}>{actions.filter((action) => !action.hidden).map((action) => <button type="button" role="menuitem" key={action.id} className={`context-action-item ${action.danger ? 'danger' : ''}`} disabled={Boolean(action.disabledReason)} title={action.disabledReason} onClick={() => { setPoint(undefined); if (!action.disabledReason) void action.run() }}><span>{action.label}</span>{action.disabledReason ? <small>{action.disabledReason}</small> : action.shortcut ? <kbd>{action.shortcut}</kbd> : null}</button>)}</div>}</div>
}

export interface ConfirmRequest { title: string; description: string; confirmLabel?: string; danger?: boolean; onConfirm: () => void | Promise<void> }

export function ConfirmDialog({ request, onClose }: { request?: ConfirmRequest; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { setError('') }, [request])
  async function confirm() {
    if (!request) return
    setBusy(true)
    try { await request.onConfirm(); onClose() } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) } finally { setBusy(false) }
  }
  return <Dialog.Root open={Boolean(request)} onOpenChange={(open) => !open && !busy && onClose()}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="confirm-dialog"><Dialog.Title>{request?.title}</Dialog.Title><Dialog.Description>{request?.description}</Dialog.Description>{error && <p className="form-error" role="alert">{error}</p>}<div className="dialog-actions"><button type="button" className="button ghost" onClick={onClose} disabled={busy}>取消</button><button type="button" className={`button ${request?.danger ? 'danger' : 'primary'}`} onClick={() => void confirm()} disabled={busy}>{busy && <LoaderCircle className="spin" size={15}/>} {busy ? '正在处理…' : request?.confirmLabel ?? '确认'}</button></div></Dialog.Content></Dialog.Portal></Dialog.Root>
}

export function CommandPalette({ open, onOpenChange, actions }: { open: boolean; onOpenChange: (open: boolean) => void; actions: WorkbenchAction[] }) {
  const [query, setQuery] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => { if (open) { setQuery(''); requestAnimationFrame(() => inputRef.current?.focus()) } }, [open])
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return actions.filter((item) => !item.hidden && (!needle || `${item.label} ${item.keywords ?? ''}`.toLowerCase().includes(needle)))
  }, [actions, query])
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content id="command-palette" className="command-dialog"><Dialog.Title className="sr-only">功能面板</Dialog.Title><div className="command-search"><Search size={18}/><input ref={inputRef} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入功能或操作名称…" aria-label="筛选命令"/><kbd>Esc</kbd></div><div className="command-list" role="listbox" aria-label="可用功能">{visible.map((action) => <button type="button" role="option" aria-disabled={Boolean(action.disabledReason)} key={action.id} className="command-item" title={action.disabledReason} onClick={() => { if (action.disabledReason) return; onOpenChange(false); void action.run() }}><span><strong>{action.label}</strong>{action.disabledReason && <small>{action.disabledReason}</small>}</span>{action.shortcut && <kbd>{action.shortcut}</kbd>}</button>)}{!visible.length && <p className="command-empty">没有匹配的功能</p>}</div></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function jobName(job: Job) {
  return ({ file_import: '文件导入', url_import: '网页导入', skill_import: 'Skill 导入', source_watch_scan: '目录扫描', document_reindex: '文档重新索引', index_rebuild: '索引重建', knowledge_summarize: '知识总结', knowledge_review: '知识审核', knowledge_publish: '知识发布', kah_knowledge_review: 'KAH 知识审核', kah_knowledge_publish: 'KAH 知识发布', backup: '备份' } as Record<string, string>)[job.kind] ?? job.kind
}

export function ActivityCenter({ open, onOpenChange, jobs, onCancel, onRetry, busyId }: { open: boolean; onOpenChange: (open: boolean) => void; jobs: Job[]; onCancel: (job: Job) => void; onRetry: (job: Job) => void; busyId?: string }) {
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay activity-overlay"/><Dialog.Content id="activity-center" className="activity-center"><div className="activity-heading"><div><Dialog.Title>活动中心</Dialog.Title><Dialog.Description>导入、扫描、索引、审核与备份任务会保留在这里。</Dialog.Description></div><Dialog.Close asChild><button type="button" className="icon-button" aria-label="关闭活动中心"><X size={18}/></button></Dialog.Close></div><div className="activity-list" aria-live="polite">{jobs.map((job) => <article className={`activity-row ${job.status}`} key={job.id}><span className="activity-icon">{job.status === 'running' || job.status === 'queued' || job.status === 'cancellation_requested' ? <LoaderCircle className="spin" size={17}/> : job.status === 'failed' ? <CircleAlert size={17}/> : job.status === 'cancelled' ? <Ban size={17}/> : <Activity size={17}/>}</span><div><strong>{jobName(job)}</strong><span>{job.message || job.status}</span><small>{new Date(job.updatedAt).toLocaleString()} · {Math.round(job.progress * 100)}%</small></div><div className="activity-actions">{job.canCancel && <button type="button" className="button ghost" onClick={() => onCancel(job)} disabled={busyId === job.id}><Ban size={14}/>取消</button>}{job.canRetry && <button type="button" className="button secondary" onClick={() => onRetry(job)} disabled={busyId === job.id}><RefreshCw size={14}/>重试</button>}</div></article>)}{!jobs.length && <div className="activity-empty"><RotateCcw size={24}/><strong>还没有活动记录</strong><span>执行导入、扫描或索引后会显示任务历史。</span></div>}</div></Dialog.Content></Dialog.Portal></Dialog.Root>
}

export function UndoToast({ message, onUndo, onDismiss }: { message: string; onUndo?: () => void | Promise<void>; onDismiss: () => void }) {
  useEffect(() => { const timer = window.setTimeout(onDismiss, 7000); return () => window.clearTimeout(timer) }, [message, onDismiss])
  return <div className="undo-toast" role="status"><span>{message}</span>{onUndo && <button type="button" onClick={() => void onUndo()}>撤销</button>}<button type="button" className="icon-button" aria-label="关闭通知" onClick={onDismiss}><X size={14}/></button></div>
}
