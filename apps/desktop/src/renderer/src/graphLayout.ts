import type { GraphNode, GraphPosition, GraphViewport } from './types'

export const GRAPH_LOGICAL_WIDTH = 1000
export const GRAPH_LOGICAL_HEIGHT = 1000
export const GRAPH_MIN_SCALE = 0.35
export const GRAPH_MAX_SCALE = 3

export function roundGraphPercentage(value: number): number {
  return Math.round(Math.min(100, Math.max(0, value)) * 1000) / 1000
}

export function percentageToLogical(position: GraphPosition, logicalWidth = GRAPH_LOGICAL_WIDTH, logicalHeight = GRAPH_LOGICAL_HEIGHT) {
  return { x: logicalWidth * position.xPct / 100, y: logicalHeight * position.yPct / 100 }
}

export function logicalToScreen(point: { x: number; y: number }, viewport: GraphViewport) {
  return { x: point.x * viewport.scale + viewport.panX, y: point.y * viewport.scale + viewport.panY }
}

export function screenToPercentage(point: { x: number; y: number }, viewport: GraphViewport, logicalWidth = GRAPH_LOGICAL_WIDTH, logicalHeight = GRAPH_LOGICAL_HEIGHT): GraphPosition {
  const scale = Number.isFinite(viewport.scale) && viewport.scale > 0 ? viewport.scale : 1
  return {
    xPct: roundGraphPercentage(((point.x - viewport.panX) / scale) / logicalWidth * 100),
    yPct: roundGraphPercentage(((point.y - viewport.panY) / scale) / logicalHeight * 100)
  }
}

function stableHash(value: string, seed: number): number {
  let hash = seed >>> 0
  for (const byte of new TextEncoder().encode(value)) {
    hash ^= byte
    hash = Math.imul(hash, 16777619)
  }
  return hash >>> 0
}

export function deterministicGraphPosition(nodeId: string): GraphPosition {
  const denominator = 0xffffffff
  return {
    xPct: roundGraphPercentage(8 + 84 * stableHash(nodeId, 2166136261) / denominator),
    yPct: roundGraphPercentage(8 + 84 * stableHash(nodeId, 3339675911) / denominator)
  }
}

function positionsOverlap(left: GraphPosition, right: GraphPosition, minimumDistance: number) {
  const x = left.xPct - right.xPct
  const y = left.yPct - right.yPct
  return x * x + y * y < minimumDistance * minimumDistance
}

function resolveGraphCollision(base: GraphPosition, occupied: GraphPosition[], minimumDistance: number): GraphPosition {
  let candidate = base
  for (let attempt = 1; attempt <= 64 && occupied.some((position) => positionsOverlap(candidate, position, minimumDistance)); attempt += 1) {
    candidate = {
      xPct: roundGraphPercentage(8 + ((base.xPct - 8 + attempt * 17.371) % 84)),
      yPct: roundGraphPercentage(8 + ((base.yPct - 8 + attempt * 29.117) % 84))
    }
  }
  return candidate
}

export function positionsForMissingNodes(nodes: GraphNode[]): Map<string, GraphPosition> {
  const result = new Map(nodes.filter((node) => node.position).map((node) => [node.id, node.position!]))
  const occupied = Array.from(result.values())
  const minimumDistance = Math.max(3, Math.min(12, 50 / Math.sqrt(Math.max(nodes.length, 1))))
  for (const node of nodes.filter((item) => !item.position).sort((left, right) => left.id.localeCompare(right.id))) {
    const position = resolveGraphCollision(deterministicGraphPosition(node.id), occupied, minimumDistance)
    result.set(node.id, position)
    occupied.push(position)
  }
  return result
}

export function fitGraphToViewport(positions: Iterable<GraphPosition>, width: number, height: number, padding = 80): GraphViewport {
  const logical = Array.from(positions, (position) => percentageToLogical(position))
  if (!logical.length || width <= 0 || height <= 0) return { scale: 1, panX: 0, panY: 0 }
  const xs = logical.map((point) => point.x)
  const ys = logical.map((point) => point.y)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  const contentWidth = Math.max(maxX - minX, 1)
  const contentHeight = Math.max(maxY - minY, 1)
  const scale = Math.min(GRAPH_MAX_SCALE, Math.max(GRAPH_MIN_SCALE, Math.min(Math.max(width - padding * 2, 1) / contentWidth, Math.max(height - padding * 2, 1) / contentHeight)))
  return {
    scale,
    panX: width / 2 - ((minX + maxX) / 2) * scale,
    panY: height / 2 - ((minY + maxY) / 2) * scale
  }
}
