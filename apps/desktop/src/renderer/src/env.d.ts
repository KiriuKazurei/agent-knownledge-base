interface RuntimeConfig { apiBaseUrl: string; desktopToken: string; dataRoot: string; version: string; applicationMode: import('../../shared/updates').ApplicationMode; updatesEnabled: boolean }
interface Window {
  kah: {
    updates: import('../../shared/updates').UpdatesBridge
    runtimeConfig(): Promise<RuntimeConfig>
    setTitleBarTheme(theme: 'light' | 'dark'): Promise<void>
    selectFiles(kind?: string): Promise<string[]>
    selectDirectory(): Promise<string>
    saveFile(defaultPath: string, data: string | Uint8Array): Promise<string>
    selectBackupCandidate(): Promise<string>
    restoreBackup(path: string): Promise<{ restarted: true }>
    openPath(path: string): Promise<string>
    openExternal(url: string): Promise<void>
    onBackendExit(callback: (code: number | null) => void): () => void
  }
}
