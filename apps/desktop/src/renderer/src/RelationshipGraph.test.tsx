import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { RelationshipGraphInspector, RelationshipGraphWorkspace } from './RelationshipGraph'
import { client } from './api'
import type { GraphSnapshot } from './types'

vi.mock('./api', () => ({
  client: {
    graph: vi.fn(),
    saveGraphPositions: vi.fn(),
    updateGraphWorkspace: vi.fn(),
    autoLayoutGraph: vi.fn()
  }
}))

const snapshot: GraphSnapshot = {
  workspace: { id: 'workspace-1', libraryId: 'library-1', name: '默认图谱', filters: {}, viewport: {}, createdAt: '2026-09-03T00:00:00Z', updatedAt: '2026-09-03T00:00:00Z' },
  nodes: [
    { id: 'knowledge-a', kind: 'knowledge', label: '百分比坐标', description: '坐标使用小数百分比', uri: 'kah://knowledge/a', revision: 2, trust: 'verified', tags: ['布局'], position: { xPct: 37.625, yPct: 14.083 } },
    { id: 'document-b', kind: 'document', label: '来源文档', uri: 'kah://document/b', status: 'ready', tags: [], position: { xPct: 71.125, yPct: 62.75 } }
  ],
  edges: [{ id: 'edge-1', kind: 'source', relationType: 'source', source: 'knowledge-a', target: 'document-b', sourceId: 'source-1', locator: { section: 'intro' } }],
  truncated: false,
  hiddenNodeCount: 0,
  limits: { requestedDepth: 1, appliedDepth: 1, maxNodes: 250 }
}

function renderGraph(onSelectionChange = vi.fn()) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
    <RelationshipGraphWorkspace libraryId="library-1" libraryName="测试知识库" onSelectionChange={onSelectionChange} onOpenKnowledge={vi.fn()}/>
  </QueryClientProvider>)
}

beforeEach(() => {
  vi.mocked(client.graph).mockResolvedValue(snapshot)
  vi.mocked(client.saveGraphPositions).mockResolvedValue({ positions: [] })
  vi.mocked(client.updateGraphWorkspace).mockResolvedValue(snapshot.workspace)
  vi.mocked(client.autoLayoutGraph).mockResolvedValue({ positions: snapshot.nodes.map((node) => ({ nodeId: node.id, ...node.position! })) })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

test('renders audited nodes and supports keyboard percentage movement without changing positions on viewport zoom', async () => {
  const onSelection = vi.fn()
  renderGraph(onSelection)
  expect(await screen.findByRole('heading', { name: '测试知识库' })).toBeInTheDocument()
  const node = await screen.findByRole('button', { name: /知识：百分比坐标/ })
  const canvas = screen.getByRole('application', { name: /关系图谱画布/ })
  expect(node).toHaveAttribute('aria-label', expect.stringContaining('37.625%'))
  fireEvent.click(screen.getByRole('button', { name: '百分比坐标' }))
  fireEvent.click(screen.getByRole('button', { name: '聚焦节点' }))
  await waitFor(() => expect(client.graph).toHaveBeenCalledWith('library-1', '', 'knowledge-a', 1))
  fireEvent.click(screen.getByRole('button', { name: '展开一层' }))
  await waitFor(() => expect(client.graph).toHaveBeenCalledWith('library-1', '', 'knowledge-a', 2))

  fireEvent.keyDown(canvas, { key: '+' })
  expect(screen.getByLabelText('当前缩放比例')).toHaveTextContent('115%')
  expect(client.saveGraphPositions).not.toHaveBeenCalled()
  fireEvent.wheel(canvas, { deltaY: -100 })
  expect(screen.getByLabelText('当前缩放比例')).toHaveTextContent('126%')
  expect(canvas).toHaveAttribute('aria-label', expect.stringContaining('滚轮缩放'))
  expect(screen.getByRole('contentinfo', { name: '图谱操作与节点列表' })).toBeInTheDocument()

  fireEvent.keyDown(node, { key: 'ArrowRight' })
  await waitFor(() => expect(client.saveGraphPositions).toHaveBeenCalledWith('workspace-1', [{ nodeId: 'knowledge-a', xPct: 37.875, yPct: 14.083 }]))
  expect(onSelection).toHaveBeenCalledWith(expect.objectContaining({ type: 'node' }))
})

test('filters source nodes and exposes a text node-list alternative', async () => {
  renderGraph()
  expect(await screen.findByRole('button', { name: /来源：来源文档/ })).toBeInTheDocument()
  expect(screen.getByRole('region', { name: '可访问节点列表' })).toHaveTextContent('百分比坐标')
  fireEvent.click(screen.getByRole('checkbox', { name: '显示来源节点' }))
  expect(screen.queryByRole('button', { name: /来源：来源文档/ })).not.toBeInTheDocument()
  expect(screen.getByText('当前节点尚无符合筛选的明确关系或来源引用。')).toBeInTheDocument()
})

test('keeps a failed local move and offers an explicit retry', async () => {
  vi.mocked(client.saveGraphPositions).mockRejectedValueOnce(new Error('disk busy')).mockResolvedValueOnce({ positions: [] })
  renderGraph()
  const node = await screen.findByRole('button', { name: /知识：百分比坐标/ })
  fireEvent.keyDown(node, { key: 'ArrowDown' })
  expect(await screen.findByRole('alert')).toHaveTextContent('位置未保存：disk busy')
  fireEvent.click(screen.getByRole('button', { name: '重试保存' }))
  await waitFor(() => expect(client.saveGraphPositions).toHaveBeenCalledTimes(2))
})

test('inspector explains an edge and opens a fixed knowledge revision', () => {
  const openKnowledge = vi.fn()
  const { rerender } = render(<RelationshipGraphInspector selection={{ type: 'edge', edge: snapshot.edges[0] }} onOpenKnowledge={openKnowledge} onOpenDocument={vi.fn()}/>)
  expect(screen.getByRole('heading', { name: 'source' })).toBeInTheDocument()
  expect(screen.getByText(/knowledge-a/)).toBeInTheDocument()
  rerender(<RelationshipGraphInspector selection={{ type: 'node', node: snapshot.nodes[0] }} onOpenKnowledge={openKnowledge} onOpenDocument={vi.fn()}/>)
  fireEvent.click(screen.getByRole('button', { name: '打开知识' }))
  expect(openKnowledge).toHaveBeenCalledWith('kah://knowledge/a', 2)
})
