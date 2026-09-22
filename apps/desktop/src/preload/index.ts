import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('kah', {
  runtimeConfig: () => ipcRenderer.invoke('runtime:config'),
  setTitleBarTheme: (theme: 'light' | 'dark') => ipcRenderer.invoke('window:set-titlebar-theme', theme),
  selectFiles: (kind?: string) => ipcRenderer.invoke('dialog:select-files', kind),
  selectDirectory: () => ipcRenderer.invoke('dialog:select-directory'),
  saveFile: (defaultPath: string, data: string | Uint8Array) => ipcRenderer.invoke('dialog:save-file', { defaultPath, data }),
  selectBackupCandidate: () => ipcRenderer.invoke('backup:select-candidate'),
  restoreBackup: (path: string) => ipcRenderer.invoke('backup:restore', path),
  openPath: (path: string) => ipcRenderer.invoke('shell:open-path', path),
  openExternal: (url: string) => ipcRenderer.invoke('shell:open-external', url),
  onBackendExit: (callback: (code: number | null) => void) => {
    const listener = (_event: Electron.IpcRendererEvent, code: number | null) => callback(code)
    ipcRenderer.on('runtime:backend-exit', listener)
    return () => ipcRenderer.removeListener('runtime:backend-exit', listener)
  }
})
