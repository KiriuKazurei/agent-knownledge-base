import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { RetrievalModelSettings } from './RetrievalModelSettings'
import { RetrievalSearchPanel } from './RetrievalSearchPanel'
import { client } from './api'
import type { ArchitectureProject, EmbeddingModel, Library, RetrievalSearchResponse, RetrievalResult } from './types'

vi.mock('./api', () => ({
  client: {
    embeddingModels: vi.fn(),
    embeddingModelHealth: vi.fn(),
    setEmbeddingModelEnabled: vi.fn(),
    deleteEmbeddingModel: vi.fn(),
    providers: vi.fn(),
    query: vi.fn(),
  }
}))

const library: Library = {
  id: 'library-1', name: '知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false,
  allowRemoteModels: false, autoSummarizeImports: false, autoReviewAgentSubmissions: false,
  createdAt: '2026-09-21T00:00:00.000Z', updatedAt: '2026-09-21T00:00:00.000Z'
}
const project: ArchitectureProject = {
  id: 'project-1', libraryId: library.id, name: '项目', rootPath: 'E:\\project', excludePatterns: [],
  scanPeriodSeconds: 0, scheduleKind: 'manual', allowRemoteCode: false,
  execution: { executor: 'none', allowExternal: false, timeoutSeconds: 0 },
  createdAt: library.createdAt, updatedAt: library.updatedAt
}
const currentResult: RetrievalResult = {
  id: 'current-1', text: '当前检索结果', sourceType: 'raw', reviewStatus: 'unreviewed', libraryId: library.id,
  filePath: 'docs/current.md', lineStart: 3, lineEnd: 4, contentHash: 'a'.repeat(64),
  scores: { lexical: 0.7, vector: 0.8, fusion: 0.04, final: 0.04 }
}
const response: RetrievalSearchResponse = {
  results: [currentResult], semanticAvailable: true, keywordOnly: false, degraded: false, retrievalMode: 'hybrid'
}

function renderWithClient(ui: React.ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(client.providers).mockResolvedValue([{ id: 'provider-1', name: '本地生成', kind: 'lmstudio', baseUrl: 'http://127.0.0.1', model: 'answer', embeddingModel: '', local: true }])
})

afterEach(() => cleanup())

describe('检索桌面阶段 3B', () => {
  test('toggle failure retries the original target state', async () => {
    const model: EmbeddingModel = {
      id: 'bge-small-zh', version: '1.5', license: 'Apache-2.0', dimension: 512,
      installed: true, enabled: true, health: { available: true, modelId: 'bge-small-zh', version: '1.5' }
    }
    vi.mocked(client.embeddingModels).mockResolvedValue([model])
    vi.mocked(client.setEmbeddingModelEnabled).mockRejectedValue(new Error('保存失败'))

    renderWithClient(<RetrievalModelSettings libraries={[]} />)
    fireEvent.click(await screen.findByRole('button', { name: '停用' }))
    expect(await screen.findByText('保存失败')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(client.setEmbeddingModelEnabled).toHaveBeenCalledTimes(2))
    expect(client.setEmbeddingModelEnabled).toHaveBeenNthCalledWith(1, model.id, false)
    expect(client.setEmbeddingModelEnabled).toHaveBeenNthCalledWith(2, model.id, false)
  })

  test('uses ranking score wording and sends the active filters to citation QA', async () => {
    const answerCitation: RetrievalResult = {
      id: 'answer-only-1', text: '答案专用证据', sourceType: 'architecture', reviewStatus: 'unreviewed',
      libraryId: library.id, projectId: project.id, filePath: 'src/main.ts', lineStart: 18,
      contentHash: 'b'.repeat(64), scores: { lexical: 0, vector: 0.9, fusion: 0.03, final: 0.03 }
    }
    vi.mocked(client.query).mockResolvedValue({ answer: '答案正文', evidence: [answerCitation], degraded: false })

    renderWithClient(<RetrievalSearchPanel
      query="入口"
      response={response}
      filters={{ sourceType: 'raw', libraryIds: [library.id], projectIds: [project.id] }}
      loading={false}
      error={undefined}
      libraries={[library]}
      selectedLibrary={library.id}
      projects={[project]}
      onSearch={vi.fn()}
      onRetry={vi.fn()}
    />)

    expect(screen.getByText('排序分 0.0400')).toBeInTheDocument()
    expect(screen.queryByText('4%')).not.toBeInTheDocument()
    await screen.findByRole('option', { name: /本地生成/ })
    fireEvent.change(await screen.findByLabelText('引用问答生成模型'), { target: { value: 'provider-1' } })
    await waitFor(() => expect(screen.getByRole('button', { name: '生成引用答案' })).not.toBeDisabled())
    fireEvent.click(screen.getByRole('button', { name: '生成引用答案' }))
    await waitFor(() => expect(client.query).toHaveBeenCalledWith(expect.objectContaining({
      filters: { sourceType: 'raw', libraryIds: [library.id], projectIds: [project.id] }
    })))
    fireEvent.click(await screen.findByRole('button', { name: /src\/main\.ts/ }))
    expect(await screen.findByText('答案引用内容')).toBeInTheDocument()
    expect(screen.getByText('未审核')).toBeInTheDocument()
    expect(screen.getByText('答案专用证据')).toBeInTheDocument()
  })
})
