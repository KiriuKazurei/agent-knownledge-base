// @vitest-environment node
import { expect, test } from 'vitest'
import path from 'node:path'
import { installedDataRoot } from './runtimePaths'

test('keeps persistent data separate from directories replaced by install or restore', () => {
  const installation = path.resolve('acceptance/program')
  const sibling = path.resolve('acceptance/data')
  expect(installedDataRoot(sibling, installation)).toBe(sibling)
  for (const unsafe of [installation, path.join(installation, 'data'), path.dirname(installation), 'relative-data']) {
    expect(() => installedDataRoot(unsafe, installation)).toThrow()
  }
})
