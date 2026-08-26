import { app, BrowserWindow, dialog, ipcMain, Menu, shell } from 'electron'
import { spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync } from 'node:fs'
import path from 'node:path'

const API_PORT = 48761
let backend: ChildProcess | undefined
let mainWindow: BrowserWindow | undefined
let isQuitting = false
const desktopToken = `desktop_${randomBytes(32).toString('base64url')}`


const titleBarTheme = {
  light: { color: '#ffffff', symbolColor: '#62707d' },
  dark: { color: '#121920', symbolColor: '#a5b3bd' }
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
    raw: args.slice(1)
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
  const portable = process.env.KAH_PORTABLE === '1' || existsSync(path.join(path.dirname(app.getPath('exe')), 'portable.flag'))
  return portable ? path.join(path.dirname(app.getPath('exe')), 'data') : path.join(app.getPath('userData'), 'data')
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
  if (app.isPackaged) {
    const backendDirectory = path.join(process.resourcesPath, 'backend')
    environment.KAH_WORKER_CMD = path.join(backendDirectory, 'kah-worker.exe')
    backend = spawn(path.join(backendDirectory, 'kah-api.exe'), [], { env: environment, windowsHide: true })
  } else {
    const root = projectRoot()
    environment.KAH_WORKER_CMD = process.env.KAH_PYTHON ?? 'python'
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
    if (!isQuitting) mainWindow?.webContents.send('runtime:backend-exit', null)
  })
  backend.on('exit', (code) => {
    if (!isQuitting) mainWindow?.webContents.send('runtime:backend-exit', code)
  })
}

async function waitForBackend(): Promise<void> {
  let lastError: unknown
  for (let attempt = 0; attempt < 80; attempt += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${API_PORT}/api/v1/health`)
      if (response.ok) return
    } catch (error) { lastError = error }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`Backend did not become healthy: ${String(lastError ?? 'timeout')}`)
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
  mainWindow.once('ready-to-show', () => mainWindow?.show())
  mainWindow.webContents.on('did-fail-load', (_event, code, description, validatedUrl) => console.error('renderer load failed', { code, description, validatedUrl }))
  mainWindow.webContents.on('render-process-gone', (_event, details) => console.error('renderer process exited', details))
  ;(mainWindow.webContents as unknown as { on: (event: string, listener: (...args: unknown[]) => void) => void }).on('console-message', logRendererConsole)
  mainWindow.webContents.on('did-finish-load', () => {
    mainWindow?.webContents.executeJavaScript('({ url: location.href, childCount: document.body.children.length, text: document.body.innerText.slice(0, 80), rootHtml: document.querySelector("#root")?.innerHTML.slice(0, 200), scripts: [...document.scripts].map(s => s.src) })')
      .then((state) => console.info('KAH_RENDERER_READY', state))
      .catch((error) => console.error('renderer inspection failed', error))
  })
  if (process.env.ELECTRON_RENDERER_URL) await mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL)
  else await mainWindow.loadFile(path.join(__dirname, '../renderer/index.html'))
}

app.whenReady().then(async () => {
  ipcMain.handle('runtime:config', () => ({ apiBaseUrl: `http://127.0.0.1:${API_PORT}/api/v1`, desktopToken, dataRoot: dataRoot(), version: app.getVersion() }))
  Menu.setApplicationMenu(null)
  ipcMain.handle('window:set-titlebar-theme', (_event, theme: 'light' | 'dark') => {
    mainWindow?.setTitleBarOverlay({ ...titleBarTheme[theme], height: 36 })
  })
  ipcMain.handle('dialog:select-files', async (_event, kind: string = 'knowledge') => {
    const filters = kind === 'skill-markdown'
      ? [{ name: 'Skill Markdown', extensions: ['md'] }, { name: 'All files', extensions: ['*'] }]
      : kind === 'skill-zip'
        ? [{ name: 'Skill packages', extensions: ['zip'] }, { name: 'All files', extensions: ['*'] }]
        : [{ name: 'Knowledge files', extensions: ['md','markdown','txt','pdf','doc','docx','xls','xlsx','xlsm','ppt','pptx','html','htm','go','py','js','jsx','ts','tsx','java','cs','rs','cpp','c','h','json','yaml','yml','toml','xml','sql','sh','ps1'] }, { name: 'All files', extensions: ['*'] }]
    const result = await dialog.showOpenDialog({
      properties: kind === 'knowledge' ? ['openFile', 'multiSelections'] : ['openFile'],
      filters
    })
    return result.canceled ? [] : result.filePaths
  })
  ipcMain.handle('shell:open-path', async (_event, target: string) => shell.openPath(target))
  ipcMain.handle('shell:open-external', async (_event, target: string) => {
    const parsed = new URL(target)
    if (!['http:', 'https:', 'mailto:'].includes(parsed.protocol)) {
      throw new Error('只允许打开 http、https 或 mailto 链接')
    }
    await shell.openExternal(target)
  })
  startBackend()
  try { await waitForBackend() } catch (error) { dialog.showErrorBox('Knowledge Agent Hub', String(error)) }
  await createWindow()
  app.on('activate', async () => { if (BrowserWindow.getAllWindows().length === 0) await createWindow() })
})

app.on('before-quit', () => { isQuitting = true; backend?.kill() })
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
