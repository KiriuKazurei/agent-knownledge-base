import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ArchitectureWorkspace } from './ArchitectureWorkspace'
import { ApiError, client } from './api'
import type { ArchitectureAnalysis, ArchitectureProject, ArchitectureSnapshot, ArchitectureTask } from './types'

vi.mock('./api', () => ({
  ApiError: class ApiError extends Error {
    status: number
    constructor(message: string, status = 409) { super(message); this.status = status }
  },
  client: {
    architectureProjects: vi.fn(), architectureProject: vi.fn(), architectureTasks: vi.fn(), architectureSnapshots: vi.fn(), architectureSnapshot: vi.fn(), architectureAnalysis: vi.fn(), architectureDiff: vi.fn(), architectureArtifacts: vi.fn(), downloadArchitectureArtifact: vi.fn(),
    scanArchitectureProject: vi.fn(), generateArchitectureProject: vi.fn(), cancelArchitectureTask: vi.fn(), createArchitectureProject: vi.fn(), updateArchitectureProject: vi.fn(), deleteArchitectureProject: vi.fn(), providers: vi.fn(async () => [])
  }
}))

const timestamp = '2026-09-20T08:00:00.000Z'
const project: ArchitectureProject = { id: 'project-1', libraryId: 'library-1', name: 'KAH', description: '桌面知识库', rootPath: 'E:\\AI\\agent-knownledge-base', excludePatterns: [], scanPeriodSeconds: 0, scheduleKind: 'manual', allowRemoteCode: false, execution: { executor: 'auto', allowExternal: false, timeoutSeconds: 0 }, lastSnapshotId: 'snapshot-1', analysisStatus: 'ready', currentAnalysisSnapshotId: 'snapshot-1', createdAt: timestamp, updatedAt: timestamp }
const snapshot: ArchitectureSnapshot = { id: 'snapshot-1', projectId: 'project-1', generation: 1, status: 'ready', contentHash: 'snapshot-hash', gitRevision: 'abc123', gitDirty: true, gitChangedPaths: ['apps/desktop/src/App.tsx'], fileCount: 2, moduleCount: 1, unknownCount: 0, coverage: { totalFiles: 3, includedFiles: 2, ignoredFiles: 1, secretFiles: 1, binaryFiles: 0, outsideLinks: 0, unknownFiles: 0, byLanguage: { typescript: 2 }, ignoredReasons: { secret: 1 } }, files: [{ path: 'apps/desktop/src/App.tsx', language: 'typescript', size: 10, lineStart: 1, lineEnd: 4, lineCount: 4, contentHash: 'file-hash', entryPoint: true }], modules: [{ name: 'desktop', language: 'typescript', rootPath: '.', dependencies: ['react'], dependencyVersions: { react: '19' }, entryPoints: ['src/App.tsx'], dependencyFiles: ['package.json'], containerFiles: [], unknown: false, evidencePaths: ['package.json'] }], createdAt: timestamp }
const previousSnapshot: ArchitectureSnapshot = { ...snapshot, id: 'snapshot-0', generation: 0, contentHash: 'previous-hash', createdAt: '2026-09-19T08:00:00.000Z' }
const analysis: ArchitectureAnalysis = { schemaVersion: 'kah.architecture.analysis.v1', projectId: 'project-1', snapshotId: 'snapshot-1', facts: [{ id: 'fact-1', subject: 'desktop', predicate: 'uses', object: 'React', evidence: ['evidence-1'] }], inferences: [{ id: 'inference-1', statement: 'API 与桌面端通过本地 HTTP 连接', evidence: ['evidence-1'], confidence: .81 }], evidence: [{ id: 'evidence-1', path: 'apps/desktop/src/App.tsx', lineStart: 1, lineEnd: 4, contentHash: 'file-hash', kind: 'code' }], incremental: { added: [], changed: [], deleted: [], unchanged: [] }, coverage: snapshot.coverage }
const previousAnalysis: ArchitectureAnalysis = { ...analysis, snapshotId: previousSnapshot.id, facts: [{ ...analysis.facts[0], subject: 'legacy desktop' }] }
const task = (status: ArchitectureTask['status']): ArchitectureTask => ({ id: `task-${status}`, projectId: project.id, kind: 'scan', status, generation: 1, cancelRequested: false, createdAt: timestamp, updatedAt: timestamp, message: status === 'failed' ? '扫描失败' : '读取文件' })

function renderWorkspace() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })
  return render(<QueryClientProvider client={queryClient}><ArchitectureWorkspace libraries={[{ id: 'library-1', name: '知识库', description: '', icon: 'library', color: 'jade', tags: [], favorite: false, allowRemoteModels: false, autoSummarizeImports: false, autoReviewAgentSubmissions: false, createdAt: timestamp, updatedAt: timestamp }]} /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(client.architectureProjects).mockResolvedValue([project])
  vi.mocked(client.architectureTasks).mockResolvedValue([])
  vi.mocked(client.architectureSnapshots).mockResolvedValue([snapshot])
  vi.mocked(client.architectureSnapshot).mockImplementation(async (_projectId, snapshotId) => snapshotId === previousSnapshot.id ? previousSnapshot : snapshot)
  vi.mocked(client.architectureAnalysis).mockResolvedValue(analysis)
  vi.mocked(client.architectureDiff).mockResolvedValue({ projectId: project.id, fromSnapshotId: '', toSnapshotId: snapshot.id, added: ['new.ts'], changed: ['src/App.tsx'], deleted: [], unchanged: [] })
  vi.mocked(client.architectureArtifacts).mockResolvedValue([])
  vi.mocked(client.scanArchitectureProject).mockResolvedValue(task('queued'))
  vi.mocked(client.generateArchitectureProject).mockResolvedValue({ ...task('queued'), kind: 'analysis' })
  vi.mocked(client.cancelArchitectureTask).mockResolvedValue(task('cancelled'))
  Object.defineProperty(window, 'kah', { configurable: true, value: { selectDirectory: vi.fn(async () => 'E:\\Projects\\demo'), saveFile: vi.fn(async () => 'E:\\exports\\architecture.md') } })
})

afterEach(() => { cleanup() })

describe('ArchitectureWorkspace', () => {
  test('shows project architecture empty/loading and project list states', async () => {
    vi.mocked(client.architectureProjects).mockResolvedValueOnce([])
    renderWorkspace()
    expect(await screen.findByText('还没有项目')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '添加项目架构' }).length).toBeGreaterThan(1)
  })

  test('creates a project with local directory, schedule and remote permission', async () => {
    vi.mocked(client.architectureProjects).mockResolvedValue([])
    vi.mocked(client.createArchitectureProject).mockResolvedValue(project)
    renderWorkspace()
    fireEvent.click((await screen.findAllByRole('button', { name: '添加项目架构' }))[0])
    fireEvent.change(screen.getByLabelText('项目名称'), { target: { value: 'Demo' } })
    fireEvent.change(screen.getByLabelText('关联知识库'), { target: { value: 'library-1' } })
    fireEvent.click(screen.getByRole('button', { name: '选择目录' }))
    await waitFor(() => expect(screen.getByLabelText('项目目录')).toHaveValue('E:\\Projects\\demo'))
    fireEvent.change(screen.getByLabelText('更新周期'), { target: { value: 'interval' } })
    fireEvent.change(screen.getByLabelText('间隔秒数'), { target: { value: '3600' } })
    fireEvent.click(screen.getByLabelText('允许远程处理项目代码'))
    fireEvent.click(screen.getByRole('button', { name: '创建项目' }))
    await waitFor(() => expect(client.createArchitectureProject).toHaveBeenCalledWith(expect.objectContaining({ name: 'Demo', rootPath: 'E:\\Projects\\demo', scheduleKind: 'interval', scanPeriodSeconds: 3600, allowRemoteCode: true })))
  })

  test('scans, cancels and exposes task progress with an understandable conflict-safe flow', async () => {
    vi.mocked(client.architectureTasks).mockResolvedValue([task('running')])
    renderWorkspace()
    expect(await screen.findByText(/扫描 · 进行中/)).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '52')
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(client.cancelArchitectureTask).toHaveBeenCalledWith('task-running'))
  })

  test('keeps stale/error visible and offers retry', async () => {
    vi.mocked(client.architectureProjects).mockResolvedValue([{ ...project, analysisStatus: 'stale', analysisError: '生成器暂不可用' }])
    renderWorkspace()
    expect(await screen.findByText('架构内容已过期')).toBeInTheDocument()
    expect(screen.getAllByText('生成器暂不可用').length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(client.scanArchitectureProject).toHaveBeenCalledWith(project.id))
  })

  test('switches versions, evidence, diff and artifact export views', async () => {
    vi.mocked(client.architectureArtifacts).mockResolvedValue([{ id: 'artifact-md', projectId: project.id, snapshotId: snapshot.id, kind: 'document', fileName: 'architecture.md', contentType: 'text/markdown', renderer: 'kah', relativePath: 'architecture.md', contentHash: 'hash', size: 30, createdAt: timestamp }])
    vi.mocked(client.downloadArchitectureArtifact).mockResolvedValue(new Blob(['# 架构文档'], { type: 'text/markdown' }))
    renderWorkspace()
    expect(await screen.findByText('扫描范围')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: '代码证据' }))
    expect(await screen.findByText('desktop')).toBeInTheDocument()
    expect(screen.getByText('apps/desktop/src/App.tsx')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: '版本差异' }))
    expect(await screen.findByText('new.ts')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: '文档与图' }))
    expect(await screen.findByText('architecture.md')).toBeInTheDocument()
    await waitFor(() => expect(client.downloadArchitectureArtifact).toHaveBeenCalledWith(project.id, snapshot.id, 'document'))
    fireEvent.click(screen.getByRole('button', { name: '导出 Markdown 文档' }))
    await waitFor(() => expect(window.kah.saveFile).toHaveBeenCalled())
  })

  test('loads analysis and evidence for an older generated snapshot', async () => {
    vi.mocked(client.architectureSnapshots).mockResolvedValue([snapshot, previousSnapshot])
    vi.mocked(client.architectureAnalysis).mockImplementation(async (_projectId, snapshotId) => snapshotId === previousSnapshot.id ? previousAnalysis : analysis)
    renderWorkspace()
    fireEvent.click(await screen.findByRole('tab', { name: '代码证据' }))
    expect(await screen.findByText('desktop')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('选择架构版本'), { target: { value: previousSnapshot.id } })
    await waitFor(() => expect(client.architectureAnalysis).toHaveBeenCalledWith(project.id, previousSnapshot.id))
    expect(await screen.findByText('legacy desktop')).toBeInTheDocument()
    expect(screen.queryByText('当前版本还没有经过分析的代码事实。')).not.toBeInTheDocument()
  })

  test('treats a scan-only snapshot without analysis as an evidence-only empty state', async () => {
    vi.mocked(client.architectureSnapshots).mockResolvedValue([snapshot, previousSnapshot])
    vi.mocked(client.architectureAnalysis).mockImplementation(async (_projectId, snapshotId) => {
      if (snapshotId === previousSnapshot.id) throw new ApiError('Architecture analysis not found', 404)
      return analysis
    })
    renderWorkspace()
    fireEvent.click(await screen.findByRole('tab', { name: '代码证据' }))
    fireEvent.change(screen.getByLabelText('选择架构版本'), { target: { value: previousSnapshot.id } })
    expect(await screen.findByText(/当前版本还没有经过分析的代码事实/)).toBeInTheDocument()
    expect(screen.queryByText('Architecture analysis not found')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
