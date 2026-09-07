import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { BookOpen, CircleAlert, FileText, Focus, LoaderCircle, Minus, Network, Plus, Redo2, RotateCcw, Search, Undo2 } from 'lucide-react'
import { client } from './api'
import { fitGraphToViewport, GRAPH_MAX_SCALE, GRAPH_MIN_SCALE, positionsForMissingNodes, roundGraphPercentage } from './graphLayout'
import type { GraphEdge, GraphNode, GraphPosition, GraphPositionUpdate, GraphSnapshot, GraphViewport } from './types'

export type GraphSelection = { type: 'node'; node: GraphNode } | { type: 'edge'; edge: GraphEdge }

const DEFAULT_VIEWPORT: GraphViewport = { scale: 1, panX: 0, panY: 0 }

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function readViewport(value: Record<string, unknown>): GraphViewport {
  const scale = Number(value.scale)
  const panX = Number(value.panX)
  const panY = Number(value.panY)
  return {
    scale: Number.isFinite(scale) ? Math.min(GRAPH_MAX_SCALE, Math.max(GRAPH_MIN_SCALE, scale)) : 1,
    panX: Number.isFinite(panX) ? panX : 0,
    panY: Number.isFinite(panY) ? panY : 0
  }
}

function graphPoint(nodeId: string, positions: Map<string, GraphPosition>) {
  const position = positions.get(nodeId) ?? { xPct: 50, yPct: 50 }
  return { x: position.xPct * 10, y: position.yPct * 10 }
}

function shortLabel(value: string, limit = 22) {
  return value.length > limit ? `${value.slice(0, limit)}…` : value
}

function scaleViewport(current: GraphViewport, factor: number): GraphViewport {
  const nextScale = Math.min(GRAPH_MAX_SCALE, Math.max(GRAPH_MIN_SCALE, current.scale * factor))
  const ratio = nextScale / current.scale
  return {
    scale: nextScale,
    panX: 500 - (500 - current.panX) * ratio,
    panY: 500 - (500 - current.panY) * ratio
  }
}

function updatesFromPositions(positions: Map<string, GraphPosition>): GraphPositionUpdate[] {
  return Array.from(positions, ([nodeId, position]) => ({ nodeId, ...position }))
}

export function RelationshipGraphWorkspace({ libraryId, libraryName, onSelectionChange, onOpenKnowledge }: {
  libraryId: string
  libraryName: string
  onSelectionChange: (selection?: GraphSelection) => void
  onOpenKnowledge: () => void
}) {
  const [centerNodeId, setCenterNodeId] = useState('')
  const [depth, setDepth] = useState(1)
  const graph = useQuery({
    queryKey: ['relationship-graph', libraryId, centerNodeId, depth],
    queryFn: () => client.graph(libraryId, '', centerNodeId, depth),
    enabled: Boolean(libraryId),
    placeholderData: (previous) => previous
  })
  const [positions, setPositions] = useState<Map<string, GraphPosition>>(new Map())
  const [viewport, setViewport] = useState<GraphViewport>(DEFAULT_VIEWPORT)
  const [showSources, setShowSources] = useState(true)
  const [relationType, setRelationType] = useState('all')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<GraphSelection>()
  const [saveError, setSaveError] = useState('')
  const [pendingSave, setPendingSave] = useState<GraphPositionUpdate[]>()
  const [undoPositions, setUndoPositions] = useState<GraphPositionUpdate[]>()
  const [initializingWorkspace, setInitializingWorkspace] = useState('')
  const svgRef = useRef<SVGSVGElement>(null)
  const gesture = useRef<
    | { kind: 'node'; pointerId: number; nodeId: string; clientX: number; clientY: number; start: GraphPosition; current: GraphPosition }
    | { kind: 'canvas'; pointerId: number; clientX: number; clientY: number; start: GraphViewport }
    | undefined
  >(undefined)

  const savePositions = useMutation({
    mutationFn: ({ workspaceId, updates }: { workspaceId: string; updates: GraphPositionUpdate[] }) => client.saveGraphPositions(workspaceId, updates),
    onSuccess: (_, variables) => {
      setSaveError('')
      setPendingSave((current) => current === variables.updates ? undefined : current)
    },
    onError: (error, variables) => {
      setPendingSave(variables.updates)
      setSaveError(`位置未保存：${errorMessage(error)}`)
    }
  })

  const autoLayout = useMutation({
    mutationFn: ({ snapshot, overwrite }: { snapshot: GraphSnapshot; overwrite: boolean }) => client.autoLayoutGraph(snapshot.workspace.id, snapshot.workspace.libraryId, overwrite),
    onSuccess: (result) => {
      setPositions(new Map(result.positions.map(({ nodeId, xPct, yPct }) => [nodeId, { xPct, yPct }])))
      setSaveError('')
      void graph.refetch()
    }
  })

  useEffect(() => {
    const snapshot = graph.data
    if (!snapshot) return
    const next = positionsForMissingNodes(snapshot.nodes)
    setPositions(next)
    setViewport(readViewport(snapshot.workspace.viewport))
    const missing = snapshot.nodes.filter((node) => node.position === null).map((node) => ({ nodeId: node.id, ...next.get(node.id)! }))
    if (!missing.length || initializingWorkspace === snapshot.workspace.id) return
    setInitializingWorkspace(snapshot.workspace.id)
    void client.saveGraphPositions(snapshot.workspace.id, missing)
      .then(() => graph.refetch())
      .catch((error) => {
        setPendingSave(missing)
        setSaveError(`初始布局未保存：${errorMessage(error)}`)
      })
  }, [graph.data?.workspace.id, graph.dataUpdatedAt])

  useEffect(() => {
    setCenterNodeId('')
    setDepth(1)
    setSelected(undefined)
    onSelectionChange(undefined)
  }, [libraryId])

  useEffect(() => {
    const workspaceId = graph.data?.workspace.id
    if (!workspaceId) return
    const timer = window.setTimeout(() => {
      void client.updateGraphWorkspace(workspaceId, { filters: { showSources, relationType }, viewport: { ...viewport } }).catch((error) => setSaveError(`工作区偏好未保存：${errorMessage(error)}`))
    }, 500)
    return () => window.clearTimeout(timer)
  }, [graph.data?.workspace.id, showSources, relationType, viewport])

  useEffect(() => {
    const preferences = graph.data?.workspace.filters
    if (!preferences) return
    if (typeof preferences.showSources === 'boolean') setShowSources(preferences.showSources)
    if (typeof preferences.relationType === 'string') setRelationType(preferences.relationType)
  }, [graph.data?.workspace.id])

  const relationTypes = useMemo(() => Array.from(new Set((graph.data?.edges ?? []).map((edge) => edge.relationType))).sort(), [graph.data?.edges])
  const visibleNodes = useMemo(() => (graph.data?.nodes ?? []).filter((node) => showSources || node.kind === 'knowledge'), [graph.data?.nodes, showSources])
  const visibleNodeIds = useMemo(() => new Set(visibleNodes.map((node) => node.id)), [visibleNodes])
  const visibleEdges = useMemo(() => (graph.data?.edges ?? []).filter((edge) => visibleNodeIds.has(edge.source) && visibleNodeIds.has(edge.target) && (relationType === 'all' || edge.relationType === relationType)), [graph.data?.edges, relationType, visibleNodeIds])
  const matchingNodes = useMemo(() => {
    const value = search.trim().toLocaleLowerCase()
    return value ? visibleNodes.filter((node) => `${node.label} ${node.description ?? ''} ${node.tags.join(' ')}`.toLocaleLowerCase().includes(value)) : []
  }, [search, visibleNodes])
  const highlightedIds = useMemo(() => new Set(matchingNodes.map((node) => node.id)), [matchingNodes])

  function choose(selection?: GraphSelection) {
    if (selection?.type === 'node') selection = { type: 'node', node: { ...selection.node, position: positions.get(selection.node.id) ?? selection.node.position } }
    setSelected(selection)
    onSelectionChange(selection)
  }

  function updateViewport(next: GraphViewport) {
    setViewport({
      scale: Math.min(GRAPH_MAX_SCALE, Math.max(GRAPH_MIN_SCALE, next.scale)),
      panX: next.panX,
      panY: next.panY
    })
  }

  function clientDeltaToLogical(deltaX: number, deltaY: number) {
    const rect = svgRef.current?.getBoundingClientRect()
    const unit = rect && Math.min(rect.width, rect.height) > 0 ? 1000 / Math.min(rect.width, rect.height) : 1
    return { x: deltaX * unit, y: deltaY * unit }
  }

  function beginNodeDrag(event: React.PointerEvent<SVGGElement>, node: GraphNode) {
    if (event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    const start = positions.get(node.id) ?? { xPct: 50, yPct: 50 }
    gesture.current = { kind: 'node', pointerId: event.pointerId, nodeId: node.id, clientX: event.clientX, clientY: event.clientY, start, current: start }
    try { svgRef.current?.setPointerCapture(event.pointerId) } catch { /* capture is best effort */ }
    choose({ type: 'node', node })
  }

  function beginCanvasPan(event: React.PointerEvent<SVGSVGElement>) {
    if (event.button !== 0) return
    gesture.current = { kind: 'canvas', pointerId: event.pointerId, clientX: event.clientX, clientY: event.clientY, start: viewport }
    try { event.currentTarget.setPointerCapture(event.pointerId) } catch { /* capture is best effort */ }
    if (event.target === event.currentTarget) choose(undefined)
  }

  function moveGesture(event: React.PointerEvent<SVGSVGElement>) {
    const active = gesture.current
    if (!active || active.pointerId !== event.pointerId) return
    const delta = clientDeltaToLogical(event.clientX - active.clientX, event.clientY - active.clientY)
    if (active.kind === 'canvas') {
      updateViewport({ ...active.start, panX: active.start.panX + delta.x, panY: active.start.panY + delta.y })
      return
    }
    const position = {
      xPct: roundGraphPercentage(active.start.xPct + delta.x / viewport.scale / 10),
      yPct: roundGraphPercentage(active.start.yPct + delta.y / viewport.scale / 10)
    }
    active.current = position
    setPositions((current) => new Map(current).set(active.nodeId, position))
  }

  function endGesture(event: React.PointerEvent<SVGSVGElement>) {
    const active = gesture.current
    if (!active || active.pointerId !== event.pointerId) return
    gesture.current = undefined
    try { event.currentTarget.releasePointerCapture(event.pointerId) } catch { /* capture may already be released */ }
    if (active.kind === 'node' && graph.data) {
      const updates = [{ nodeId: active.nodeId, ...active.current }]
      setPendingSave(updates)
      savePositions.mutate({ workspaceId: graph.data.workspace.id, updates })
      const node = graph.data.nodes.find((item) => item.id === active.nodeId)
      if (node) choose({ type: 'node', node: { ...node, position: active.current } })
    }
  }

  function moveNodeWithKeyboard(node: GraphNode, deltaX: number, deltaY: number) {
    const current = positions.get(node.id) ?? { xPct: 50, yPct: 50 }
    const next = { xPct: roundGraphPercentage(current.xPct + deltaX), yPct: roundGraphPercentage(current.yPct + deltaY) }
    setPositions((value) => new Map(value).set(node.id, next))
    if (graph.data) {
      const updates = [{ nodeId: node.id, ...next }]
      setPendingSave(updates)
      savePositions.mutate({ workspaceId: graph.data.workspace.id, updates })
    }
  }

  function handleNodeKey(event: React.KeyboardEvent<SVGGElement>, node: GraphNode) {
    const step = event.shiftKey ? 1 : 0.25
    const delta = event.key === 'ArrowLeft' ? [-step, 0] : event.key === 'ArrowRight' ? [step, 0] : event.key === 'ArrowUp' ? [0, -step] : event.key === 'ArrowDown' ? [0, step] : undefined
    if (delta) {
      event.preventDefault()
      moveNodeWithKeyboard(node, delta[0], delta[1])
    } else if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      choose({ type: 'node', node })
    }
  }

  function handleCanvasKey(event: React.KeyboardEvent<SVGSVGElement>) {
    if (event.target !== event.currentTarget) return
    const step = event.shiftKey ? 80 : 24
    if (event.key === '+' || event.key === '=') { event.preventDefault(); updateViewport(scaleViewport(viewport, 1.15)) }
    else if (event.key === '-') { event.preventDefault(); updateViewport(scaleViewport(viewport, 1 / 1.15)) }
    else if (event.key === '0') { event.preventDefault(); updateViewport(DEFAULT_VIEWPORT) }
    else if (event.key === 'ArrowLeft') { event.preventDefault(); updateViewport({ ...viewport, panX: viewport.panX + step }) }
    else if (event.key === 'ArrowRight') { event.preventDefault(); updateViewport({ ...viewport, panX: viewport.panX - step }) }
    else if (event.key === 'ArrowUp') { event.preventDefault(); updateViewport({ ...viewport, panY: viewport.panY + step }) }
    else if (event.key === 'ArrowDown') { event.preventDefault(); updateViewport({ ...viewport, panY: viewport.panY - step }) }
  }

  function focusSearchResult() {
    const node = matchingNodes[0]
    if (!node) return
    choose({ type: 'node', node })
    const point = graphPoint(node.id, positions)
    updateViewport({ ...viewport, panX: 500 - point.x * viewport.scale, panY: 500 - point.y * viewport.scale })
    window.setTimeout(() => document.getElementById(`graph-node-${node.id}`)?.focus(), 0)
  }

  function requestRelayout() {
    if (!graph.data?.nodes.length) return
    if (!window.confirm(`将重新排列 ${graph.data.nodes.length} 个节点。此操作可在本会话中撤销，是否继续？`)) return
    setUndoPositions(updatesFromPositions(positions))
    autoLayout.mutate({ snapshot: graph.data, overwrite: true })
  }

  function undoRelayout() {
    if (!graph.data || !undoPositions) return
    const updates = undoPositions
    setPositions(new Map(updates.map(({ nodeId, xPct, yPct }) => [nodeId, { xPct, yPct }])))
    setUndoPositions(undefined)
    savePositions.mutate({ workspaceId: graph.data.workspace.id, updates })
  }

  if (!libraryId) return <section className="graph-empty"><Network size={30}/><h1>请选择知识库</h1><p>关系图谱按知识库隔离，不会跨库混合数据。</p></section>

  const snapshot = graph.data
  const transform = `translate(${viewport.panX} ${viewport.panY}) scale(${viewport.scale})`
  return <section className="graph-workspace" aria-label={`${libraryName} 关系图谱`}>
    <header className="graph-header">
      <div><span className="eyebrow">关系图谱 · 仅稳定知识</span><h1>{libraryName || '当前知识库'}</h1><p>{snapshot ? `${visibleNodes.length} 个节点 · ${visibleEdges.length} 条关系 · ${snapshot.workspace.name}` : '正在读取可审计关系'}</p></div>
    </header>
    <div className="graph-filterbar">
      <label className="graph-search"><Search size={16}/><span className="sr-only">搜索节点</span><input value={search} onChange={(event) => setSearch(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') focusSearchResult() }} placeholder="搜索节点，Enter 定位"/></label>
      <label><span>关系类型</span><select value={relationType} onChange={(event) => setRelationType(event.target.value)}><option value="all">全部关系</option>{relationTypes.map((type) => <option key={type} value={type}>{type}</option>)}</select></label>
      <label className="graph-switch"><input type="checkbox" checked={showSources} onChange={(event) => setShowSources(event.target.checked)}/><span>显示来源节点</span></label>
      {search.trim() && <span role="status">匹配 {matchingNodes.length} 个节点</span>}
    </div>
    <div className="graph-notices">
      {(graph.error || autoLayout.error) && <div className="graph-notice error" role="alert"><CircleAlert size={17}/><span>{errorMessage(graph.error || autoLayout.error)}</span><button type="button" className="text-button" onClick={() => void graph.refetch()}>重试</button></div>}
      {saveError && <div className="graph-notice warning" role="alert"><CircleAlert size={17}/><span>{saveError}</span>{pendingSave && snapshot && <button type="button" className="text-button" onClick={() => savePositions.mutate({ workspaceId: snapshot.workspace.id, updates: pendingSave })}>重试保存</button>}</div>}
      {snapshot?.truncated && <div className="graph-notice" role="status">仅显示 {snapshot.nodes.length} 个节点，另有 {snapshot.hiddenNodeCount} 个节点未载入。请从节点缩小范围或调整筛选。</div>}
    </div>
    <div className="graph-stage">
      {graph.isLoading && !snapshot && <div className="graph-loading"><LoaderCircle className="spin" size={22}/>正在构建关系图谱…</div>}
      {snapshot && snapshot.nodes.length === 0 && <div className="graph-empty"><BookOpen size={30}/><h2>还没有可显示的稳定知识</h2><p>来源资料经审核并发布后，才会进入普通关系图谱。</p><button type="button" className="button primary" onClick={onOpenKnowledge}>前往知识列表</button></div>}
      {snapshot && snapshot.nodes.length > 0 && <>
        <svg ref={svgRef} className="graph-canvas" viewBox="0 0 1000 1000" tabIndex={0} role="application" aria-label="关系图谱画布。滚轮缩放，拖拽平移，方向键平移，加减号缩放，0 重置；Tab 可逐个访问节点。" onPointerDown={beginCanvasPan} onPointerMove={moveGesture} onPointerUp={endGesture} onPointerCancel={endGesture} onKeyDown={handleCanvasKey} onWheel={(event) => { event.preventDefault(); updateViewport(scaleViewport(viewport, event.deltaY < 0 ? 1.1 : 1 / 1.1)) }}>
          <defs><marker id="graph-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z"/></marker></defs>
          <g className="graph-viewport" transform={transform}>
            {visibleEdges.map((edge) => {
              const source = graphPoint(edge.source, positions); const target = graphPoint(edge.target, positions)
              const active = selected?.type === 'edge' && selected.edge.id === edge.id
              return <g key={edge.id} className={`graph-edge graph-edge-${edge.kind} ${active ? 'selected' : ''}`} role="button" tabIndex={0} aria-label={`${edge.relationType}：${edge.source} 到 ${edge.target}`} onPointerDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); choose({ type: 'edge', edge }) }} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); choose({ type: 'edge', edge }) } }}>
                <title>{edge.relationType}：{edge.source} → {edge.target}</title>
                <path className="graph-edge-hit" d={`M ${source.x} ${source.y} L ${target.x} ${target.y}`}/><path className="graph-edge-line" d={`M ${source.x} ${source.y} L ${target.x} ${target.y}`} markerEnd="url(#graph-arrow)"/>
              </g>
            })}
            {visibleNodes.map((node) => {
              const point = graphPoint(node.id, positions)
              const active = selected?.type === 'node' && selected.node.id === node.id
              const dimmed = Boolean(search.trim()) && !highlightedIds.has(node.id)
              return <g id={`graph-node-${node.id}`} key={node.id} className={`graph-node graph-node-${node.kind} ${active ? 'selected' : ''} ${dimmed ? 'dimmed' : ''}`} transform={`translate(${point.x} ${point.y})`} role="button" tabIndex={0} aria-label={`${node.kind === 'knowledge' ? '知识' : '来源'}：${node.label}，位置 ${positions.get(node.id)?.xPct ?? 50}%，${positions.get(node.id)?.yPct ?? 50}%`} onPointerDown={(event) => beginNodeDrag(event, node)} onDoubleClick={() => choose({ type: 'node', node })} onKeyDown={(event) => handleNodeKey(event, node)}>
                <circle r={node.kind === 'knowledge' ? 27 : 20}/><text y={node.kind === 'knowledge' ? 47 : 39} textAnchor="middle">{shortLabel(node.label)}</text>
              </g>
            })}
          </g>
        </svg>
        <aside className="graph-legend" aria-label="图谱图例"><strong>图例</strong><span><i className="knowledge"/>稳定知识</span><span><i className="source"/>来源</span><span><i className="line"/>明确关系与方向</span></aside>
        <button type="button" className="graph-minimap" aria-label="迷你地图，点击回到图谱中心" onClick={() => updateViewport(DEFAULT_VIEWPORT)}>
          <svg viewBox="0 0 100 100" aria-hidden="true">{visibleEdges.map((edge) => { const a = positions.get(edge.source); const b = positions.get(edge.target); return a && b ? <line key={edge.id} x1={a.xPct} y1={a.yPct} x2={b.xPct} y2={b.yPct}/> : null })}{visibleNodes.map((node) => { const point = positions.get(node.id); return point ? <circle key={node.id} cx={point.xPct} cy={point.yPct} r="1.7"/> : null })}</svg>
        </button>
        {savePositions.isPending && <div className="graph-save-state" role="status"><LoaderCircle className="spin" size={14}/>正在保存位置…</div>}
        {!visibleEdges.length && <div className="graph-edge-empty" role="status">当前节点尚无符合筛选的明确关系或来源引用。</div>}
      </>}
    </div>
    <footer className="graph-footer" aria-label="图谱操作与节点列表">
      <div className="graph-toolbar" role="toolbar" aria-label="图谱视图工具">
        <button type="button" className="icon-button" aria-label="缩小图谱" title="缩小 (-)" onClick={() => updateViewport(scaleViewport(viewport, 1 / 1.15))}><Minus size={17}/></button>
        <output aria-label="当前缩放比例">{Math.round(viewport.scale * 100)}%</output>
        <button type="button" className="icon-button" aria-label="放大图谱" title="放大 (+)" onClick={() => updateViewport(scaleViewport(viewport, 1.15))}><Plus size={17}/></button>
        <button type="button" className="button secondary" onClick={() => updateViewport(fitGraphToViewport(positions.values(), 1000, 1000, 120))}><Focus size={16}/>适应画布</button>
        <button type="button" className="button secondary" onClick={() => updateViewport(DEFAULT_VIEWPORT)}><RotateCcw size={16}/>重置视口</button>
        {undoPositions && <button type="button" className="button secondary" onClick={undoRelayout} disabled={savePositions.isPending}><Undo2 size={16}/>撤销布局</button>}
        {selected?.type === 'node' && <button type="button" className="button secondary" onClick={() => { setCenterNodeId(selected.node.id); setDepth(1) }}><Focus size={16}/>聚焦节点</button>}
        {centerNodeId && depth < 2 && <button type="button" className="button secondary" onClick={() => setDepth(2)}><Plus size={16}/>展开一层</button>}
        {centerNodeId && <button type="button" className="button secondary" onClick={() => { setCenterNodeId(''); setDepth(1) }}>显示全图</button>}
        <button type="button" className="button secondary" onClick={requestRelayout} disabled={!snapshot?.nodes.length || autoLayout.isPending}><Redo2 size={16}/>{autoLayout.isPending ? '布局中…' : '重新布局'}</button>
        <span className="graph-gesture-hint">滚轮缩放 · 拖拽平移 · Tab 选择节点</span>
      </div>
      <div className="graph-node-list" role="region" aria-label="可访问节点列表"><strong>节点列表</strong>{visibleNodes.map((node) => <button type="button" key={node.id} onClick={() => choose({ type: 'node', node })}>{node.kind === 'knowledge' ? <BookOpen size={14}/> : <FileText size={14}/>}<span>{node.label}</span></button>)}</div>
    </footer>
  </section>
}

export function RelationshipGraphInspector({ selection, onOpenKnowledge, onOpenDocument }: {
  selection?: GraphSelection
  onOpenKnowledge: (uri: string, revision?: number) => void
  onOpenDocument: (id: string) => void
}) {
  if (!selection) return <div className="detail-empty"><Network size={28}/><h2>选择图元素</h2><p>选择节点或关系，可查看版本、证据与固定的百分比布局信息。</p></div>
  if (selection.type === 'edge') {
    const edge = selection.edge
    return <article className="graph-inspector"><span className="eyebrow">关系详情</span><h2>{edge.relationType || edge.kind}</h2><dl><dt>类型</dt><dd>{edge.kind}</dd><dt>方向</dt><dd>{edge.source}<br/>→ {edge.target}</dd>{edge.targetRevision && <><dt>目标修订</dt><dd>r{edge.targetRevision}</dd></>}{edge.sourceId && <><dt>来源 ID</dt><dd>{edge.sourceId}</dd></>}</dl>{edge.locator && <><h3>证据位置</h3><pre>{JSON.stringify(edge.locator, null, 2)}</pre></>}</article>
  }
  const node = selection.node
  const isDocument = node.kind === 'document' && node.uri.startsWith('kah://document/')
  return <article className="graph-inspector"><span className="eyebrow">{node.kind === 'knowledge' ? '稳定知识节点' : '来源节点'}</span><h2>{node.label}</h2>{node.description && <p>{node.description}</p>}<dl><dt>节点 URI</dt><dd>{node.uri}</dd>{node.position && <><dt>逻辑位置</dt><dd>({node.position.xPct}%, {node.position.yPct}%)</dd></>}{node.revision && <><dt>修订</dt><dd>r{node.revision}</dd></>}{node.trust && <><dt>信任状态</dt><dd>{node.trust}</dd></>}{node.status && <><dt>状态</dt><dd>{node.status}</dd></>}</dl>{node.tags.length > 0 && <div className="workspace-tags">{node.tags.map((tag) => <span className="tag" key={tag}>{tag}</span>)}</div>}<div className="detail-actions">{node.kind === 'knowledge' && <button type="button" className="button primary" onClick={() => onOpenKnowledge(node.uri, node.revision)}><BookOpen size={16}/>打开知识</button>}{isDocument && <button type="button" className="button primary" onClick={() => onOpenDocument(node.uri.slice('kah://document/'.length))}><FileText size={16}/>打开来源文档</button>}</div></article>
}
