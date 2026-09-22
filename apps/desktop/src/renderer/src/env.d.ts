interface RuntimeConfig { apiBaseUrl: string; desktopToken: string; dataRoot: string; version: string }
interface Window {
  kah: {
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
