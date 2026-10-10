import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, test, vi } from 'vitest'
import { App, MarkdownContent, PrivacySettings } from './App'
import { client } from './api'
import { useUI } from './store'
import type { KAHSubmission } from './types'

vi.mock('@uiw/react-codemirror', () => ({ default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => <textarea aria-label="测试正文编辑器" value={value} onChange={event => onChange(event.target.value)}/> }))

vi.mock('./api', () => ({
  getRuntime: vi.fn(async () => ({apiBaseUrl:'http://127.0.0.1:1',desktopToken:'x',dataRoot:'data',version:'test'})),
  client: {
    libraries: vi.fn(async () => []), documents: vi.fn(async () => []), folders: vi.fn(async () => []), jobs: vi.fn(async () => []), savedSearches: vi.fn(async () => []), knowledgeSearch: vi.fn(async () => ({ results: [] })),
    document: vi.fn(), knowledge: vi.fn(), submissions: vi.fn(async () => []), submission: vi.fn(), approveSubmission: vi.fn(), rejectSubmission: vi.fn(), skills: vi.fn(async () => []), skill: vi.fn(), skillManifest: vi.fn(), importSkill: vi.fn(), updateSkillLinks: vi.fn(), deleteSkill: vi.fn(), skillMappingTargets: vi.fn(async () => []), skillMappingTarget: vi.fn(), createSkillMappingTarget: vi.fn(), updateSkillMappingTarget: vi.fn(), addSkillMappings: vi.fn(), verifySkillMappingTarget: vi.fn(), repairSkillMapping: vi.fn(), removeSkillMapping: vi.fn(), forgetSkillMapping: vi.fn(), deleteSkillMappingTarget: vi.fn(), createLibrary: vi.fn(), importFiles: vi.fn(), importUrl: vi.fn(), query: vi.fn(),
    providers: vi.fn(async () => []), embeddingModels: vi.fn(async () => []), tokens: vi.fn(async () => []), feedback: vi.fn(), updateLibrary: vi.fn(), updateDocument: vi.fn(), createFolder: vi.fn(), createWatch: vi.fn(), watches: vi.fn(async () => []), updateWatch: vi.fn(), scanWatch: vi.fn(), deleteWatch: vi.fn(), backup: vi.fn(), backups: vi.fn(async () => []), verifyBackup: vi.fn(), rebuildIndex: vi.fn(), createSavedSearch: vi.fn(), updateSavedSearch: vi.fn(), deleteSavedSearch: vi.fn(), graph: vi.fn(), createGraphWorkspace: vi.fn(), updateGraphWorkspace: vi.fn(), saveGraphPositions: vi.fn(), autoLayoutGraph: vi.fn(), trash: vi.fn(async () => []), restoreTrash: vi.fn(), purgeTrash: vi.fn(), emptyTrash: vi.fn(), batchDocuments: vi.fn(), reindexDocument: vi.fn(), relinkDocument: vi.fn(), cancelJob: vi.fn(), retryJob: vi.fn(), testProvider: vi.fn(), deleteProvider: vi.fn(), providerModels: vi.fn(), architectureProjects: vi.fn(async () => []), architectureProject: vi.fn(), architectureTasks: vi.fn(async () => []), architectureSnapshots: vi.fn(async () => []), architectureSnapshot: vi.fn(), architectureAnalysis: vi.fn(), architectureDiff: vi.fn(), architectureArtifacts: vi.fn(async () => []), downloadArchitectureArtifact: vi.fn(), scanArchitectureProject: vi.fn(), generateArchitectureProject: vi.fn(), cancelArchitectureTask: vi.fn(), createArchitectureProject: vi.fn(), updateArchitectureProject: vi.fn(), deleteArchitectureProject: vi.fn()
  }
}))

Object.defineProperty(window, 'kah', { value: { runtimeConfig: vi.fn(), setTitleBarTheme: vi.fn(), selectFiles: vi.fn(async () => []), selectDirectory: vi.fn(async () => ''), saveFile: vi.fn(async () => ''), openPath: vi.fn(), openExternal: vi.fn(), onBackendExit: vi.fn(() => () => {}) } })
Object.defineProperty(window, 'matchMedia', { value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })) })

afterEach(() => cleanup())

test('renders the accessible knowledge workbench', async () => {
  window.localStorage.removeItem('kah.sidebar-collapsed')
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(screen.getByRole('navigation', {name:'知识库导航'})).toBeInTheDocument()
  expect(document.querySelector('.brand')).not.toBeInTheDocument()
  const collapseSidebar = screen.getByRole('button', { name: '折叠左侧栏' })
  expect(collapseSidebar).toHaveAttribute('aria-expanded', 'true')
  fireEvent.click(collapseSidebar)
  expect(document.querySelector('.app-shell')).toHaveClass('sidebar-collapsed')
  expect(screen.getByRole('button', { name: '展开左侧栏' })).toHaveAttribute('aria-expanded', 'false')
  await waitFor(() => expect(window.localStorage.getItem('kah.sidebar-collapsed')).toBe('true'))
  expect(screen.getByRole('textbox', {name:'搜索知识'})).toBeInTheDocument()
  expect(screen.queryByRole('button', {name:'切换主题'})).not.toBeInTheDocument()
  expect(screen.queryByRole('button', {name:'切换详情栏'})).not.toBeInTheDocument()
  expect(await screen.findByText('还没有来源文档')).toBeInTheDocument()
  window.localStorage.removeItem('kah.sidebar-collapsed')
})

test('opens the command palette with disabled reasons for unavailable actions', async () => {
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.keyDown(window, { key: 'P', ctrlKey: true, shiftKey: true })
  expect(await screen.findByRole('textbox', { name: '筛选命令' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: '新建知识库' })).toBeInTheDocument()
  const importCommand = screen.getByRole('option', { name: /导入文件/ })
  expect(importCommand).toHaveAttribute('aria-disabled', 'true')
  expect(importCommand).toHaveAttribute('title', '请先选择知识库')
})

test('lists and restores trash items from the recoverable workspace', async () => {
  const timestamp = new Date().toISOString()
  vi.mocked(client.trash).mockResolvedValue([{ kind: 'document', id: 'trashed-document', name: '已删除资料', description: 'text/markdown', itemCount: 0, deletedAt: timestamp }])
  vi.mocked(client.restoreTrash).mockResolvedValue(undefined)
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /回收站/ }))
  expect(await screen.findByText('已删除资料')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '恢复' }))
  await waitFor(() => expect(client.restoreTrash).toHaveBeenCalledWith('document', 'trashed-document'))
})

test('keeps theme and density preferences discoverable in settings', async () => {
  useUI.setState({ theme: 'dark', density: 'comfortable' })
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /设置/ }))
  expect(await screen.findByRole('tab', { name: '外观' })).toHaveAttribute('data-state', 'active')
  expect(screen.getByRole('button', { name: '使用深色主题' })).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(screen.getByRole('button', { name: '使用紧凑信息密度' }))
  expect(screen.getByRole('button', { name: '使用紧凑信息密度' })).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(screen.getByRole('button', { name: '使用浅色主题' }))
  await waitFor(() => expect(window.kah.setTitleBarTheme).toHaveBeenLastCalledWith('light'))
  useUI.setState({ theme: 'dark', density: 'comfortable' })
})

test('switches between source documents and published knowledge, with a collapsible detail pane', async () => {
  const timestamp = new Date().toISOString()
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-knowledge', name: '产品知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.documents).mockResolvedValue([{ id: 'source-1', libraryId: 'library-knowledge', title: '原始资料', mediaType: 'text/markdown', status: 'ready', tags: [], favorite: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.knowledgeSearch).mockResolvedValue({ results: [{ uri: 'kah://knowledge/formed-1', revision: 2, title: '已成型知识', description: '经过审核发布的知识条目。', type: 'concept', language: 'zh-CN', primaryPath: [], tags: [], matchedSectionIds: [], matchReason: '', flags: [], trust: 'verified' }] })

  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(await screen.findByRole('heading', { name: '产品知识库' })).toBeInTheDocument()
  expect(await screen.findByText('原始资料')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '成型知识' }))
  expect(await screen.findByText('已成型知识')).toBeInTheDocument()
  expect(client.knowledgeSearch).toHaveBeenCalledWith('', ['library-knowledge'])
  expect(screen.getByRole('complementary', { name: '成型知识详情' })).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '收起详情栏' }))
  expect(screen.getByRole('button', { name: '展开详情栏' })).toBeInTheDocument()
  expect(document.querySelector('.app-shell')).toHaveClass('detail-pane-collapsed')
  expect(screen.getByRole('complementary', { name: '已折叠的详情栏' })).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '展开详情栏' }))
  expect(screen.getByRole('button', { name: '收起详情栏' })).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(screen.getByRole('button', { name: '来源文档' }))
  expect(await screen.findByText('原始资料')).toBeInTheDocument()
})

test('opens the relationship graph as a first-class workspace and keeps the selected library', async () => {
  const timestamp = new Date().toISOString()
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'graph-library', name: '图谱知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.graph).mockResolvedValue({ workspace: { id: 'graph-workspace', libraryId: 'graph-library', name: '默认图谱', filters: {}, viewport: {}, createdAt: timestamp, updatedAt: timestamp }, nodes: [], edges: [], truncated: false, hiddenNodeCount: 0, limits: { requestedDepth: 1, appliedDepth: 1, maxNodes: 250 } })
  vi.mocked(client.updateGraphWorkspace).mockResolvedValue({ id: 'graph-workspace', libraryId: 'graph-library', name: '默认图谱', filters: {}, viewport: {}, createdAt: timestamp, updatedAt: timestamp })
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(await screen.findByRole('heading', { name: '图谱知识库' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /关系图谱稳定知识与证据关系/ }))
  expect(await screen.findByRole('region', { name: '图谱知识库 关系图谱' })).toBeInTheDocument()
  expect(screen.getByRole('complementary', { name: '关系图谱详情' })).toBeInTheDocument()
  expect(screen.getByRole('contentinfo', { name: '全局底栏' })).toHaveTextContent('百分比布局已与内容版本分离')
  expect(client.graph).toHaveBeenCalledWith('graph-library', '', '', 1)
})

test('opens project architecture as a discoverable first-class workspace', async () => {
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /项目架构代码理解、技术栈与架构图/ }))
  expect(await screen.findByRole('region', { name: '项目架构' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: '项目架构' })).toBeInTheDocument()
  expect(await screen.findByText('还没有项目')).toBeInTheDocument()
})

test('explains how to continue when opening the graph before any library exists', async () => {
  vi.mocked(client.libraries).mockResolvedValue([])
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /关系图谱稳定知识与证据关系/ }))
  expect(await screen.findByRole('region', { name: '关系图谱' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: '请先选择或创建知识库' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '从关系图谱新建知识库' }))
  expect(await screen.findByRole('dialog')).toHaveTextContent('新建知识库')
})

test('edits library name, mark, tags, color, and icon from library details', async () => {
  const timestamp = new Date().toISOString()
  const library = { id: 'library-edit', name: '待编辑知识库', description: '旧描述', icon: 'library', color: 'jade', tags: ['旧标签'], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }
  const updated = { ...library, name: '已编辑知识库', description: '新描述', icon: 'book-open', color: 'violet', tags: ['产品', '内部'], favorite: true, updatedAt: new Date(Date.now() + 1000).toISOString() }
  vi.mocked(client.libraries).mockResolvedValue([library])
  vi.mocked(client.updateLibrary).mockResolvedValue(updated)

  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect((await screen.findAllByRole('heading', { name: '待编辑知识库' })).length).toBeGreaterThanOrEqual(1)
  fireEvent.click(screen.getByRole('button', { name: '编辑知识库 待编辑知识库' }))
  expect((await screen.findAllByRole('heading', { name: '待编辑知识库' })).length).toBeGreaterThanOrEqual(2)
  fireEvent.change(screen.getByLabelText('名称'), { target: { value: '已编辑知识库' } })
  fireEvent.change(screen.getByLabelText('描述'), { target: { value: '新描述' } })
  fireEvent.click(screen.getByRole('button', { name: '使用阅读图标' }))
  fireEvent.click(screen.getByRole('button', { name: '使用紫罗兰颜色' }))
  fireEvent.change(screen.getByLabelText('标签'), { target: { value: '产品，内部' } })
  fireEvent.click(screen.getByRole('checkbox', { name: /标记为重要/ }))
  fireEvent.click(screen.getByRole('button', { name: '保存详情' }))

  await waitFor(() => expect(client.updateLibrary).toHaveBeenCalledWith('library-edit', { name: '已编辑知识库', description: '新描述', icon: 'book-open', color: 'violet', tags: ['产品', '内部'], favorite: true }))
  expect(await screen.findByText('知识库详情已保存。')).toBeInTheDocument()
})

test('opens the content filter and filters source documents by tag', async () => {
  const timestamp = new Date().toISOString()
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-filter', name: '筛选测试库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.documents).mockResolvedValue([
    { id: 'document-product', libraryId: 'library-filter', title: '产品文档', mediaType: 'text/markdown', status: 'ready', tags: ['产品'], favorite: false, createdAt: timestamp, updatedAt: timestamp },
    { id: 'document-internal', libraryId: 'library-filter', title: '内部文档', mediaType: 'text/markdown', status: 'ready', tags: ['内部'], favorite: false, createdAt: timestamp, updatedAt: timestamp },
  ])
  vi.mocked(client.knowledgeSearch).mockResolvedValue({ results: [] })

  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(await screen.findByRole('heading', { name: '筛选测试库' })).toBeInTheDocument()
  expect(await screen.findByText('产品文档')).toBeInTheDocument()
  expect(screen.getByText('内部文档')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '筛选' }))
  fireEvent.click(screen.getByRole('checkbox', { name: '产品' }))

  expect(screen.getByText('产品文档')).toBeInTheDocument()
  expect(screen.queryByText('内部文档')).not.toBeInTheDocument()
})

test('keeps source multi-select unobtrusive and supports modifier-key selection', async () => {
  const timestamp = new Date().toISOString()
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-select', name: '多选测试库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.documents).mockResolvedValue([
    { id: 'select-a', libraryId: 'library-select', title: '原始资料 A', mediaType: 'text/markdown', status: 'ready', tags: [], favorite: false, createdAt: timestamp, updatedAt: timestamp },
    { id: 'select-b', libraryId: 'library-select', title: '原始资料 B', mediaType: 'text/markdown', status: 'ready', tags: [], favorite: false, createdAt: timestamp, updatedAt: timestamp },
  ])
  vi.mocked(client.knowledgeSearch).mockResolvedValue({ results: [] })

  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App/></QueryClientProvider>)
  expect(await screen.findByText('原始资料 A')).toBeInTheDocument()
  expect(screen.queryByText(/勾选左侧复选框进行批量操作/)).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '全选当前结果' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /原始资料 A/ }))
  fireEvent.click(screen.getByRole('button', { name: /原始资料 B/ }), { ctrlKey: true })
  expect(screen.getByText('2 项已选择')).toBeInTheDocument()
})

test('places pending review details in the right pane and resizes it with the keyboard', async () => {
  const timestamp = new Date().toISOString()
  const pending: KAHSubmission = { id: 'submission-pending', libraryId: 'library-1', knowledgeUri: 'kah://knowledge/pending', revision: 1, mode: 'create', reviewStatus: 'pending_review', validation: { valid: true }, title: '待审核测试知识', summary: '用于验证右侧详情预览。', tags: ['测试'], provenance: { sources: [] }, clientSubmissionId: 'client-pending', markdown: '# 内容预览\n\n这里是待审核知识正文。', createdAt: timestamp, updatedAt: timestamp }
  const processed: KAHSubmission = { ...pending, id: 'submission-processed', knowledgeUri: 'kah://knowledge/processed', title: '已处理知识', reviewStatus: 'published', clientSubmissionId: 'client-processed' }
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-1', name: '测试知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.submissions).mockResolvedValue([pending, processed])
  vi.mocked(client.submission).mockResolvedValue(pending)
  window.localStorage.removeItem('kah.detail-pane-width')
  const originalInnerWidth = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })

  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /待审核/ }))

  expect(await screen.findByRole('heading', { name: '待审核知识' })).toBeInTheDocument()
  expect(await screen.findByRole('heading', { name: '待审核测试知识' })).toBeInTheDocument()
  const globalBottomBar = screen.getByRole('contentinfo', { name: '全局底栏' })
  expect(globalBottomBar).toHaveTextContent('审核通过前不会写入正式索引')
  expect(globalBottomBar).toHaveTextContent('审核记录可追溯')
  expect(document.querySelector('.status-bar')).not.toBeInTheDocument()
  expect(document.querySelector('.review-layout .review-card')).not.toBeInTheDocument()
  expect(screen.queryByText('已处理知识')).not.toBeInTheDocument()

  const resizer = screen.getByRole('separator', { name: '调整详情栏宽度' })
  expect(resizer).toHaveAttribute('aria-valuenow', '420')
  const bounds = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ right: 1000 } as DOMRect)
  fireEvent.pointerDown(resizer, { button: 0, pointerId: 1 })
  fireEvent.pointerMove(window, { clientX: 500, pointerId: 1 })
  fireEvent.pointerUp(window, { pointerId: 1 })
  expect(resizer).toHaveAttribute('aria-valuenow', '500')
  bounds.mockRestore()
  fireEvent.keyDown(resizer, { key: 'ArrowLeft' })
  expect(resizer).toHaveAttribute('aria-valuenow', '516')
  fireEvent.keyDown(resizer, { key: 'End' })
  expect(resizer).toHaveAttribute('aria-valuenow', '720')
  fireEvent.keyDown(resizer, { key: 'Home' })
  expect(resizer).toHaveAttribute('aria-valuenow', '330')
  window.localStorage.removeItem('kah.detail-pane-width')
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalInnerWidth })
})

test('shows failed Skill imports in the management workspace', async () => {
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getAllByRole('button', { name: 'SkillsAgent 能力包' })[0])
  expect(await screen.findByText('还没有 Skill')).toBeInTheDocument()

  vi.mocked(window.kah.selectFiles).mockResolvedValueOnce(['C:\\skills\\broken.zip'])
  vi.mocked(client.importSkill).mockRejectedValueOnce(new Error('invalid skill package'))
  fireEvent.click(screen.getByRole('button', { name: '导入 SKILL.md' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('invalid skill package')
})
test('creates an external Skill mapping target from the secondary view', async () => {
  const target = { id: 'target-1', name: '本机 Agent', kind: 'agent' as const, directoryPath: 'C:\\agent\\skills', status: 'ready' as const, mappings: [], createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }
  vi.mocked(client.skillMappingTargets).mockResolvedValue([target])
  vi.mocked(client.skillMappingTarget).mockResolvedValue(target)
  vi.mocked(client.createSkillMappingTarget).mockResolvedValue(target)
  vi.mocked(window.kah.selectDirectory).mockResolvedValue('C:\\agent\\skills')
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getAllByRole('button', { name: 'SkillsAgent 能力包' })[0])
  fireEvent.click(screen.getByRole('tab', { name: '外部映射' }))
  expect(await screen.findByText('本机 Agent')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '新建映射' }))
  fireEvent.change(screen.getByLabelText('目标名称'), { target: { value: '新 Agent' } })
  fireEvent.click(screen.getByRole('button', { name: '选择目录' }))
  expect(await screen.findByDisplayValue('C:\\agent\\skills')).toBeInTheDocument()
  expect(screen.getByDisplayValue('新 Agent')).toBeInTheDocument()
  const createButton = screen.getByRole('button', { name: '创建映射' })
  expect(createButton).not.toBeDisabled()
  fireEvent.click(createButton)
  await waitFor(() => expect(client.createSkillMappingTarget).toHaveBeenCalledWith({ name: '新 Agent', kind: 'agent', directoryPath: 'C:\\agent\\skills', skillIds: [] }))
})
test('routes Chinese Markdown links through the link handler', () => {
  const onLink = vi.fn()
  render(<MarkdownContent content="[打开中文文档](./章节/第二篇.md)" onLink={onLink} />)
  fireEvent.click(screen.getByRole('link', { name: '打开中文文档' }))
  expect(onLink).toHaveBeenCalledWith('./%E7%AB%A0%E8%8A%82/%E7%AC%AC%E4%BA%8C%E7%AF%87.md')
})

test('shows the import pipeline while automatic summary is running', async () => {
  vi.mocked(client.jobs).mockResolvedValue([{ id: 'summary-job', kind: 'knowledge_summarize', status: 'running', progress: 0.45, message: '正在调用总结模型', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }])
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(await screen.findByText('导入管线处理中')).toBeInTheDocument()
  expect(screen.getByText('自动总结')).toBeInTheDocument()
  expect(screen.getByText('正在调用总结模型')).toBeInTheDocument()
})

test('exposes per-library automatic summary model controls', async () => {
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-1', name: '产品知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: true, summaryProviderId: 'provider-1', autoReviewAgentSubmissions: false, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }])
  vi.mocked(client.providers).mockResolvedValue([{ id: 'provider-1', name: '本地总结模型', kind: 'lmstudio', baseUrl: 'http://127.0.0.1:1234', model: 'summary', embeddingModel: '', local: true }])
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><PrivacySettings libraries={[{ id: 'library-1', name: '产品知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: true, summaryProviderId: 'provider-1', autoReviewAgentSubmissions: false, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }]} /></QueryClientProvider>)
  expect(await screen.findByText('导入后自动总结')).toBeInTheDocument()
  expect(await screen.findByRole('option', { name: '本地总结模型 · 本地' })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '产品知识库总结模型' })).toHaveValue('provider-1')
})


test('allows a new library to choose a summary model before enabling enhancement', async () => {
  const library = { id: 'new-library', name: '新库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: '', updatedAt: '' }
  vi.mocked(client.providers).mockResolvedValue([{ id: 'local-model', name: '离线模型', kind: 'lmstudio', baseUrl: 'http://127.0.0.1:1234', model: 'summary', embeddingModel: '', local: true }])
  vi.mocked(client.updateLibrary).mockResolvedValue({...library, summaryProviderId:'local-model'})
  const query = new QueryClient({defaultOptions:{queries:{retry:false}}})
  const view = render(<QueryClientProvider client={query}><PrivacySettings libraries={[library]}/></QueryClientProvider>)
  await screen.findByRole('option', {name:'离线模型 · 本地'})
  fireEvent.change(screen.getByRole('combobox', {name:'新库总结模型'}), {target:{value:'local-model'}})
  await waitFor(() => expect(client.updateLibrary).toHaveBeenCalledWith('new-library', {summaryProviderId:'local-model'}))
  await waitFor(() => expect(screen.queryByText('正在保存自动化设置…')).not.toBeInTheDocument())
  view.rerender(<QueryClientProvider client={query}><PrivacySettings libraries={[{...library, summaryProviderId:'local-model'}]}/></QueryClientProvider>)
  fireEvent.change(screen.getByRole('combobox', {name:'新库导入转换策略'}), {target:{value:'enhanced'}})
  await waitFor(() => expect(client.updateLibrary).toHaveBeenCalledWith('new-library', {importConversionMode:'enhanced'}))
})

test('requires restart confirmation and preserves an unsaved document when update is declined', async () => {
  const library = { id: 'update-library', name: '更新保护测试库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, autoReviewAgentSubmissions: false, createdAt: '', updatedAt: '' }
  const document = { id: 'update-document', libraryId: library.id, title: '更新保护文档', mediaType: 'text/markdown', status: 'ready' as const, tags: [], favorite: false, createdAt: '', updatedAt: '' }
  vi.mocked(client.libraries).mockResolvedValue([library])
  vi.mocked(client.documents).mockResolvedValue([document])
  vi.mocked(client.document).mockResolvedValue({ ...document, preview: [] })
  const original = window.kah.updates
  const install = vi.fn(async () => ({ enabled: true, status: 'installing' as const, currentVersion: '0.1.2' }))
  const ready = { enabled: true, status: 'ready' as const, currentVersion: '0.1.2', targetVersion: '0.1.3' }
  window.kah.updates = { getState: vi.fn(async () => ready), check: vi.fn(async () => ready), download: vi.fn(async () => ready), install, onStateChanged: vi.fn(() => () => {}) }
  const confirm = vi.spyOn(window, 'confirm')
  try {
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App/></QueryClientProvider>)
    fireEvent.click(await screen.findByText(document.title))
    fireEvent.click(await screen.findByRole('button', { name: '编辑正文' }))
    fireEvent.change(await screen.findByLabelText('测试正文编辑器'), { target: { value: '未保存的正文' } })
    const update = await screen.findByRole('button', { name: '重启并更新' })
    confirm.mockReturnValueOnce(false)
    fireEvent.click(update)
    expect(install).not.toHaveBeenCalled()
    confirm.mockReturnValueOnce(true).mockReturnValueOnce(false)
    fireEvent.click(update)
    expect(confirm).toHaveBeenLastCalledWith('文档正文有未保存修改。确定放弃这些修改并继续？')
    expect(install).not.toHaveBeenCalled()
    expect(screen.getByLabelText('测试正文编辑器')).toHaveValue('未保存的正文')
    confirm.mockReturnValueOnce(true).mockReturnValueOnce(true)
    fireEvent.click(update)
    await waitFor(() => expect(install).toHaveBeenCalledTimes(1))
  } finally { cleanup(); window.kah.updates = original; confirm.mockRestore() }
})
