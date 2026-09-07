import { describe, expect, test } from 'vitest'
import { deterministicGraphPosition, fitGraphToViewport, logicalToScreen, percentageToLogical, positionsForMissingNodes, roundGraphPercentage, screenToPercentage } from './graphLayout'
import type { GraphNode, GraphPosition, GraphViewport } from './types'

describe('relationship graph coordinate model', () => {
  test('retains fractional percentage coordinates through viewport transforms', () => {
    const position: GraphPosition = { xPct: 37.625, yPct: 14.083 }
    const viewport: GraphViewport = { scale: 1.75, panX: -143.4, panY: 86.25 }
    const screen = logicalToScreen(percentageToLogical(position), viewport)
    expect(screenToPercentage(screen, viewport)).toEqual(position)
  })

  test('canvas pixel size changes do not alter relative node positions', () => {
    const position: GraphPosition = { xPct: 62.375, yPct: 40.125 }
    const first = { x: 800 * position.xPct / 100, y: 600 * position.yPct / 100 }
    const second = { x: 1500 * position.xPct / 100, y: 900 * position.yPct / 100 }
    expect(first.x / 800).toBeCloseTo(second.x / 1500)
    expect(first.y / 600).toBeCloseTo(second.y / 900)
  })

  test('clamps and rounds only when persisting percentages', () => {
    expect(roundGraphPercentage(37.6254)).toBe(37.625)
    expect(roundGraphPercentage(-0.1)).toBe(0)
    expect(roundGraphPercentage(100.1)).toBe(100)
  })

  test('generates deterministic positions only for nodes without saved positions', () => {
    const saved = { xPct: 20.125, yPct: 80.875 }
    const nodes = [
      { id: 'saved', position: saved },
      { id: 'missing', position: null }
    ] as GraphNode[]
    const positions = positionsForMissingNodes(nodes)
    expect(positions.get('saved')).toBe(saved)
    expect(positions.get('missing')).toEqual(deterministicGraphPosition('missing'))
    expect(deterministicGraphPosition('missing')).toEqual(deterministicGraphPosition('missing'))
  })

  test('keeps deterministic layout seeds aligned with the Go service', () => {
    expect(deterministicGraphPosition('kah://knowledge/abc?revision=2')).toEqual({ xPct: 73.236, yPct: 67.57 })
  })

  test('deterministically separates newly placed nodes that would overlap', () => {
    const candidates = Array.from({ length: 500 }, (_, index) => ({ id: `candidate-${index}`, position: null })) as GraphNode[]
    let pair: GraphNode[] = []
    for (let left = 0; left < candidates.length && pair.length === 0; left += 1) {
      const a = deterministicGraphPosition(candidates[left].id)
      for (let right = left + 1; right < candidates.length; right += 1) {
        const b = deterministicGraphPosition(candidates[right].id)
        if (Math.hypot(a.xPct - b.xPct, a.yPct - b.yPct) < 12) { pair = [candidates[left], candidates[right]]; break }
      }
    }
    expect(pair).toHaveLength(2)
    const placed = Array.from(positionsForMissingNodes(pair).values())
    expect(Math.hypot(placed[0].xPct - placed[1].xPct, placed[0].yPct - placed[1].yPct)).toBeGreaterThanOrEqual(12)
  })

  test('fits positions by changing only the viewport', () => {
    const positions = [{ xPct: 10.125, yPct: 20.5 }, { xPct: 80.75, yPct: 90.125 }]
    const snapshot = structuredClone(positions)
    const viewport = fitGraphToViewport(positions, 1200, 700)
    expect(viewport.scale).toBeGreaterThan(0)
    expect(positions).toEqual(snapshot)
  })
})
