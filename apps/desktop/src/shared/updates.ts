export type ApplicationMode = 'installed' | 'portable' | 'development' | 'unpacked'
export type UpdateStatus = 'disabled' | 'idle' | 'checking' | 'not-available' | 'available' | 'downloading' | 'ready' | 'installing' | 'error'
export interface UpdateState {
  enabled: boolean
  status: UpdateStatus
  currentVersion: string
  targetVersion?: string
  releaseNotes?: string
  lastCheckAt?: string
  progress?: { percent: number; transferred: number; total: number; bytesPerSecond: number }
  error?: string
  canInstall?: boolean
}
export interface UpdatesBridge {
  getState(): Promise<UpdateState>
  check(): Promise<UpdateState>
  download(): Promise<UpdateState>
  install(): Promise<UpdateState>
  onStateChanged(callback: (state: UpdateState) => void): () => void
}
