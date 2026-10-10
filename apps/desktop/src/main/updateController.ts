import type { ApplicationMode, UpdateState } from '../shared/updates'

export interface UpdateInfo { version: string; releaseNotes?: string | Array<{ version: string; note: string | null }> | null }
export interface UpdaterAdapter {
  autoDownload: boolean
  autoInstallOnAppQuit: boolean
  allowPrerelease: boolean
  allowDowngrade: boolean
  on(event: string, listener: (...args: any[]) => void): unknown
  checkForUpdates(): Promise<{ updateInfo: UpdateInfo } | null>
  downloadUpdate(): Promise<unknown>
  quitAndInstall(isSilent?: boolean, isForceRunAfter?: boolean): void
}

/** The mode gate runs before importing electron-updater, not just before HTTP. */
export function applicationMode(input: { packaged: boolean; platform: string; portableDirectory?: string; portableEnvironment?: string; portableFlag: boolean; installationMarker: unknown }): ApplicationMode {
  if (!input.packaged) return 'development'
  if (input.portableDirectory?.trim() || input.portableEnvironment === '1' || input.portableFlag) return 'portable'
  const marker = input.installationMarker as { mode?: unknown } | null
  return input.platform === 'win32' && marker?.mode === 'installed' ? 'installed' : 'unpacked'
}

export function newerStable(candidate: string, current: string): boolean {
  const parse = (value: string) => /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\+[\w.-]+)?$/.exec(value)
  const next = parse(candidate), old = parse(current)
  if (!next || !old) return false
  for (let i = 1; i <= 3; i++) { if (Number(next[i]) !== Number(old[i])) return Number(next[i]) > Number(old[i]) }
  return false
}

export class UpdateController {
  private state: UpdateState
  private checking?: Promise<UpdateState>
  private downloading?: Promise<UpdateState>
  private installing?: Promise<UpdateState>
  private verifiedVersion?: string
  private timeout?: ReturnType<typeof setTimeout>
  private interval?: ReturnType<typeof setInterval>
  constructor(private readonly options: { version: string; mode: ApplicationMode; updater?: UpdaterAdapter; emit: (state: UpdateState) => void; prepareInstall: () => Promise<void>; logger?: Pick<Console, 'info' | 'error'> }) {
    const enabled = options.mode === 'installed' && Boolean(options.updater)
    this.state = { enabled, status: enabled ? 'idle' : 'disabled', currentVersion: options.version }
    const updater = enabled ? options.updater : undefined
    if (!updater) return
    updater.autoDownload = false
    updater.autoInstallOnAppQuit = false
    updater.allowPrerelease = false
    updater.allowDowngrade = false
    updater.on('download-progress', (progress: UpdateState['progress']) => this.publish({ status: 'downloading', progress }))
    updater.on('error', (error: Error) => this.fail(error))
    updater.on('update-downloaded', (info: UpdateInfo) => {
      if (info.version === this.state.targetVersion && newerStable(info.version, options.version)) {
        this.verifiedVersion = info.version
        this.publish({ status: 'ready', error: undefined, progress: undefined })
      }
    })
  }
  getState(): UpdateState { return { ...this.state, canInstall: Boolean(this.verifiedVersion && this.verifiedVersion === this.state.targetVersion && ['ready', 'error'].includes(this.state.status)), ...(this.state.progress ? { progress: { ...this.state.progress } } : {}) } }
  private publish(patch: Partial<UpdateState>): UpdateState {
    this.state = { ...this.state, ...patch }
    this.options.emit(this.getState())
    this.options.logger?.info('KAH_UPDATE', { status: this.state.status, currentVersion: this.state.currentVersion, targetVersion: this.state.targetVersion })
    return this.getState()
  }
  private fail(error: unknown): UpdateState {
    this.options.logger?.error('KAH_UPDATE_ERROR', error)
    return this.publish({ status: 'error', error: error instanceof Error ? error.message : String(error) })
  }
  start(): void {
    if (!this.state.enabled || this.timeout || this.interval) return
    this.timeout = setTimeout(() => { this.timeout = undefined; void this.check() }, 30_000)
    this.interval = setInterval(() => { void this.check() }, 6 * 60 * 60 * 1000)
  }
  dispose(): void { clearTimeout(this.timeout); clearInterval(this.interval); this.timeout = undefined; this.interval = undefined }
  check(): Promise<UpdateState> {
    if (this.checking) return this.checking
    if (!this.state.enabled || ['ready', 'downloading', 'installing'].includes(this.state.status)) return Promise.resolve(this.getState())
    this.checking = this.runCheck().finally(() => { this.checking = undefined })
    return this.checking
  }
  private async runCheck(): Promise<UpdateState> {
    this.publish({ status: 'checking', error: undefined, lastCheckAt: new Date().toISOString() })
    try {
      const result = await this.options.updater!.checkForUpdates()
      const info = result?.updateInfo
      if (!info || !newerStable(info.version, this.options.version)) return this.publish({ status: 'not-available', targetVersion: undefined, releaseNotes: undefined })
      const notes = Array.isArray(info.releaseNotes) ? info.releaseNotes.map((item) => `${item.version}\n${item.note ?? ''}`).join('\n\n') : info.releaseNotes ?? ''
      this.publish({ status: 'available', targetVersion: info.version, releaseNotes: notes })
      return await this.download()
    } catch (error) { return this.fail(error) }
  }
  download(): Promise<UpdateState> {
    if (this.downloading) return this.downloading
    if (!this.state.enabled || !this.state.targetVersion || ['ready', 'checking', 'installing'].includes(this.state.status)) return Promise.resolve(this.getState())
    this.downloading = this.runDownload().finally(() => { this.downloading = undefined })
    return this.downloading
  }
  private async runDownload(): Promise<UpdateState> {
    this.verifiedVersion = undefined
    this.publish({ status: 'downloading', error: undefined, progress: undefined })
    try {
      await this.options.updater!.downloadUpdate()
      if (this.verifiedVersion !== this.state.targetVersion) throw new Error('更新包未完成校验，请重试下载。')
      return this.getState()
    } catch (error) { return this.fail(error) }
  }
  install(): Promise<UpdateState> {
    if (this.installing) return this.installing
    if (!this.state.enabled || !this.verifiedVersion || this.verifiedVersion !== this.state.targetVersion || !['ready', 'error'].includes(this.state.status)) return Promise.resolve(this.getState())
    this.installing = this.runInstall().finally(() => { this.installing = undefined })
    return this.installing
  }
  private async runInstall(): Promise<UpdateState> {
    this.publish({ status: 'installing', error: undefined })
    try {
      await this.options.prepareInstall()
      this.dispose()
      this.options.updater!.quitAndInstall(true, true)
      return this.getState()
    } catch (error) { return this.fail(error) }
  }
}
