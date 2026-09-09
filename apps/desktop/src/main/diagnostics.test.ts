// @vitest-environment node
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { expect, test } from 'vitest'
import { installDiagnostics } from './diagnostics'

test('persists error stacks, redacts credentials and rotates the previous log', () => {
  const directory = mkdtempSync(path.join(tmpdir(), 'kah-diagnostics-'))
  const original = { info: console.info, warn: console.warn, error: console.error }
  try {
    console.info = console.warn = console.error = () => {}
    const file = path.join(directory, 'desktop.log')
    writeFileSync(file, 'x'.repeat(5 * 1024 * 1024))
    installDiagnostics(directory)
    console.error('Bearer desktop_abcdefghijklmnopqrstuvwxyz123456', new Error('diagnostic fixture'))
    const text = readFileSync(file, 'utf8')
    const entry = JSON.parse(text)
    expect(entry.level).toBe('error')
    expect(entry.message).toContain('Error: diagnostic fixture')
    expect(text).not.toContain('abcdefghijklmnopqrstuvwxyz123456')
    expect(readFileSync(file + '.1', 'utf8').length).toBe(5 * 1024 * 1024)
  } finally { Object.assign(console, original); rmSync(directory, { recursive: true, force: true }) }
})
