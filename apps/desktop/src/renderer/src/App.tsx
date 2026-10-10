import { OcrPluginSettings } from './OcrPluginSettings'
import { UpdateNotice, UpdatesSettings, useDesktopUpdates } from './UpdatesSettings'
import type { UpdateState } from '../../shared/updates'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as Dialog from '@radix-ui/react-dialog'
import * as Tabs from '@radix-ui/react-tabs'
import CodeMirror from '@uiw/react-codemirror'
import { markdown } from '@codemirror/lang-markdown'
import ReactMarkdown from 'react-markdown'
import {
  Activity, Archive, BookOpen, Bot, CheckCircle2, ChevronRight, CircleAlert, Clock3, Command, Database,
  File, FileCode2, FilePlus2, FolderArchive, FolderSearch2, Import, KeyRound, Layers3, Library, Link2, ListFilter,
  LoaderCircle, Menu, Monitor, Moon, Network, Palette, PanelLeftClose, PanelLeftOpen, PanelRight, Plus, RefreshCw, Save, Search, Settings, ShieldCheck,
  Pencil, Sparkles, Star, Sun, Tag, Trash2, X
} from 'lucide-react'
import { client, getRuntime } from './api'
import { SkillMappingDetailPane, SkillMappingsWorkspace } from './SkillMappings'
import { RelationshipGraphInspector, RelationshipGraphWorkspace, type GraphSelection } from './RelationshipGraph'
import { ArchitectureWorkspace } from './ArchitectureWorkspace'
import { RetrievalModelSettings } from './RetrievalModelSettings'
import { RetrievalSearchPanel } from './RetrievalSearchPanel'
import { ActivityCenter, CommandPalette, ConfirmDialog, ContextActions, MoreActions, UndoToast, type ConfirmRequest, type WorkbenchAction } from './WorkbenchControls'
import { canonicalDocumentPath, isExternalMarkdownLink, resolveMarkdownDocumentPath } from './markdownLinks'
import { useUI } from './store'
import type { AgentToken, ArchitectureProject, Document, DocumentBatchRequest, Job, KAHSubmission, KnowledgeDirectoryEntry, KnowledgeRevision, Library as LibraryType, Provider, RetrievalSearchResponse, SavedSearch, Skill, SourceWatch, TrashEntry, VirtualFolder } from './types'

function IconButton({ label, children, onClick, active = false, pressed, controls, disabled = false, busy = false }: { label: string; children: React.ReactNode; onClick: () => void; active?: boolean; pressed?: boolean; controls?: string; disabled?: boolean; busy?: boolean }) {
  return <button type="button" className={`icon-button ${active ? 'active' : ''}`} aria-label={label} aria-pressed={pressed} aria-controls={controls} aria-busy={busy || undefined} title={label} onClick={onClick} disabled={disabled || busy}>{children}</button>
}
export function MarkdownContent({ content, onLink }: { content: string; onLink?: (href: string) => void }) {
  return <div className="markdown-body"><ReactMarkdown components={{
    a: ({ href, children, ...props }) => <a {...props} href={href} onClick={(event) => {
      if (!href || !onLink) return
      event.preventDefault()
      onLink(href)
    }}>{children}</a>
  }}>{content}</ReactMarkdown></div>
}


function Empty({ icon, title, text }: { icon: React.ReactNode; title: string; text: string }) {
  return <div className="empty"><div className="empty-icon">{icon}</div><strong>{title}</strong><p>{text}</p></div>
}

function GlobalBottomBar({ version, activeSection, activeJobs, backendError, searchInfo, skillsView, trashItemCount, commandOpen, activityOpen, onOpenCommands, onOpenActivity }: { version: string; activeSection: 'knowledge'|'graph'|'architecture'|'skills'|'review'|'trash'; activeJobs: Job[]; backendError: string; searchInfo: string; skillsView: 'skills'|'mappings'; trashItemCount: number; commandOpen: boolean; activityOpen: boolean; onOpenCommands: () => void; onOpenActivity: () => void }) {
  const sectionLabel = ({ knowledge: '知识管理', graph: '关系图谱', architecture: '项目架构', skills: 'Skills', review: '待审核', trash: '回收站' } as const)[activeSection]
  const activityCount = activeJobs.length
  const sectionStatus = activeSection === 'knowledge'
    ? (activityCount ? `${activityCount} 个任务正在处理` : '索引队列空闲')
    : activeSection === 'graph'
      ? '百分比布局已与内容版本分离'
    : activeSection === 'architecture'
      ? (activityCount ? `${activityCount} 个任务正在处理` : '代码证据与架构版本可追溯')
    : activeSection === 'review'
      ? '审核通过前不会写入正式索引'
      : activeSection === 'skills'
        ? (skillsView === 'skills' ? 'Skill 目录已就绪' : '映射状态由外部目录验证')
        : '回收站不会自动过期'
  const sectionMeta = activeSection === 'knowledge'
    ? (searchInfo || '证据优先 · 本地优先')
    : activeSection === 'graph'
      ? '稳定知识 · 可审计关系'
    : activeSection === 'architecture'
      ? '生成内容通过审核后进入正式知识'
    : activeSection === 'review'
      ? '审核记录可追溯'
      : activeSection === 'skills'
        ? (skillsView === 'skills' ? '标准入口 · 按需加载' : '真实目录软链接 · 不复制内容')
        : `${trashItemCount} 个项目`
  return <footer className="global-bottom-bar" aria-label="全局底栏">
    <div className="global-bottom-status" role="status" aria-live="polite">
      <span className={`status-dot ${backendError ? 'error' : ''}`} aria-hidden="true" />
      <span>{backendError ? '后台服务异常' : '本地服务已连接'}</span>
      <span className="global-bottom-separator" aria-hidden="true" />
      <span>{sectionLabel}</span>
      <span className="global-bottom-separator" aria-hidden="true" />
      <span>{sectionStatus}</span>
    </div>
    <div className="global-bottom-right">
      <div className="global-bottom-meta"><span>{sectionMeta}</span><span className="global-bottom-separator" aria-hidden="true" /><span>v{version}</span></div>
      <nav className="global-bottom-actions" aria-label="全局功能">
        <button type="button" className={`global-bottom-button ${commandOpen ? 'active' : ''}`} aria-label="打开功能面板" aria-haspopup="dialog" aria-expanded={commandOpen} aria-controls="command-palette" onClick={onOpenCommands}>
          <Command size={16} aria-hidden="true" /><span>功能面板</span><kbd>Ctrl ⇧ P</kbd>
        </button>
        <button type="button" className={`global-bottom-button ${activityOpen ? 'active' : ''}`} aria-label="打开活动中心" aria-haspopup="dialog" aria-expanded={activityOpen} aria-controls="activity-center" onClick={onOpenActivity}>
          <Activity size={16} aria-hidden="true" /><span>活动中心</span>{activityCount > 0 && <span className="global-bottom-badge" aria-label={`${activityCount} 个进行中任务`}>{activityCount}</span>}
        </button>
      </nav>
    </div>
  </footer>
}

const DETAIL_PANE_WIDTH_STORAGE_KEY = 'kah.detail-pane-width'
const SIDEBAR_COLLAPSED_STORAGE_KEY = 'kah.sidebar-collapsed'
const DETAIL_PANE_DEFAULT_WIDTH = 420
const DETAIL_PANE_MIN_WIDTH = 330
const DETAIL_PANE_MAX_WIDTH = 720

function clampDetailPaneWidth(value: number) {
  return Math.min(DETAIL_PANE_MAX_WIDTH, Math.max(DETAIL_PANE_MIN_WIDTH, value))
}

function detailPaneMaximumForViewport() {
  if (typeof window === 'undefined') return DETAIL_PANE_MAX_WIDTH
  const sidebarWidth = window.innerWidth <= 1180 ? 220 : 250
  const primaryWorkspaceMinimum = 450
  return Math.max(DETAIL_PANE_MIN_WIDTH, Math.min(DETAIL_PANE_MAX_WIDTH, window.innerWidth - sidebarWidth - primaryWorkspaceMinimum))
}

function clampDetailPaneWidthForViewport(value: number) {
  return Math.min(detailPaneMaximumForViewport(), Math.max(DETAIL_PANE_MIN_WIDTH, value))
}

function readDetailPaneWidth() {
  if (typeof window === 'undefined') return DETAIL_PANE_DEFAULT_WIDTH
  try {
    const storedValue = window.localStorage.getItem(DETAIL_PANE_WIDTH_STORAGE_KEY)
    if (!storedValue) return clampDetailPaneWidthForViewport(DETAIL_PANE_DEFAULT_WIDTH)
    const stored = Number(storedValue)
    return Number.isFinite(stored) ? clampDetailPaneWidthForViewport(stored) : clampDetailPaneWidthForViewport(DETAIL_PANE_DEFAULT_WIDTH)
  } catch {
    return clampDetailPaneWidthForViewport(DETAIL_PANE_DEFAULT_WIDTH)
  }
}

function readSidebarCollapsed() {
  if (typeof window === 'undefined') return false
  try {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === 'true'
  } catch {
    return false
  }
}

const LIBRARY_ICON_OPTIONS = [
  { value: 'library', label: '知识库', icon: Library },
  { value: 'book-open', label: '阅读', icon: BookOpen },
  { value: 'bot', label: 'Agent', icon: Bot },
  { value: 'folder', label: '资料', icon: FolderArchive },
  { value: 'sparkles', label: '智能', icon: Sparkles },
  { value: 'database', label: '数据', icon: Database },
  { value: 'archive', label: '归档', icon: Archive },
  { value: 'tag', label: '标签', icon: Tag },
] as const

const LIBRARY_COLOR_OPTIONS = [
  { value: 'jade', label: '青玉' },
  { value: 'blue', label: '蓝色' },
  { value: 'amber', label: '琥珀' },
  { value: 'violet', label: '紫罗兰' },
  { value: 'rose', label: '玫瑰' },
  { value: 'slate', label: '石板' },
] as const

function LibraryIcon({ icon, size = 17 }: { icon?: string; size?: number }) {
  const option = LIBRARY_ICON_OPTIONS.find((item) => item.value === icon) ?? LIBRARY_ICON_OPTIONS[0]
  const Icon = option.icon
  return <Icon size={size} aria-hidden="true" />
}

function libraryColorClass(color?: string) {
  return LIBRARY_COLOR_OPTIONS.some((item) => item.value === color) ? `library-color-${color}` : 'library-color-jade'
}

function parseLibraryTags(value: string) {
  return Array.from(new Set(value.split(/[,，]/).map((item) => item.trim()).filter(Boolean))).slice(0, 30)
}

function isKnowledgePayload(value: unknown): value is KnowledgeRevision['payload'] {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<KnowledgeRevision['payload']>
  if (typeof candidate.title !== 'string' || typeof candidate.description !== 'string' || typeof candidate.type !== 'string') return false
  if (candidate.language !== 'zh-CN' && candidate.language !== 'en') return false
  if (!Array.isArray(candidate.sections) || candidate.sections.some((section) => !section || typeof section.id !== 'string' || typeof section.heading !== 'string' || typeof section.content !== 'string')) return false
  if (candidate.sources !== undefined && (!Array.isArray(candidate.sources) || candidate.sources.some((source) => !source || typeof source.id !== 'string' || typeof source.resource !== 'string'))) return false
  if (candidate.relations !== undefined && (!Array.isArray(candidate.relations) || candidate.relations.some((relation) => !relation || typeof relation.type !== 'string' || typeof relation.target !== 'string' || (relation.target_revision !== undefined && typeof relation.target_revision !== 'number')))) return false
  return true
}

export function App() {
  const updateState = useDesktopUpdates()
  const queryClient = useQueryClient()
  const ui = useUI()
  const [selectedLibrary, setSelectedLibrary] = useState<string>('')
  const [selectedFolder, setSelectedFolder] = useState('')
  const [favoritesOnly, setFavoritesOnly] = useState(false)
  const [watchOpen, setWatchOpen] = useState(false)
  const [watchPath, setWatchPath] = useState('')
  const [watchError, setWatchError] = useState('')
  const [selectedDocument, setSelectedDocument] = useState<string>('')
  const [selectedDocumentIds, setSelectedDocumentIds] = useState<string[]>([])
  const [query, setQuery] = useState('')
  const [searchResults, setSearchResults] = useState<KnowledgeDirectoryEntry[] | null>(null)
  const [retrievalResponse, setRetrievalResponse] = useState<RetrievalSearchResponse | null>(null)
  const [retrievalFilters, setRetrievalFilters] = useState<{ sourceType: 'all'|'knowledge'|'raw'|'architecture'; libraryIds?: string[]; projectIds?: string[] }>({ sourceType: 'all' })
  const [searchInfo, setSearchInfo] = useState('')
  const [knowledgeView, setKnowledgeView] = useState<'sources'|'knowledge'>('sources')
  const [selectedKnowledge, setSelectedKnowledge] = useState('')
  const [libraryDetailsOpen, setLibraryDetailsOpen] = useState(false)
  const [filterOpen, setFilterOpen] = useState(false)
  const [filterTags, setFilterTags] = useState<string[]>([])
  const [markdownLinkError, setMarkdownLinkError] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [createLibraryOpen, setCreateLibraryOpen] = useState(false)
  const [activityOpen, setActivityOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)
  const [watchManagerOpen, setWatchManagerOpen] = useState(false)
  const [savedSearchOpen, setSavedSearchOpen] = useState(false)
  const [saveSearchDialogOpen, setSaveSearchDialogOpen] = useState(false)
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest>()
  const [undo, setUndo] = useState<{ message: string; run?: () => Promise<void> }>()
  const [urlValue, setUrlValue] = useState('')
  const [urlOpen, setUrlOpen] = useState(false)
  const [backendError, setBackendError] = useState('')
  const [activeSection, setActiveSection] = useState<'knowledge'|'graph'|'architecture'|'skills'|'review'|'trash'>('knowledge')
  const [graphSelection, setGraphSelection] = useState<GraphSelection>()
  const [selectedSkill, setSelectedSkill] = useState('')
  const [skillsView, setSkillsView] = useState<'skills'|'mappings'>('skills')
  const [selectedMappingTarget, setSelectedMappingTarget] = useState('')
  const [selectedSubmission, setSelectedSubmission] = useState('')
  const [knowledgeDraftDirty, setKnowledgeDraftDirty] = useState(false)
  const [documentDraftDirty, setDocumentDraftDirty] = useState(false)
  const [detailPaneWidth, setDetailPaneWidth] = useState(readDetailPaneWidth)
  const [detailPaneResizing, setDetailPaneResizing] = useState(false)
  const [detailPaneOpen, setDetailPaneOpen] = useState(true)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readSidebarCollapsed)
  const [folderError, setFolderError] = useState('')
  const searchInput = useRef<HTMLInputElement>(null)
  const detailPaneRef = useRef<HTMLElement>(null)
  const resizeRightEdge = useRef<number | null>(null)
  const selectionAnchor = useRef<number | null>(null)

  const hasUnsavedDraft = knowledgeDraftDirty || documentDraftDirty
  function confirmUnsavedDraftLeave() {
    if (!hasUnsavedDraft) return true
    const kind = knowledgeDraftDirty && documentDraftDirty ? '知识修订和文档正文' : knowledgeDraftDirty ? '知识修订' : '文档正文'
    if (!window.confirm(`${kind}有未保存修改。确定放弃这些修改并继续？`)) return false
    setKnowledgeDraftDirty(false)
    setDocumentDraftDirty(false)
    return true
  }
  function installUpdate() {
    if (!window.confirm('确认重启 AKB 并安装已下载的更新？')) return
    if (!confirmUnsavedDraftLeave()) return
    void window.kah.updates.install()
  }
  useEffect(() => {
    if (!hasUnsavedDraft) return
    const listener = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', listener)
    return () => window.removeEventListener('beforeunload', listener)
  }, [hasUnsavedDraft])

  const allDocuments = useQuery({ queryKey: ['documents-all', selectedLibrary], queryFn: () => client.documents(selectedLibrary), enabled: Boolean(selectedLibrary) })
  const libraries = useQuery({ queryKey: ['libraries'], queryFn: client.libraries })
  const folders = useQuery({ queryKey: ['folders', selectedLibrary], queryFn: () => client.folders(selectedLibrary), enabled: Boolean(selectedLibrary) })
  const documents = useQuery({ queryKey: ['documents', selectedLibrary, selectedFolder, favoritesOnly], queryFn: () => client.documents(selectedLibrary || undefined, selectedFolder || undefined, favoritesOnly), refetchInterval: 5000 })
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: client.jobs, refetchInterval: 2500 })
  const savedSearches = useQuery({ queryKey: ['saved-searches'], queryFn: client.savedSearches })
  const detail = useQuery({ queryKey: ['document', selectedDocument], queryFn: () => client.document(selectedDocument), enabled: Boolean(selectedDocument) })
  const knowledgeDetail = useQuery({ queryKey: ['knowledge', selectedKnowledge], queryFn: () => client.knowledge(selectedKnowledge), enabled: Boolean(selectedKnowledge) })
  const knowledgeDirectory = useQuery({ queryKey: ['knowledge-directory', selectedLibrary], queryFn: () => client.knowledgeSearch('', selectedLibrary ? [selectedLibrary] : []), enabled: knowledgeView === 'knowledge', refetchInterval: 5000 })
  const retrievalProjects = useQuery({ queryKey: ['retrieval-projects'], queryFn: () => client.architectureProjects(), enabled: Boolean(retrievalResponse), staleTime: 30_000, retry: false })
  const trash = useQuery({ queryKey: ['trash'], queryFn: () => client.trash?.() ?? Promise.resolve([]), enabled: activeSection === 'trash' })

  useEffect(() => { if (!selectedLibrary && libraries.data?.[0]) setSelectedLibrary(libraries.data[0].id) }, [libraries.data, selectedLibrary])
  useEffect(() => {
    const dark = ui.theme === 'dark' || (ui.theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
    document.documentElement.dataset.density = ui.density
    void window.kah.setTitleBarTheme(dark ? 'dark' : 'light')
  }, [ui.theme, ui.density])
  useEffect(() => window.kah.onBackendExit(() => setBackendError('后台服务意外停止。请重新启动应用以恢复完整功能。')), [])
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); searchInput.current?.focus() }
      if ((event.ctrlKey || event.metaKey) && event.key === ',') { event.preventDefault(); setSettingsOpen(true) }
      if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 'p') { event.preventDefault(); setCommandOpen(true) }
    }
    addEventListener('keydown', listener); return () => removeEventListener('keydown', listener)
  }, [])
  useEffect(() => {
    try { window.localStorage.setItem(DETAIL_PANE_WIDTH_STORAGE_KEY, String(detailPaneWidth)) } catch { /* local storage is optional */ }
  }, [detailPaneWidth])
  useEffect(() => {
    try { window.localStorage.setItem(SIDEBAR_COLLAPSED_STORAGE_KEY, String(sidebarCollapsed)) } catch { /* local storage is optional */ }
  }, [sidebarCollapsed])
  useEffect(() => {
    if (!detailPaneResizing) return
    const handlePointerMove = (event: PointerEvent) => {
      const rightEdge = resizeRightEdge.current ?? window.innerWidth
      setDetailPaneWidth(clampDetailPaneWidthForViewport(rightEdge - event.clientX))
    }
    const stopResizing = () => {
      resizeRightEdge.current = null
      setDetailPaneResizing(false)
    }
    window.addEventListener('pointermove', handlePointerMove)
    window.addEventListener('pointerup', stopResizing)
    window.addEventListener('pointercancel', stopResizing)
    return () => {
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerup', stopResizing)
      window.removeEventListener('pointercancel', stopResizing)
    }
  }, [detailPaneResizing])
  useEffect(() => {
    const constrainToViewport = () => setDetailPaneWidth((current) => clampDetailPaneWidthForViewport(current))
    window.addEventListener('resize', constrainToViewport)
    return () => window.removeEventListener('resize', constrainToViewport)
  }, [])
  useEffect(() => {
    document.body.classList.toggle('is-resizing-detail-pane', detailPaneResizing)
    return () => document.body.classList.remove('is-resizing-detail-pane')
  }, [detailPaneResizing])

  const createLibrary = useMutation({ mutationFn: ({ name, description }: { name: string; description: string }) => client.createLibrary(name, description), onSuccess: (library) => { queryClient.setQueryData<LibraryType[]>(['libraries'], (current) => current ? [...current, library] : [library]); void queryClient.invalidateQueries({ queryKey: ['libraries'] }); setCreateLibraryOpen(false); setSelectedLibrary(library.id); setSelectedFolder(''); setFavoritesOnly(false); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(true); setDetailPaneOpen(true) } })
  const createFolder = useMutation({ mutationFn: async () => { if (!selectedLibrary) throw new Error('请先选择知识库'); const name = window.prompt('目录名称'); if (!name?.trim()) return undefined; return client.createFolder(selectedLibrary, name.trim()) }, onSuccess: () => { setFolderError(''); void folders.refetch() }, onError: (error) => setFolderError(error instanceof Error ? error.message : String(error)) })
  const createWatch = useMutation({ mutationFn: () => { if (!selectedLibrary || !watchPath.trim()) throw new Error('请选择知识库并选择监视目录'); return client.createWatch(selectedLibrary, watchPath.trim()) }, onSuccess: () => { setWatchOpen(false); setWatchPath(''); setWatchError(''); void queryClient.invalidateQueries({ queryKey: ['jobs'] }); void queryClient.invalidateQueries({ queryKey: ['watches', selectedLibrary] }) }, onError: (error) => setWatchError(error instanceof Error ? error.message : String(error)) })
  const importFiles = useMutation({ mutationFn: async () => { if (!selectedLibrary) throw new Error('请先选择知识库'); const paths = await window.kah.selectFiles(); if (!paths.length) return []; return client.importFiles(selectedLibrary, paths) }, onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['jobs'] }); queryClient.invalidateQueries({ queryKey: ['documents'] }); queryClient.invalidateQueries({ queryKey: ['submissions'] }) } })
  const importUrl = useMutation({ mutationFn: async () => { if (!selectedLibrary || !urlValue.trim()) throw new Error('请选择知识库并输入 URL'); return client.importUrl(selectedLibrary, urlValue.trim()) }, onSuccess: () => { setUrlOpen(false); setUrlValue(''); queryClient.invalidateQueries({ queryKey: ['jobs'] }); queryClient.invalidateQueries({ queryKey: ['documents'] }); queryClient.invalidateQueries({ queryKey: ['submissions'] }) } })
  const search = useMutation({
    mutationFn: ({ text, libraryId }: { text: string; libraryId: string }) => client.knowledgeSearch(text, libraryId ? [libraryId] : []),
    onSuccess: (response) => { setSearchResults(response.results); setSearchInfo(`${response.results.length} 个知识体`) }
  })
  const retrievalSearch = useMutation({
    mutationFn: ({ text, filters }: { text: string; filters: typeof retrievalFilters }) => {
      if (typeof client.retrievalSearch !== 'function') throw new Error('当前桌面后端没有提供统一检索接口')
      return client.retrievalSearch({ query: text, filters, topK: 20, retrievalMode: 'hybrid' })
    },
    onSuccess: (response) => { setRetrievalResponse(response); setSearchResults(null); setSearchInfo(`${response.results.length} 个检索结果${response.keywordOnly || response.degraded ? ' · 关键词降级' : ''}`) }
  })
  const saveSearch = useMutation({ mutationFn: (name: string) => client.createSavedSearch(name, query, selectedLibrary ? [selectedLibrary] : [], filterTags, knowledgeView, { tags: filterTags, favorite: favoritesOnly, tagMode: 'all' }, { field: 'updatedAt', direction: 'desc' }), onSuccess: () => { setSaveSearchDialogOpen(false); void queryClient.invalidateQueries({ queryKey: ['saved-searches'] }) } })
  const batchDocuments = useMutation({ mutationFn: client.batchDocuments, onSuccess: () => { setSelectedDocumentIds([]); selectionAnchor.current = null; void queryClient.invalidateQueries({ queryKey: ['documents'] }); void queryClient.invalidateQueries({ queryKey: ['documents-all'] }); void queryClient.invalidateQueries({ queryKey: ['jobs'] }); void queryClient.invalidateQueries({ queryKey: ['trash'] }) } })
  const jobAction = useMutation({ mutationFn: ({ id, action }: { id: string; action: 'cancel'|'retry' }) => action === 'cancel' ? client.cancelJob(id) : client.retryJob(id), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['jobs'] }) })

  const availableTags = useMemo(() => Array.from(new Set([
    ...(allDocuments.data ?? []).flatMap((item) => item.tags),
    ...(knowledgeDirectory.data?.results ?? []).flatMap((item) => item.tags),
    ...(searchResults ?? []).flatMap((item) => item.tags),
  ])).sort((left, right) => left.localeCompare(right)), [allDocuments.data, knowledgeDirectory.data?.results, searchResults])
  const shownDocuments = useMemo(() => (documents.data ?? []).filter((item) => !filterTags.length || filterTags.every((tag) => item.tags.includes(tag))), [documents.data, filterTags])
  const shownKnowledge = useMemo(() => (searchResults ?? knowledgeDirectory.data?.results ?? []).filter((item) => !filterTags.length || filterTags.every((tag) => item.tags.includes(tag))), [knowledgeDirectory.data?.results, searchResults, filterTags])
  const selectedLibraryValue = libraries.data?.find((item) => item.id === selectedLibrary)
  const activeJobs = jobs.data?.filter((job) => job.status === 'running' || job.status === 'queued' || job.status === 'cancellation_requested') ?? []
  const pipelineJobs = jobs.data?.filter((job) => ['file_import', 'url_import', 'knowledge_summarize', 'kah_knowledge_review', 'kah_knowledge_publish'].includes(job.kind)) ?? []

  useEffect(() => { setSelectedDocumentIds([]); selectionAnchor.current = null }, [selectedLibrary, selectedFolder, favoritesOnly])

  function selectDocument(document: Document, index: number, event: React.MouseEvent) {
    if (document.id !== selectedDocument && !confirmUnsavedDraftLeave()) return
    setSelectedDocument(document.id)
    setSelectedKnowledge('')
    setLibraryDetailsOpen(false)
    if (event.shiftKey && selectionAnchor.current !== null) {
      const [start, end] = [selectionAnchor.current, index].sort((a, b) => a - b)
      setSelectedDocumentIds(shownDocuments.slice(start, end + 1).map((item) => item.id))
    } else if (event.ctrlKey || event.metaKey) {
      setSelectedDocumentIds((current) => {
        const seed = current.length ? current : selectedDocument ? [selectedDocument] : []
        return seed.includes(document.id) ? seed.filter((id) => id !== document.id) : [...seed, document.id]
      })
      selectionAnchor.current = index
    } else {
      setSelectedDocumentIds([])
      selectionAnchor.current = index
    }
  }

  function trashDocuments(ids: string[]) {
    const selectedIds = Array.from(new Set(ids))
    if (!selectedIds.length) return
    setConfirmRequest({ title: `将 ${selectedIds.length} 个文档移入回收站？`, description: '文档会立即从检索和索引中排除，可在回收站恢复。', confirmLabel: '移入回收站', danger: true, onConfirm: async () => {
      await batchDocuments.mutateAsync({ documentIds: selectedIds, action: 'trash' })
      if (selectedIds.includes(selectedDocument)) setSelectedDocument('')
      setUndo({ message: `已将 ${selectedIds.length} 个文档移入回收站`, run: async () => { await Promise.all(selectedIds.map((id) => client.restoreTrash('document', id))); await queryClient.invalidateQueries({ queryKey: ['documents'] }); await queryClient.invalidateQueries({ queryKey: ['trash'] }) } })
    } })
  }
  function trashSelectedDocuments() { trashDocuments(selectedDocumentIds) }

  function runSearch(value = query, libraryId = selectedLibrary) {
    const normalized = value.trim()
    setQuery(value)
    setSelectedKnowledge('')
    setLibraryDetailsOpen(false)
    if (!normalized) { setSearchResults(null); setRetrievalResponse(null); setSearchInfo(''); return }
    setKnowledgeView('knowledge')
    setSelectedDocument('')
    const filters = { ...retrievalFilters, libraryIds: libraryId ? [libraryId] : undefined }
    setRetrievalFilters(filters)
    setTimeout(() => {
      if (typeof client.retrievalSearch === 'function') retrievalSearch.mutate({ text: normalized, filters })
      else search.mutate({ text: normalized, libraryId })
    }, 0)
  }

  function switchKnowledgeView(view: 'sources'|'knowledge') {
    if (view !== knowledgeView && !confirmUnsavedDraftLeave()) return
    setKnowledgeView(view)
    setSearchResults(null)
    setRetrievalResponse(null)
    setSearchInfo('')
    setLibraryDetailsOpen(false)
    if (view === 'sources') setSelectedKnowledge('')
    else setSelectedDocument('')
  }
  async function chooseWatchDirectory() {
    setWatchError('')
    try {
      const path = await window.kah.selectDirectory()
      if (path) setWatchPath(path)
    } catch (error) {
      setWatchError(error instanceof Error ? error.message : String(error))
    }
  }
  async function openMarkdownLink(href: string, source?: Pick<Document, 'sourcePath' | 'libraryId'>) {
    const target = href.trim()
    if (!target) return
    setMarkdownLinkError('')
    try {
      if (isExternalMarkdownLink(target)) {
        await window.kah.openExternal(target)
        return
      }
      if (target.startsWith('#')) return
      const resolved = resolveMarkdownDocumentPath(source?.sourcePath, target)
      let candidates = allDocuments.data ?? []
      if (source?.libraryId && !candidates.some((doc) => doc.libraryId === source.libraryId)) {
        candidates = await client.documents(source.libraryId)
      }
      const canonicalTarget = canonicalDocumentPath(resolved)
      const linked = candidates.find((doc) => doc.sourcePath && canonicalDocumentPath(doc.sourcePath) === canonicalTarget)
      if (linked) {
        if (!confirmUnsavedDraftLeave()) return
        setActiveSection('knowledge')
        setSelectedKnowledge('')
        setSelectedDocument(linked.id)
        setSearchResults(null)
        return
      }
      const error = await window.kah.openPath(resolved)
      if (error) setMarkdownLinkError('无法打开 Markdown 链接：' + error)
    } catch (error) {
      setMarkdownLinkError('无法打开 Markdown 链接：' + (error instanceof Error ? error.message : String(error)))
    }
  }



  function beginDetailPaneResize(event: React.PointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return
    event.preventDefault()
    resizeRightEdge.current = detailPaneRef.current?.getBoundingClientRect().right ?? window.innerWidth
    try { event.currentTarget.setPointerCapture?.(event.pointerId) } catch { /* the pointer may have ended before capture */ }
    setDetailPaneResizing(true)
  }
  function handleDetailPaneResizeKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const step = event.shiftKey ? 48 : 16
    if (event.key === 'ArrowLeft') {
      event.preventDefault()
      setDetailPaneWidth((width) => clampDetailPaneWidthForViewport(width + step))
    } else if (event.key === 'ArrowRight') {
      event.preventDefault()
      setDetailPaneWidth((width) => clampDetailPaneWidthForViewport(width - step))
    } else if (event.key === 'Home') {
      event.preventDefault()
      setDetailPaneWidth(DETAIL_PANE_MIN_WIDTH)
    } else if (event.key === 'End') {
      event.preventDefault()
      setDetailPaneWidth(detailPaneMaximumForViewport())
    }
  }

  return <div className={`app-shell ${detailPaneOpen ? '' : 'detail-pane-collapsed'} ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`} style={{ '--detail-pane-width': `${detailPaneWidth}px` } as React.CSSProperties}>
    <div className="window-titlebar" aria-hidden="true"><div className="window-titlebar-brand"><span className="window-titlebar-mark"><Sparkles size={14}/></span><span>Knowledge Agent Hub</span></div></div>
    <aside className="sidebar" aria-label="知识库导航" data-collapsed={sidebarCollapsed || undefined}>
      <nav id="primary-sidebar-navigation" className="sidebar-scroll" aria-label="知识库导航">
        <div className="nav-heading workspace-heading">
          <span>工作区</span>
          <button type="button" className="sidebar-toggle" aria-label={sidebarCollapsed ? '展开左侧栏' : '折叠左侧栏'} aria-expanded={!sidebarCollapsed} aria-controls="primary-sidebar-navigation" title={sidebarCollapsed ? '展开左侧栏' : '折叠左侧栏'} onClick={() => setSidebarCollapsed((value) => !value)}>
            {sidebarCollapsed ? <PanelLeftOpen size={18} aria-hidden="true"/> : <PanelLeftClose size={18} aria-hidden="true"/>}
          </button>
        </div>
        <button type="button" className={`workspace-nav-row ${activeSection === 'knowledge' ? 'active' : ''}`} aria-label="知识管理文档、网页与检索" title="知识管理" aria-current={activeSection === 'knowledge' ? 'page' : undefined} onClick={() => { if (confirmUnsavedDraftLeave()) setActiveSection('knowledge') }}>
          <span className="workspace-nav-icon"><Library size={17}/></span><span className="workspace-nav-label">知识管理</span>
        </button>
        <button type="button" className={`workspace-nav-row ${activeSection === 'graph' ? 'active' : ''}`} aria-label="关系图谱稳定知识与证据关系" title="关系图谱" aria-current={activeSection === 'graph' ? 'page' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('graph'); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(false); setDetailPaneOpen(true) }}>
          <span className="workspace-nav-icon"><Network size={17}/></span><span className="workspace-nav-label">关系图谱</span>
        </button>
        <button type="button" className={`workspace-nav-row ${activeSection === 'architecture' ? 'active' : ''}`} aria-label="项目架构代码理解、技术栈与架构图" title="项目架构" aria-current={activeSection === 'architecture' ? 'page' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('architecture'); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(false); setDetailPaneOpen(false) }}>
          <span className="workspace-nav-icon"><Layers3 size={17}/></span><span className="workspace-nav-label">项目架构</span>
        </button>
        <button type="button" className={`workspace-nav-row ${activeSection === 'skills' ? 'active' : ''}`} aria-label="SkillsAgent 能力包" title="Skills" aria-current={activeSection === 'skills' ? 'page' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('skills'); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(false) }}>
          <span className="workspace-nav-icon"><FolderArchive size={17}/></span><span className="workspace-nav-label">Skills</span>
        </button>
        <button type="button" className={`workspace-nav-row ${activeSection === 'review' ? 'active' : ''}`} aria-label="待审核 Agent 提交的知识" title="待审核" aria-current={activeSection === 'review' ? 'page' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('review'); setSelectedDocument(''); setSelectedKnowledge(''); setSelectedSubmission(''); setLibraryDetailsOpen(false) }}><span className="workspace-nav-icon"><Clock3 size={17}/></span><span className="workspace-nav-label">待审核</span></button>
        <button type="button" className={`workspace-nav-row ${activeSection === 'trash' ? 'active' : ''}`} aria-label="回收站，恢复或永久清理" title="回收站" aria-current={activeSection === 'trash' ? 'page' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('trash'); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(false); setDetailPaneOpen(false) }}><span className="workspace-nav-icon"><Trash2 size={17}/></span><span className="workspace-nav-label">回收站</span></button>
        {(activeSection === 'knowledge' || activeSection === 'graph') && <>
        <div className="nav-heading"><span>知识库</span><IconButton label="新建知识库" onClick={() => setCreateLibraryOpen(true)} busy={createLibrary.isPending}><Plus size={17} /></IconButton></div>
        {libraries.isLoading && <div className="sidebar-loading"><LoaderCircle className="spin" size={16}/> 正在载入</div>}
        {createLibrary.error && <p className="sidebar-error" role="alert">{(createLibrary.error as Error).message}</p>}
        {libraries.data?.map((library) => <div key={library.id} className="library-row-shell">
          <button type="button" className={`library-row ${selectedLibrary === library.id ? 'active' : ''}`} aria-label={library.name} title={library.name} aria-current={selectedLibrary === library.id ? 'true' : undefined} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setSelectedLibrary(library.id); setSelectedFolder(''); setFavoritesOnly(false); setSelectedDocument(''); setSelectedKnowledge(''); setSearchResults(null); setRetrievalResponse(null); setSearchInfo(''); setFilterTags([]); setLibraryDetailsOpen(false) }}>
            <span className={`library-icon ${libraryColorClass(library.color)}`}><LibraryIcon icon={library.icon}/></span><span className="library-copy"><span className="library-name-line"><strong>{library.name}</strong>{library.favorite && <Star size={12} fill="currentColor" aria-label="已标记重要"/>}</span><small>{library.description || '本地知识库'}</small></span><ChevronRight size={15}/>
          </button>
          {activeSection === 'knowledge' && <IconButton label={`编辑知识库 ${library.name}`} active={libraryDetailsOpen && selectedLibrary === library.id} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setSelectedLibrary(library.id); setSelectedFolder(''); setFavoritesOnly(false); setSelectedDocument(''); setSelectedKnowledge(''); setLibraryDetailsOpen(true); setDetailPaneOpen(true) }}><Pencil size={14}/></IconButton>}
        </div>)}
        {activeSection === 'knowledge' && <>
        <div className="nav-heading saved-heading"><span>整理</span></div>
        <button type="button" className={`saved-row ${favoritesOnly && !selectedFolder ? 'active' : ''}`} onClick={() => { setFavoritesOnly(true); setSelectedFolder(''); setSearchResults(null); setLibraryDetailsOpen(false) }}><Star size={16}/><span>我的收藏</span></button>
        {selectedLibrary && <FolderTree libraryId={selectedLibrary} folders={folders.data ?? []} selectedId={selectedFolder} onSelect={(id) => { setSelectedFolder(id); setFavoritesOnly(false); setSearchResults(null); setLibraryDetailsOpen(false) }}/>}
        {selectedLibrary && <button type="button" className="text-button sidebar-add" onClick={() => setWatchManagerOpen(true)}><Activity size={14}/>监视目录管理</button>}
        <div className="nav-heading saved-heading"><span>固定搜索</span></div>
        {!savedSearches.data?.length && <p className="sidebar-hint">搜索后可保存为快捷入口</p>}
        {savedSearches.data?.map((saved: SavedSearch) => <div className="saved-row-shell" key={saved.id}><button type="button" className="saved-row" onClick={() => { if (!confirmUnsavedDraftLeave()) return; const libraryId = saved.libraryIds[0] || selectedLibrary; if (saved.libraryIds[0]) setSelectedLibrary(saved.libraryIds[0]); setSelectedFolder(''); setFavoritesOnly(saved.filters?.favorite ?? false); setFilterTags(saved.filters?.tags ?? saved.tags); setKnowledgeView(saved.view ?? 'knowledge'); runSearch(saved.query, libraryId) }}><FolderSearch2 size={16}/><span>{saved.name}</span></button><MoreActions label={`${saved.name} 的更多操作`} actions={[{ id: 'edit', label: '重命名或更新条件', run: () => setSavedSearchOpen(true) }, { id: 'copy', label: '复制固定搜索', run: async () => { await client.createSavedSearch(`${saved.name} 副本`, saved.query, saved.libraryIds, saved.tags, saved.view, saved.filters, saved.sort); void queryClient.invalidateQueries({ queryKey: ['saved-searches'] }) } }, { id: 'delete', label: '删除固定搜索', danger: true, run: () => setConfirmRequest({ title: `删除“${saved.name}”？`, description: '只会删除这个快捷入口，不会删除任何知识或文档。', danger: true, onConfirm: async () => { await client.deleteSavedSearch(saved.id); await queryClient.invalidateQueries({ queryKey: ['saved-searches'] }) } }) }]}/></div>)}
        </>}
        </>}
      </nav>
      <div className="sidebar-footer">
        <button type="button" className="footer-button" aria-label="设置" title="设置" onClick={() => setSettingsOpen(true)}><Settings size={18}/><span>设置</span><kbd>Ctrl ,</kbd></button>
      </div>
    </aside>

    <main className="workspace">
      {activeSection === 'architecture' ? <ArchitectureWorkspace libraries={libraries.data ?? []} /> : activeSection === 'skills' ? <SkillsWorkspace libraries={libraries.data ?? []} selectedId={selectedSkill} onSelect={setSelectedSkill} view={skillsView} onViewChange={setSkillsView} selectedTargetId={selectedMappingTarget} onTargetSelect={setSelectedMappingTarget} /> : activeSection === 'review' ? <ReviewWorkspace libraries={libraries.data ?? []} selectedId={selectedSubmission} onSelect={setSelectedSubmission} /> : activeSection === 'trash' ? <TrashWorkspace items={trash.data ?? []} loading={trash.isLoading} error={trash.error as Error | null} onChanged={() => { void trash.refetch(); void libraries.refetch(); void documents.refetch() }} onConfirm={setConfirmRequest}/> : activeSection === 'graph' ? <RelationshipGraphWorkspace libraryId={selectedLibrary} libraryName={selectedLibraryValue?.name ?? ''} onSelectionChange={setGraphSelection} onOpenKnowledge={() => { setKnowledgeView('knowledge'); setActiveSection('knowledge') }} onCreateLibrary={() => setCreateLibraryOpen(true)}/> : <>
      {backendError && <div role="alert" className="alert-banner"><CircleAlert size={17}/>{backendError}</div>}
      <header className="workspace-header">
        <div className="workspace-title-block"><span className="eyebrow">当前知识库</span><div className="workspace-title-line"><h1>{selectedLibraryValue?.name ?? '全部知识'}</h1>{selectedLibraryValue?.favorite && <Star size={15} fill="currentColor" aria-label="已标记重要"/>}</div>{selectedLibraryValue?.tags?.length ? <div className="workspace-tags">{selectedLibraryValue.tags.slice(0, 4).map((tag) => <span className="tag" key={tag}><Tag size={11}/>{tag}</span>)}</div> : null}</div>
        <div className="header-actions">
          <IconButton label="切换密度" onClick={ui.toggleDensity}><Menu size={18}/></IconButton>
          <IconButton label="知识库详情" active={libraryDetailsOpen} pressed={libraryDetailsOpen} controls="library-detail-pane" disabled={!selectedLibraryValue} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setLibraryDetailsOpen((value) => !value); setSelectedDocument(''); setSelectedKnowledge(''); setDetailPaneOpen(true) }}><Pencil size={17}/></IconButton>
          <button type="button" className="button secondary" onClick={() => { setUrlOpen((value) => !value) }} disabled={!selectedLibrary}><Link2 size={17}/> 网页</button>
          <button type="button" className="button secondary" aria-expanded={watchOpen} aria-controls="watch-directory-form" onClick={() => { setWatchOpen((value) => !value); setWatchError('') }} disabled={!selectedLibrary || createWatch.isPending}><FolderSearch2 size={17}/> {createWatch.isPending ? '正在保存…' : '监视目录'}</button>
          <button type="button" className="button primary" onClick={() => importFiles.mutate()} disabled={!selectedLibrary || importFiles.isPending}><Import size={17}/> {importFiles.isPending ? '正在导入…' : '导入'}</button>
        </div>
      </header>
      <div className="search-row">
        <Search size={19}/><input ref={searchInput} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') runSearch() }} placeholder="搜索知识、问题或精确短语…" aria-label="搜索知识" />
        {query && <IconButton label="清空搜索" onClick={() => { if (!confirmUnsavedDraftLeave()) return; setQuery(''); setSearchResults(null); setRetrievalResponse(null); setSelectedKnowledge('') }}><X size={16}/></IconButton>}
        <kbd>Ctrl K</kbd><button type="button" className="search-submit" onClick={() => runSearch()} disabled={!query.trim() || search.isPending || retrievalSearch.isPending}>{search.isPending || retrievalSearch.isPending ? <LoaderCircle className="spin" size={17}/> : '搜索'}</button>
      </div>
      {urlOpen && <form className="url-import" onSubmit={(event) => { event.preventDefault(); importUrl.mutate() }}><Link2 size={18}/><label htmlFor="import-url">网页地址</label><input id="import-url" type="url" value={urlValue} onChange={(event) => setUrlValue(event.target.value)} placeholder="https://example.com/article" autoFocus/><button className="button primary" type="submit" disabled={importUrl.isPending || !urlValue.trim()}>{importUrl.isPending ? '正在保存…' : '保存网页'}</button><IconButton label="关闭" onClick={() => setUrlOpen(false)}><X size={17}/></IconButton>{importUrl.error && <span className="form-error" role="alert">{(importUrl.error as Error).message}</span>}</form>}
      {watchOpen && <form id="watch-directory-form" className="watch-directory-form" onSubmit={(event) => { event.preventDefault(); createWatch.mutate() }}><div className="watch-form-heading"><div><strong>添加监视目录</strong><span>目录中的新增或变更文件会进入导入队列。</span></div><IconButton label="关闭监视目录设置" onClick={() => { setWatchOpen(false); setWatchError('') }}><X size={17}/></IconButton></div><div className="watch-form-controls"><label htmlFor="watch-directory-path">目录路径</label><input id="watch-directory-path" value={watchPath} readOnly placeholder="选择一个本地目录"/><button type="button" className="button secondary" onClick={() => void chooseWatchDirectory()}>选择目录</button><button type="submit" className="button primary" disabled={createWatch.isPending || !watchPath.trim()}>{createWatch.isPending ? '正在添加…' : '添加监视'}</button></div>{watchError && <p className="form-error" role="alert">{watchError}</p>}</form>}
      <div className="content-heading"><div><h2>{knowledgeView === 'knowledge' ? (searchResults || retrievalResponse ? '统一检索结果' : '成型知识') : '来源文档'}</h2><span>{knowledgeView === 'knowledge' ? (retrievalResponse ? `${retrievalResponse.results.length} 个检索结果` : `${shownKnowledge.length} 个知识体`) : `${shownDocuments.length} 个项目`}</span></div><div className="content-tools"><div className="segmented-control" role="group" aria-label="知识库内容视图"><button type="button" className={knowledgeView === 'sources' ? 'active' : ''} aria-pressed={knowledgeView === 'sources'} onClick={() => switchKnowledgeView('sources')}>来源文档</button><button type="button" className={knowledgeView === 'knowledge' ? 'active' : ''} aria-pressed={knowledgeView === 'knowledge'} onClick={() => switchKnowledgeView('knowledge')}>成型知识</button></div>{(searchResults || retrievalResponse) && <button type="button" className="text-button" onClick={() => setSaveSearchDialogOpen(true)} disabled={saveSearch.isPending}><Save size={15}/> {saveSearch.isPending ? '正在固定…' : '固定搜索'}</button>}<IconButton label="刷新" onClick={() => { void documents.refetch(); void knowledgeDirectory.refetch(); void jobs.refetch(); if (retrievalResponse) retrievalSearch.mutate({ text: query, filters: retrievalFilters }) }}><RefreshCw size={17}/></IconButton><IconButton label="筛选" active={filterOpen || filterTags.length > 0} pressed={filterOpen} controls="content-filter-panel" onClick={() => setFilterOpen((value) => !value)}><ListFilter size={17}/></IconButton></div></div>
      {knowledgeView === 'sources' && <BatchToolbar selectedIds={selectedDocumentIds} folders={folders.data ?? []} busy={batchDocuments.isPending} onClear={() => setSelectedDocumentIds([])} onAction={(action) => batchDocuments.mutate({ documentIds: selectedDocumentIds, ...action })} onTrash={trashSelectedDocuments}/>}
      {filterOpen && <div id="content-filter-panel" className="filter-panel" role="region" aria-label="按标签筛选"><div className="filter-panel-heading"><strong>按标签筛选</strong><button type="button" className="text-button" onClick={() => setFilterTags([])} disabled={!filterTags.length}>清除筛选</button></div>{availableTags.length ? <div className="filter-options">{availableTags.map((tag) => <label key={tag}><input type="checkbox" checked={filterTags.includes(tag)} onChange={(event) => setFilterTags((current) => event.target.checked ? [...current, tag] : current.filter((item) => item !== tag))}/><span>{tag}</span></label>)}</div> : <p className="muted">当前内容还没有标签。</p>}</div>}
      <PipelineStatus jobs={pipelineJobs}/>
       {markdownLinkError && <div role="alert" className="inline-error"><CircleAlert size={18}/>{markdownLinkError}</div>}
      <section className="result-list" aria-live="polite">
        {(documents.error || knowledgeDirectory.error || search.error || retrievalSearch.error || importFiles.error || importUrl.error || saveSearch.error) && <div role="alert" className="inline-error"><CircleAlert size={18}/>{String((documents.error || knowledgeDirectory.error || search.error || retrievalSearch.error || importFiles.error || importUrl.error || saveSearch.error)?.message)}</div>}
        {knowledgeView === 'knowledge' ? retrievalResponse ? <RetrievalSearchPanel query={query} response={retrievalResponse} filters={retrievalFilters} loading={retrievalSearch.isPending} error={retrievalSearch.error} libraries={libraries.data ?? []} selectedLibrary={selectedLibrary} projects={retrievalProjects.data ?? []} onSearch={({ query: nextQuery, filters }) => { setQuery(nextQuery); setRetrievalFilters(filters); retrievalSearch.mutate({ text: nextQuery, filters }) }} onRetry={() => retrievalSearch.mutate({ text: query, filters: retrievalFilters })} /> : shownKnowledge.map((item, index) => <button type="button" className={`result-card ${selectedKnowledge.startsWith(item.uri) ? 'active' : ''}`} key={item.uri} onClick={() => { if (!confirmUnsavedDraftLeave()) return; setSelectedKnowledge(`${item.uri}?revision=${item.revision}`); setSelectedDocument(''); setLibraryDetailsOpen(false) }}><span className="rank">{String(index + 1).padStart(2, '0')}</span><div className="result-body"><div className="result-title"><strong>{item.title}</strong><span>{item.type}{item.subtype ? ` · ${item.subtype}` : ''}</span></div><p>{item.description}</p><div className="result-meta"><span>{item.language} · r{item.revision}</span><span>{item.trust === 'verified' ? '已验证' : '未验证'}{item.flags.length ? ` · ${item.flags.join('、')}` : ''}</span></div></div></button>) : shownDocuments.map((document, index) => <DocumentRow key={document.id} document={document} active={selectedDocument === document.id} selected={selectedDocumentIds.includes(document.id)} onSelect={(event) => selectDocument(document, index, event)} onReindex={() => batchDocuments.mutate({ documentIds: selectedDocumentIds.includes(document.id) ? selectedDocumentIds : [document.id], action: 'reindex' })} onTrash={() => trashDocuments(selectedDocumentIds.includes(document.id) ? selectedDocumentIds : [document.id])} />)}
        {knowledgeView === 'sources' && !documents.isLoading && shownDocuments.length === 0 && <Empty icon={<FilePlus2 size={25}/>} title={filterTags.length ? '没有符合筛选的来源文档' : '还没有来源文档'} text={filterTags.length ? '清除部分标签筛选，或导入带有这些标签的资料。' : '导入文件或保存网页后，会在此保留原始资料。'} />}
        {knowledgeView === 'knowledge' && !retrievalResponse && !knowledgeDirectory.isLoading && shownKnowledge.length === 0 && <Empty icon={<BookOpen size={25}/>} title={filterTags.length ? '没有符合筛选的成型知识' : searchResults ? '没有找到匹配内容' : '还没有成型知识'} text={filterTags.length ? '清除部分标签筛选，或选择其他标签。' : searchResults ? '尝试更换关键词，或检查所选知识库的索引状态。' : '来源资料经整理、审核并发布后，会在这里供你浏览。'} />}
      </section>
      </>}
    </main>

    <aside id="knowledge-detail-pane" ref={detailPaneRef} className={`detail-pane ${detailPaneResizing ? 'resizing' : ''}`} aria-label={detailPaneOpen ? (activeSection === 'architecture' ? '项目架构详情' : activeSection === 'skills' ? (skillsView === 'mappings' ? '外部映射详情' : 'Skill 详情') : activeSection === 'review' ? '待审核知识详情' : activeSection === 'graph' ? '关系图谱详情' : libraryDetailsOpen ? '知识库详情' : knowledgeView === 'knowledge' ? '成型知识详情' : '来源文档预览') : '已折叠的详情栏'}>
      {detailPaneOpen ? <><div className={`detail-resizer ${detailPaneResizing ? 'active' : ''}`} role="separator" tabIndex={0} aria-label="调整详情栏宽度" aria-orientation="vertical" aria-valuemin={DETAIL_PANE_MIN_WIDTH} aria-valuemax={detailPaneMaximumForViewport()} aria-valuenow={detailPaneWidth} aria-valuetext={`${detailPaneWidth} 像素`} title="拖动调整详情栏宽度；使用左右方向键微调" onPointerDown={beginDetailPaneResize} onKeyDown={handleDetailPaneResizeKeyDown}><span aria-hidden="true" /></div>
      <div className="detail-pane-toolbar"><strong>{libraryDetailsOpen ? '知识库详情' : '详情'}</strong><IconButton label="收起详情栏" pressed={true} controls="knowledge-detail-pane" onClick={() => { if (confirmUnsavedDraftLeave()) setDetailPaneOpen(false) }}><PanelRight size={18}/></IconButton></div>
      <div className="detail-pane-content">{activeSection === 'architecture' ? <div className="architecture-side-note"><Layers3 size={24}/><strong>项目架构</strong><p>文档、图形、版本差异和代码证据都按快照保存。生成成功后，可从主工作区导出文件。</p></div> : activeSection === 'skills' ? skillsView === 'mappings' ? <SkillMappingDetailPane targetId={selectedMappingTarget} onDeleted={() => setSelectedMappingTarget('')} /> : <SkillDetailPane skillId={selectedSkill} libraries={libraries.data ?? []} onDeleted={() => setSelectedSkill('')} /> : activeSection === 'review' ? <ReviewDetailPane selectedId={selectedSubmission} libraries={libraries.data ?? []} onOpenDocument={(id) => { if (!confirmUnsavedDraftLeave()) return; setActiveSection('knowledge'); setSelectedSubmission(''); setSelectedKnowledge(''); setSelectedDocument(id) }} /> : activeSection === 'graph' ? <RelationshipGraphInspector selection={graphSelection} onOpenKnowledge={(uri, revision) => { setSelectedKnowledge(`${uri}?revision=${revision ?? 1}`); setSelectedDocument(''); setKnowledgeView('knowledge'); setActiveSection('knowledge') }} onOpenDocument={(id) => { setSelectedKnowledge(''); setSelectedDocument(id); setKnowledgeView('sources'); setActiveSection('knowledge') }}/> : libraryDetailsOpen ? <LibraryDetailPane library={selectedLibraryValue} onSaved={(library) => { queryClient.setQueryData<LibraryType[]>(['libraries'], (current) => current?.map((item) => item.id === library.id ? library : item)); setSelectedLibrary(library.id) }} onTrash={(library) => setConfirmRequest({ title: `将“${library.name}”移入回收站？`, description: '知识库及其文档会从列表、检索、MCP 和索引中排除；监视目录会暂停。', confirmLabel: '移入回收站', danger: true, onConfirm: async () => { await client.deleteLibrary(library.id); setSelectedLibrary(''); setLibraryDetailsOpen(false); await queryClient.invalidateQueries({ queryKey: ['libraries'] }); await queryClient.invalidateQueries({ queryKey: ['trash'] }); setUndo({ message: `已将“${library.name}”移入回收站`, run: async () => { await client.restoreTrash('library', library.id); await queryClient.invalidateQueries({ queryKey: ['libraries'] }) } }) } })} /> : selectedKnowledge ? <KnowledgePreview detail={knowledgeDetail.data} loading={knowledgeDetail.isLoading} onOpenDocument={(id) => { if (!confirmUnsavedDraftLeave()) return; setSelectedKnowledge(''); setSelectedDocument(id) }} onDraftDirty={setKnowledgeDraftDirty} /> : <DocumentPreview detail={detail.data} loading={detail.isLoading} onSaved={() => { void detail.refetch(); void documents.refetch() }} onLink={(href, source) => { void openMarkdownLink(href, source) }} onDraftDirty={setDocumentDraftDirty} />}</div></> : <button className="collapsed-detail-toggle" type="button" aria-label="展开详情栏" title="展开详情栏" onClick={() => setDetailPaneOpen(true)}><PanelRight size={18}/></button>}
    </aside>
    <GlobalBottomBar version={updateState.currentVersion} activeSection={activeSection} activeJobs={activeJobs} backendError={backendError} searchInfo={searchInfo} skillsView={skillsView} trashItemCount={trash.data?.length ?? 0} commandOpen={commandOpen} activityOpen={activityOpen} onOpenCommands={() => setCommandOpen(true)} onOpenActivity={() => setActivityOpen(true)} />
    <UpdateNotice state={updateState} onInstall={installUpdate}/>
    <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} libraries={libraries.data ?? []} updateState={updateState} onInstallUpdate={installUpdate}/>
    <CreateLibraryDialog open={createLibraryOpen} onOpenChange={setCreateLibraryOpen} busy={createLibrary.isPending} error={createLibrary.error as Error | null} onCreate={(name, description) => createLibrary.mutate({ name, description })}/>
    <SaveSearchDialog open={saveSearchDialogOpen} onOpenChange={setSaveSearchDialogOpen} defaultName={query.slice(0, 48)} query={query} view={knowledgeView} tagCount={filterTags.length} busy={saveSearch.isPending} error={saveSearch.error as Error | null} onSave={(name) => saveSearch.mutate(name)}/>
    <WatchManagerDialog open={watchManagerOpen} onOpenChange={setWatchManagerOpen} libraryId={selectedLibrary}/>
    <SavedSearchManager open={savedSearchOpen} onOpenChange={setSavedSearchOpen} current={{ query, view: knowledgeView, libraryIds: selectedLibrary ? [selectedLibrary] : [], tags: filterTags, filters: { tags: filterTags, favorite: favoritesOnly, folderIds: selectedFolder ? [selectedFolder] : [], tagMode: 'all' }, sort: { field: 'updatedAt', direction: 'desc' } }}/>
    <ActivityCenter open={activityOpen} onOpenChange={setActivityOpen} jobs={jobs.data ?? []} busyId={jobAction.variables?.id} onCancel={(job) => jobAction.mutate({ id: job.id, action: 'cancel' })} onRetry={(job) => jobAction.mutate({ id: job.id, action: 'retry' })}/>
    <CommandPalette open={commandOpen} onOpenChange={setCommandOpen} actions={buildCommands({ selectedLibrary, setCreateLibraryOpen, setSettingsOpen, setActivityOpen, importFiles: () => importFiles.mutate(), runSearch: () => runSearch(), setWatchManagerOpen, setActiveSection: (section) => { if (confirmUnsavedDraftLeave()) setActiveSection(section) } })}/>
    <ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(undefined)}/>
    {undo && <UndoToast message={undo.message} onUndo={undo.run} onDismiss={() => setUndo(undefined)}/>}
  </div>
}

function buildCommands(input: {
  selectedLibrary: string
  setCreateLibraryOpen: (value: boolean) => void
  setSettingsOpen: (value: boolean) => void
  setActivityOpen: (value: boolean) => void
  setWatchManagerOpen: (value: boolean) => void
  importFiles: () => void
  runSearch: () => void
  setActiveSection: (value: 'knowledge'|'graph'|'architecture'|'skills'|'review'|'trash') => void
}): WorkbenchAction[] {
  const libraryReason = input.selectedLibrary ? undefined : '请先选择知识库'
  return [
    { id: 'library-new', label: '新建知识库', keywords: 'create library', run: () => input.setCreateLibraryOpen(true) },
    { id: 'document-import', label: '导入文件', keywords: 'file import', shortcut: '主界面按钮', disabledReason: libraryReason, run: input.importFiles },
    { id: 'search', label: '执行当前搜索', keywords: 'find query', shortcut: 'Enter', disabledReason: libraryReason, run: input.runSearch },
    { id: 'watches', label: '管理监视目录', keywords: 'scan watch folder', disabledReason: libraryReason, run: () => input.setWatchManagerOpen(true) },
    { id: 'activity', label: '打开活动中心', keywords: 'jobs task history', run: () => input.setActivityOpen(true) },
    { id: 'review', label: '前往审核工作台', keywords: 'review revision', run: () => input.setActiveSection('review') },
    { id: 'graph', label: '打开关系图谱', keywords: 'graph network relation', disabledReason: libraryReason, run: () => input.setActiveSection('graph') },
    { id: 'architecture', label: '打开项目架构', keywords: 'project architecture code map', run: () => input.setActiveSection('architecture') },
    { id: 'trash', label: '打开回收站', keywords: 'restore delete', run: () => input.setActiveSection('trash') },
    { id: 'settings', label: '打开设置', keywords: 'preferences provider backup', shortcut: 'Ctrl ,', run: () => input.setSettingsOpen(true) },
  ]
}

function FolderTree({ libraryId, folders, selectedId, onSelect }: { libraryId: string; folders: VirtualFolder[]; selectedId: string; onSelect: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [expanded, setExpanded] = useState<string[]>(folders.map((item) => item.id))
  const [creatingParent, setCreatingParent] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [renaming, setRenaming] = useState<string>()
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['folders', libraryId] })
  const create = useMutation({ mutationFn: () => client.createFolder(libraryId, name.trim(), creatingParent || undefined), onSuccess: (folder) => { setName(''); setCreatingParent(null); setExpanded((current) => Array.from(new Set([...current, folder.parentId ?? '']))); refresh() } })
  const update = useMutation({ mutationFn: ({ folder, value }: { folder: VirtualFolder; value: string }) => client.updateFolder(folder.id, value.trim(), folder.parentId ?? ''), onSuccess: () => { setRenaming(undefined); setName(''); refresh() } })
  const remove = useMutation({ mutationFn: client.deleteFolder, onSuccess: refresh })
  const children = (parentId = '') => folders.filter((item) => (item.parentId ?? '') === parentId)
  function render(parentId = '', depth = 0): React.ReactNode {
    return children(parentId).map((folder) => {
      const nested = children(folder.id)
      const open = expanded.includes(folder.id)
      const editing = renaming === folder.id
      return <div key={folder.id} className="folder-tree-node"><div className="folder-tree-row" style={{ paddingLeft: `${depth * 14}px` }}>{nested.length ? <button type="button" className="tree-toggle" aria-label={`${open ? '折叠' : '展开'} ${folder.name}`} aria-expanded={open} onClick={() => setExpanded((current) => open ? current.filter((id) => id !== folder.id) : [...current, folder.id])}><ChevronRight className={open ? 'expanded' : ''} size={13}/></button> : <span className="tree-spacer"/>}{editing ? <form onSubmit={(event) => { event.preventDefault(); if (name.trim()) update.mutate({ folder, value: name }) }}><input value={name} onChange={(event) => setName(event.target.value)} autoFocus onBlur={() => !update.isPending && setRenaming(undefined)}/></form> : <button type="button" className={`saved-row ${selectedId === folder.id ? 'active' : ''}`} onClick={() => onSelect(folder.id)}><FolderSearch2 size={15}/><span>{folder.name}</span></button>}<MoreActions label={`${folder.name} 目录操作`} actions={[{ id: 'new', label: '新建子目录', run: () => { setCreatingParent(folder.id); setName(''); setExpanded((current) => Array.from(new Set([...current, folder.id]))) } }, { id: 'rename', label: '重命名', run: () => { setRenaming(folder.id); setName(folder.name) } }, { id: 'delete', label: '删除目录', danger: true, run: () => { if (window.confirm(`删除目录“${folder.name}”？文档只会解除归类，不会被删除。`)) remove.mutate(folder.id) } }]}/></div>{open && render(folder.id, depth + 1)}{creatingParent === folder.id && <FolderCreateRow depth={depth + 1} name={name} setName={setName} busy={create.isPending} onSubmit={() => create.mutate()} onCancel={() => { setCreatingParent(null); setName('') }}/>}</div>
    })
  }
  const error = create.error || update.error || remove.error
  return <div className="folder-tree" role="tree" aria-label="虚拟目录">{render()}{creatingParent === '' && <FolderCreateRow depth={0} name={name} setName={setName} busy={create.isPending} onSubmit={() => create.mutate()} onCancel={() => { setCreatingParent(null); setName('') }}/>}<button type="button" className="text-button sidebar-add" onClick={() => { setCreatingParent(''); setName('') }}><Plus size={14}/>新建目录</button>{error && <p className="sidebar-error" role="alert">{(error as Error).message}</p>}</div>
}

function FolderCreateRow({ depth, name, setName, busy, onSubmit, onCancel }: { depth: number; name: string; setName: (value: string) => void; busy: boolean; onSubmit: () => void; onCancel: () => void }) {
  return <form className="folder-create-row" style={{ paddingLeft: `${depth * 14 + 20}px` }} onSubmit={(event) => { event.preventDefault(); if (name.trim()) onSubmit() }}><input value={name} onChange={(event) => setName(event.target.value)} placeholder="目录名称" autoFocus onKeyDown={(event) => event.key === 'Escape' && onCancel()}/><button type="submit" aria-label="创建目录" disabled={busy || !name.trim()}><CheckCircle2 size={14}/></button><button type="button" aria-label="取消" onClick={onCancel}><X size={14}/></button></form>
}

function BatchToolbar({ selectedIds, folders, busy, onClear, onAction, onTrash }: { selectedIds: string[]; folders: VirtualFolder[]; busy: boolean; onClear: () => void; onAction: (action: Omit<DocumentBatchRequest, 'documentIds'>) => void; onTrash: () => void }) {
  const [tag, setTag] = useState('')
  const [folderId, setFolderId] = useState('')
  if (!selectedIds.length) return null
  const tags = parseLibraryTags(tag)
  return <div className="batch-toolbar" role="toolbar" aria-label="批量文档操作"><strong>{selectedIds.length} 项已选择</strong><label>标签<input value={tag} onChange={(event) => setTag(event.target.value)} placeholder="输入标签"/></label><button type="button" className="button ghost" disabled={busy || !tags.length} onClick={() => onAction({ action: 'add_tags', tags })}>增加</button><button type="button" className="button ghost" disabled={busy || !tags.length} onClick={() => onAction({ action: 'remove_tags', tags })}>移除</button><label>目录<select value={folderId} onChange={(event) => setFolderId(event.target.value)}><option value="">选择目录</option>{folders.map((folder) => <option key={folder.id} value={folder.id}>{folder.name}</option>)}</select></label><button type="button" className="button ghost" disabled={busy || !folderId} onClick={() => onAction({ action: 'add_folders', folderIds: [folderId] })}>加入</button><button type="button" className="button ghost" disabled={busy || !folderId} onClick={() => onAction({ action: 'remove_folders', folderIds: [folderId] })}>移出</button><MoreActions actions={[{ id: 'favorite', label: '收藏', run: () => onAction({ action: 'favorite', favorite: true }) }, { id: 'unfavorite', label: '取消收藏', run: () => onAction({ action: 'favorite', favorite: false }) }, { id: 'reindex', label: '重新索引', run: () => onAction({ action: 'reindex' }) }, { id: 'trash', label: '移入回收站', danger: true, run: onTrash }]}/><button type="button" className="icon-button" aria-label="清除选择" onClick={onClear}><X size={16}/></button></div>
}

function CreateLibraryDialog({ open, onOpenChange, busy, error, onCreate }: { open: boolean; onOpenChange: (open: boolean) => void; busy: boolean; error: Error | null; onCreate: (name: string, description: string) => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  useEffect(() => { if (open) { setName(''); setDescription('') } }, [open])
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="confirm-dialog library-create-dialog"><Dialog.Title>新建知识库</Dialog.Title><Dialog.Description>创建后可继续设置图标、颜色、标签和自动化权限。</Dialog.Description><form onSubmit={(event) => { event.preventDefault(); if (name.trim()) onCreate(name.trim(), description.trim()) }}><label>名称<input value={name} onChange={(event) => setName(event.target.value)} autoFocus maxLength={120}/></label><label>描述<textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} maxLength={1000}/></label>{error && <p className="form-error" role="alert">{error.message}</p>}<div className="dialog-actions"><button type="button" className="button ghost" onClick={() => onOpenChange(false)} disabled={busy}>取消</button><button type="submit" className="button primary" disabled={busy || !name.trim()}>{busy && <LoaderCircle className="spin" size={15}/>}创建知识库</button></div></form></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function SaveSearchDialog({ open, onOpenChange, defaultName, query, view, tagCount, busy, error, onSave }: { open: boolean; onOpenChange: (open: boolean) => void; defaultName: string; query: string; view: 'sources'|'knowledge'; tagCount: number; busy: boolean; error: Error | null; onSave: (name: string) => void }) {
  const [name, setName] = useState('')
  useEffect(() => { if (open) setName(defaultName.trim() || '未命名搜索') }, [defaultName, open])
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="confirm-dialog"><Dialog.Title>固定当前搜索</Dialog.Title><Dialog.Description>保存完整的查询、视图、标签筛选和排序，之后可从侧边栏快速恢复。</Dialog.Description><form onSubmit={(event) => { event.preventDefault(); if (name.trim()) onSave(name.trim()) }}><label>名称<input value={name} maxLength={120} onChange={(event) => setName(event.target.value)} autoFocus required /></label><div className="saved-search-preview"><span>查询：{query || '（空）'}</span><span>视图：{view === 'knowledge' ? '成型知识' : '来源文档'} · 标签：{tagCount} 个 · 排序：最近更新</span></div>{error && <p className="form-error" role="alert">{error.message}</p>}<div className="dialog-actions"><button type="button" className="button ghost" onClick={() => onOpenChange(false)} disabled={busy}>取消</button><button type="submit" className="button primary" disabled={busy || !name.trim()}>{busy && <LoaderCircle className="spin" size={15}/>}保存固定搜索</button></div></form></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function WatchManagerDialog({ open, onOpenChange, libraryId }: { open: boolean; onOpenChange: (open: boolean) => void; libraryId: string }) {
  const queryClient = useQueryClient()
  const watches = useQuery({ queryKey: ['watches', libraryId], queryFn: () => client.watches(libraryId), enabled: open && Boolean(libraryId) })
  const [path, setPath] = useState('')
  const [error, setError] = useState('')
  const refresh = () => { void queryClient.invalidateQueries({ queryKey: ['watches', libraryId] }); void queryClient.invalidateQueries({ queryKey: ['jobs'] }) }
  const update = useMutation({ mutationFn: ({ id, patch }: { id: string; patch: { enabled?: boolean; recursive?: boolean } }) => client.updateWatch(id, patch), onSuccess: refresh })
  const scan = useMutation({ mutationFn: client.scanWatch, onSuccess: refresh })
  const remove = useMutation({ mutationFn: client.deleteWatch, onSuccess: refresh })
  const create = useMutation({ mutationFn: () => client.createWatch(libraryId, path), onSuccess: () => { setPath(''); refresh() } })
  async function choose() { try { const value = await window.kah.selectDirectory(); if (value) setPath(value) } catch (value) { setError(value instanceof Error ? value.message : String(value)) } }
  const mutationError = update.error || scan.error || remove.error || create.error
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="manager-dialog"><div className="activity-heading"><div><Dialog.Title>监视目录</Dialog.Title><Dialog.Description>查看扫描状态，并暂停、恢复或修改递归范围。</Dialog.Description></div><Dialog.Close asChild><button className="icon-button" aria-label="关闭"><X size={18}/></button></Dialog.Close></div><div className="manager-create-row"><input value={path} readOnly placeholder="选择要监视的本地目录"/><button type="button" className="button secondary" onClick={() => void choose()}>选择目录</button><button type="button" className="button primary" disabled={!path || create.isPending} onClick={() => create.mutate()}>添加</button></div>{(error || mutationError) && <p className="form-error" role="alert">{error || (mutationError as Error).message}</p>}<div className="manager-list">{watches.data?.map((watch: SourceWatch) => <article className="manager-row" key={watch.id}><div><strong>{watch.rootPath}</strong><span>{watch.lastMessage || '尚未扫描'}{watch.lastScanAt ? ` · ${new Date(watch.lastScanAt).toLocaleString()}` : ''}</span></div><label className="compact-check"><input type="checkbox" checked={watch.recursive} onChange={(event) => update.mutate({ id: watch.id, patch: { recursive: event.target.checked } })}/>递归</label><button type="button" className="button ghost" onClick={() => update.mutate({ id: watch.id, patch: { enabled: !watch.enabled } })}>{watch.enabled ? '暂停' : '恢复'}</button><button type="button" className="button secondary" onClick={() => scan.mutate(watch.id)}>立即扫描</button><button type="button" className="icon-button" aria-label="打开目录" onClick={() => void window.kah.openPath(watch.rootPath)}><FolderSearch2 size={16}/></button><button type="button" className="icon-button danger-icon" aria-label="删除监视目录" onClick={() => { if (window.confirm(`删除监视目录“${watch.rootPath}”？`)) remove.mutate(watch.id) }}><Trash2 size={16}/></button></article>)}{!watches.isLoading && !watches.data?.length && <p className="muted">当前知识库没有监视目录。</p>}</div></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function SavedSearchManager({ open, onOpenChange, current }: { open: boolean; onOpenChange: (open: boolean) => void; current: Pick<SavedSearch, 'query'|'view'|'libraryIds'|'tags'|'filters'|'sort'> }) {
  const queryClient = useQueryClient()
  const saved = useQuery({ queryKey: ['saved-searches'], queryFn: client.savedSearches, enabled: open })
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const update = useMutation({ mutationFn: (item: SavedSearch) => client.updateSavedSearch(item.id, { ...item, name: drafts[item.id] ?? item.name }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['saved-searches'] }) })
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="manager-dialog"><div className="activity-heading"><div><Dialog.Title>固定搜索管理</Dialog.Title><Dialog.Description>重命名、复制，或用当前搜索的完整条件覆盖某个快捷入口。</Dialog.Description></div><Dialog.Close asChild><button className="icon-button" aria-label="关闭"><X size={18}/></button></Dialog.Close></div><div className="manager-list">{saved.data?.map((item) => <article className="manager-row" key={item.id}><div><input aria-label={`${item.name} 名称`} value={drafts[item.id] ?? item.name} onChange={(event) => setDrafts((current) => ({ ...current, [item.id]: event.target.value }))}/><span>{item.view === 'knowledge' ? '成型知识' : '来源文档'} · {item.query || '空查询'} · {item.filters?.tags?.length ?? 0} 个标签</span></div><button type="button" className="button secondary" disabled={update.isPending || !(drafts[item.id] ?? item.name).trim()} onClick={() => update.mutate(item)}>保存名称</button><button type="button" className="button ghost" disabled={update.isPending || !current.query.trim()} title={!current.query.trim() ? '先执行一次搜索再更新条件' : undefined} onClick={() => update.mutate({ ...item, ...current, name: drafts[item.id] ?? item.name })}>用当前条件更新</button></article>)}</div>{update.error && <p className="form-error" role="alert">{(update.error as Error).message}</p>}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

function TrashWorkspace({ items, loading, error, onChanged, onConfirm }: { items: TrashEntry[]; loading: boolean; error: Error | null; onChanged: () => void; onConfirm: (request: ConfirmRequest) => void }) {
  const [selected, setSelected] = useState<string[]>([])
  async function restore(item: TrashEntry) { await client.restoreTrash(item.kind, item.id); setSelected((current) => current.filter((id) => id !== `${item.kind}:${item.id}`)); onChanged() }
  const selectedItems = items.filter((item) => selected.includes(`${item.kind}:${item.id}`))
  return <><header className="workspace-header"><div><span className="eyebrow">可恢复删除</span><h1>回收站</h1></div><div className="header-actions"><button type="button" className="button secondary" disabled={!selectedItems.length} onClick={() => void Promise.all(selectedItems.map(restore))}>恢复所选</button><button type="button" className="button danger" disabled={!items.length} onClick={() => onConfirm({ title: '永久清空回收站？', description: '此操作会删除数据库记录和不再被引用的对象文件，无法撤销。', confirmLabel: '永久清空', danger: true, onConfirm: async () => { await client.emptyTrash(); setSelected([]); onChanged() } })}><Trash2 size={16}/>永久清空</button></div></header>{error && <div className="inline-error" role="alert"><CircleAlert size={17}/>{error.message}</div>}<section className="result-list trash-list">{loading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入回收站</div>}{items.map((item) => { const key = `${item.kind}:${item.id}`; return <article className="document-row" key={key}><input type="checkbox" aria-label={`选择 ${item.name}`} checked={selected.includes(key)} onChange={(event) => setSelected((current) => event.target.checked ? [...current, key] : current.filter((value) => value !== key))}/><span className="file-icon">{item.kind === 'library' ? <Library size={18}/> : <File size={18}/>}</span><span className="document-copy"><strong>{item.name}</strong><small>{item.kind === 'library' ? `知识库 · ${item.itemCount} 个文档 · 恢复后监视仍保持暂停` : `文档 · ${item.description}`}</small></span><time>{new Date(item.deletedAt).toLocaleString()}</time><button type="button" className="button secondary" onClick={() => void restore(item)}>恢复</button><MoreActions actions={[{ id: 'purge', label: '永久删除', danger: true, run: () => onConfirm({ title: `永久删除“${item.name}”？`, description: '将删除记录和不再被引用的对象文件，无法恢复。', danger: true, onConfirm: async () => { await client.purgeTrash(item.kind, item.id); onChanged() } }) }]}/></article>})}{!loading && !items.length && <Empty icon={<Trash2 size={25}/>} title="回收站为空" text="移入回收站的知识库和文档会保留在这里，直到手动清空。"/>}</section></>
}

function LibraryDetailPane({ library, onSaved, onTrash }: { library?: LibraryType; onSaved: (library: LibraryType) => void; onTrash: (library: LibraryType) => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [icon, setIcon] = useState('library')
  const [color, setColor] = useState('jade')
  const [tags, setTags] = useState('')
  const [favorite, setFavorite] = useState(false)
  const [savedMessage, setSavedMessage] = useState('')
  const [validationError, setValidationError] = useState('')

  const save = useMutation({
    mutationFn: () => {
      if (!library) throw new Error('没有选择知识库')
      const normalizedName = name.trim()
      if (!normalizedName) throw new Error('知识库名称不能为空')
      return client.updateLibrary(library.id, { name: normalizedName, description: description.trim(), icon, color, tags: parseLibraryTags(tags), favorite })
    },
    onSuccess: (updated) => {
      queryClient.setQueryData<LibraryType[]>(['libraries'], (current) => current?.map((item) => item.id === updated.id ? updated : item))
      void queryClient.invalidateQueries({ queryKey: ['libraries'] })
      setName(updated.name)
      setDescription(updated.description)
      setIcon(updated.icon)
      setColor(updated.color)
      setTags(updated.tags.join(', '))
      setFavorite(updated.favorite)
      setValidationError('')
      setSavedMessage('知识库详情已保存。')
      onSaved(updated)
    },
    onError: () => setSavedMessage('')
  })

  useEffect(() => {
    setName(library?.name ?? '')
    setDescription(library?.description ?? '')
    setIcon(library?.icon ?? 'library')
    setColor(library?.color ?? 'jade')
    setTags((library?.tags ?? []).join(', '))
    setFavorite(Boolean(library?.favorite))
    setSavedMessage('')
    setValidationError('')
    save.reset()
  }, [library?.id, library?.updatedAt])

  if (!library) return <Empty icon={<Library size={25}/>} title="选择一个知识库" text="从左侧选择知识库后，可以在这里编辑名称、标记、标签和图标。" />

  const currentLibrary = library
  const normalizedTags = parseLibraryTags(tags)
  const dirty = name.trim() !== library.name || description.trim() !== library.description || icon !== library.icon || color !== library.color || JSON.stringify(normalizedTags) !== JSON.stringify(library.tags ?? []) || favorite !== library.favorite
  function resetDraft() {
    setName(currentLibrary.name)
    setDescription(currentLibrary.description)
    setIcon(currentLibrary.icon)
    setColor(currentLibrary.color)
    setTags((currentLibrary.tags ?? []).join(', '))
    setFavorite(currentLibrary.favorite)
    setValidationError('')
    setSavedMessage('')
    save.reset()
  }
  function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setValidationError('')
    if (!name.trim()) {
      setValidationError('请输入知识库名称。')
      return
    }
    save.mutate()
  }

  return <form className="library-detail-form" onSubmit={submit}>
    <header className="library-detail-heading"><div className={`library-detail-icon ${libraryColorClass(color)}`}><LibraryIcon icon={icon} size={22}/></div><div><span className="eyebrow">知识库设置</span><h2>{library.name}</h2><small>修改后会同步到侧边栏和本地数据库。</small></div></header>
    <div className="library-detail-fields">
      <label htmlFor="library-name">名称<input id="library-name" value={name} onChange={(event) => setName(event.target.value)} maxLength={120} required /></label>
      <label htmlFor="library-description">描述<textarea id="library-description" value={description} onChange={(event) => setDescription(event.target.value)} maxLength={1000} rows={3} placeholder="说明这个知识库用于什么内容" /></label>
      <div className="library-detail-field"><span className="field-label">图标</span><div className="library-icon-options" role="group" aria-label="知识库图标">{LIBRARY_ICON_OPTIONS.map((option) => { const OptionIcon = option.icon; return <button type="button" key={option.value} className={`library-choice ${icon === option.value ? 'active' : ''}`} aria-label={`使用${option.label}图标`} aria-pressed={icon === option.value} title={option.label} onClick={() => setIcon(option.value)}><OptionIcon size={17}/></button> })}</div></div>
      <div className="library-detail-field"><span className="field-label">颜色</span><div className="library-color-options" role="group" aria-label="知识库颜色">{LIBRARY_COLOR_OPTIONS.map((option) => <button type="button" key={option.value} className={`library-color-choice ${option.value} ${color === option.value ? 'active' : ''}`} aria-label={`使用${option.label}颜色`} aria-pressed={color === option.value} onClick={() => setColor(option.value)}><span aria-hidden="true"/></button>)}</div></div>
      <div className="library-detail-field"><label htmlFor="library-tags">标签</label><input id="library-tags" value={tags} onChange={(event) => setTags(event.target.value)} placeholder="产品, 规范, 内部；用逗号分隔" /><small className="field-hint">最多 30 个标签，保存时会自动去重。</small></div>
      <label className="library-favorite-toggle"><input type="checkbox" checked={favorite} onChange={(event) => setFavorite(event.target.checked)} /><span><strong>标记为重要</strong><small>在侧边栏和工作区标题中显示标记。</small></span></label>
    </div>
    {(validationError || save.error) && <div className="inline-error" role="alert"><CircleAlert size={16}/>{validationError || (save.error as Error).message}</div>}
    <div className="library-detail-actions"><button type="button" className="button danger" onClick={() => onTrash(library)} disabled={save.isPending}><Trash2 size={15}/>移入回收站</button><span className="action-spacer"/><button type="button" className="button secondary" onClick={resetDraft} disabled={!dirty || save.isPending}>重置</button><button type="submit" className="button primary" disabled={!dirty || save.isPending || !name.trim()}>{save.isPending ? <><LoaderCircle className="spin" size={16}/>正在保存…</> : <><Save size={16}/>保存详情</>}</button></div>
    {savedMessage && <p className="settings-success" role="status">{savedMessage}</p>}
    <p className="library-detail-meta">创建于 {new Date(library.createdAt).toLocaleString()} · 最近更新 {new Date(library.updatedAt).toLocaleString()}</p>
  </form>
}

function jobLabel(job: Job): string {
  switch (job.kind) {
    case 'file_import': return '资料导入'
    case 'url_import': return '网页导入'
    case 'knowledge_summarize': return '自动总结'
    case 'kah_knowledge_review': return 'KAH 自动审核'
    case 'kah_knowledge_publish': return 'KAH 发布'
    default: return job.kind
  }
}

function PipelineStatus({ jobs }: { jobs: Job[] }) {
  const visible = jobs.filter((job) => job.status === 'queued' || job.status === 'running' || job.status === 'failed').slice(0, 4)
  if (!visible.length) return null
  const failed = visible.some((job) => job.status === 'failed')
  return <div className={`pipeline-status ${failed ? 'has-error' : ''}`} role={failed ? 'alert' : 'status'} aria-live="polite"><div className="pipeline-status-heading">{failed ? <CircleAlert size={16}/> : <LoaderCircle className="spin" size={16}/>}<strong>{failed ? '管线有任务失败' : '导入管线处理中'}</strong></div><div className="pipeline-status-list">{visible.map((job) => <div className="pipeline-status-row" key={job.id}><span>{jobLabel(job)}</span><span className={`pipeline-job-state ${job.status}`}>{job.status === 'queued' ? '排队中' : job.status === 'running' ? `${Math.round(job.progress * 100)}%` : '失败'}</span><small>{job.message || '等待后台更新'}</small></div>)}</div></div>
}

function ReviewWorkspace({ libraries, selectedId, onSelect }: { libraries: LibraryType[]; selectedId: string; onSelect: (id: string) => void }) {
  const submissions = useQuery({ queryKey: ['submissions'], queryFn: () => client.submissions(), refetchInterval: 3000 })
  const [libraryFilter, setLibraryFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState<'actionable'|'all'|'published'|'rejected'>('actionable')
  const [submitterFilter, setSubmitterFilter] = useState('')
  const actionable = (submissions.data ?? []).filter((item) => (!libraryFilter || item.libraryId === libraryFilter) && (!submitterFilter || (item.submittedByTokenId ?? 'desktop').toLowerCase().includes(submitterFilter.toLowerCase())) && (statusFilter === 'all' || statusFilter === 'actionable' ? statusFilter === 'all' || item.reviewStatus === 'pending_review' || item.reviewStatus === 'reviewing' : item.reviewStatus === statusFilter))
  const libraryName = (id: string) => libraries.find((item) => item.id === id)?.name ?? id
  useEffect(() => {
    if (selectedId && actionable.some((item) => item.id === selectedId)) return
    onSelect(actionable[0]?.id ?? '')
  }, [actionable, onSelect, selectedId])

  return <>
    <header className="workspace-header"><div><span className="eyebrow">质量门禁</span><h1>待审核知识</h1></div><div className="header-actions"><button type="button" className="button secondary" onClick={() => void submissions.refetch()} disabled={submissions.isFetching}><RefreshCw size={17}/>刷新队列</button></div></header>
    <div className="review-filters"><label>知识库<select value={libraryFilter} onChange={(event) => setLibraryFilter(event.target.value)}><option value="">全部知识库</option>{libraries.map((library) => <option key={library.id} value={library.id}>{library.name}</option>)}</select></label><label>状态<select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as typeof statusFilter)}><option value="actionable">待处理</option><option value="all">全部</option><option value="published">已发布</option><option value="rejected">已驳回</option></select></label><label>提交者<input value={submitterFilter} onChange={(event) => setSubmitterFilter(event.target.value)} placeholder="令牌 ID 或 desktop"/></label></div>
    <div className="content-heading"><div><h2>知识修订提交</h2><span>{actionable.length} 条结果</span></div><span className="muted">选中后在右侧并排比较并审核</span></div>
    {submissions.error && <div className="inline-error" role="alert"><CircleAlert size={18}/>{String((submissions.error as Error).message)}</div>}
    <section className="review-layout">
      <div className="result-list" aria-label="待审核提交列表">
        {submissions.isLoading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入审核队列</div>}
        {actionable.map((item) => <button type="button" className={'document-row ' + (selectedId === item.id ? 'active' : '')} key={item.id} onClick={() => onSelect(item.id)}><span className="file-icon"><FileCode2 size={19}/></span><span className="document-copy"><strong>{item.title}</strong><small>{libraryName(item.libraryId)} · {item.summary || '无摘要'}</small></span><span className={'status-pill ' + item.reviewStatus}>{item.reviewStatus === 'reviewing' ? '自动审核中' : '待审核'}</span><time>{new Date(item.updatedAt).toLocaleDateString()}</time></button>)}
        {!submissions.isLoading && !actionable.length && <Empty icon={<CheckCircle2 size={25}/>} title="审核队列为空" text="新的 Agent 提交会先停留在这里，审核通过后才可检索。" />}
      </div>
    </section>
  </>
}

function ReviewDetailPane({ selectedId, libraries, onOpenDocument }: { selectedId: string; libraries: LibraryType[]; onOpenDocument: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [reason, setReason] = useState('')
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest>()
  const detail = useQuery({ queryKey: ['submission', selectedId], queryFn: () => client.submission(selectedId), enabled: Boolean(selectedId) })
  const stable = useQuery({ queryKey: ['submission-base', detail.data?.knowledgeUri], queryFn: () => client.knowledge(detail.data!.knowledgeUri), enabled: Boolean(detail.data?.knowledgeUri && detail.data?.mode === 'propose_revision') })
  const approve = useMutation({ mutationFn: () => client.approveSubmission(selectedId, reason.trim()), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['submissions'] }); queryClient.invalidateQueries({ queryKey: ['jobs'] }); setReason('') } })
  const reject = useMutation({ mutationFn: () => client.rejectSubmission(selectedId, reason.trim()), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['submissions'] }); setReason('') } })
  const libraryName = (id: string) => libraries.find((item) => item.id === id)?.name ?? id

  useEffect(() => {
    setReason('')
    approve.reset()
    reject.reset()
  }, [selectedId])

  if (!selectedId) return <Empty icon={<PanelRight size={25}/>} title="选择一条提交" text="从中间列表选择待审核知识，右侧会显示完整内容和审核操作。" />
  if (detail.isLoading) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入审核详情</div>
  if (detail.error) return <div className="inline-error" role="alert"><CircleAlert size={18}/>{String((detail.error as Error).message)}</div>
  if (!detail.data) return <Empty icon={<PanelRight size={25}/>} title="提交不存在" text="该审核项可能已被处理，请刷新审核队列。" />

  const submission = detail.data
  return <article className="review-card review-detail-card" aria-live="polite">
    <div className="review-card-heading"><div><span className="eyebrow">{libraryName(submission.libraryId)}</span><h2>{submission.title}</h2></div><span className={'status-pill ' + submission.reviewStatus}>{submission.reviewStatus}</span></div>
    <p className="settings-lead">{submission.summary || '暂无摘要'}</p>
    {submission.markdown ? <RevisionDiff current={stable.data?.markdown ?? ''} candidate={submission.markdown}/> : <div className="review-content-empty muted">此提交没有可预览的 Markdown 正文。</div>}
    <SubmissionSources submission={submission} onOpenDocument={onOpenDocument}/>
    <div className="review-provenance"><strong>来源与生成元数据</strong><pre>{JSON.stringify(submission.provenance ?? {}, null, 2)}</pre></div>
    {(approve.error || reject.error) && <div className="inline-error" role="alert"><CircleAlert size={17}/>{String(((approve.error || reject.error) as Error).message)}</div>}
    <label className="review-reason">审核说明<textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="驳回时必须填写理由；批准说明会进入审计记录。" rows={3}/></label>
    <div className="preview-actions"><button type="button" className="button primary" onClick={() => setConfirmRequest({ title: `批准“${submission.title}”并发布？`, description: '候选修订将成为新的稳定 revision，并进入正式索引。', confirmLabel: '批准并发布', onConfirm: async () => { await approve.mutateAsync() } })} disabled={approve.isPending || reject.isPending || submission.reviewStatus !== 'pending_review'}>{approve.isPending ? <LoaderCircle className="spin" size={16}/> : <CheckCircle2 size={16}/>} {approve.isPending ? '正在批准…' : '批准并发布'}</button><button type="button" className="button danger" onClick={() => reject.mutate()} disabled={reject.isPending || approve.isPending || !reason.trim() || submission.reviewStatus !== 'pending_review'}>{reject.isPending ? <LoaderCircle className="spin" size={16}/> : <CircleAlert size={16}/>} {reject.isPending ? '正在驳回…' : '驳回'}</button></div>
    {submission.reviews?.length ? <div className="review-history"><h3>审核记录</h3>{submission.reviews.map((review) => { const reviewerLabel = review.reviewerType === 'model' ? '审查模型' : review.reviewerType === 'agent' ? 'Agent审核' : '人工审核'; const confidence = review.reviewerType !== 'human' && typeof review.confidence === 'number' && Number.isFinite(review.confidence) ? ` · 信度 ${Math.round(review.confidence * 100)}%` : ''; return <div className="review-history-row" key={review.id}><strong>{reviewerLabel} · {review.decision}{confidence}</strong><span>{review.reason}</span></div> })}</div> : null}
    <ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(undefined)}/>
  </article>
}

function RevisionDiff({ current, candidate }: { current: string; candidate: string }) {
  const currentLines = current.split('\n')
  const candidateLines = candidate.split('\n')
  const changed = new Set<number>()
  const max = Math.max(currentLines.length, candidateLines.length)
  for (let index = 0; index < max; index += 1) if (currentLines[index] !== candidateLines[index]) changed.add(index)
  return <section className="revision-diff" aria-label="稳定版与候选版差异"><div><strong>当前稳定版</strong><pre>{currentLines.map((line, index) => <span className={changed.has(index) ? 'changed removed' : ''} key={index}>{line || ' '}{'\n'}</span>)}</pre></div><div><strong>候选修订</strong><pre>{candidateLines.map((line, index) => <span className={changed.has(index) ? 'changed added' : ''} key={index}>{line || ' '}{'\n'}</span>)}</pre></div></section>
}

function SkillHint() {
  return <Empty icon={<FolderArchive size={25}/>} title="选择一个 Skill" text="Skill 的标准入口、文件清单和知识库关联会显示在这里。" />
}

function SkillsWorkspace({ libraries: _libraries, selectedId, onSelect, view, onViewChange, selectedTargetId, onTargetSelect }: { libraries: LibraryType[]; selectedId: string; onSelect: (id: string) => void; view: 'skills'|'mappings'; onViewChange: (view: 'skills'|'mappings') => void; selectedTargetId: string; onTargetSelect: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState('')
  const skills = useQuery({ queryKey: ['skills'], queryFn: client.skills, refetchInterval: 4000 })
  const importMutation = useMutation({
    mutationFn: async ({ kind, replace = false }: { kind: 'skill-markdown'|'skill-zip'; replace?: boolean }) => {
      const paths = await window.kah.selectFiles(kind)
      if (!paths[0]) return undefined
      return client.importSkill(paths[0], replace)
    },
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['skills'] }); queryClient.invalidateQueries({ queryKey: ['jobs'] }) }
  })
  useEffect(() => { if (!selectedId && skills.data?.[0]) onSelect(skills.data[0].id) }, [skills.data, selectedId, onSelect])

  const visible = useMemo(() => (skills.data ?? []).filter((item) => `${item.name} ${item.description}`.toLowerCase().includes(filter.toLowerCase())), [skills.data, filter])
  const importError = importMutation.error as Error | null

  return <>
    <header className="workspace-header">
      <div><span className="eyebrow">全局能力</span><h1>{view === 'skills' ? 'Skills' : '外部映射'}</h1></div>
      <div className="header-actions">
        <div className="segmented-control" role="tablist" aria-label="Skills 工作台视图"><button type="button" role="tab" aria-selected={view === 'skills'} className={view === 'skills' ? 'active' : ''} onClick={() => onViewChange('skills')}>已安装 Skills</button><button type="button" role="tab" aria-selected={view === 'mappings'} className={view === 'mappings' ? 'active' : ''} onClick={() => onViewChange('mappings')}>外部映射</button></div>
        {view === 'skills' && <><button type="button" className="button secondary" onClick={() => importMutation.mutate({ kind: 'skill-markdown' })} disabled={importMutation.isPending}><FileCode2 size={17}/> 导入 SKILL.md</button><button type="button" className="button primary" onClick={() => importMutation.mutate({ kind: 'skill-zip' })} disabled={importMutation.isPending}><Import size={17}/> 导入 Skill zip</button></>}
      </div>
    </header>
    {view === 'skills' ? <><div className="search-row"><Search size={19}/><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="筛选 Skill 名称或能力描述…" aria-label="筛选 Skill" />{filter && <IconButton label="清空筛选" onClick={() => setFilter('')}><X size={16}/></IconButton>}</div>{importError && <div role="alert" className="inline-error"><CircleAlert size={18}/>{importError.message}</div>}<div className="content-heading"><div><h2>已安装 Skills</h2><span>{visible.length} 个能力包</span></div><IconButton label="刷新 Skills" onClick={() => void skills.refetch()}><RefreshCw size={17}/></IconButton></div><section className="result-list" aria-live="polite">{skills.isLoading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入 Skills</div>}{visible.map((item) => <button type="button" className={`document-row ${selectedId === item.id ? 'active' : ''}`} key={item.id} onClick={() => onSelect(item.id)}><span className="file-icon"><FolderArchive size={19}/></span><span className="document-copy"><strong>{item.name}</strong><small>{item.description}</small></span><span className={`status-pill ${item.status}`}>{item.status === 'ready' ? `${item.fileCount} 个文件` : '校验失败'}</span><time>{new Date(item.updatedAt).toLocaleDateString()}</time></button>)}{!skills.isLoading && visible.length === 0 && <Empty icon={<FolderArchive size={25}/>} title="还没有 Skill" text="导入 SKILL.md 或 Skill zip，建立可复用的 Agent 能力。" />}</section></> : <SkillMappingsWorkspace skills={skills.data ?? []} selectedTargetId={selectedTargetId} onTargetSelect={onTargetSelect} />}
  </>
}

function SubmissionSources({ submission, onOpenDocument }: { submission: KAHSubmission; onOpenDocument: (id: string) => void }) {
  const provenance = submission.provenance ?? {}
  const sources = Array.isArray(provenance.sources) ? provenance.sources as Array<Record<string, unknown>> : []
  if (!sources.length) return <div className="source-connection muted"><Link2 size={15}/>未声明结构化来源</div>
  return <div className="source-connection"><strong><Link2 size={15}/>来源连接</strong>{sources.map((source, index) => { const locator = source.locator && typeof source.locator === 'object' ? source.locator as Record<string, unknown> : {}; const resource = String(source.resource ?? ''); const documentId = resource.startsWith('kah://document/') ? resource.slice('kah://document/'.length) : typeof locator.documentId === 'string' ? locator.documentId : ''; return <div className="source-connection-row" key={`${resource}-${index}`}><code>{resource || '未命名来源'}</code>{documentId ? <button type="button" className="text-button" onClick={() => onOpenDocument(documentId)}>查看来源文档</button> : <span className="muted">需通过外部链接核验</span>}</div> })}</div>
}

function SkillDetailPane({ skillId, libraries, onDeleted }: { skillId: string; libraries: LibraryType[]; onDeleted: () => void }) {
  const queryClient = useQueryClient()
  const skill = useQuery({ queryKey: ['skill', skillId], queryFn: () => client.skill(skillId), enabled: Boolean(skillId) })
  const manifest = useQuery({ queryKey: ['skill-manifest', skillId], queryFn: () => client.skillManifest(skillId), enabled: Boolean(skillId) })
  const [runtime, setRuntime] = useState<RuntimeConfig>()
  const deleteMutation = useMutation({ mutationFn: client.deleteSkill, onSuccess: () => { onDeleted(); queryClient.invalidateQueries({ queryKey: ['skills'] }) } })
  const linkMutation = useMutation({ mutationFn: ({ id, uses, requires }: { id: string; uses: string[]; requires: string[] }) => client.updateSkillLinks(id, uses, requires), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['skills'] }); skill.refetch() } })
  useEffect(() => { getRuntime().then(setRuntime) }, [])
  if (!skillId) return <SkillHint />
  if (skill.isLoading || !skill.data) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入 Skill</div>
  return <SkillDetail skill={skill.data} manifest={manifest.data} libraries={libraries} runtime={runtime} onDelete={() => { if (window.confirm(`确定删除 ${skill.data.name}？`)) deleteMutation.mutate(skill.data.id) }} deleteBusy={deleteMutation.isPending} onSaveLinks={(uses, requires) => linkMutation.mutate({ id: skill.data!.id, uses, requires })} linkBusy={linkMutation.isPending} linkError={linkMutation.error as Error | null} />
}

function SkillDetail({ skill, manifest, libraries, runtime, onDelete, deleteBusy, onSaveLinks, linkBusy, linkError }: { skill: Skill; manifest?: Awaited<ReturnType<typeof client.skillManifest>>; libraries: LibraryType[]; runtime?: RuntimeConfig; onDelete: () => void; deleteBusy: boolean; onSaveLinks: (uses: string[], requires: string[]) => void; linkBusy: boolean; linkError: Error | null }) {
  const queryClient = useQueryClient()
  const [uses, setUses] = useState<string[]>(skill.usesLibraryIds)
  const [requires, setRequires] = useState<string[]>(skill.requiresLibraryIds)
  const [openError, setOpenError] = useState('')
  const [detailTab, setDetailTab] = useState<'entry' | 'files' | 'links'>('entry')
  const replaceMutation = useMutation({
    mutationFn: async (kind: 'skill-markdown'|'skill-zip') => {
      const paths = await window.kah.selectFiles(kind)
      if (!paths[0] || !window.confirm(`确定用所选文件替换 ${skill.name}？`)) return undefined
      return client.importSkill(paths[0], true)
    },
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['skills'] }); queryClient.invalidateQueries({ queryKey: ['skill', skill.id] }); queryClient.invalidateQueries({ queryKey: ['skill-manifest', skill.id] }) }
  })
  useEffect(() => { setUses(skill.usesLibraryIds); setRequires(skill.requiresLibraryIds); setDetailTab('entry') }, [skill.id, skill.usesLibraryIds, skill.requiresLibraryIds])
  const toggle = (setter: React.Dispatch<React.SetStateAction<string[]>>, id: string) => setter((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id])
  const folderPath = runtime ? `${runtime.dataRoot.replace(/[\\/]$/, '')}\\${skill.rootPath.replaceAll('/', '\\')}` : ''
  async function openSkillFolder() {
    if (!folderPath) return
    setOpenError('')
    try {
      const result = await window.kah.openPath(folderPath)
      if (result) setOpenError(result)
    } catch (error) {
      setOpenError(error instanceof Error ? error.message : String(error))
    }
  }
  return <div className="preview-shell skill-detail-shell">
    <header className="preview-header"><div className="preview-icon"><FolderArchive size={20}/></div><div><span>Agent Skill</span><h2>{skill.name}</h2></div></header>
    <div className="preview-meta"><span className={`status-pill ${skill.status}`}>{skill.status === 'ready' ? '可用' : '无效'}</span><span><Database size={14}/>{skill.fileCount} 个文件</span></div>
    <p className="settings-lead">{skill.description}</p>
    {skill.systemRole && <div className="info-card"><ShieldCheck size={18}/><div><strong>系统规范 Skill</strong><span>由系统维护，不能替换或删除。</span></div></div>}
    {skill.error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{skill.error}</div>}
    {(replaceMutation.error || linkError || openError) && <div role="alert" className="inline-error"><CircleAlert size={17}/>{openError || String(((replaceMutation.error || linkError) as Error).message)}</div>}
    <div className="preview-actions skill-detail-actions">
      {folderPath && <button type="button" className="button secondary" title="打开文件夹" onClick={() => void openSkillFolder()}><FolderSearch2 size={14}/><span>打开文件夹</span></button>}
      <button type="button" className="button secondary" title="替换 Markdown" onClick={() => replaceMutation.mutate('skill-markdown')} disabled={Boolean(skill.systemRole) || replaceMutation.isPending || deleteBusy}><FileCode2 size={14}/><span>{replaceMutation.isPending ? '正在替换…' : '替换 Markdown'}</span></button>
      <button type="button" className="button secondary" title="替换 zip" onClick={() => replaceMutation.mutate('skill-zip')} disabled={Boolean(skill.systemRole) || replaceMutation.isPending || deleteBusy}><Import size={14}/><span>{replaceMutation.isPending ? '正在替换…' : '替换 zip'}</span></button>
      <button type="button" className="button secondary" title="删除" onClick={onDelete} disabled={Boolean(skill.systemRole) || deleteBusy}><Trash2 size={14}/><span>{deleteBusy ? '正在删除…' : '删除'}</span></button>
    </div>
    <Tabs.Root className="skill-detail-tabs-root" value={detailTab} onValueChange={(value) => setDetailTab(value as typeof detailTab)}>
      <Tabs.List className="skill-detail-tabs" aria-label="Skill 详情分页">
        <Tabs.Trigger value="entry">入口内容</Tabs.Trigger>
        <Tabs.Trigger value="files">附属文件 {manifest ? `· ${manifest.files.length}` : ''}</Tabs.Trigger>
        <Tabs.Trigger value="links">知识库关联</Tabs.Trigger>
      </Tabs.List>
      <Tabs.Content value="entry" className="skill-detail-tab-panel">
        <div className="skill-frontmatter"><strong>标准入口</strong><code>{skill.rootPath}/SKILL.md</code>{manifest?.entryPoint.content ? <ReactMarkdown>{manifest.entryPoint.content}</ReactMarkdown> : <p className="muted">正在读取 SKILL.md 内容…</p>}</div>
      </Tabs.Content>
      <Tabs.Content value="files" className="skill-detail-tab-panel">
        <div className="skill-files"><h4>附属文件</h4>{manifest ? manifest.files.length ? manifest.files.map((file) => <div className="token-row" key={file.path}><File size={15}/><span>{file.path}</span><small>{file.size} bytes</small></div>) : <p className="muted">此 Skill 没有附属文件。</p> : <p className="muted">正在读取文件列表…</p>}</div>
      </Tabs.Content>
      <Tabs.Content value="links" className="skill-detail-tab-panel">
        <div className="skill-links">
          <section><h4>Skill 可调用的知识库</h4>{libraries.length ? libraries.map((library) => <label className="switch-row" key={`uses-${library.id}`}><span>{library.name}</span><input type="checkbox" checked={uses.includes(library.id)} disabled={linkBusy} onChange={() => toggle(setUses, library.id)}/></label>) : <p className="muted">当前没有可关联的知识库。</p>}</section>
          <section><h4>知识库需要此 Skill</h4>{libraries.length ? libraries.map((library) => <label className="switch-row" key={`requires-${library.id}`}><span>{library.name}</span><input type="checkbox" checked={requires.includes(library.id)} disabled={linkBusy} onChange={() => toggle(setRequires, library.id)}/></label>) : <p className="muted">创建知识库后，可以在这里配置依赖关系。</p>}</section>
          <button type="button" className="button primary" onClick={() => onSaveLinks(uses, requires)} disabled={linkBusy}>{linkBusy ? <LoaderCircle className="spin" size={16}/> : <Save size={16}/>} {linkBusy ? '正在保存…' : '保存关联'}</button>
        </div>
      </Tabs.Content>
    </Tabs.Root>
  </div>
}

function DocumentRow({ document, active, selected, onSelect, onReindex, onTrash }: { document: Document; active: boolean; selected: boolean; onSelect: (event: React.MouseEvent) => void; onReindex: () => void; onTrash: () => void }) {
  const queryClient = useQueryClient()
  const favorite = useMutation({ mutationFn: () => client.updateDocument(document.id, { favorite: !document.favorite }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['documents'] }) })
  const TypeIcon = document.mediaType.includes('markdown') || document.mediaType.startsWith('text/') ? FileCode2 : File
  const actions: WorkbenchAction[] = [
    { id: 'favorite', label: document.favorite ? '取消收藏' : '收藏', run: () => favorite.mutate() },
    { id: 'reindex', label: '重新索引', disabledReason: document.status === 'pending' ? '文档正在处理中' : undefined, run: onReindex },
    { id: 'trash', label: '移入回收站', danger: true, run: onTrash },
  ]
  return <ContextActions actions={actions}><div className={`document-row-shell ${active || selected ? 'selected' : ''}`}><button type="button" className={`document-row ${active || selected ? 'active' : ''}`} onClick={onSelect}><span className="file-icon"><TypeIcon size={19}/></span><span className="document-copy"><strong>{document.title}</strong><small>{document.sourceUrl || document.sourcePath || document.mediaType}</small></span><span className={`status-pill ${document.status}`}>{document.status === 'ready' ? '已索引' : document.status === 'pending' ? '处理中' : document.status === 'pending_review' ? '待审核' : document.status === 'approved_pending_index' ? '待发布' : document.status === 'rejected' ? '已驳回' : document.status === 'failed' ? '失败' : '源丢失'}</span><time>{new Date(document.updatedAt).toLocaleDateString()}</time></button><MoreActions label="当前文档更多操作" actions={actions}/><button type="button" className="favorite-toggle" aria-label={document.favorite ? '取消收藏' : '收藏'} aria-pressed={document.favorite} onClick={() => favorite.mutate()} disabled={favorite.isPending}><Star size={16} fill={document.favorite ? 'currentColor' : 'none'}/></button></div></ContextActions>
}

function LocationLabel({ location }: { location: Record<string, unknown> }) {
  const parts = Object.entries(location).filter(([key]) => ['page','slide','sheet','heading','lineStart','rowStart'].includes(key)).map(([key, value]) => `${key}: ${String(value)}`)
  return <span><BookOpen size={13}/>{parts.join(' · ') || '文档片段'}</span>
}

function KnowledgePreview({ detail, loading, onOpenDocument, onDraftDirty }: { detail?: KnowledgeRevision; loading: boolean; onOpenDocument: (id: string) => void; onDraftDirty: (dirty: boolean) => void }) {
  const [openError, setOpenError] = useState('')
  const [editing, setEditing] = useState(false)
  useEffect(() => { setEditing(false); onDraftDirty(false) }, [detail?.knowledgeId, detail?.revision, onDraftDirty])
  if (loading) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入知识体</div>
  if (!detail) return <Empty icon={<BookOpen size={25}/>} title="选择一个知识体" text="知识正文、来源和审查提示会显示在这里。" />
  if (editing) return <KnowledgeRevisionEditor detail={detail} onCancel={() => setEditing(false)} onDirtyChange={onDraftDirty}/>
  const payload = detail.payload
  async function openExternal(resource: string) {
    setOpenError('')
    try {
      await window.kah.openExternal(resource)
    } catch (error) {
      setOpenError(error instanceof Error ? error.message : String(error))
    }
  }
  return <div className="preview-pane knowledge-preview-pane">
    <div className="preview-heading"><div><span className="eyebrow">KAH Knowledge Profile v1</span><h2>{payload.title}</h2><p>{payload.description}</p></div><div className="knowledge-heading-actions"><span className={`status-pill ${detail.status}`}>{detail.stable ? `稳定 r${detail.revision}` : detail.status}</span>{detail.stable && <button type="button" className="button secondary" onClick={() => setEditing(true)}><Pencil size={15}/>创建修订</button>}</div></div>
    <div className="preview-meta knowledge-preview-meta"><span>{payload.type}{payload.subtype ? ` · ${payload.subtype}` : ''}</span><span>{payload.language}</span><span>{detail.flags.length ? detail.flags.join('、') : '无运行时警告'}</span></div>
    {payload.sections.map((section) => <section className="preview-section" key={section.id}><h3>{section.heading}</h3><MarkdownContent content={section.content}/></section>)}
    {openError && <div className="inline-error" role="alert"><CircleAlert size={16}/>{openError}</div>}
    {payload.sources?.length ? <section className="preview-section knowledge-sources"><h3>可引用来源</h3><ul>{payload.sources.map((source) => { const documentId = source.resource.startsWith('kah://document/') ? source.resource.slice('kah://document/'.length) : ''; return <li key={source.id}><div className="knowledge-source-main"><code>{`[^${source.id}]`}</code>{source.resource.startsWith('https://') ? <a href={source.resource} onClick={(event) => { event.preventDefault(); void openExternal(source.resource) }}>{source.title || source.resource}</a> : <span>{source.title || source.resource}</span>}</div><div className="knowledge-source-meta">{documentId && <button type="button" className="text-button source-open-button" onClick={() => onOpenDocument(documentId)}>查看来源文档</button>}{source.locator && <small>{Object.entries(source.locator).map(([key, value]) => `${key}: ${String(value)}`).join(' · ')}</small>}</div></li> })}</ul></section> : null}
  </div>
}

function KnowledgeRevisionEditor({ detail, onCancel, onDirtyChange }: { detail: KnowledgeRevision; onCancel: () => void; onDirtyChange: (dirty: boolean) => void }) {
  const queryClient = useQueryClient()
  const storageKey = `kah.knowledge-draft.${detail.knowledgeId}.r${detail.revision}`
  const [payload, setPayload] = useState<KnowledgeRevision['payload']>(() => {
    const fallback = structuredClone(detail.payload)
    try {
      const stored = JSON.parse(window.localStorage.getItem(storageKey) || 'null')
      return isKnowledgePayload(stored) ? stored : fallback
    } catch { return fallback }
  })
  const [advanced, setAdvanced] = useState(false)
  const [source, setSource] = useState(() => JSON.stringify(payload, null, 2))
  const [sourceError, setSourceError] = useState('')
  const [submitted, setSubmitted] = useState('')
  const dirty = JSON.stringify(payload) !== JSON.stringify(detail.payload)
  useEffect(() => { onDirtyChange(dirty); return () => onDirtyChange(false) }, [dirty, onDirtyChange])
  useEffect(() => { if (dirty) window.localStorage.setItem(storageKey, JSON.stringify(payload)) }, [dirty, payload, storageKey])
  useEffect(() => { if (!advanced) setSource(JSON.stringify(payload, null, 2)) }, [advanced, payload])
  useEffect(() => { if (!dirty) return; const listener = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }; window.addEventListener('beforeunload', listener); return () => window.removeEventListener('beforeunload', listener) }, [dirty])
  const issues = useMemo(() => {
    const values: string[] = []
    if (!payload.title.trim()) values.push('标题不能为空')
    if (!payload.description.trim()) values.push('描述不能为空')
    if (!payload.type.trim()) values.push('类型不能为空')
    if (!payload.sections.length) values.push('至少需要一个章节')
    const sourceIds = new Set((payload.sources ?? []).map((item) => item.id))
    const duplicateSourceIds = new Set<string>()
    for (const source of payload.sources ?? []) {
      if (!source.id.trim()) values.push('来源 ID 不能为空')
      if (!source.resource.trim()) values.push(`来源“${source.id || '未命名'}”缺少资源地址`)
      if (sourceIds.has(source.id) && duplicateSourceIds.has(source.id)) values.push(`来源 ID ${source.id} 不能重复`)
      duplicateSourceIds.add(source.id)
    }
    for (const section of payload.sections) for (const match of section.content.matchAll(/\[\^([^\]]+)\]/g)) if (!sourceIds.has(match[1])) values.push(`章节“${section.heading}”引用了缺失来源 ${match[1]}`)
    for (const relation of payload.relations ?? []) {
      if (!relation.type.trim()) values.push('关系类型不能为空')
      if (!relation.target.trim()) values.push(`关系“${relation.type || '未命名'}”缺少目标 URI`)
      if (relation.target_revision !== undefined && (!Number.isInteger(relation.target_revision) || relation.target_revision < 1)) values.push(`关系“${relation.type || '未命名'}”的目标 revision 必须为正整数`)
    }
    return Array.from(new Set(values))
  }, [payload])
  const submit = useMutation({ mutationFn: () => client.createKnowledgeRevision({ libraryId: detail.libraryId, mode: 'propose_revision', knowledgeUri: detail.uri, baseRevision: detail.revision, clientSubmissionId: crypto.randomUUID(), payload: { ...payload, revision: detail.revision + 1 } }), onSuccess: (item) => { window.localStorage.removeItem(storageKey); onDirtyChange(false); setSubmitted(`修订草稿已提交审核：${item.id}`); void queryClient.invalidateQueries({ queryKey: ['submissions'] }) } })
  function update<K extends keyof KnowledgeRevision['payload']>(key: K, value: KnowledgeRevision['payload'][K]) { setPayload((current) => ({ ...current, [key]: value })) }
  function changeSource(value: string) {
    setSource(value)
    try {
      const next: unknown = JSON.parse(value)
      if (!isKnowledgePayload(next)) {
        setSourceError('源码必须包含标题、描述、类型、语言和结构化章节；章节的 id、标题和内容均不能为空。')
        return
      }
      setPayload(next)
      setSourceError('')
    } catch (error) { setSourceError((error as Error).message) }
  }
  function cancel() { if (dirty && !window.confirm('本机仍保留修订草稿。确定关闭编辑器？')) return; onDirtyChange(false); onCancel() }
  return <div className="revision-editor"><header className="revision-editor-heading"><div><span className="eyebrow">基于稳定版 r{detail.revision}</span><h2>创建知识修订</h2></div><div className="segmented-control"><button type="button" className={!advanced ? 'active' : ''} onClick={() => setAdvanced(false)}>结构化</button><button type="button" className={advanced ? 'active' : ''} onClick={() => setAdvanced(true)}>JSON 源码</button></div></header>{advanced ? <div className="revision-source"><CodeMirror value={source} height="420px" onChange={changeSource}/>{sourceError && <p className="form-error" role="alert">源码校验：{sourceError}</p>}</div> : <div className="revision-fields"><label>标题<input value={payload.title} onChange={(event) => update('title', event.target.value)}/></label><label>描述<textarea value={payload.description} rows={3} onChange={(event) => update('description', event.target.value)}/></label><div className="form-grid"><label>类型<input value={payload.type} onChange={(event) => update('type', event.target.value)}/></label><label>语言<select value={payload.language} onChange={(event) => update('language', event.target.value as 'zh-CN'|'en')}><option value="zh-CN">zh-CN</option><option value="en">en</option></select></label></div><label>标签<input value={(payload.tags ?? []).join(', ')} onChange={(event) => update('tags', parseLibraryTags(event.target.value))}/></label><div className="revision-sections"><div className="content-heading"><h3>章节</h3><button type="button" className="text-button" onClick={() => update('sections', [...payload.sections, { id: `section-${payload.sections.length + 1}`, heading: '新章节', content: '' }])}><Plus size={14}/>增加章节</button></div>{payload.sections.map((section, index) => <article key={`${section.id}-${index}`}><div><input aria-label={`章节 ${index + 1} 标题`} value={section.heading} onChange={(event) => update('sections', payload.sections.map((item, itemIndex) => itemIndex === index ? { ...item, heading: event.target.value } : item))}/><input aria-label={`章节 ${index + 1} ID`} value={section.id} onChange={(event) => update('sections', payload.sections.map((item, itemIndex) => itemIndex === index ? { ...item, id: event.target.value } : item))}/><button type="button" className="icon-button" aria-label="删除章节" onClick={() => update('sections', payload.sections.filter((_, itemIndex) => itemIndex !== index))}><Trash2 size={15}/></button></div><textarea value={section.content} rows={6} onChange={(event) => update('sections', payload.sections.map((item, itemIndex) => itemIndex === index ? { ...item, content: event.target.value } : item))}/></article>)}</div><div className="revision-related-fields"><div className="content-heading"><h3>来源</h3><button type="button" className="text-button" onClick={() => update('sources', [...(payload.sources ?? []), { id: `source-${(payload.sources?.length ?? 0) + 1}`, resource: '', title: '' }])}><Plus size={14}/>增加来源</button></div>{(payload.sources ?? []).map((item, index) => <article className="revision-related-row" key={`${item.id}-${index}`}><input aria-label={`来源 ${index + 1} ID`} value={item.id} onChange={(event) => update('sources', (payload.sources ?? []).map((source, sourceIndex) => sourceIndex === index ? { ...source, id: event.target.value } : source))}/><input aria-label={`来源 ${index + 1} 地址`} value={item.resource} onChange={(event) => update('sources', (payload.sources ?? []).map((source, sourceIndex) => sourceIndex === index ? { ...source, resource: event.target.value } : source))}/><input aria-label={`来源 ${index + 1} 标题`} value={item.title ?? ''} onChange={(event) => update('sources', (payload.sources ?? []).map((source, sourceIndex) => sourceIndex === index ? { ...source, title: event.target.value } : source))}/><button type="button" className="icon-button" aria-label={`删除来源 ${index + 1}`} onClick={() => update('sources', (payload.sources ?? []).filter((_, sourceIndex) => sourceIndex !== index))}><Trash2 size={15}/></button></article>)}</div><div className="revision-related-fields"><div className="content-heading"><h3>关系</h3><button type="button" className="text-button" onClick={() => update('relations', [...(payload.relations ?? []), { type: 'related_to', target: '' }])}><Plus size={14}/>增加关系</button></div>{(payload.relations ?? []).map((item, index) => <article className="revision-related-row" key={`${item.type}-${item.target}-${index}`}><input aria-label={`关系 ${index + 1} 类型`} value={item.type} onChange={(event) => update('relations', (payload.relations ?? []).map((relation, relationIndex) => relationIndex === index ? { ...relation, type: event.target.value } : relation))}/><input aria-label={`关系 ${index + 1} 目标`} value={item.target} onChange={(event) => update('relations', (payload.relations ?? []).map((relation, relationIndex) => relationIndex === index ? { ...relation, target: event.target.value } : relation))}/><input aria-label={`关系 ${index + 1} revision`} type="number" min={1} value={item.target_revision ?? ''} onChange={(event) => update('relations', (payload.relations ?? []).map((relation, relationIndex) => relationIndex === index ? { ...relation, target_revision: event.target.value ? Number(event.target.value) : undefined } : relation))}/><button type="button" className="icon-button" aria-label={`删除关系 ${index + 1}`} onClick={() => update('relations', (payload.relations ?? []).filter((_, relationIndex) => relationIndex !== index))}><Trash2 size={15}/></button></article>)}</div></div>}<div className={`revision-validation ${issues.length ? 'has-error' : ''}`} role="status"><strong>{issues.length ? `${issues.length} 个提交前问题` : '结构校验通过'}</strong>{issues.map((issue) => <span key={issue}>{issue}</span>)}<small>来源快照：{(payload.sources ?? []).filter((source) => source.snapshot?.content_hash).length}/{payload.sources?.length ?? 0} 个具备内容哈希。提交时若稳定版已变化，服务会返回冲突并要求重新基于最新版本。</small></div>{submit.error && <div className="inline-error" role="alert"><CircleAlert size={16}/>{(submit.error as Error).message}</div>}{submitted && <p className="settings-success" role="status">{submitted}</p>}<div className="revision-actions"><button type="button" className="button ghost" onClick={cancel}>关闭</button><button type="button" className="button secondary" onClick={() => { window.localStorage.setItem(storageKey, JSON.stringify(payload)); setSubmitted('草稿已保存在本机，尚未提交。') }} disabled={!dirty}>保留本机草稿</button><button type="button" className="button primary" onClick={() => submit.mutate()} disabled={submit.isPending || issues.length > 0 || Boolean(sourceError)}>{submit.isPending ? <LoaderCircle className="spin" size={15}/> : <CheckCircle2 size={15}/>}提交审核</button></div></div>
}

function DocumentPreview({ detail, loading, onSaved, onLink, onDraftDirty }: { detail?: Awaited<ReturnType<typeof client.document>>; loading: boolean; onSaved: () => void; onLink: (href: string, source?: Pick<Document, 'sourcePath' | 'libraryId'>) => void; onDraftDirty: (dirty: boolean) => void }) {
  const [editing, setEditing] = useState(false)
  const [content, setContent] = useState('')
  const [titleDraft, setTitleDraft] = useState('')
  const [tagDraft, setTagDraft] = useState('')
  const [openError, setOpenError] = useState('')
  const [trashed, setTrashed] = useState(false)
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest>()
  const folders = useQuery({ queryKey: ['folders', detail?.libraryId], queryFn: () => client.folders(detail!.libraryId), enabled: Boolean(detail?.libraryId) })
  const metadataMutation = useMutation({ mutationFn: ({ title, tags }: { title: string; tags: string[] }) => client.updateDocument(detail!.id, { title, tags }), onSuccess: onSaved })
  const save = useMutation({ mutationFn: () => client.updateDocument(detail!.id, { content }), onSuccess: () => { setEditing(false); onSaved() } })
  const folderMutation = useMutation({ mutationFn: ({ folderId, checked }: { folderId: string; checked: boolean }) => checked ? client.assignFolder(detail!.id, folderId) : client.removeFolder(detail!.id, folderId), onSuccess: onSaved })
  const reindex = useMutation({ mutationFn: () => client.reindexDocument(detail!.id), onSuccess: onSaved })
  const trashDocument = useMutation({ mutationFn: () => client.deleteDocument(detail!.id), onSuccess: () => { setTrashed(true); onSaved() } })
  const relink = useMutation({ mutationFn: async () => { const paths = await window.kah.selectFiles(); if (!paths[0]) return undefined; return client.relinkDocument(detail!.id, paths[0]) }, onSuccess: (value) => { if (value) onSaved() } })
  useEffect(() => { setContent(detail?.preview.map((chunk) => chunk.text).join('\n\n') ?? ''); setTitleDraft(detail?.title ?? ''); setTagDraft(detail?.tags.join(', ') ?? ''); setEditing(false); setTrashed(false) }, [detail?.id])
  const originalContent = detail?.preview.map((chunk) => chunk.text).join('\n\n') ?? ''
  const contentDirty = editing && content !== originalContent
  useEffect(() => {
    onDraftDirty(contentDirty)
    return () => onDraftDirty(false)
  }, [contentDirty, onDraftDirty])
  useEffect(() => {
    if (!contentDirty) return
    const listener = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', listener)
    return () => window.removeEventListener('beforeunload', listener)
  }, [contentDirty])
  if (loading) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入预览</div>
  if (!detail || trashed) return <Empty icon={<PanelRight size={25}/>} title={trashed ? '文档已移入回收站' : '选择一个文档'} text={trashed ? '可从左侧回收站恢复此文档。' : '证据、来源和结构化预览会显示在这里。'} />
  const currentDetail = detail
  const editable = currentDetail.mediaType.startsWith('text/') || currentDetail.mediaType.includes('markdown')
  async function openSource() {
    if (!currentDetail.sourcePath) return
    setOpenError('')
    try {
      const result = await window.kah.openPath(currentDetail.sourcePath)
      if (result) setOpenError(result)
    } catch (error) {
      setOpenError(error instanceof Error ? error.message : String(error))
    }
  }
  function saveTags() {
    metadataMutation.mutate({ title: titleDraft.trim(), tags: parseLibraryTags(tagDraft) })
  }
  const operationError = metadataMutation.error || save.error || folderMutation.error || reindex.error || trashDocument.error || relink.error
  return <div className="preview-pane document-preview-pane">
    <div className="preview-heading document-preview-heading"><div className="document-preview-heading-main"><div className="preview-icon"><File size={20}/></div><div><span className="eyebrow">{detail.mediaType}</span><h2>{detail.title}</h2><p>{detail.sourceUrl || detail.sourcePath || '本地来源资料'}</p></div></div><div className="knowledge-heading-actions"><span className={`status-pill ${detail.status}`}>{detail.status === 'ready' ? '索引就绪' : detail.status}</span></div></div>
    <div className="preview-meta knowledge-preview-meta"><span><Database size={14}/>{detail.preview.length} 个片段</span>{detail.favorite && <span><Star size={13} fill="currentColor"/>已收藏</span>}<span>{editable ? '可编辑正文' : '只读预览'}</span></div>
    <section className="preview-section document-info-section"><div className="document-section-heading"><div><h3>资料信息</h3><p>修改标题或标签后保存到当前知识库。</p></div><button type="button" className="button secondary" onClick={saveTags} disabled={metadataMutation.isPending || !titleDraft.trim()}>{metadataMutation.isPending ? <LoaderCircle className="spin" size={14}/> : <Save size={14}/>}保存信息</button></div><div className="document-metadata-editor"><label>标题<input value={titleDraft} onChange={(event) => setTitleDraft(event.target.value)} maxLength={240}/></label><label>标签<input value={tagDraft} onChange={(event) => setTagDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); saveTags() } }} placeholder="用逗号分隔"/></label></div></section>
    <section className="preview-section document-folder-section"><h3>目录归类</h3><p>一份文档可以加入多个目录。</p><fieldset className="document-folder-picker" aria-label="文档目录归类">{folders.data?.map((folder) => <label key={folder.id}><input type="checkbox" checked={(detail.folderIds ?? []).includes(folder.id)} disabled={folderMutation.isPending} onChange={(event) => folderMutation.mutate({ folderId: folder.id, checked: event.target.checked })}/>{folder.name}</label>)}{!folders.data?.length && <span className="muted">当前知识库还没有目录。</span>}</fieldset></section>
    {(operationError || openError) && <div className="inline-error" role="alert"><CircleAlert size={16}/>{openError || String((operationError as Error).message)}</div>}
    <section className="preview-section document-content-section"><div className="document-section-heading"><div><h3>{editing ? '编辑正文' : '文档正文'}</h3><p>{editing ? '保存后会重新建立索引。' : '按原始资料内容展示，可继续查看结构和定位信息。'}</p></div><div className="preview-actions">{editable && <button type="button" className="button secondary" onClick={() => { if (contentDirty && !window.confirm('正文有未保存修改，确定放弃？')) return; setEditing((value) => !value) }}><FileCode2 size={16}/>{editing ? '取消编辑' : '编辑正文'}</button>}{detail.sourcePath && <button type="button" className="button ghost" onClick={() => void openSource()}><FolderSearch2 size={16}/>外部打开</button>}<button type="button" className="button ghost" onClick={() => reindex.mutate()} disabled={reindex.isPending}><RefreshCw size={16}/>{reindex.isPending ? '正在索引…' : '重新索引'}</button>{detail.status === 'source_missing' && <button type="button" className="button secondary" onClick={() => relink.mutate()} disabled={relink.isPending}><Link2 size={16}/>{relink.isPending ? '正在定位…' : '重新定位'}</button>}<MoreActions actions={[{ id: 'trash', label: '移入回收站', danger: true, run: () => setConfirmRequest({ title: `将“${detail.title}”移入回收站？`, description: '文档会从检索与索引中排除，但可恢复。', confirmLabel: '移入回收站', danger: true, onConfirm: () => trashDocument.mutateAsync() }) }]}/></div></div><div className="preview-content">{editing ? <><CodeMirror value={content} height="100%" extensions={[markdown()]} onChange={setContent}/><button type="button" className="button primary editor-save" onClick={() => save.mutate()} disabled={save.isPending || !contentDirty}>{save.isPending ? <LoaderCircle className="spin" size={16}/> : <Save size={16}/>} {save.isPending ? '正在保存…' : '保存并重建索引'}</button></> : detail.mediaType.includes('markdown') ? <MarkdownContent content={content} onLink={(href) => onLink(href, detail)} /> : detail.preview.map((chunk) => <article className="chunk-preview" key={chunk.id}><LocationLabel location={chunk.location}/><p>{chunk.text}</p></article>)}</div></section>
    <ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(undefined)}/>
  </div>
}

function SettingsDialog({ open, onOpenChange, libraries, updateState, onInstallUpdate }: { open: boolean; onOpenChange: (open:boolean) => void; libraries: LibraryType[]; updateState: UpdateState; onInstallUpdate: () => void }) {
  const [runtime, setRuntime] = useState<RuntimeConfig>()
  const [runtimeError, setRuntimeError] = useState('')
  useEffect(() => { let mounted = true; getRuntime().then((value) => { if (mounted) setRuntime(value) }).catch((error) => { if (mounted) setRuntimeError(error instanceof Error ? error.message : String(error)) }); return () => { mounted = false } }, [])
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="settings-dialog"><Dialog.Title>设置</Dialog.Title><Dialog.Description>管理外观、模型、检索、Agent 访问、安全和便携数据。</Dialog.Description><Dialog.Close asChild><button type="button" className="dialog-close" aria-label="关闭设置"><X size={19}/></button></Dialog.Close><Tabs.Root defaultValue="appearance" orientation="vertical" className="settings-tabs"><Tabs.List aria-label="设置分类"><Tabs.Trigger value="appearance"><Palette size={17}/>外观</Tabs.Trigger><Tabs.Trigger value="models"><Bot size={17}/>模型服务</Tabs.Trigger><Tabs.Trigger value="retrieval"><Search size={17}/>检索模型</Tabs.Trigger><Tabs.Trigger value="agents"><KeyRound size={17}/>Agent 访问</Tabs.Trigger><Tabs.Trigger value="data"><Database size={17}/>数据与备份</Tabs.Trigger><Tabs.Trigger value="plugins">可选插件</Tabs.Trigger>{updateState.enabled && <Tabs.Trigger value="updates"><RefreshCw size={17}/>应用更新</Tabs.Trigger>}<Tabs.Trigger value="privacy"><ShieldCheck size={17}/>隐私与安全</Tabs.Trigger></Tabs.List><div className="settings-panel"><Tabs.Content value="appearance"><AppearanceSettings/></Tabs.Content><Tabs.Content value="models"><ProviderSettings/></Tabs.Content><Tabs.Content value="retrieval"><RetrievalModelSettings libraries={libraries}/></Tabs.Content><Tabs.Content value="agents"><TokenSettings libraries={libraries}/></Tabs.Content><Tabs.Content value="data"><DataSettings runtime={runtime} runtimeError={runtimeError}/></Tabs.Content><Tabs.Content value="plugins"><OcrPluginSettings/></Tabs.Content>{updateState.enabled && <Tabs.Content value="updates"><UpdatesSettings state={updateState} onInstall={onInstallUpdate}/></Tabs.Content>}<Tabs.Content value="privacy"><PrivacySettings libraries={libraries}/></Tabs.Content></div></Tabs.Root></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function AppearanceSettings() {
  const ui = useUI()
  const systemPrefersDark = typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches
  const appliedTheme = ui.theme === 'system' ? (systemPrefersDark ? 'dark' : 'light') : ui.theme
  const themeOptions: Array<{ value: 'dark' | 'light' | 'system'; label: string; description: string; icon: React.ReactNode }> = [
    { value: 'dark', label: '深色', description: '适合长时间阅读与夜间使用。', icon: <Moon size={18}/> },
    { value: 'light', label: '浅色', description: '在明亮环境中保持更清晰的层级。', icon: <Sun size={18}/> },
    { value: 'system', label: '跟随系统', description: `当前会使用系统${systemPrefersDark ? '深色' : '浅色'}模式。`, icon: <Monitor size={18}/> }
  ]
  return <section><h3>外观</h3><p className="settings-lead">主题和信息密度会保存在此设备；切换后立即应用到窗口标题栏和所有工作区。</p><fieldset className="appearance-group"><legend>主题</legend><div className="appearance-choice-grid">{themeOptions.map((option) => <button type="button" key={option.value} className={`appearance-choice ${ui.theme === option.value ? 'active' : ''}`} aria-label={`使用${option.label}主题`} aria-pressed={ui.theme === option.value} onClick={() => ui.setTheme(option.value)}><span className="appearance-choice-icon" aria-hidden="true">{option.icon}</span><span><strong>{option.label}</strong><small>{option.description}</small></span></button>)}</div><p className="appearance-status" role="status">当前已应用：{appliedTheme === 'dark' ? '深色主题' : '浅色主题'}。</p></fieldset><fieldset className="appearance-group"><legend>信息密度</legend><div className="appearance-choice-grid density-choice-grid"><button type="button" className={`appearance-choice ${ui.density === 'comfortable' ? 'active' : ''}`} aria-label="使用舒适信息密度" aria-pressed={ui.density === 'comfortable'} onClick={() => ui.density === 'compact' && ui.toggleDensity()}><span><strong>舒适</strong><small>更充裕的行距与控件留白，便于专注阅读。</small></span></button><button type="button" className={`appearance-choice ${ui.density === 'compact' ? 'active' : ''}`} aria-label="使用紧凑信息密度" aria-pressed={ui.density === 'compact'} onClick={() => ui.density === 'comfortable' && ui.toggleDensity()}><span><strong>紧凑</strong><small>在相同窗口中显示更多资料与待审核条目。</small></span></button></div></fieldset></section>
}

function ProviderSettings() {
  const queryClient = useQueryClient()
  const providers = useQuery({ queryKey:['providers'], queryFn:client.providers })
  const [draft,setDraft] = useState<Provider>({id:'',name:'LM Studio',kind:'lmstudio',baseUrl:'http://127.0.0.1:1234',model:'',embeddingModel:'',local:true,apiKey:''})
  const [savedMessage, setSavedMessage] = useState('')
  const [discoveredModels, setDiscoveredModels] = useState<Record<string, string[]>>({})
  const [discoveringId, setDiscoveringId] = useState('')
  const [modelError, setModelError] = useState('')
  const save=useMutation({ mutationFn:()=>client.saveProvider(draft), onSuccess:(provider)=>{ queryClient.invalidateQueries({queryKey:['providers']}); setSavedMessage(`已保存 ${provider.name}。`); setDraft({...draft,id:'',apiKey:''}) } })
  const test = useMutation({ mutationFn: client.testProvider, onSuccess: (result) => setSavedMessage(`连接成功，发现 ${result.models.length} 个模型。`) })
  const remove = useMutation({ mutationFn: client.deleteProvider, onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['providers'] }); setDraft({id:'',name:'LM Studio',kind:'lmstudio',baseUrl:'http://127.0.0.1:1234',model:'',embeddingModel:'',local:true,apiKey:''}) } })
  async function discoverModels(provider: Provider) {
    setDiscoveringId(provider.id); setModelError('')
    try { const result = await client.providerModels(provider.id); setDiscoveredModels((current) => ({ ...current, [provider.id]: result.models })) }
    catch (error) { setModelError(`${provider.name}：${(error as Error).message}`) }
    finally { setDiscoveringId('') }
  }
  const draftReady = Boolean(draft.name.trim() && draft.baseUrl.trim() && draft.model.trim())
  return <section><h3>模型服务</h3><p className="settings-lead">选择现有服务即可编辑、测试连接、发现模型或删除。被知识库自动化引用的服务会阻止删除并列出依赖。</p><div className="provider-list">{providers.data?.map((provider)=><button type="button" className={`provider-card provider-card-expanded ${draft.id === provider.id ? 'active' : ''}`} key={provider.id} onClick={() => { setDraft({ ...provider, apiKey: '' }); setSavedMessage('') }}><div className="provider-logo"><Bot size={18}/></div><div><strong>{provider.name}</strong><span>{provider.kind} · {provider.baseUrl}</span>{discoveredModels[provider.id] && <small className="provider-model-list" role="status">可用模型：{discoveredModels[provider.id].length ? discoveredModels[provider.id].join(' · ') : '此服务未返回模型列表'}</small>}</div><span className="local-badge">{provider.local?'本地':'远程'}</span></button>)}</div>{modelError && <p className="form-error" role="alert">{modelError}</p>}<div className="form-grid"><label>名称<input value={draft.name} onChange={(e)=>setDraft({...draft,name:e.target.value})}/></label><label>类型<select value={draft.kind} onChange={(e)=>{const kind=e.target.value as Provider['kind'];setDraft({...draft,kind,local:kind==='lmstudio'})}}><option value="lmstudio">LM Studio</option><option value="openai">OpenAI compatible</option><option value="anthropic">Anthropic</option><option value="custom">自定义 OpenAI 兼容端点</option></select></label><label className="span-2">Base URL<input value={draft.baseUrl} onChange={(e)=>setDraft({...draft,baseUrl:e.target.value})}/></label><label>生成模型<input value={draft.model} onChange={(e)=>setDraft({...draft,model:e.target.value})}/></label><label>嵌入模型<input value={draft.embeddingModel} onChange={(e)=>setDraft({...draft,embeddingModel:e.target.value})}/></label><label className="span-2">API Key<input type="password" value={draft.apiKey} onChange={(e)=>setDraft({...draft,apiKey:e.target.value})} autoComplete="off"/></label></div><div className="settings-actions"><button type="button" className="button primary" onClick={()=>save.mutate()} disabled={save.isPending || !draftReady}><Save size={16}/>{save.isPending ? '正在保存…' : draft.id ? '保存修改' : '保存服务'}</button>{draft.id && <><button type="button" className="button secondary" onClick={() => test.mutate(draft.id)} disabled={test.isPending}><RefreshCw size={15}/>测试连接</button><button type="button" className="button secondary" onClick={()=>void discoverModels(draft)} disabled={discoveringId === draft.id}><RefreshCw className={discoveringId === draft.id ? 'spin' : ''} size={15}/>发现模型</button><button type="button" className="button danger" onClick={() => { if (window.confirm(`删除模型服务“${draft.name}”？`)) remove.mutate(draft.id) }} disabled={remove.isPending}><Trash2 size={15}/>删除</button></>}</div>{!draftReady && <p className="field-hint">填写名称、Base URL 和生成模型后即可保存。</p>}{savedMessage && <p className="settings-success" role="status">{savedMessage}</p>}{(save.error||test.error||remove.error)&&<p className="form-error" role="alert">{String(((save.error||test.error||remove.error) as Error).message)}</p>}</section>
}

function TokenSettings({ libraries }: { libraries: LibraryType[] }) {
  const queryClient = useQueryClient()
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: client.tokens })
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [scopes, setScopes] = useState<AgentToken['scopes']>(['mcp_read'])
  const [libraryIds, setLibraryIds] = useState<string[]>([])
  const [copyMessage, setCopyMessage] = useState('')
  const create = useMutation({ mutationFn: () => client.createToken(name, scopes, libraryIds), onSuccess: (token) => { setSecret(token.secret ?? ''); setName(''); setScopes(['mcp_read']); setLibraryIds([]); queryClient.invalidateQueries({ queryKey: ['tokens'] }) } })
  const revoke = useMutation({ mutationFn: client.revokeToken, onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tokens'] }) })
  const toggleScope = (scope: AgentToken['scopes'][number]) => setScopes((current) => current.includes(scope) ? current.filter((value) => value !== scope) as AgentToken['scopes'] : [...current, scope])
  const manageEnabled = scopes.includes('mcp_manage')
  async function copySecret() {
    try { await navigator.clipboard.writeText(secret); setCopyMessage('令牌已复制。') }
    catch { setCopyMessage('无法访问剪贴板，请手动复制令牌。') }
  }
  return <section><h3>Agent MCP 访问令牌</h3><p className="settings-lead">令牌只显示一次；创建时必须明确选择可访问的知识库与权限。</p>{secret&&<div className="secret-box"><strong>立即复制此令牌</strong><code>{secret}</code><button type="button" onClick={()=>void copySecret()}>复制</button>{copyMessage&&<span className="settings-success" role="status">{copyMessage}</span>}</div>}<div className="inline-form"><label><span>令牌名称</span><input value={name} onChange={(e)=>setName(e.target.value)} placeholder="例如：本机 Codex"/></label><button type="button" className="button primary" onClick={()=>create.mutate()} disabled={!name.trim() || !scopes.length || !libraryIds.length || create.isPending}>创建令牌</button></div><fieldset className="token-library-picker"><legend>可访问知识库</legend>{libraries.map((library) => <label key={library.id}><input type="checkbox" checked={libraryIds.includes(library.id)} onChange={(event) => setLibraryIds((current) => event.target.checked ? [...current, library.id] : current.filter((id) => id !== library.id))}/><span><strong>{library.name}</strong><small>{library.description || '本地知识库'}</small></span></label>)}</fieldset><div className="token-scope-grid"><strong>令牌能力</strong>{(['mcp_read', 'mcp_manage'] as const).map((scope)=><label className="switch-row" key={scope}><span><strong>{scope === 'mcp_read' ? 'Read MCP：获取知识' : 'Manage MCP：管理、比较与审核知识'}</strong><small>{scope === 'mcp_manage' ? '可提交、比较和审核；仅信度 > 95% 才直接批准并发布，其他情况转人工' : '搜索、目录读取与章节读取'}</small></span><input type="checkbox" checked={scopes.includes(scope)} onChange={()=>toggleScope(scope)}/></label>)}</div>{manageEnabled && <p className="settings-lead">Manage 权限绑定所选 {libraryIds.length} 个知识库；Agent 审批仍受严格大于 95% 的信度门槛约束。</p>}{(create.error || revoke.error) && <p className="form-error" role="alert">{((create.error || revoke.error) as Error).message}</p>}<div className="token-list">{tokens.data?.map((token:AgentToken)=><div className="token-row" key={token.id}><KeyRound size={17}/><div><strong>{token.name}</strong><span>{token.scopes.join(' · ')} · {token.libraryIds.length} 个知识库 · {new Date(token.createdAt).toLocaleDateString()}</span></div><button type="button" className="danger-icon" aria-label={'撤销 ' + token.name} onClick={()=>{if(window.confirm(`立即撤销令牌“${token.name}”？`)) revoke.mutate(token.id)}} disabled={revoke.isPending}><Trash2 size={17}/>{revoke.isPending ? '…' : ''}</button></div>)}</div></section>
}
function DataSettings({runtime, runtimeError}:{runtime?:RuntimeConfig; runtimeError?:string}) {
  const [result,setResult] = useState<{path:string;sha256:string}>()
  const [copyMessage, setCopyMessage] = useState('')
  const [openError, setOpenError] = useState('')
  const [libraryId, setLibraryId] = useState('')
  const libraries = useQuery({ queryKey: ['libraries'], queryFn: client.libraries })
  const audit = useQuery({ queryKey: ['audit'], queryFn: client.audit })
  const backups = useQuery({ queryKey: ['backups'], queryFn: client.backups })
  const backup = useMutation({ mutationFn:client.backup, onSuccess:(value) => { setResult(value); void backups.refetch() } })
  const verify = useMutation({ mutationFn: client.verifyBackup, onSuccess: (value) => setCopyMessage(`备份校验通过：${value.sha256}`) })
  const rebuild = useMutation({ mutationFn: client.rebuildIndex, onSuccess: () => setCopyMessage('索引重建任务已加入活动中心。') })
  async function copyBackupProof() {
    if (!result) return
    try { await navigator.clipboard.writeText(`SHA-256 ${result.sha256}`); setCopyMessage('校验值已复制。') }
    catch { setCopyMessage('无法访问剪贴板，请手动复制校验值。') }
  }
  async function openPath(path: string) {
    setOpenError('')
    try {
      const normalized = path.replaceAll('/', '\\')
      const absolute = runtime?.dataRoot && !/^(?:[A-Za-z]:[\\/]|\\\\|\/)/.test(normalized)
        ? `${runtime.dataRoot.replace(/[\\/]$/, '')}\\${normalized}`
        : path
      const error = await window.kah.openPath(absolute)
      if (error) setOpenError(error)
    } catch (value) {
      setOpenError(value instanceof Error ? value.message : String(value))
    }
  }
  return <section><h3>数据与备份</h3><p className="settings-lead">备份文件可在恢复前独立校验；恢复由 Electron 主进程停启后台并执行原子切换。</p>{runtimeError && <p className="form-error" role="alert">{runtimeError}</p>}<div className="info-card"><Database size={19}/><div><strong>当前数据目录</strong><code>{runtime?.dataRoot??'正在读取…'}</code></div>{runtime?.dataRoot && <button type="button" className="text-button" onClick={()=>void openPath(runtime.dataRoot)}>打开目录</button>}</div><button type="button" className="button primary" onClick={()=>backup.mutate()} disabled={backup.isPending}><Archive size={16}/>{backup.isPending?'正在生成…':'创建完整备份'}</button>{backup.error&&<p className="form-error" role="alert">{backup.error.message}</p>}{result&&<div className="backup-result"><strong>备份已创建</strong><code>{result.path}</code><code>SHA-256 {result.sha256}</code><div className="settings-actions"><button type="button" className="button secondary" onClick={()=>void openPath(result.path)}>打开备份文件</button><button type="button" className="button secondary" onClick={()=>void copyBackupProof()}>复制校验值</button><button type="button" className="button secondary" onClick={()=>verify.mutate(result.path)} disabled={verify.isPending}>校验备份</button></div></div>}<BackupRestorePanel/><div className="data-maintenance"><h4>索引维护</h4><div className="inline-form"><label><span>知识库</span><select value={libraryId} onChange={(event) => setLibraryId(event.target.value)}><option value="">选择知识库</option>{libraries.data?.map((library) => <option key={library.id} value={library.id}>{library.name}</option>)}</select></label><button type="button" className="button secondary" disabled={!libraryId || rebuild.isPending} onClick={() => rebuild.mutate(libraryId)}><RefreshCw size={15}/>重建索引</button></div><h4>备份历史</h4><div className="backup-history">{backups.isLoading && <span className="muted">正在读取备份历史…</span>}{!backups.isLoading && !backups.data?.length && <span className="muted">还没有本地备份。</span>}{backups.data?.map((item) => <div key={item.path}><div><strong>{item.path.replace(/^backups[\\/]/, '')}</strong><span>{(item.size / 1024 / 1024).toFixed(1)} MB · {new Date(item.createdAt).toLocaleString()}</span></div><div className="settings-actions"><button type="button" className="button ghost" onClick={()=>verify.mutate(item.path)} disabled={verify.isPending}>校验</button><button type="button" className="button ghost" onClick={()=>void openPath(item.path)}>打开</button></div></div>)}</div><h4>最近审计</h4><div className="audit-list">{audit.data?.slice(0,20).map((entry) => <div key={entry.id}><strong>{entry.action}</strong><span>{entry.actor} · {entry.target}</span><time>{new Date(entry.createdAt).toLocaleString()}</time></div>)}</div></div>{copyMessage&&<p className="settings-success" role="status">{copyMessage}</p>}{(openError||verify.error||rebuild.error)&&<p className="form-error" role="alert">{openError || String(((verify.error||rebuild.error) as Error).message)}</p>}</section>
}

function BackupRestorePanel() {
  const [candidate, setCandidate] = useState<Awaited<ReturnType<typeof client.verifyBackup>>>()
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest>()
  const preflight = useMutation({ mutationFn: async () => { const path = await window.kah.selectBackupCandidate(); if (!path) return undefined; return client.verifyBackup(path) }, onSuccess: setCandidate })
  const restore = useMutation({ mutationFn: async () => { if (!candidate) return; await window.kah.restoreBackup(candidate.path) } })
  const files = candidate && typeof candidate.manifest.files === 'object' && candidate.manifest.files ? Object.keys(candidate.manifest.files as Record<string, unknown>).length : 0
  return <div className="backup-restore-panel"><div><strong>恢复备份</strong><span>选择文件 → 校验与预检 → 确认 → 停止后台 → 临时目录恢复 → 原子切换 → 重启</span></div><button type="button" className="button secondary" onClick={() => preflight.mutate()} disabled={preflight.isPending || restore.isPending}>{preflight.isPending ? '正在校验…' : '选择备份文件'}</button>{candidate && <div className="backup-preflight"><CheckCircle2 size={17}/><div><strong>预检通过</strong><code>{candidate.sha256}</code><span>{files} 个文件将被恢复；当前数据会保留到新后台启动成功。</span></div><button type="button" className="button danger" disabled={restore.isPending} onClick={() => setConfirmRequest({ title: '用此备份替换当前数据？', description: '应用将短暂停止后台。若解压、切换或重启失败，会自动恢复原数据目录。', confirmLabel: '恢复并重启', danger: true, onConfirm: async () => { await restore.mutateAsync() } })}>恢复并重启</button></div>}{(preflight.error || restore.error) && <p className="form-error" role="alert">{String(((preflight.error || restore.error) as Error).message)}</p>}<ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(undefined)}/></div>
}

export function PrivacySettings({ libraries }: { libraries: LibraryType[] }) {
  const queryClient = useQueryClient()
  const providers = useQuery({ queryKey: ['providers'], queryFn: client.providers })
  const update = useMutation({ mutationFn: ({ id, patch }: { id: string; patch: Partial<LibraryType> }) => client.updateLibrary(id, patch), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['libraries'] }) })
  const providerOptions = providers.data ?? []
  return <section><h3>隐私与安全</h3><p className="settings-lead">默认不把知识片段发送到远程模型。密钥保存在 Windows Credential Manager；每个知识库单独决定是否允许自动处理。</p><div className="security-card"><ShieldCheck size={22}/><div><strong>零遥测模式已启用</strong><p>日志仅保存在本机，可由用户主动导出。</p></div></div><h4>逐库自动化与远程授权</h4>{libraries.map((library)=><div className="library-automation-card" key={library.id}><div className="library-automation-heading"><strong>{library.name}</strong><span>{library.allowRemoteModels ? '允许远程模型' : '仅限本地模型'}</span></div><label className="switch-row"><span><strong>允许远程模型</strong><small>关闭后总结和审核只能选择本地模型。</small></span><input type="checkbox" checked={library.allowRemoteModels} disabled={update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{allowRemoteModels:e.target.checked}})}/></label><label className="switch-row"><span><strong>导入后自动总结</strong><small>{library.autoSummarizeImports ? '导入完成后生成 KAH 待审核草稿' : '导入后只生成可追溯资料草稿'}</small></span><input type="checkbox" checked={library.autoSummarizeImports} disabled={!providerOptions.length || update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{autoSummarizeImports:e.target.checked}})}/></label><label className="switch-row"><span><strong>导入转换策略</strong><small>reference 保留原文，enhanced 使用所选模型增强，off 只保存原始文档。</small></span><select aria-label={library.name + '导入转换策略'} value={library.importConversionMode ?? (library.autoSummarizeImports ? 'enhanced' : 'reference')} disabled={update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{importConversionMode:e.target.value as LibraryType['importConversionMode']}})}><option value="reference">参考草稿（无模型也可用）</option><option value="enhanced">模型增强（仍需审核）</option><option value="off">关闭 KAH 转换</option></select></label>{<label className="switch-row"><span><strong>总结模型</strong><small>模型只接收导入资料提取文本，输出仍需审核。</small></span><select aria-label={library.name + '总结模型'} value={library.summaryProviderId ?? ''} disabled={update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{summaryProviderId:e.target.value}})}><option value="">选择总结模型</option>{providerOptions.map((provider)=><option value={provider.id} key={provider.id}>{provider.name} · {provider.local ? '本地' : '远程'}</option>)}</select></label>}<label className="switch-row"><span><strong>自动审核 Agent / KAH 草稿</strong><small>{library.autoReviewAgentSubmissions ? '仅信度超过 95% 且无阻塞问题时自动发布稳定 revision' : '关闭，所有草稿进入人工审核队列'}</small></span><input type="checkbox" checked={library.autoReviewAgentSubmissions} disabled={!providerOptions.length || update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{autoReviewAgentSubmissions:e.target.checked}})}/></label>{library.autoReviewAgentSubmissions && <label className="switch-row"><span><strong>审核模型</strong><small>远程模型只有在上方允许后才能使用。</small></span><select aria-label={library.name + '审核模型'} value={library.reviewProviderId ?? ''} disabled={update.isPending} onChange={(e)=>update.mutate({id:library.id,patch:{reviewProviderId:e.target.value}})}><option value="">选择审核模型</option>{providerOptions.map((provider)=><option value={provider.id} key={provider.id}>{provider.name} · {provider.local ? '本地' : '远程'}</option>)}</select></label>}</div>)}{update.isPending && <p className="settings-lead" role="status">正在保存自动化设置…</p>}{!libraries.length && <p className="muted">先创建知识库，再配置导入自动化。</p>}{update.error&&<p className="form-error" role="alert">{(update.error as Error).message}</p>}</section>
}
