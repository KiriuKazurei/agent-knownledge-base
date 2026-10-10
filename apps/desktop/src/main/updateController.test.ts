// @vitest-environment node
import { afterEach, describe, expect, test, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import { applicationMode, newerStable, UpdateController } from './updateController'

function fixture(mode: 'installed' | 'portable' | 'development' | 'unpacked' = 'installed') {
  const engine = Object.assign(new EventEmitter(), {
    autoDownload: true, autoInstallOnAppQuit: true, allowPrerelease: true, allowDowngrade: true,
    checkForUpdates: vi.fn(async () => ({ updateInfo: { version: '0.1.3', releaseNotes: '修复' } })),
    downloadUpdate: vi.fn(async () => { engine.emit('update-downloaded', { version: '0.1.3' }); return [] }),
    quitAndInstall: vi.fn()
  })
  const prepareInstall = vi.fn(async () => {})
  const emit = vi.fn()
  const controller = new UpdateController({ mode, version: '0.1.2', updater: engine, prepareInstall, emit })
  return { engine, prepareInstall, controller, emit }
}
afterEach(() => vi.useRealTimers())

describe('installed update boundary', () => {
  test.each(['portable', 'development', 'unpacked'] as const)('%s does not initialize or use updater', async (mode) => {
    vi.useFakeTimers()
    const { controller, engine, prepareInstall } = fixture(mode)
    controller.start()
    await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000)
    await controller.check(); await controller.download(); await controller.install()
    expect(engine.eventNames()).toEqual([])
    expect(engine.checkForUpdates).not.toHaveBeenCalled()
    expect(engine.downloadUpdate).not.toHaveBeenCalled()
    expect(prepareInstall).not.toHaveBeenCalled()
    expect(controller.getState().status).toBe('disabled')
  })
  test('portable indicators override installed marker, unknown marker fails closed', () => {
    const input = { packaged: true, platform: 'win32', portableFlag: false, installationMarker: { mode: 'installed' } }
    expect(applicationMode(input)).toBe('installed')
    expect(applicationMode({ ...input, portableDirectory: 'E:/portable' })).toBe('portable')
    expect(applicationMode({ ...input, portableEnvironment: '1' })).toBe('portable')
    expect(applicationMode({ ...input, portableFlag: true })).toBe('portable')
    expect(applicationMode({ ...input, installationMarker: null })).toBe('unpacked')
    expect(applicationMode({ ...input, installationMarker: 'installed' })).toBe('unpacked')
    expect(applicationMode({ ...input, packaged: false })).toBe('development')
    expect(applicationMode({ ...input, platform: 'linux' })).toBe('unpacked')
  })
  test.each(['0.1.2', '0.1.1', '0.2.0-beta.1', 'v0.2.0', 'invalid', '01.2.3'])('rejects %s', (version) => expect(newerStable(version, '0.1.2')).toBe(false))
  test('semver numeric comparison', () => expect(newerStable('0.10.0', '0.9.9')).toBe(true))
})

describe('update lifecycle', () => {
  test('checks after 30s and every 6h; merged requests, explicit install only', async () => {
    vi.useFakeTimers()
    const { controller, engine, prepareInstall } = fixture()
    expect(engine.autoInstallOnAppQuit).toBe(false)
    expect(engine.allowPrerelease).toBe(false)
    controller.start(); controller.start()
    await vi.advanceTimersByTimeAsync(29_999)
    expect(engine.checkForUpdates).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(controller.getState().status).toBe('ready')
    expect(engine.quitAndInstall).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000)
    expect(engine.checkForUpdates).toHaveBeenCalledTimes(1) // retain ready package
    await Promise.all([controller.install(), controller.install()])
    expect(prepareInstall).toHaveBeenCalledTimes(1)
    expect(engine.quitAndInstall).toHaveBeenCalledExactlyOnceWith(true, true)
    controller.dispose()
  })
  test('periodic no-update checks continue and repeat check requests merge', async () => {
    vi.useFakeTimers()
    const { controller, engine } = fixture()
    engine.checkForUpdates.mockResolvedValue({ updateInfo: { version: '0.1.2', releaseNotes: '' } })
    controller.start()
    await vi.advanceTimersByTimeAsync(30_000)
    await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000)
    expect(engine.checkForUpdates).toHaveBeenCalledTimes(2)
    await Promise.all([controller.check(), controller.check()])
    expect(engine.checkForUpdates).toHaveBeenCalledTimes(3)
    expect(engine.downloadUpdate).not.toHaveBeenCalled()
    controller.dispose()
  })
  test('offline failure does not install and manual retry works', async () => {
    const { controller, engine } = fixture()
    engine.checkForUpdates.mockRejectedValueOnce(new Error('offline'))
    expect((await controller.check()).error).toBe('offline')
    expect((await controller.check()).status).toBe('ready')
    expect(engine.quitAndInstall).not.toHaveBeenCalled()
  })
  test('hash/metadata failure never becomes installable', async () => {
    const { controller, engine, prepareInstall } = fixture()
    engine.downloadUpdate.mockRejectedValue(new Error('SHA-512 mismatch'))
    expect((await controller.check()).status).toBe('error')
    await controller.install()
    expect(prepareInstall).not.toHaveBeenCalled()
    expect(engine.quitAndInstall).not.toHaveBeenCalled()
  })
  test('download resolving without verified event cannot install', async () => {
    const { controller, engine } = fixture()
    engine.downloadUpdate.mockResolvedValue([])
    expect((await controller.check()).canInstall).toBe(false)
    await controller.install()
    expect(engine.quitAndInstall).not.toHaveBeenCalled()
  })
  test.each(['后台忙碌', '20 秒超时', '权限错误'])('preflight %s preserves old program and permits retry', async (message) => {
    const { controller, engine, prepareInstall } = fixture()
    await controller.check()
    prepareInstall.mockRejectedValueOnce(new Error(message))
    expect((await controller.install()).error).toBe(message)
    expect(engine.quitAndInstall).not.toHaveBeenCalled()
    expect(controller.getState().canInstall).toBe(true)
    await controller.install()
    expect(engine.quitAndInstall).toHaveBeenCalledTimes(1)
  })
  test('installer failure after shutdown is reported and never enables quit-time installation', async () => {
    const { controller, engine } = fixture()
    await controller.check()
    engine.quitAndInstall.mockImplementation(() => { engine.emit('error', new Error('EACCES: installer launch denied')) })
    const state = await controller.install()
    expect(state.status).toBe('error')
    expect(state.error).toContain('EACCES')
    expect(state.canInstall).toBe(true)
    expect(engine.autoInstallOnAppQuit).toBe(false)
  })
})
