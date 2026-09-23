import { app, BrowserWindow, dialog, ipcMain, Menu, shell } from 'electron'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import { copyFile, mkdtemp, rename, rm, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { installDiagnostics } from './diagnostics'
import { installedDataRoot } from './runtimePaths'
import { showWindowWhenReady } from './windowVisibility'

const API_PORT = 48761
const BACKEND_START_TIMEOUT_MS = 90_000
const BACKEND_RETRY_INTERVAL_MS = 250
let backend: ChildProcess | undefined
let mainWindow: BrowserWindow | undefined
let isQuitting = false
let backendMaintenance = false
const desktopToken = `desktop_${randomBytes(32).toString('base64url')}`
const isPrimaryInstance = app.requestSingleInstanceLock()


const titleBarTheme = {
  light: { color: '#ffffff', symbolColor: '#5f6368' },
  dark: { color: '#212121', symbolColor: '#c4c4c4' }
} as const
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function logRendererConsole(...args: unknown[]): void {
  const [, first, second, third, fourth] = args
  const details = isRecord(first) ? first : undefined
  const legacy = typeof first === 'number'
    ? { level: first, message: second, line: third, sourceId: fourth }
    : undefined

  console.info('KAH_RENDERER_CONSOLE', {
    ...(legacy ?? {}),
    ...(details ?? {}),
  })
}

function projectRoot(): string {
  const candidates = [
    path.resolve(__dirname, '..', '..', '..', '..'),
    path.resolve(__dirname, '..', '..', '..'),
    path.resolve(app.getAppPath(), '..', '..', '..'),
    path.resolve(app.getAppPath(), '..', '..'),
    path.resolve(app.getAppPath())
  ]
  return candidates.find((candidate) => existsSync(path.join(candidate, 'services', 'api'))) ?? candidates[0]
}

function dataRoot(): string {
  const portableExecutableDirectory = process.env.PORTABLE_EXECUTABLE_DIR?.trim()
  if (portableExecutableDirectory) return path.join(portableExecutableDirectory, 'data')
  const portable = process.env.KAH_PORTABLE === '1' || existsSync(path.join(path.dirname(app.getPath('exe')), 'portable.flag'))
  if (portable) return path.join(path.dirname(app.getPath('exe')), 'data')
  if (app.isPackaged) {
    const configuration = path.join(app.getPath('appData'), 'Knowledge Agent Hub', 'runtime.json')
    if (existsSync(configuration)) {
      const value: unknown = JSON.parse(readFileSync(configuration, 'utf8').replace(/^\uFEFF/, ''))
      if (!isRecord(value) || typeof value.dataRoot !== 'string' || !path.isAbsolute(value.dataRoot)) {
        throw new Error(`Invalid absolute dataRoot in ${configuration}`)
      }
      return installedDataRoot(value.dataRoot, path.dirname(app.getPath('exe')))
    }
  }
  // Installed application data must outlive NSIS replacement of the program
  // directory. Keep the product data separate from historical development data.
  return app.isPackaged
    ? path.join(app.getPath('appData'), 'Knowledge Agent Hub', 'data')
    : path.join(app.getPath('userData'), 'data')
}

function findExecutable(command: string): string | undefined {
  if (path.isAbsolute(command)) return existsSync(command) ? command : undefined
  const pathValue = process.env.Path ?? process.env.PATH ?? ''
  for (const directory of pathValue.split(path.delimiter)) {
    if (!directory) continue
    const candidate = path.join(directory, process.platform === 'win32' ? `${command}.exe` : command)
    if (existsSync(candidate)) return candidate
  }
  return undefined
}

function startBackend(): void {
  const environment: NodeJS.ProcessEnv = {
    ...process.env,
    KAH_DESKTOP_TOKEN: desktopToken,
    KAH_DATA_ROOT: dataRoot(),
    KAH_HOST: '127.0.0.1',
    KAH_PORT: String(API_PORT)
  }
  const archifyRoot = app.isPackaged
    ? path.join(process.resourcesPath, 'archify')
    : path.join(projectRoot(), 'third_party', 'archify', 'upstream')
  if (existsSync(path.join(archifyRoot, 'bin', 'archify.mjs'))) {
    environment.KAH_ARCHIFY_ROOT = archifyRoot
  }
  if (app.isPackaged) {
    const backendDirectory = path.join(process.resourcesPath, 'backend')
    const bundledNode = path.join(process.resourcesPath, 'node', process.platform === 'win32' ? 'node.exe' : 'node')
    const systemNode = findExecutable('node')
    if (existsSync(bundledNode)) {
      environment.KAH_NODE_PATH = bundledNode
    } else if (systemNode) {
      environment.KAH_NODE_PATH = systemNode
    } else {
      // Electron can execute the vendored CLI in Node mode. The API only
      // enables this mode for the path supplied by this main process.
      environment.KAH_NODE_PATH = process.execPath
      environment.KAH_ARCHIFY_ELECTRON_NODE = '1'
    }
    environment.KAH_WORKER_CMD = path.join(backendDirectory, 'kah-worker.exe')
    backend = spawn(path.join(backendDirectory, 'kah-api.exe'), [], { env: environment, windowsHide: true })
  } else {
    const root = projectRoot()
    const configuredPython = process.env.KAH_PYTHON?.trim()
    environment.KAH_WORKER_CMD = configuredPython || findExecutable('python3.14') || findExecutable('python') || 'python'
    environment.KAH_WORKER_MODULE = 'knowledge_worker'
    environment.KAH_WORKER_CWD = path.join(root, 'services', 'worker')
    environment.PYTHONPATH = path.join(root, 'services', 'worker', 'src')
    const configuredGo = process.env.KAH_GO_CMD?.trim()
    const goCommand = configuredGo ? findExecutable(configuredGo) : undefined
    const bundledBackend = path.join(root, 'services', 'api', 'kah-api.exe')
    const availableGo = goCommand ?? findExecutable('go')
    if (availableGo) {
      // In development, use the checked-out API source. A local kah-api.exe
      // may be older than the renderer and lack the current import endpoints.
      backend = spawn(availableGo, ['run', './cmd/server'], { cwd: path.join(root, 'services', 'api'), env: environment, windowsHide: true })
    } else if (existsSync(bundledBackend)) {
      console.warn('Go was not found in the Electron environment; using the existing development API binary.', { bundledBackend })
      backend = spawn(bundledBackend, [], { cwd: path.join(root, 'services', 'api'), env: environment, windowsHide: true })
    } else {
      throw new Error('Go was not found in PATH and services/api/kah-api.exe is unavailable.')
    }
  }
  backend.stdout?.on('data', (value) => console.info(String(value).trim()))
  backend.stderr?.on('data', (value) => console.error(String(value).trim()))
  backend.on('error', (error) => {
    console.error('backend process failed to start', error)
    if (!isQuitting && !backendMaintenance) mainWindow?.webContents.send('runtime:backend-exit', null)
  })
  backend.on('exit', (code) => {
    console.info('backend exited', { code, quitting: isQuitting, maintenance: backendMaintenance })
    if (!isQuitting && !backendMaintenance) mainWindow?.webContents.send('runtime:backend-exit', code)
  })
}

function backendPortOwnershipHint(): string {
  if (process.platform !== 'win32') return ''
  try {
    const output = execFileSync('netstat.exe', ['-ano', '-p', 'tcp'], { windowsHide: true, encoding: 'utf8' })
    const listeners = output.split(/\r?\n/)
      .filter((line) => line.includes(`:${API_PORT}`) && /LISTENING/i.test(line))
      .map((line) => line.trim().split(/\s+/).at(-1))
      .filter((pid): pid is string => Boolean(pid && /^\d+$/.test(pid)))
    return listeners.length > 0 ? ` Listener PID: ${listeners.join(', ')}.` : ''
  } catch {
    return ''
  }
}

function backupCandidatePath(relativePath: string): string {
  const normalized = path.normalize(relativePath)
  if (path.isAbsolute(normalized) || normalized.startsWith(`..${path.sep}`) || !normalized.startsWith(`backups${path.sep}`) || !normalized.endsWith('.kahbackup')) throw new Error('备份路径必须位于受控 backups 目录')
  const root = path.resolve(dataRoot())
  const target = path.resolve(root, normalized)
  if (!target.startsWith(root + path.sep)) throw new Error('备份路径越出数据目录')
  return target
}

async function restoreBackup(relativePath: string): Promise<{ restarted: true }> {
  const activeRoot = path.resolve(dataRoot())
  const parent = path.dirname(activeRoot)
  const archive = backupCandidatePath(relativePath)
  // The renderer performs this preflight before showing confirmation, but the
  // IPC handler is also a security boundary. Re-verify the candidate in the
  // main process immediately before stopping the backend and extracting it.
  const verification = await fetch(`http://127.0.0.1:${API_PORT}/api/v1/backups/verify`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${desktopToken}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: path.relative(activeRoot, archive).split(path.sep).join('/') })
  })
  if (!verification.ok) {
    const detail = await verification.text().catch(() => '')
    throw new Error(`备份预检失败（${verification.status}）：${detail || '文件校验未通过'}`)
  }
  const suffix = randomBytes(8).toString('hex')
  const stage = await mkdtemp(path.join(parent, `.kah-restore-${suffix}-`))
  const rollback = path.join(parent, `${path.basename(activeRoot)}.rollback-${suffix}`)
  const failed = path.join(parent, `${path.basename(activeRoot)}.failed-${suffix}`)
  backendMaintenance = true
  stopBackend()
  try {
    execFileSync(process.platform === 'win32' ? 'tar.exe' : 'tar', ['-xf', archive, '-C', stage], { windowsHide: true, stdio: 'pipe' })
    const restoredRoot = path.join(stage, 'data')
    if (!existsSync(path.join(restoredRoot, 'knowledge.db'))) throw new Error('备份预检失败：缺少 data/knowledge.db')
    await rename(activeRoot, rollback)
    try {
      await rename(restoredRoot, activeRoot)
      startBackend()
      await waitForBackend()
    } catch (error) {
      stopBackend()
      if (existsSync(activeRoot)) await rename(activeRoot, failed)
      await rename(rollback, activeRoot)
      startBackend()
      await waitForBackend()
      if (existsSync(failed)) await rm(failed, { recursive: true, force: true })
      throw error
    }
    await rm(rollback, { recursive: true, force: true })
    mainWindow?.webContents.reload()
    return { restarted: true }
  } catch (error) {
    if (!backend && existsSync(activeRoot)) {
      startBackend()
      await waitForBackend()
    }
    throw error
  } finally {
    backendMaintenance = false
    if (existsSync(stage)) await rm(stage, { recursive: true, force: true })
  }
}

async function waitForBackend(): Promise<void> {
  const startedAt = Date.now()
  const deadline = startedAt + BACKEND_START_TIMEOUT_MS
  let lastError: unknown
  let sessionConflict: string | undefined
  while (Date.now() < deadline) {
    try {
      const health = await fetch('http://127.0.0.1:' + API_PORT + '/api/v1/health')
      if (health.ok) {
        const authenticated = await fetch('http://127.0.0.1:' + API_PORT + '/api/v1/libraries', {
          headers: { Authorization: 'Bearer ' + desktopToken }
        })
        if (authenticated.ok) return
        if (authenticated.status === 401) {
          sessionConflict = `A different KAH backend is already responding on port ${API_PORT} and rejected this desktop session token.${backendPortOwnershipHint()} Expected data root: ${dataRoot()}. Close the other KAH session, then start this session again.`
          break
        }
        lastError = new Error('Backend rejected the current desktop token: ' + authenticated.status)
      } else {
        lastError = new Error('Backend health returned ' + health.status)
      }
    } catch (error) { lastError = error }
    await new Promise((resolve) => setTimeout(resolve, BACKEND_RETRY_INTERVAL_MS))
  }
  if (sessionConflict) throw new Error(sessionConflict)
  const elapsedSeconds = Math.round((Date.now() - startedAt) / 1000)
  throw new Error(`Backend did not become healthy within ${elapsedSeconds}s for this desktop session: ${String(lastError ?? 'timeout')}`)
}

async function createWindow(): Promise<void> {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 1040,
    minHeight: 680,
    show: false,
    backgroundColor: titleBarTheme.light.color,
    title: 'Knowledge Agent Hub',
    titleBarStyle: 'hidden',
    titleBarOverlay: { ...titleBarTheme.light, height: 36 },
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true
    }
  })
  showWindowWhenReady(mainWindow)
  mainWindow.webContents.on('did-fail-load', (_event, code, description, validatedUrl) => console.error('renderer load failed', { code, description, validatedUrl }))
  mainWindow.webContents.on('render-process-gone', (_event, details) => console.error('renderer process exited', details))
  ;(mainWindow.webContents as unknown as { on: (event: string, listener: (...args: unknown[]) => void) => void }).on('console-message', logRendererConsole)
  mainWindow.webContents.on('did-finish-load', () => {
    mainWindow?.webContents.executeJavaScript('({ url: location.href, rootReady: Boolean(document.querySelector("#root")?.children.length) })')
      .then((state) => console.info('KAH_RENDERER_READY', state))
      .catch((error) => console.error('renderer inspection failed', error))
  })
  if (process.env.ELECTRON_RENDERER_URL) await mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL)
  else await mainWindow.loadFile(path.join(__dirname, '../renderer/index.html'))
}

app.whenReady().then(async () => {
  if (!isPrimaryInstance) {
    app.quit()
    return
  }
  installDiagnostics(path.join(dataRoot(), 'logs'))
  console.info('desktop starting', { version: app.getVersion(), packaged: app.isPackaged, executable: app.getPath('exe'), dataRoot: dataRoot(), platform: process.platform, electron: process.versions.electron })
  process.on('uncaughtExceptionMonitor', (error) => console.error('uncaught exception', error))
  process.on('unhandledRejection', (error) => console.error('unhandled rejection', error))
  ipcMain.handle('runtime:config', () => ({ apiBaseUrl: `http://127.0.0.1:${API_PORT}/api/v1`, desktopToken, dataRoot: dataRoot(), version: app.getVersion() }))
  Menu.setApplicationMenu(null)
  ipcMain.handle('window:set-titlebar-theme', (_event, theme: 'light' | 'dark') => {
    mainWindow?.setTitleBarOverlay({ ...titleBarTheme[theme], height: 36 })
  })
  ipcMain.handle('dialog:select-files', async (_event, kind: string = 'knowledge') => {
    const filters = kind === 'skill-markdown'
      ? [{ name: 'Skill Markdown', extensions: ['md'] }, { name: 'All files', extensions: ['*'] }]
      : kind === 'ocr-plugin'
        ? [{ name: 'OCR plugin packages', extensions: ['zip'] }]
        : kind === 'embedding-model'
          ? [{ name: 'Embedding model packages', extensions: ['zip'] }]
        : kind === 'skill-zip'
        ? [{ name: 'Skill packages', extensions: ['zip'] }, { name: 'All files', extensions: ['*'] }]
        : [{ name: 'Knowledge files', extensions: ['md','markdown','txt','pdf','doc','docx','xls','xlsx','xlsm','ppt','pptx','html','htm','go','py','js','jsx','ts','tsx','java','cs','rs','cpp','c','h','json','yaml','yml','toml','xml','sql','sh','ps1'] }, { name: 'All files', extensions: ['*'] }]
    const result = await dialog.showOpenDialog({
      properties: kind === 'knowledge' ? ['openFile', 'multiSelections'] : ['openFile'],
      filters
    })
    return result.canceled ? [] : result.filePaths
  })
  ipcMain.handle('dialog:select-directory', async () => {
    const result = await dialog.showOpenDialog({ properties: ['openDirectory'] })
    return result.canceled ? '' : (result.filePaths[0] ?? '')
  })
  ipcMain.handle('dialog:save-file', async (_event, input: { defaultPath?: string; data: string | Uint8Array }) => {
    const defaultPath = typeof input?.defaultPath === 'string' && input.defaultPath.trim()
      ? path.basename(input.defaultPath.trim())
      : 'architecture-export'
    const result = await dialog.showSaveDialog({ defaultPath })
    if (result.canceled || !result.filePath) return ''
    const data = typeof input?.data === 'string' ? Buffer.from(input.data, 'utf8') : Buffer.from(input.data)
    await writeFile(result.filePath, data)
    return result.filePath
  })
  ipcMain.handle('backup:select-candidate', async () => {
    const result = await dialog.showOpenDialog({ properties: ['openFile'], filters: [{ name: 'Knowledge Agent Hub backup', extensions: ['kahbackup'] }] })
    if (result.canceled || !result.filePaths[0]) return ''
    const backupsRoot = path.join(dataRoot(), 'backups')
    const name = `.restore-candidate-${randomBytes(8).toString('hex')}.kahbackup`
    const destination = path.join(backupsRoot, name)
    await copyFile(result.filePaths[0], destination)
    return path.posix.join('backups', name)
  })
  ipcMain.handle('backup:restore', async (_event, relativePath: string) => restoreBackup(relativePath))
  ipcMain.handle('shell:open-path', async (_event, target: string) => shell.openPath(target))
  ipcMain.handle('shell:open-external', async (_event, target: string) => {
    const parsed = new URL(target)
    if (!['http:', 'https:', 'mailto:'].includes(parsed.protocol)) {
      throw new Error('只允许打开 http、https 或 mailto 链接')
    }
    await shell.openExternal(target)
  })
  startBackend()
  try {
    await waitForBackend()
  } catch (error) {
    console.error('backend startup failed', error)
    stopBackend()
    dialog.showErrorBox('Knowledge Agent Hub', String(error))
    app.quit()
    return
  }
  await createWindow()
  app.on('activate', async () => { if (BrowserWindow.getAllWindows().length === 0) await createWindow() })
}).catch((error) => { console.error('desktop startup failed', error); dialog.showErrorBox('Knowledge Agent Hub', String(error)); app.quit() })

app.on('second-instance', () => {
  if (!mainWindow) return
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.focus()
})

function stopBackend(): void {
  const pid = backend?.pid
  backend = undefined
  if (!pid) return
  if (process.platform === 'win32') {
    try {
      execFileSync('taskkill', ['/pid', String(pid), '/t', '/f'], { windowsHide: true, stdio: 'ignore' })
    } catch {
      // The process may already have exited; there is nothing left to clean up.
    }
    return
  }
  try { process.kill(pid) } catch { /* The process may already have exited. */ }
}

app.on('before-quit', () => { console.info('desktop quitting'); isQuitting = true; stopBackend() })
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
