import type { BrowserWindow } from 'electron'

type WindowVisibilityTarget = Pick<BrowserWindow, 'isDestroyed' | 'isVisible' | 'once' | 'show' | 'webContents'>

export function showWindowWhenReady(window: WindowVisibilityTarget): void {
  let showHandled = false

  const showOnce = (): void => {
    if (showHandled || window.isDestroyed()) return
    showHandled = true
    if (!window.isVisible()) window.show()
  }

  window.once('ready-to-show', showOnce)
  window.webContents.on('did-finish-load', showOnce)
}
