// @vitest-environment node
import { beforeEach, describe, expect, test, vi } from 'vitest'

const expose = vi.fn()
const invoke = vi.fn(async () => '')
const on = vi.fn()
const removeListener = vi.fn()

vi.mock('electron', () => ({
  contextBridge: { exposeInMainWorld: expose },
  ipcRenderer: { invoke, on, removeListener }
}))

describe('desktop preload bridge', () => {
  beforeEach(() => { vi.resetModules(); expose.mockClear(); invoke.mockClear() })

  test('keeps directory selection and save dialog behind explicit IPC methods', async () => {
    await import('./index')
    expect(expose).toHaveBeenCalledWith('kah', expect.any(Object))
    const bridge = expose.mock.calls.at(-1)?.[1] as { selectDirectory: () => Promise<unknown>; saveFile: (path: string, data: Uint8Array) => Promise<unknown> }
    await bridge.selectDirectory()
    await bridge.saveFile('architecture.svg', new Uint8Array([1, 2, 3]))
    expect(invoke).toHaveBeenNthCalledWith(1, 'dialog:select-directory')
    expect(invoke).toHaveBeenNthCalledWith(2, 'dialog:save-file', { defaultPath: 'architecture.svg', data: new Uint8Array([1, 2, 3]) })
  })
})
