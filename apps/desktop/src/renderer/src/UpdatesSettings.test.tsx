import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { UpdateNotice, UpdatesSettings, useDesktopUpdates } from './UpdatesSettings'
import type { UpdateState } from '../../shared/updates'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
const ready: UpdateState = { enabled: true, status: 'ready', currentVersion: '0.1.2', targetVersion: '0.1.3', canInstall: true }
test('later dismisses only the notice and never installs; new version reappears', () => {
  const install = vi.fn()
  const view = render(<UpdateNotice state={ready} onInstall={install}/>)
  fireEvent.click(screen.getByRole('button', { name: '稍后' }))
  expect(screen.queryByRole('button', { name: '重启并更新' })).not.toBeInTheDocument()
  expect(install).not.toHaveBeenCalled()
  view.rerender(<UpdateNotice state={{ ...ready, targetVersion: '0.1.4' }} onInstall={install}/>)
  fireEvent.click(screen.getByRole('button', { name: '重启并更新' }))
  expect(install).toHaveBeenCalledTimes(1)
})
test('disabled modes have no global update notice', () => {
  render(<UpdateNotice state={{ ...ready, enabled: false, status: 'disabled' }} onInstall={vi.fn()}/>)
  expect(screen.queryByLabelText('更新已就绪')).not.toBeInTheDocument()
})
test('failed install can be retried and release notes are rendered as text', () => {
  const install = vi.fn()
  render(<UpdatesSettings state={{ ...ready, status: 'error', error: '后台任务尚未完成', releaseNotes: '<script>invalid()</script>' }} onInstall={install}/>)
  expect(screen.getByRole('alert')).toHaveTextContent('后台任务尚未完成')
  expect(document.querySelector('script')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '重启并更新' }))
  expect(install).toHaveBeenCalledTimes(1)
})
test('a delayed initial snapshot cannot overwrite a newer event; unsubscribes', async () => {
  let resolve!: (state: UpdateState) => void
  let notify!: (state: UpdateState) => void
  const unsubscribe = vi.fn()
  Object.defineProperty(window, 'kah', { configurable: true, value: { updates: {
    getState: () => new Promise<UpdateState>(r => { resolve = r }),
    onStateChanged: (callback: (state: UpdateState) => void) => { notify = callback; return unsubscribe }
  } } })
  function Host() { return <span>{useDesktopUpdates().status}</span> }
  const view = render(<Host/>)
  act(() => notify(ready))
  await act(async () => resolve({ ...ready, status: 'idle' }))
  expect(screen.getByText('ready')).toBeInTheDocument()
  view.unmount()
  expect(unsubscribe).toHaveBeenCalledTimes(1)
})
