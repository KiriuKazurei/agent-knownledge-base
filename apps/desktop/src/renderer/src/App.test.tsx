import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, test, vi } from 'vitest'
import { App, MarkdownContent, PrivacySettings } from './App'
import { client } from './api'
import { useUI } from './store'
import type { KAHSubmission } from './types'

vi.mock('./api', () => ({
  getRuntime: vi.fn(async () => ({apiBaseUrl:'http://127.0.0.1:1',desktopToken:'x',dataRoot:'data',version:'test'})),
  client: {
    libraries: vi.fn(async () => []), documents: vi.fn(async () => []), folders: vi.fn(async () => []), jobs: vi.fn(async () => []), savedSearches: vi.fn(async () => []), knowledgeSearch: vi.fn(async () => ({ results: [] })),
    document: vi.fn(), knowledge: vi.fn(), submissions: vi.fn(async () => []), submission: vi.fn(), approveSubmission: vi.fn(), rejectSubmission: vi.fn(), skills: vi.fn(async () => []), skill: vi.fn(), skillManifest: vi.fn(), importSkill: vi.fn(), updateSkillLinks: vi.fn(), deleteSkill: vi.fn(), skillMappingTargets: vi.fn(async () => []), skillMappingTarget: vi.fn(), createSkillMappingTarget: vi.fn(), updateSkillMappingTarget: vi.fn(), addSkillMappings: vi.fn(), verifySkillMappingTarget: vi.fn(), repairSkillMapping: vi.fn(), removeSkillMapping: vi.fn(), forgetSkillMapping: vi.fn(), deleteSkillMappingTarget: vi.fn(), createLibrary: vi.fn(), importFiles: vi.fn(), importUrl: vi.fn(), query: vi.fn(),
    providers: vi.fn(async () => []), tokens: vi.fn(async () => []), feedback: vi.fn(), updateLibrary: vi.fn(), updateDocument: vi.fn(), createFolder: vi.fn(), backup: vi.fn(), createSavedSearch: vi.fn(), deleteSavedSearch: vi.fn()
  }
}))

Object.defineProperty(window, 'kah', { value: { runtimeConfig: vi.fn(), setTitleBarTheme: vi.fn(), selectFiles: vi.fn(async () => []), selectDirectory: vi.fn(async () => ''), openPath: vi.fn(), openExternal: vi.fn(), onBackendExit: vi.fn(() => () => {}) } })
Object.defineProperty(window, 'matchMedia', { value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })) })

afterEach(() => cleanup())

test('renders the accessible knowledge workbench', async () => {
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  expect(screen.getByRole('navigation', {name:'知识库导航'})).toBeInTheDocument()
  expect(screen.getByRole('textbox', {name:'搜索知识'})).toBeInTheDocument()
  expect(screen.queryByRole('button', {name:'切换主题'})).not.toBeInTheDocument()
  expect(screen.queryByRole('button', {name:'切换详情栏'})).not.toBeInTheDocument()
  expect(await screen.findByText('还没有来源文档')).toBeInTheDocument()
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
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-knowledge', name: '产品知识库', description: '', allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
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

test('places pending review details in the right pane and resizes it with the keyboard', async () => {
  const timestamp = new Date().toISOString()
  const pending: KAHSubmission = { id: 'submission-pending', libraryId: 'library-1', knowledgeUri: 'kah://knowledge/pending', revision: 1, mode: 'create', reviewStatus: 'pending_review', validation: { valid: true }, title: '待审核测试知识', summary: '用于验证右侧详情预览。', tags: ['测试'], provenance: { sources: [] }, clientSubmissionId: 'client-pending', markdown: '# 内容预览\n\n这里是待审核知识正文。', createdAt: timestamp, updatedAt: timestamp }
  const processed: KAHSubmission = { ...pending, id: 'submission-processed', knowledgeUri: 'kah://knowledge/processed', title: '已处理知识', reviewStatus: 'published', clientSubmissionId: 'client-processed' }
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-1', name: '测试知识库', description: '', allowRemoteModels: false, autoSummarizeImports: false, summaryProviderId: '', autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }])
  vi.mocked(client.submissions).mockResolvedValue([pending, processed])
  vi.mocked(client.submission).mockResolvedValue(pending)
  window.localStorage.removeItem('kah.detail-pane-width')

  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><App/></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: /待审核/ }))

  expect(await screen.findByRole('heading', { name: '待审核知识' })).toBeInTheDocument()
  expect(await screen.findByRole('heading', { name: '待审核测试知识' })).toBeInTheDocument()
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
  vi.mocked(client.libraries).mockResolvedValue([{ id: 'library-1', name: '产品知识库', description: '', allowRemoteModels: false, autoSummarizeImports: true, summaryProviderId: 'provider-1', autoReviewAgentSubmissions: false, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }])
  vi.mocked(client.providers).mockResolvedValue([{ id: 'provider-1', name: '本地总结模型', kind: 'lmstudio', baseUrl: 'http://127.0.0.1:1234', model: 'summary', embeddingModel: '', local: true }])
  render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><PrivacySettings libraries={[{ id: 'library-1', name: '产品知识库', description: '', allowRemoteModels: false, autoSummarizeImports: true, summaryProviderId: 'provider-1', autoReviewAgentSubmissions: false, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }]} /></QueryClientProvider>)
  expect(await screen.findByText('导入后自动总结')).toBeInTheDocument()
  expect(await screen.findByRole('option', { name: '本地总结模型 · 本地' })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '产品知识库总结模型' })).toHaveValue('provider-1')
})
