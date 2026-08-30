import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as Dialog from '@radix-ui/react-dialog'
import * as Tabs from '@radix-ui/react-tabs'
import CodeMirror from '@uiw/react-codemirror'
import { markdown } from '@codemirror/lang-markdown'
import ReactMarkdown from 'react-markdown'
import {
  Archive, BookOpen, Bot, CheckCircle2, ChevronRight, CircleAlert, Clock3, Database,
  File, FileCode2, FilePlus2, FolderArchive, FolderSearch2, Import, KeyRound, Library, Link2, ListFilter,
  LoaderCircle, Menu, Moon, PanelRight, Plus, RefreshCw, Save, Search, Settings, ShieldCheck,
  Sparkles, Star, Sun, Tag, Trash2, X
} from 'lucide-react'
import { client, getRuntime } from './api'
import { SkillMappingDetailPane, SkillMappingsWorkspace } from './SkillMappings'
import { canonicalDocumentPath, isExternalMarkdownLink, resolveMarkdownDocumentPath } from './markdownLinks'
import { useUI } from './store'
import type { AgentToken, Document, Job, KAHSubmission, KnowledgeDirectoryEntry, KnowledgeRevision, Library as LibraryType, Provider, SavedSearch, Skill } from './types'

function IconButton({ label, children, onClick, active = false }: { label: string; children: React.ReactNode; onClick?: () => void; active?: boolean }) {
  return <button className={`icon-button ${active ? 'active' : ''}`} aria-label={label} title={label} onClick={onClick}>{children}</button>
}
export function MarkdownContent({ content, onLink }: { content: string; onLink?: (href: string) => void }) {
  return <ReactMarkdown components={{
    a: ({ href, children, ...props }) => <a {...props} href={href} onClick={(event) => {
      if (!href || !onLink) return
      event.preventDefault()
      onLink(href)
    }}>{children}</a>
  }}>{content}</ReactMarkdown>
}


function Empty({ icon, title, text }: { icon: React.ReactNode; title: string; text: string }) {
  return <div className="empty"><div className="empty-icon">{icon}</div><strong>{title}</strong><p>{text}</p></div>
}

export function App() {
  const queryClient = useQueryClient()
  const ui = useUI()
  const [selectedLibrary, setSelectedLibrary] = useState<string>('')
  const [selectedFolder, setSelectedFolder] = useState('')
  const [favoritesOnly, setFavoritesOnly] = useState(false)
  const [watchOpen, setWatchOpen] = useState(false)
  const [selectedDocument, setSelectedDocument] = useState<string>('')
  const [query, setQuery] = useState('')
  const [searchResults, setSearchResults] = useState<KnowledgeDirectoryEntry[] | null>(null)
  const [searchInfo, setSearchInfo] = useState('')
  const [selectedKnowledge, setSelectedKnowledge] = useState('')
  const [markdownLinkError, setMarkdownLinkError] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [urlValue, setUrlValue] = useState('')
  const [urlOpen, setUrlOpen] = useState(false)
  const [backendError, setBackendError] = useState('')
  const [activeSection, setActiveSection] = useState<'knowledge'|'skills'|'review'>('knowledge')
  const [selectedSkill, setSelectedSkill] = useState('')
  const [skillsView, setSkillsView] = useState<'skills'|'mappings'>('skills')
  const [selectedMappingTarget, setSelectedMappingTarget] = useState('')
  const searchInput = useRef<HTMLInputElement>(null)

  const allDocuments = useQuery({ queryKey: ['documents-all', selectedLibrary], queryFn: () => client.documents(selectedLibrary), enabled: Boolean(selectedLibrary) })
  const libraries = useQuery({ queryKey: ['libraries'], queryFn: client.libraries })
  const folders = useQuery({ queryKey: ['folders', selectedLibrary], queryFn: () => client.folders(selectedLibrary), enabled: Boolean(selectedLibrary) })
  const documents = useQuery({ queryKey: ['documents', selectedLibrary, selectedFolder, favoritesOnly], queryFn: () => client.documents(selectedLibrary || undefined, selectedFolder || undefined, favoritesOnly), refetchInterval: 5000 })
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: client.jobs, refetchInterval: 2500 })
  const savedSearches = useQuery({ queryKey: ['saved-searches'], queryFn: client.savedSearches })
  const detail = useQuery({ queryKey: ['document', selectedDocument], queryFn: () => client.document(selectedDocument), enabled: Boolean(selectedDocument) })
  const knowledgeDetail = useQuery({ queryKey: ['knowledge', selectedKnowledge], queryFn: () => client.knowledge(selectedKnowledge), enabled: Boolean(selectedKnowledge) })

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
    }
    addEventListener('keydown', listener); return () => removeEventListener('keydown', listener)
  }, [])

  const createLibrary = useMutation({ mutationFn: () => client.createLibrary(`新知识库 ${new Date().toLocaleDateString()}`), onSuccess: (library) => { queryClient.invalidateQueries({ queryKey: ['libraries'] }); setSelectedLibrary(library.id) } })
  const favoriteMutation = useMutation({ mutationFn: ({ id, value }: { id: string; value: boolean }) => client.updateDocument(id, { favorite: value }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['documents'] }) })
  const importFiles = useMutation({ mutationFn: async () => { if (!selectedLibrary) throw new Error('请先选择知识库'); const paths = await window.kah.selectFiles(); if (!paths.length) return []; return client.importFiles(selectedLibrary, paths) }, onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['jobs'] }); queryClient.invalidateQueries({ queryKey: ['documents'] }); queryClient.invalidateQueries({ queryKey: ['submissions'] }) } })
  const importUrl = useMutation({ mutationFn: async () => { if (!selectedLibrary || !urlValue.trim()) throw new Error('请选择知识库并输入 URL'); return client.importUrl(selectedLibrary, urlValue.trim()) }, onSuccess: () => { setUrlOpen(false); setUrlValue(''); queryClient.invalidateQueries({ queryKey: ['jobs'] }); queryClient.invalidateQueries({ queryKey: ['documents'] }); queryClient.invalidateQueries({ queryKey: ['submissions'] }) } })
  const search = useMutation({
    mutationFn: ({ text, libraryId }: { text: string; libraryId: string }) => client.knowledgeSearch(text, libraryId ? [libraryId] : []),
    onSuccess: (response) => { setSearchResults(response.results); setSearchInfo(`${response.results.length} 个知识体`) }
  })
  const saveSearch = useMutation({ mutationFn: () => client.createSavedSearch(query.slice(0, 24), query, selectedLibrary ? [selectedLibrary] : []), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['saved-searches'] }) })

  const shownDocuments = useMemo(() => documents.data ?? [], [documents.data])
  const selectedLibraryValue = libraries.data?.find((item) => item.id === selectedLibrary)
  const activeJobs = jobs.data?.filter((job) => job.status === 'running' || job.status === 'queued') ?? []
  const pipelineJobs = jobs.data?.filter((job) => ['file_import', 'url_import', 'knowledge_summarize', 'kah_knowledge_review', 'kah_knowledge_publish'].includes(job.kind)) ?? []

  function runSearch(value = query) {
    const normalized = value.trim()
    setQuery(value)
    setSelectedKnowledge('')
    if (!normalized) { setSearchResults(null); setSearchInfo(''); return }
    setTimeout(() => search.mutate({ text: normalized, libraryId: selectedLibrary }), 0)
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



  return <div className="app-shell">
    <div className="window-titlebar" aria-hidden="true"><div className="window-titlebar-brand"><span className="window-titlebar-mark"><Sparkles size={14}/></span><span>Knowledge Agent Hub</span></div></div>
    <aside className="sidebar" aria-label="知识库导航">
      <div className="brand"><div className="brand-mark"><Sparkles size={19} /></div><div><strong>Knowledge</strong><span>Agent Hub</span></div></div>
      <nav className="sidebar-scroll" aria-label="知识库导航">
        <div className="nav-heading"><span>工作区</span></div>
        <button className={`library-row ${activeSection === 'knowledge' ? 'active' : ''}`} onClick={() => setActiveSection('knowledge')}>
          <span className="library-icon"><Library size={17}/></span><span className="library-copy"><strong>知识管理</strong><small>文档、网页与检索</small></span><ChevronRight size={15}/>
        </button>
        <button className={`library-row ${activeSection === 'skills' ? 'active' : ''}`} onClick={() => { setActiveSection('skills'); setSelectedDocument(''); setSelectedKnowledge('') }}>
          <span className="library-icon"><FolderArchive size={17}/></span><span className="library-copy"><strong>Skills</strong><small>Agent 能力包</small></span><ChevronRight size={15}/>
        </button>
        <button className="library-row" aria-current={activeSection === 'review' ? 'page' : undefined} onClick={() => { setActiveSection('review'); setSelectedDocument(''); setSelectedKnowledge('') }}><span className="library-icon"><Clock3 size={17}/></span><span className="library-copy"><strong>待审核</strong><small>Agent 提交的知识</small></span><ChevronRight size={15}/></button>
        {activeSection === 'knowledge' && <>
        <div className="nav-heading"><span>知识库</span><IconButton label="新建知识库" onClick={() => createLibrary.mutate()}><Plus size={17} /></IconButton></div>
        {libraries.isLoading && <div className="sidebar-loading"><LoaderCircle className="spin" size={16}/> 正在载入</div>}
        {libraries.data?.map((library) => <button key={library.id} className={`library-row ${selectedLibrary === library.id ? 'active' : ''}`} onClick={() => { setSelectedLibrary(library.id); setSelectedDocument(''); setSelectedKnowledge(''); setSearchResults(null) }}>
          <span className="library-icon"><Library size={17}/></span><span className="library-copy"><strong>{library.name}</strong><small>{library.description || '本地知识库'}</small></span><ChevronRight size={15}/>
        </button>)}
        <div className="nav-heading saved-heading"><span>整理</span></div>
        <button className={`saved-row ${favoritesOnly && !selectedFolder ? 'active' : ''}`} onClick={() => { setFavoritesOnly(true); setSelectedFolder(''); setSearchResults(null) }}><Star size={16}/><span>我的收藏</span></button>
        {folders.data?.map((folder) => <button className={`saved-row ${selectedFolder === folder.id ? 'active' : ''}`} key={folder.id} onClick={() => { setSelectedFolder(folder.id); setFavoritesOnly(false); setSearchResults(null) }}><FolderSearch2 size={16}/><span>{folder.name}</span></button>)}
        {selectedLibrary && <button className="text-button sidebar-add" onClick={() => { const name = window.prompt('目录名称'); if (name?.trim()) client.createFolder(selectedLibrary, name.trim()).then(() => folders.refetch()) }}><Plus size={14}/> 新建目录</button>}
        <div className="nav-heading saved-heading"><span>固定搜索</span></div>
        {!savedSearches.data?.length && <p className="sidebar-hint">搜索后可保存为快捷入口</p>}
        {savedSearches.data?.map((saved: SavedSearch) => <button className="saved-row" key={saved.id} onClick={() => { if (saved.libraryIds[0]) setSelectedLibrary(saved.libraryIds[0]); runSearch(saved.query) }}><FolderSearch2 size={16}/><span>{saved.name}</span></button>)}
        </>}
      </nav>
      <div className="sidebar-footer">
        <button className="footer-button" onClick={() => setSettingsOpen(true)}><Settings size={18}/><span>设置</span><kbd>Ctrl ,</kbd></button>
        <div className="health-line"><span className={backendError ? 'status-dot error' : 'status-dot'} />{backendError ? '服务异常' : '本地服务已连接'}</div>
      </div>
    </aside>

    <main className="workspace">
      {activeSection === 'skills' ? <SkillsWorkspace libraries={libraries.data ?? []} selectedId={selectedSkill} onSelect={setSelectedSkill} view={skillsView} onViewChange={setSkillsView} selectedTargetId={selectedMappingTarget} onTargetSelect={setSelectedMappingTarget} /> : activeSection === 'review' ? <ReviewWorkspace libraries={libraries.data ?? []} onOpenDocument={(id) => { setActiveSection('knowledge'); setSelectedKnowledge(''); setSelectedDocument(id) }} /> : <>
      {backendError && <div role="alert" className="alert-banner"><CircleAlert size={17}/>{backendError}</div>}
      <header className="workspace-header">
        <div><span className="eyebrow">当前知识库</span><h1>{selectedLibraryValue?.name ?? '全部知识'}</h1></div>
        <div className="header-actions">
          <IconButton label="切换密度" onClick={ui.toggleDensity}><Menu size={18}/></IconButton>
          <IconButton label="切换主题" onClick={() => ui.setTheme(ui.theme === 'dark' ? 'light' : 'dark')}>{ui.theme === 'dark' ? <Sun size={18}/> : <Moon size={18}/>}</IconButton>
          <button className="button secondary" onClick={() => { setUrlOpen((value) => !value) }}><Link2 size={17}/> 网页</button>
          <button className="button secondary" onClick={() => { const path = window.prompt('监视目录绝对路径'); if (selectedLibrary && path?.trim()) client.createWatch(selectedLibrary, path.trim()).then(() => queryClient.invalidateQueries({ queryKey: ['jobs'] })) }} disabled={!selectedLibrary}><FolderSearch2 size={17}/> 监视目录</button>
          <button className="button primary" onClick={() => importFiles.mutate()} disabled={!selectedLibrary || importFiles.isPending}><Import size={17}/> 导入</button>
        </div>
      </header>
      <div className="search-row">
        <Search size={19}/><input ref={searchInput} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') runSearch() }} placeholder="搜索知识、问题或精确短语…" aria-label="搜索知识" />
        {query && <IconButton label="清空搜索" onClick={() => { setQuery(''); setSearchResults(null); setSelectedKnowledge('') }}><X size={16}/></IconButton>}
        <kbd>Ctrl K</kbd><button className="search-submit" onClick={() => runSearch()} disabled={!query.trim() || search.isPending}>{search.isPending ? <LoaderCircle className="spin" size={17}/> : '搜索'}</button>
      </div>
      {urlOpen && <form className="url-import" onSubmit={(event) => { event.preventDefault(); importUrl.mutate() }}><Link2 size={18}/><label htmlFor="import-url">网页地址</label><input id="import-url" type="url" value={urlValue} onChange={(event) => setUrlValue(event.target.value)} placeholder="https://example.com/article" autoFocus/><button className="button primary" type="submit">保存网页</button><IconButton label="关闭" onClick={() => setUrlOpen(false)}><X size={17}/></IconButton></form>}
      <div className="content-heading"><div><h2>{searchResults ? '知识搜索结果' : '来源文档'}</h2><span>{searchResults ? `${searchResults.length} 个知识体` : `${shownDocuments.length} 个项目`}</span></div><div className="content-tools">{searchResults && <button className="text-button" onClick={() => saveSearch.mutate()} disabled={saveSearch.isPending}><Save size={15}/> 固定搜索</button>}<IconButton label="刷新" onClick={() => { documents.refetch(); jobs.refetch() }}><RefreshCw size={17}/></IconButton><IconButton label="筛选"><ListFilter size={17}/></IconButton></div></div>
      <PipelineStatus jobs={pipelineJobs}/>
       {markdownLinkError && <div role="alert" className="inline-error"><CircleAlert size={18}/>{markdownLinkError}</div>}
      <section className="result-list" aria-live="polite">
        {(documents.error || search.error || importFiles.error || importUrl.error) && <div role="alert" className="inline-error"><CircleAlert size={18}/>{String((documents.error || search.error || importFiles.error || importUrl.error)?.message)}</div>}
        {searchResults ? searchResults.map((item, index) => <button className="result-card" key={item.uri} onClick={() => { setSelectedKnowledge(`${item.uri}?revision=${item.revision}`); setSelectedDocument('') }}><span className="rank">{String(index + 1).padStart(2, '0')}</span><div className="result-body"><div className="result-title"><strong>{item.title}</strong><span>{item.type}{item.subtype ? ` · ${item.subtype}` : ''}</span></div><p>{item.description}</p><div className="result-meta"><span>{item.language} · r{item.revision}</span><span>{item.trust === 'verified' ? '已验证' : '未验证'}{item.flags.length ? ` · ${item.flags.join('、')}` : ''}</span></div></div></button>) : shownDocuments.map((document) => <DocumentRow key={document.id} document={document} active={selectedDocument === document.id} onSelect={() => { setSelectedDocument(document.id); setSelectedKnowledge('') }} />)}
        {!documents.isLoading && !searchResults && shownDocuments.length === 0 && <Empty icon={<FilePlus2 size={25}/>} title="还没有知识文档" text="导入文件或保存网页，索引完成后即可检索。" />}
        {searchResults?.length === 0 && <Empty icon={<Search size={25}/>} title="没有找到匹配内容" text="尝试更换关键词，或检查所选知识库的索引状态。" />}
      </section>
      <footer className="status-bar" role="status" aria-live="polite"><div>{activeJobs.length ? <><LoaderCircle className="spin" size={13}/> {activeJobs.length} 个任务正在处理</> : <><CheckCircle2 size={13}/> 索引队列空闲</>}</div><div>{searchInfo || '证据优先 · 本地优先'}<span className="separator"/>v0.1.0</div></footer>
      </>}
    </main>

    <aside className="detail-pane" aria-label={activeSection === 'skills' ? (skillsView === 'mappings' ? '外部映射详情' : 'Skill 详情') : activeSection === 'review' ? '审核提示' : '文档预览'}>
      {activeSection === 'skills' ? skillsView === 'mappings' ? <SkillMappingDetailPane targetId={selectedMappingTarget} onDeleted={() => setSelectedMappingTarget('')} /> : <SkillDetailPane skillId={selectedSkill} libraries={libraries.data ?? []} onDeleted={() => setSelectedSkill('')} /> : selectedKnowledge ? <KnowledgePreview detail={knowledgeDetail.data} loading={knowledgeDetail.isLoading} onOpenDocument={(id) => { setSelectedKnowledge(''); setSelectedDocument(id) }} /> : <DocumentPreview detail={detail.data} loading={detail.isLoading} onSaved={() => { detail.refetch(); documents.refetch() }} onLink={(href, source) => { void openMarkdownLink(href, source) }} />}
    </aside>
    <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} libraries={libraries.data ?? []}/>
  </div>
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

function ReviewWorkspace({ libraries, onOpenDocument }: { libraries: LibraryType[]; onOpenDocument: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState('')
  const [reason, setReason] = useState('')
  const submissions = useQuery({ queryKey: ['submissions'], queryFn: () => client.submissions(), refetchInterval: 3000 })
  const detail = useQuery({ queryKey: ['submission', selectedId], queryFn: () => client.submission(selectedId), enabled: Boolean(selectedId) })
  const approve = useMutation({ mutationFn: () => client.approveSubmission(selectedId, reason.trim()), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['submissions'] }); queryClient.invalidateQueries({ queryKey: ['jobs'] }); setReason('') } })
  const reject = useMutation({ mutationFn: () => client.rejectSubmission(selectedId, reason.trim()), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['submissions'] }); setReason('') } })
  const actionable = submissions.data?.filter((item) => item.reviewStatus === 'pending_review' || item.reviewStatus === 'reviewing') ?? []
  const libraryName = (id: string) => libraries.find((item) => item.id === id)?.name ?? id
  return <>
    <header className="workspace-header"><div><span className="eyebrow">质量门禁</span><h1>待审核知识</h1></div><div className="header-actions"><button className="button secondary" onClick={() => submissions.refetch()} disabled={submissions.isFetching}><RefreshCw size={17}/>刷新队列</button></div></header>
    <div className="content-heading"><div><h2>Agent 提交</h2><span>{actionable.length} 条待处理</span></div><span className="muted">审核通过后才会进入正式检索</span></div>
    {(submissions.error || detail.error || approve.error || reject.error) && <div className="inline-error" role="alert"><CircleAlert size={18}/>{String(((submissions.error || detail.error || approve.error || reject.error) as Error).message)}</div>}
    <section className="review-layout">
      <div className="result-list" aria-label="待审核提交列表">
        {submissions.isLoading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入审核队列</div>}
        {actionable.map((item) => <button className={'document-row ' + (selectedId === item.id ? 'active' : '')} key={item.id} onClick={() => { setSelectedId(item.id); setReason('') }}><span className="file-icon"><FileCode2 size={19}/></span><span className="document-copy"><strong>{item.title}</strong><small>{libraryName(item.libraryId)} · {item.summary || '无摘要'}</small></span><span className={'status-pill ' + item.reviewStatus}>{item.reviewStatus === 'reviewing' ? '自动审核中' : '待审核'}</span><time>{new Date(item.updatedAt).toLocaleDateString()}</time></button>)}
        {!submissions.isLoading && !actionable.length && <Empty icon={<CheckCircle2 size={25}/>} title="审核队列为空" text="新的 Agent 提交会先停留在这里，审核通过后才可检索。" />}
      </div>
      <article className="review-card" aria-live="polite">
        {!detail.data && <Empty icon={<PanelRight size={25}/>} title="选择一条提交" text="查看完整 Markdown、来源声明和审核记录。" />}
        {detail.data && <><div className="review-card-heading"><div><span className="eyebrow">{libraryName(detail.data.libraryId)}</span><h2>{detail.data.title}</h2></div><span className={'status-pill ' + detail.data.reviewStatus}>{detail.data.reviewStatus}</span></div><p className="settings-lead">{detail.data.summary}</p><SubmissionSources submission={detail.data} onOpenDocument={onOpenDocument}/><div className="review-provenance"><strong>来源与生成元数据</strong><pre>{JSON.stringify(detail.data.provenance ?? {}, null, 2)}</pre></div>{detail.data.markdown && <div className="review-markdown"><MarkdownContent content={detail.data.markdown}/></div>}<label className="review-reason">审核说明<textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="驳回时必须填写理由；批准说明会进入审计记录。" rows={3}/></label><div className="preview-actions"><button className="button primary" onClick={() => approve.mutate()} disabled={approve.isPending || detail.data.reviewStatus !== 'pending_review'}><CheckCircle2 size={16}/>批准并发布</button><button className="button danger" onClick={() => reject.mutate()} disabled={reject.isPending || !reason.trim() || detail.data.reviewStatus !== 'pending_review'}><CircleAlert size={16}/>驳回</button></div>{detail.data.reviews?.length ? <div className="review-history"><h3>审核记录</h3>{detail.data.reviews.map((review) => <div className="review-history-row" key={review.id}><strong>{review.reviewerType === 'model' ? '审查模型' : '人工审核'} · {review.decision}</strong><span>{review.reason}</span></div>)}</div> : null}</>}
      </article>
    </section>
    <footer className="status-bar" role="status" aria-live="polite"><div><Clock3 size={13}/> 审核通过前不会写入正式索引</div><div>{submissions.isFetching ? '正在刷新审核状态' : '审核记录可追溯'}<span className="separator"/>v0.1.0</div></footer>
  </>
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
        <div className="segmented-control" role="tablist" aria-label="Skills 工作台视图"><button role="tab" aria-selected={view === 'skills'} className={view === 'skills' ? 'active' : ''} onClick={() => onViewChange('skills')}>已安装 Skills</button><button role="tab" aria-selected={view === 'mappings'} className={view === 'mappings' ? 'active' : ''} onClick={() => onViewChange('mappings')}>外部映射</button></div>
        {view === 'skills' && <><button className="button secondary" onClick={() => importMutation.mutate({ kind: 'skill-markdown' })} disabled={importMutation.isPending}><FileCode2 size={17}/> 导入 SKILL.md</button><button className="button primary" onClick={() => importMutation.mutate({ kind: 'skill-zip' })} disabled={importMutation.isPending}><Import size={17}/> 导入 Skill zip</button></>}
      </div>
    </header>
    {view === 'skills' ? <><div className="search-row"><Search size={19}/><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="筛选 Skill 名称或能力描述…" aria-label="筛选 Skill" />{filter && <IconButton label="清空筛选" onClick={() => setFilter('')}><X size={16}/></IconButton>}</div>{importError && <div role="alert" className="inline-error"><CircleAlert size={18}/>{importError.message}</div>}<div className="content-heading"><div><h2>已安装 Skills</h2><span>{visible.length} 个能力包</span></div><IconButton label="刷新 Skills" onClick={() => skills.refetch()}><RefreshCw size={17}/></IconButton></div><section className="result-list" aria-live="polite">{skills.isLoading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入 Skills</div>}{visible.map((item) => <button className={`document-row ${selectedId === item.id ? 'active' : ''}`} key={item.id} onClick={() => onSelect(item.id)}><span className="file-icon"><FolderArchive size={19}/></span><span className="document-copy"><strong>{item.name}</strong><small>{item.description}</small></span><span className={`status-pill ${item.status}`}>{item.status === 'ready' ? `${item.fileCount} 个文件` : '校验失败'}</span><time>{new Date(item.updatedAt).toLocaleDateString()}</time></button>)}{!skills.isLoading && visible.length === 0 && <Empty icon={<FolderArchive size={25}/>} title="还没有 Skill" text="导入 SKILL.md 或 Skill zip，建立可复用的 Agent 能力。" />}</section></> : <SkillMappingsWorkspace skills={skills.data ?? []} selectedTargetId={selectedTargetId} onTargetSelect={onTargetSelect} />}
    <footer className="status-bar" role="status" aria-live="polite"><div>{view === 'skills' ? importMutation.isPending ? <><LoaderCircle className="spin" size={13}/> 正在导入 Skill</> : <><CheckCircle2 size={13}/> Skill 目录已就绪</> : <><Link2 size={13}/> 映射状态由外部目录验证</>}</div><div>{view === 'skills' ? '标准入口 · 按需加载' : '真实目录软链接 · 不复制内容'}<span className="separator"/>v0.1.0</div></footer>
  </>
}

function SubmissionSources({ submission, onOpenDocument }: { submission: KAHSubmission; onOpenDocument: (id: string) => void }) {
  const provenance = submission.provenance ?? {}
  const sources = Array.isArray(provenance.sources) ? provenance.sources as Array<Record<string, unknown>> : []
  if (!sources.length) return <div className="source-connection muted"><Link2 size={15}/>未声明结构化来源</div>
  return <div className="source-connection"><strong><Link2 size={15}/>来源连接</strong>{sources.map((source, index) => { const locator = source.locator && typeof source.locator === 'object' ? source.locator as Record<string, unknown> : {}; const resource = String(source.resource ?? ''); const documentId = resource.startsWith('kah://document/') ? resource.slice('kah://document/'.length) : typeof locator.documentId === 'string' ? locator.documentId : ''; return <div className="source-connection-row" key={`${resource}-${index}`}><code>{resource || '未命名来源'}</code>{documentId ? <button className="text-button" onClick={() => onOpenDocument(documentId)}>查看来源文档</button> : <span className="muted">需通过外部链接核验</span>}</div> })}</div>
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
  return <SkillDetail skill={skill.data} manifest={manifest.data} libraries={libraries} runtime={runtime} onDelete={() => { if (window.confirm(`确定删除 ${skill.data.name}？`)) deleteMutation.mutate(skill.data.id) }} onSaveLinks={(uses, requires) => linkMutation.mutate({ id: skill.data!.id, uses, requires })} />
}

function SkillDetail({ skill, manifest, libraries, runtime, onDelete, onSaveLinks }: { skill: Skill; manifest?: Awaited<ReturnType<typeof client.skillManifest>>; libraries: LibraryType[]; runtime?: RuntimeConfig; onDelete: () => void; onSaveLinks: (uses: string[], requires: string[]) => void }) {
  const queryClient = useQueryClient()
  const [uses, setUses] = useState<string[]>(skill.usesLibraryIds)
  const [requires, setRequires] = useState<string[]>(skill.requiresLibraryIds)
  const replaceMutation = useMutation({
    mutationFn: async (kind: 'skill-markdown'|'skill-zip') => {
      const paths = await window.kah.selectFiles(kind)
      if (!paths[0] || !window.confirm(`确定用所选文件替换 ${skill.name}？`)) return undefined
      return client.importSkill(paths[0], true)
    },
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['skills'] }); queryClient.invalidateQueries({ queryKey: ['skill', skill.id] }); queryClient.invalidateQueries({ queryKey: ['skill-manifest', skill.id] }) }
  })
  useEffect(() => { setUses(skill.usesLibraryIds); setRequires(skill.requiresLibraryIds) }, [skill.id, skill.usesLibraryIds, skill.requiresLibraryIds])
  const toggle = (setter: React.Dispatch<React.SetStateAction<string[]>>, id: string) => setter((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id])
  const folderPath = runtime ? `${runtime.dataRoot.replace(/[\\/]$/, '')}\\${skill.rootPath.replaceAll('/', '\\')}` : ''
  return <div className="preview-shell skill-detail-shell"><header className="preview-header"><div className="preview-icon"><FolderArchive size={20}/></div><div><span>Agent Skill</span><h2>{skill.name}</h2></div></header><div className="preview-meta"><span className={`status-pill ${skill.status}`}>{skill.status === 'ready' ? '可用' : '无效'}</span><span><Database size={14}/>{skill.fileCount} 个文件</span></div><p className="settings-lead">{skill.description}</p>{skill.systemRole && <div className="info-card"><ShieldCheck size={18}/><div><strong>系统规范 Skill</strong><span>由系统维护，不能替换或删除。</span></div></div>}{skill.error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{skill.error}</div>}{replaceMutation.error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{String((replaceMutation.error as Error).message)}</div>}<div className="preview-actions">{folderPath && <button className="button ghost" onClick={() => window.kah.openPath(folderPath)}><FolderSearch2 size={16}/>打开文件夹</button>}<button className="button secondary" onClick={() => replaceMutation.mutate('skill-markdown')} disabled={Boolean(skill.systemRole) || replaceMutation.isPending}><FileCode2 size={16}/>替换 Markdown</button><button className="button secondary" onClick={() => replaceMutation.mutate('skill-zip')} disabled={Boolean(skill.systemRole) || replaceMutation.isPending}><Import size={16}/>替换 zip</button><button className="button ghost" onClick={onDelete} disabled={Boolean(skill.systemRole)}><Trash2 size={16}/>删除</button></div><div className="skill-frontmatter"><strong>标准入口</strong><code>{skill.rootPath}/SKILL.md</code>{manifest?.entryPoint.content && <ReactMarkdown>{manifest.entryPoint.content}</ReactMarkdown>}</div>{manifest && <div className="skill-files"><h4>附属文件</h4>{manifest.files.map((file) => <div className="token-row" key={file.path}><File size={15}/><span>{file.path}</span><small>{file.size} bytes</small></div>)}</div>}<div className="skill-links"><h4>Skill 可调用的知识库</h4>{libraries.map((library) => <label className="switch-row" key={`uses-${library.id}`}><span>{library.name}</span><input type="checkbox" checked={uses.includes(library.id)} onChange={() => toggle(setUses, library.id)}/></label>)}<h4>知识库需要此 Skill</h4>{libraries.map((library) => <label className="switch-row" key={`requires-${library.id}`}><span>{library.name}</span><input type="checkbox" checked={requires.includes(library.id)} onChange={() => toggle(setRequires, library.id)}/></label>)}<button className="button primary" onClick={() => onSaveLinks(uses, requires)}><Save size={16}/>保存关联</button></div></div>
}

function DocumentRow({ document, active, onSelect }: { document: Document; active: boolean; onSelect: () => void }) {
  const queryClient = useQueryClient()
  const favorite = useMutation({ mutationFn: () => client.updateDocument(document.id, { favorite: !document.favorite }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['documents'] }) })
  const TypeIcon = document.mediaType.includes('markdown') || document.mediaType.startsWith('text/') ? FileCode2 : File
  return <button className={`document-row ${active ? 'active' : ''}`} onClick={onSelect}><span className="file-icon"><TypeIcon size={19}/></span><span className="document-copy"><strong>{document.title}</strong><small>{document.sourceUrl || document.sourcePath || document.mediaType}</small></span><span className={`status-pill ${document.status}`}>{document.status === 'ready' ? '已索引' : document.status === 'pending' ? '处理中' : document.status === 'pending_review' ? '待审核' : document.status === 'approved_pending_index' ? '待发布' : document.status === 'rejected' ? '已驳回' : document.status === 'failed' ? '失败' : '源丢失'}</span><span className="favorite-toggle" role="button" tabIndex={0} aria-label={document.favorite ? '取消收藏' : '收藏'} onClick={(event) => { event.stopPropagation(); favorite.mutate() }} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); event.stopPropagation(); favorite.mutate() } }}><Star size={16} fill={document.favorite ? 'currentColor' : 'none'}/></span><time>{new Date(document.updatedAt).toLocaleDateString()}</time></button>
}

function LocationLabel({ location }: { location: Record<string, unknown> }) {
  const parts = Object.entries(location).filter(([key]) => ['page','slide','sheet','heading','lineStart','rowStart'].includes(key)).map(([key, value]) => `${key}: ${String(value)}`)
  return <span><BookOpen size={13}/>{parts.join(' · ') || '文档片段'}</span>
}

function KnowledgePreview({ detail, loading, onOpenDocument }: { detail?: KnowledgeRevision; loading: boolean; onOpenDocument: (id: string) => void }) {
  if (loading) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入知识体</div>
  if (!detail) return <Empty icon={<BookOpen size={25}/>} title="选择一个知识体" text="知识正文、来源和审查提示会显示在这里。" />
  const payload = detail.payload
  return <div className="preview-pane">
    <div className="preview-heading"><div><span className="eyebrow">KAH Knowledge Profile v1</span><h2>{payload.title}</h2><p>{payload.description}</p></div><span className={`status-pill ${detail.status}`}>{detail.stable ? `稳定 r${detail.revision}` : detail.status}</span></div>
    <div className="preview-meta"><span>{payload.type}{payload.subtype ? ` · ${payload.subtype}` : ''}</span><span>{payload.language}</span><span>{detail.flags.length ? detail.flags.join('、') : '无运行时警告'}</span></div>
    {payload.sections.map((section) => <section className="preview-section" key={section.id}><h3>{section.heading}</h3><MarkdownContent content={section.content}/></section>)}
    {payload.sources?.length ? <section className="preview-section"><h3>可引用来源</h3><ul>{payload.sources.map((source) => { const documentId = source.resource.startsWith('kah://document/') ? source.resource.slice('kah://document/'.length) : ''; return <li key={source.id}><code>{`[^${source.id}]`}</code> {source.resource.startsWith('https://') ? <a href={source.resource} onClick={(event) => { event.preventDefault(); void window.kah.openExternal(source.resource) }}>{source.title || source.resource}</a> : <span>{source.title || source.resource}</span>}{documentId && <button className="text-button source-open-button" onClick={() => onOpenDocument(documentId)}>查看来源文档</button>}{source.locator && <small> · {Object.entries(source.locator).map(([key, value]) => `${key}: ${String(value)}`).join(', ')}</small>}</li> })}</ul></section> : null}
  </div>
}

function DocumentPreview({ detail, loading, onSaved, onLink }: { detail?: Awaited<ReturnType<typeof client.document>>; loading: boolean; onSaved: () => void; onLink: (href: string, source?: Pick<Document, 'sourcePath' | 'libraryId'>) => void }) {
  const [editing, setEditing] = useState(false)
  const [content, setContent] = useState('')
  const [tagDraft, setTagDraft] = useState('')
  const tagMutation = useMutation({ mutationFn: (tags: string[]) => client.updateDocument(detail!.id, { tags }), onSuccess: onSaved })
  const save = useMutation({ mutationFn: () => client.updateDocument(detail!.id, { content }), onSuccess: () => { setEditing(false); onSaved() } })
  useEffect(() => { setContent(detail?.preview.map((chunk) => chunk.text).join('\n\n') ?? ''); setTagDraft(detail?.tags.join(', ') ?? ''); setEditing(false) }, [detail?.id])
  if (loading) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入预览</div>
  if (!detail) return <Empty icon={<PanelRight size={25}/>} title="选择一个文档" text="证据、来源和结构化预览会显示在这里。" />
  const editable = detail.mediaType.startsWith('text/') || detail.mediaType.includes('markdown')
  return <div className="preview-shell"><header className="preview-header"><div className="preview-icon"><File size={20}/></div><div><span>{detail.mediaType}</span><h2>{detail.title}</h2></div><IconButton label="更多操作"><Menu size={18}/></IconButton></header><div className="preview-meta"><span className={`status-pill ${detail.status}`}>{detail.status === 'ready' ? '索引就绪' : detail.status}</span><span><Database size={14}/>{detail.preview.length} 个片段</span></div><div className="tag-row">{detail.tags.map((tag) => <span className="tag" key={tag}><Tag size={12}/>{tag}</span>)}{!detail.tags.length && <span className="muted">暂无标签</span>}<input className="tag-input" aria-label="编辑标签" value={tagDraft} onChange={(event) => setTagDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') tagMutation.mutate(tagDraft.split(',').map((value) => value.trim()).filter(Boolean)) }} placeholder="添加标签，用逗号分隔"/><button className="tag-save" onClick={() => tagMutation.mutate(tagDraft.split(',').map((value) => value.trim()).filter(Boolean))}><Save size={13}/>保存标签</button></div><div className="preview-actions">{editable && <button className="button secondary" onClick={() => setEditing((value) => !value)}><FileCode2 size={16}/>{editing ? '取消编辑' : '编辑正文'}</button>}{detail.sourcePath && <button className="button ghost" onClick={() => window.kah.openPath(detail.sourcePath!)}><FolderSearch2 size={16}/>外部打开</button>}</div><div className="preview-content">{editing ? <><CodeMirror value={content} height="100%" extensions={[markdown()]} onChange={setContent}/><button className="button primary editor-save" onClick={() => save.mutate()} disabled={save.isPending}><Save size={16}/>保存并重建索引</button></> : detail.mediaType.includes('markdown') ? <MarkdownContent content={content} onLink={(href) => onLink(href, detail)} /> : detail.preview.map((chunk) => <article className="chunk-preview" key={chunk.id}><LocationLabel location={chunk.location}/><p>{chunk.text}</p></article>)}</div></div>
}

function SettingsDialog({ open, onOpenChange, libraries }: { open: boolean; onOpenChange: (open:boolean) => void; libraries: LibraryType[] }) {
  const [runtime, setRuntime] = useState<RuntimeConfig>()
  useEffect(() => { getRuntime().then(setRuntime) }, [])
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="settings-dialog"><Dialog.Title>设置</Dialog.Title><Dialog.Description>管理模型、Agent 访问、安全和便携数据。</Dialog.Description><Dialog.Close asChild><button className="dialog-close" aria-label="关闭设置"><X size={19}/></button></Dialog.Close><Tabs.Root defaultValue="models" orientation="vertical" className="settings-tabs"><Tabs.List aria-label="设置分类"><Tabs.Trigger value="models"><Bot size={17}/>模型服务</Tabs.Trigger><Tabs.Trigger value="agents"><KeyRound size={17}/>Agent 访问</Tabs.Trigger><Tabs.Trigger value="data"><Database size={17}/>数据与备份</Tabs.Trigger><Tabs.Trigger value="privacy"><ShieldCheck size={17}/>隐私与安全</Tabs.Trigger></Tabs.List><div className="settings-panel"><Tabs.Content value="models"><ProviderSettings/></Tabs.Content><Tabs.Content value="agents"><TokenSettings libraries={libraries}/></Tabs.Content><Tabs.Content value="data"><DataSettings runtime={runtime}/></Tabs.Content><Tabs.Content value="privacy"><PrivacySettings libraries={libraries}/></Tabs.Content></div></Tabs.Root></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ProviderSettings() {
  const queryClient = useQueryClient(); const providers = useQuery({ queryKey:['providers'], queryFn:client.providers }); const [draft,setDraft] = useState<Provider>({id:'',name:'LM Studio',kind:'lmstudio',baseUrl:'http://127.0.0.1:1234',model:'',embeddingModel:'',local:true,apiKey:''})
  const save=useMutation({mutationFn:()=>client.saveProvider(draft),onSuccess:()=>{queryClient.invalidateQueries({queryKey:['providers']});setDraft({...draft,id:'',apiKey:''})}})
  return <section><h3>模型服务</h3><p className="settings-lead">生成模型和嵌入模型独立配置；LM Studio 本地端点不会触发远程外发限制。</p><div className="provider-list">{providers.data?.map((provider)=><div className="provider-card" key={provider.id}><div className="provider-logo"><Bot size={18}/></div><div><strong>{provider.name}</strong><span>{provider.kind} · {provider.baseUrl}</span></div><span className="local-badge">{provider.local?'本地':'远程'}</span></div>)}</div><div className="form-grid"><label>名称<input value={draft.name} onChange={(e)=>setDraft({...draft,name:e.target.value})}/></label><label>类型<select value={draft.kind} onChange={(e)=>{const kind=e.target.value as Provider['kind'];setDraft({...draft,kind,local:kind==='lmstudio'})}}><option value="lmstudio">LM Studio</option><option value="openai">OpenAI compatible</option><option value="anthropic">Anthropic</option><option value="custom">自定义 OpenAI 兼容端点</option></select></label><label className="span-2">Base URL<input value={draft.baseUrl} onChange={(e)=>setDraft({...draft,baseUrl:e.target.value})}/></label><label>生成模型<input value={draft.model} onChange={(e)=>setDraft({...draft,model:e.target.value})}/></label><label>嵌入模型<input value={draft.embeddingModel} onChange={(e)=>setDraft({...draft,embeddingModel:e.target.value})}/></label><label className="span-2">API Key<input type="password" value={draft.apiKey} onChange={(e)=>setDraft({...draft,apiKey:e.target.value})} autoComplete="off"/></label></div><button className="button primary" onClick={()=>save.mutate()} disabled={save.isPending}><Save size={16}/>保存服务</button>{save.error&&<p className="form-error">{save.error.message}</p>}</section>
}

function TokenSettings({ libraries }: { libraries: LibraryType[] }) {
  const queryClient = useQueryClient()
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: client.tokens })
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [scopes, setScopes] = useState<AgentToken['scopes']>(['mcp_read'])
  const create = useMutation({ mutationFn: () => client.createToken(name, scopes, libraries.map((item) => item.id)), onSuccess: (token) => { setSecret(token.secret ?? ''); setName(''); setScopes(['mcp_read']); queryClient.invalidateQueries({ queryKey: ['tokens'] }) } })
  const revoke = useMutation({ mutationFn: client.revokeToken, onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tokens'] }) })
  const toggleScope = (scope: AgentToken['scopes'][number]) => setScopes((current) => current.includes(scope) ? current.filter((value) => value !== scope) as AgentToken['scopes'] : [...current, scope])
  const manageEnabled = scopes.includes('mcp_manage')
  return <section><h3>Agent MCP 访问令牌</h3><p className="settings-lead">令牌只显示一次。Read 令牌只读取已发布知识；Manage 令牌只能提交待人工审核的草稿。</p>{secret&&<div className="secret-box"><strong>立即复制此令牌</strong><code>{secret}</code><button onClick={()=>navigator.clipboard.writeText(secret)}>复制</button></div>}<div className="inline-form"><label><span>令牌名称</span><input value={name} onChange={(e)=>setName(e.target.value)} placeholder="例如：本机 Codex"/></label><button className="button primary" onClick={()=>create.mutate()} disabled={!name.trim() || !scopes.length || create.isPending || libraries.length === 0}>创建令牌</button></div><div className="token-scope-grid"><strong>令牌能力</strong>{(['mcp_read', 'mcp_manage'] as const).map((scope)=><label className="switch-row" key={scope}><span><strong>{scope === 'mcp_read' ? 'Read MCP：获取知识' : 'Manage MCP：提交知识草稿'}</strong><small>{scope === 'mcp_manage' ? '不能直接发布或删除知识' : '搜索、目录读取与章节读取'}</small></span><input type="checkbox" checked={scopes.includes(scope)} onChange={()=>toggleScope(scope)}/></label>)}</div>{manageEnabled && <p className="settings-lead">Manage 权限同样绑定当前 {libraries.length} 个知识库，所有写入都进入人工审核。</p>}{(create.error || revoke.error) && <p className="form-error" role="alert">{((create.error || revoke.error) as Error).message}</p>}<div className="token-list">{tokens.data?.map((token:AgentToken)=><div className="token-row" key={token.id}><KeyRound size={17}/><div><strong>{token.name}</strong><span>{token.scopes.join(' · ')} · {new Date(token.createdAt).toLocaleDateString()}</span></div><button className="danger-icon" aria-label={'撤销 ' + token.name} onClick={()=>revoke.mutate(token.id)}><Trash2 size={17}/></button></div>)}</div></section>
}
function DataSettings({runtime}:{runtime?:RuntimeConfig}) { const [result,setResult]=useState('');const backup=useMutation({mutationFn:client.backup,onSuccess:(value)=>setResult(`${value.path}\nSHA-256 ${value.sha256}`)});return <section><h3>数据与备份</h3><p className="settings-lead">安装模式使用用户数据目录；便携模式使用程序旁的相对 <code>data</code> 目录。</p><div className="info-card"><Database size={19}/><div><strong>当前数据目录</strong><code>{runtime?.dataRoot??'正在读取…'}</code></div></div><button className="button primary" onClick={()=>backup.mutate()} disabled={backup.isPending}><Archive size={16}/>{backup.isPending?'正在生成…':'创建完整备份'}</button>{result&&<pre className="backup-result">{result}</pre>}</section> }

export function PrivacySettings({ libraries }: { libraries: LibraryType[] }) {
  const queryClient = useQueryClient()
  const providers = useQuery({ queryKey: ['providers'], queryFn: client.providers })
  const update = useMutation({ mutationFn: ({ id, patch }: { id: string; patch: Partial<LibraryType> }) => client.updateLibrary(id, patch), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['libraries'] }) })
  const providerOptions = providers.data ?? []
  return <section><h3>隐私与安全</h3><p className="settings-lead">默认不把知识片段发送到远程模型。密钥保存在 Windows Credential Manager；每个知识库单独决定是否允许自动处理。</p><div className="security-card"><ShieldCheck size={22}/><div><strong>零遥测模式已启用</strong><p>日志仅保存在本机，可由用户主动导出。</p></div></div><h4>逐库自动化与远程授权</h4>{libraries.map((library)=><div className="library-automation-card" key={library.id}><div className="library-automation-heading"><strong>{library.name}</strong><span>{library.allowRemoteModels ? '允许远程模型' : '仅限本地模型'}</span></div><label className="switch-row"><span><strong>允许远程模型</strong><small>关闭后总结和审核只能选择本地模型。</small></span><input type="checkbox" checked={library.allowRemoteModels} onChange={(e)=>update.mutate({id:library.id,patch:{allowRemoteModels:e.target.checked}})}/></label><label className="switch-row"><span><strong>导入后自动总结</strong><small>{library.autoSummarizeImports ? '导入完成后生成 KAH 待审核草稿' : '导入后只生成可追溯资料草稿'}</small></span><input type="checkbox" checked={library.autoSummarizeImports} disabled={!providerOptions.length} onChange={(e)=>update.mutate({id:library.id,patch:{autoSummarizeImports:e.target.checked}})}/></label>{library.autoSummarizeImports && <label className="switch-row"><span><strong>总结模型</strong><small>模型只接收导入资料提取文本，输出仍需审核。</small></span><select aria-label={library.name + '总结模型'} value={library.summaryProviderId ?? ''} onChange={(e)=>update.mutate({id:library.id,patch:{summaryProviderId:e.target.value}})}><option value="">选择总结模型</option>{providerOptions.map((provider)=><option value={provider.id} key={provider.id}>{provider.name} · {provider.local ? '本地' : '远程'}</option>)}</select></label>}<label className="switch-row"><span><strong>自动审核 Agent / KAH 草稿</strong><small>{library.autoReviewAgentSubmissions ? '审核模型通过后自动发布稳定 revision' : '关闭，所有草稿进入人工审核队列'}</small></span><input type="checkbox" checked={library.autoReviewAgentSubmissions} disabled={!providerOptions.length} onChange={(e)=>update.mutate({id:library.id,patch:{autoReviewAgentSubmissions:e.target.checked}})}/></label>{library.autoReviewAgentSubmissions && <label className="switch-row"><span><strong>审核模型</strong><small>远程模型只有在上方允许后才能使用。</small></span><select aria-label={library.name + '审核模型'} value={library.reviewProviderId ?? ''} onChange={(e)=>update.mutate({id:library.id,patch:{reviewProviderId:e.target.value}})}><option value="">选择审核模型</option>{providerOptions.map((provider)=><option value={provider.id} key={provider.id}>{provider.name} · {provider.local ? '本地' : '远程'}</option>)}</select></label>}</div>)}{!libraries.length && <p className="muted">先创建知识库，再配置导入自动化。</p>}{update.error&&<p className="form-error" role="alert">{(update.error as Error).message}</p>}</section>
}
