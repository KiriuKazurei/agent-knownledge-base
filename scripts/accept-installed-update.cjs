// Runs only the isolated fixture appId/profile/port created by
// build-update-fixtures.cjs. Never terminates a production AKB process.
const fs = require('node:fs')
const path = require('node:path')
const http = require('node:http')
const net = require('node:net')
const { spawn, execFileSync } = require('node:child_process')
const root = path.resolve(__dirname, '..')
const work = path.join(root, '.run-data/updater-acceptance')
const fixtures = JSON.parse(fs.readFileSync(path.join(work, 'fixtures.json'), 'utf8'))
const pause = ms => new Promise(r => setTimeout(r, ms))
function ensure(condition, message) { if (!condition) throw Error(message) }
async function waitFor(fn, timeout = 120_000) {
  const end = Date.now() + timeout
  let last
  while (Date.now() < end) { try { const value = await fn(); if (value) return value } catch (error) { last = error } await pause(500) }
  throw Error(`Timed out: ${last?.message ?? 'condition'}`)
}
async function portFree(port) {
  return new Promise(resolve => { const socket = net.connect(port, '127.0.0.1'); socket.once('connect', () => { socket.destroy(); resolve(false) }); socket.once('error', () => resolve(true)) })
}
function run(command, args, env) {
  return new Promise((resolve, reject) => { const child = spawn(command, args, { env, windowsHide: true, stdio: 'ignore' }); child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(Error(`Child exit ${code}`))) })
}
async function connect() {
  const target = await waitFor(async () => (await (await fetch('http://127.0.0.1:9228/json/list')).json()).find(x => x.type === 'page'))
  const socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject })
  let seq = 0
  const pending = new Map()
  socket.onmessage = event => { const result = JSON.parse(event.data); const callback = pending.get(result.id); if (!callback) return; pending.delete(result.id); result.error ? callback.reject(Error(JSON.stringify(result.error))) : callback.resolve(result.result) }
  socket.onclose = () => { for (const callback of pending.values()) callback.reject(Error('CDP closed')); pending.clear() }
  const send = (method, params = {}) => new Promise((resolve, reject) => { const id = ++seq; pending.set(id, { resolve, reject }); socket.send(JSON.stringify({ id, method, params })) })
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true })
    if (result.exceptionDetails) throw Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text)
    return result.result.value
  }
  const api = (route, method = 'GET', body) => evaluate(`(async()=>{const c=await window.kah.runtimeConfig();const r=await fetch(c.apiBaseUrl+${JSON.stringify(route)},{method:${JSON.stringify(method)},headers:{Authorization:'Bearer '+c.desktopToken,'Content-Type':'application/json'},body:${body === undefined ? 'undefined' : JSON.stringify(JSON.stringify(body))}});const t=await r.text();if(!r.ok)throw Error(r.status+' '+t);return t?JSON.parse(t):null})()`)
  return { socket, send, evaluate, api }
}
async function screenshot(cdp, name) { const shot = await cdp.send('Page.captureScreenshot', { format: 'png' }); fs.writeFileSync(path.join(work, name), Buffer.from(shot.data, 'base64')) }
async function inspectInstalledUI(cdp, installed) {
  const nativeWindows = JSON.parse(execFileSync('powershell.exe', ['-NoProfile', '-File', path.join(root, 'scripts/inspect-test-window.ps1'), '-InstallationRoot', installed], { encoding: 'utf8', windowsHide: true }))
  ensure(nativeWindows.some(window => window.visible), 'No visible native test window')
  await cdp.evaluate(`Array.from(document.querySelectorAll('button')).find(b=>b.textContent.trim().startsWith('设置'))?.click()`)
  await pause(250)
  const cases = []
  for (const [width, height] of [[1040,680],[1440,900],[1920,1080]]) {
    await cdp.evaluate(`window.resizeTo(${width},${height})`)
    for (const theme of ['light','dark','system']) for (const density of ['comfortable','compact']) {
      const themeLabel = { light: '浅色', dark: '深色', system: '跟随系统' }[theme]
      await cdp.evaluate(`document.querySelector('[aria-label="使用${themeLabel}主题"]').click();document.querySelector('[aria-label="使用${density === 'compact' ? '紧凑' : '舒适'}信息密度"]').click()`)
      await pause(100)
      const state = await cdp.evaluate(`(()=>{const n=document.querySelector('.update-notice').getBoundingClientRect();return {width:innerWidth,height:innerHeight,devicePixelRatio,preferences:JSON.parse(localStorage.getItem('kah-ui')).state,noticeVisible:n.left>=0&&n.right<=innerWidth&&n.top>=0&&n.bottom<=innerHeight}})()`)
      ensure(state.preferences.theme === theme && state.preferences.density === density && state.noticeVisible, 'Appearance/update notice constraint failed')
      await screenshot(cdp, `ui-${width}-${theme}-${density}.png`)
      cases.push({ requested: { width, height, theme, density }, actual: state })
    }
  }
  // Restore the persisted choices checked after the upgrade.
  await cdp.evaluate(`document.querySelector('[aria-label="使用深色主题"]').click();document.querySelector('[aria-label="使用紧凑信息密度"]').click()`)
  await cdp.evaluate('window.resizeTo(1440,900)')
  return { nativeWindows, cases, manualAcceptance: false, windows125Percent: 'not changed or manually validated' }
}
async function main() {
  ensure(fixtures.production === false, 'Only test fixtures allowed')
  for (const port of [48765, 48766, 9228]) ensure(await portFree(port), `Test port already occupied: ${port}`)
  const profile = path.join(work, 'profile'), data = path.join(work, 'data'), installed = path.join(work, 'installed')
  fs.mkdirSync(path.join(profile, 'user'), { recursive: true })
  fs.mkdirSync(path.join(profile, 'Knowledge Agent Hub'), { recursive: true })
  fs.writeFileSync(path.join(profile, 'Knowledge Agent Hub/runtime.json'), JSON.stringify({ dataRoot: data }))
  const env = { ...process.env, KAH_UPDATE_TEST_PROFILE: profile }
  const requests = []
  const server = http.createServer((req, res) => {
    requests.push({ path: req.url, range: req.headers.range ?? null })
    const name = decodeURIComponent(new URL(req.url, 'http://127.0.0.1').pathname).slice(1)
    const file = path.join(work, 'package-b', name)
    if (name.includes('/') || name.includes('\\') || !fs.existsSync(file)) { res.writeHead(404); res.end(); return }
    const size = fs.statSync(file).size
    const range = /^bytes=(\d+)-(\d*)$/.exec(req.headers.range ?? '')
    const start = range ? Number(range[1]) : 0, end = range && range[2] ? Math.min(Number(range[2]), size - 1) : size - 1
    if (start > end || start >= size) { res.writeHead(416); res.end(); return }
    res.writeHead(range ? 206 : 200, { 'Content-Length': end - start + 1, 'Accept-Ranges': 'bytes', ...(range ? { 'Content-Range': `bytes ${start}-${end}/${size}` } : {}) })
    fs.createReadStream(file, { start, end }).pipe(res)
  })
  await new Promise(resolve => server.listen(48766, '127.0.0.1', resolve))
  let cdp
  const evidence = { startedAt: new Date().toISOString(), fixture: fixtures, testOnly: true, checks: {}, requests }
  try {
    await run(path.join(work, `package-a/Knowledge-Agent-Hub-Update-Test-${fixtures.sourceVersion}-x64-setup.exe`), ['/S', `/D=${installed}`], env)
    ensure(JSON.parse(fs.readFileSync(path.join(installed, 'resources/kah-installation.json'), 'utf8')).mode === 'installed', 'NSIS marker missing')
    const child = spawn(path.join(installed, 'Knowledge Agent Hub Update Test.exe'), [], { env, windowsHide: false, stdio: 'ignore' })
    child.on('error', error => { throw error })
    cdp = await connect()
    const config = await cdp.evaluate('window.kah.runtimeConfig().then(({desktopToken,...safe})=>safe)')
    ensure(config.version === fixtures.sourceVersion && config.applicationMode === 'installed' && config.dataRoot === data, 'Installed fixture runtime mismatch')
    evidence.before = { config, health: await cdp.api('/health') }
    const library = await cdp.api('/libraries', 'POST', { name: '更新验收专用库', description: 'isolated updater acceptance' })
    const sample = path.join(work, 'sample.md')
    fs.writeFileSync(sample, '# 更新验收\n\nAKBUPDATEFIXTURE 本地资料必须在程序升级后保留。\n')
    const jobs = await cdp.api('/imports/files', 'POST', { libraryId: library.id, paths: [sample], watchSource: false })
    await waitFor(async () => {
      const all = await cdp.api('/jobs')
      const current = all.filter(j => jobs.some(original => original.id === j.id))
      ensure(!current.some(j => j.status === 'failed'), 'Import fixture failed')
      return current.length === jobs.length && current.every(j => j.status === 'completed')
    })
    const submissions = await cdp.api(`/knowledge/submissions?libraryId=${library.id}`)
    for (const item of submissions) if (item.reviewStatus !== 'published') await cdp.api(`/knowledge/submissions/${item.id}/approve`, 'POST', { reason: 'known isolated update fixture' })
    const beforeDocs = await cdp.api(`/documents?libraryId=${library.id}`)
    const backup = await cdp.api('/backups', 'POST', { includeIndexes: true })
    ensure((await cdp.api('/backups/verify', 'POST', { path: backup.path })).sha256 === backup.sha256, 'Backup failed')
    console.log('UPDATE_FIXTURE_DATA_READY')
    await cdp.evaluate(`localStorage.setItem('kah-ui',JSON.stringify({state:{theme:'dark',density:'compact',savedSearches:[]},version:0}));location.reload()`)
    await pause(1500)
    await cdp.evaluate('window.kah.updates.check()')
    await waitFor(async () => { const state = await cdp.evaluate('window.kah.updates.getState()'); ensure(state.status !== 'error', state.error ?? 'Update error'); return state.status === 'ready' }, 180_000)
    evidence.checks.downloadedAndVerified = true
    await screenshot(cdp, 'before-update.png')
    evidence.ui = await inspectInstalledUI(cdp, installed)
    // Dismiss via the real renderer, then verify it remains running at A.
    await cdp.evaluate(`document.querySelector('.update-notice button:last-child')?.click()`)
    ensure((await cdp.evaluate('window.kah.runtimeConfig().then(c=>c.version)')) === fixtures.sourceVersion, 'Later installed unexpectedly')
    evidence.checks.laterDoesNotInstall = true
    // Normal close must not install. Reopen A to prove no next-launch install.
    await cdp.evaluate('window.close()').catch(() => {})
    cdp.socket.close(); cdp = undefined
    await waitFor(() => portFree(48765), 30_000)
    spawn(path.join(installed, 'Knowledge Agent Hub Update Test.exe'), [], { env, windowsHide: false, stdio: 'ignore' })
    cdp = await connect()
    ensure((await cdp.evaluate('window.kah.runtimeConfig().then(c=>c.version)')) === fixtures.sourceVersion, 'Normal quit/launch installed unexpectedly')
    evidence.checks.normalQuitAndNextLaunchDoNotInstall = true
    await cdp.evaluate('window.kah.updates.check()')
    await waitFor(async () => (await cdp.evaluate('window.kah.updates.getState()')).status === 'ready')
    // Exercise the native Electron unload barrier that dirty editors use.
    // Explicit install is allowed to close only after successful shutdown.
    await cdp.evaluate(`window.addEventListener('beforeunload',event=>{event.preventDefault();event.returnValue=''})`)
    // Only isolated fixture calls its explicit install bridge. Production UI
    // confirmation/draft protection is covered separately in component tests.
    await waitFor(async () => {
      const result = await cdp.evaluate('window.kah.updates.install()').catch(error => { if (/CDP closed/.test(error.message)) return { status: 'installing' }; throw error })
      return result.status === 'installing'
    }, 60_000)
    cdp.socket.close(); cdp = undefined
    await waitFor(async () => {
      const health = await (await fetch('http://127.0.0.1:48765/api/v1/health')).json()
      return health.version === fixtures.nextVersion && health.worker === 'ok'
    }, 180_000)
    evidence.checks.installerAutomaticallyStartedHealthyB = true
    evidence.autoRestartWindows = JSON.parse(execFileSync('powershell.exe', ['-NoProfile', '-File', path.join(root, 'scripts/inspect-test-window.ps1'), '-InstallationRoot', installed], { encoding: 'utf8', windowsHide: true }))
    ensure(evidence.autoRestartWindows.some(window => window.visible), 'Updated app has no visible native window')
    // Explorer's NSIS relaunch does not consistently expose the test CDP
    // server. Health/version/visible-window prove automatic B startup first;
    // reopen with explicit debug args solely for data/renderer inspection.
    if (await portFree(9228)) {
      evidence.inspectionRequiredRestart = true
      execFileSync('powershell.exe', ['-NoProfile', '-File', path.join(root, 'scripts/inspect-test-window.ps1'), '-InstallationRoot', installed, '-CloseForInspection'], { encoding: 'utf8', windowsHide: true })
      await waitFor(() => portFree(48765), 30_000)
      spawn(path.join(installed, 'Knowledge Agent Hub Update Test.exe'), ['--remote-debugging-port=9228', '--disable-gpu'], { env, windowsHide: false, stdio: 'ignore' })
    }
    cdp = await connect()
    const after = await cdp.evaluate('window.kah.runtimeConfig().then(({desktopToken,...safe})=>safe)')
    const docs = await cdp.api(`/documents?libraryId=${library.id}`)
    const knowledge = await cdp.api('/knowledge/search', 'POST', { query: 'AKBUPDATEFIXTURE', libraryIds: [library.id], limit: 20 })
    ensure(after.version === fixtures.nextVersion && after.dataRoot === data, 'Version/dataRoot changed incorrectly')
    ensure(docs.length === beforeDocs.length && docs[0]?.id === beforeDocs[0]?.id, 'Document identity lost')
    ensure(knowledge.results?.length > 0, 'Knowledge missing after update')
    ensure((await cdp.evaluate(`JSON.parse(localStorage.getItem('kah-ui')).state.theme`)) === 'dark', 'Preferences lost')
    evidence.after = { config: after, health: await cdp.api('/health'), documents: docs.length, knowledgeResults: knowledge.results.length }
    evidence.checks.versionAndDataPersisted = true
    evidence.checks.draftUnloadDoesNotBlockConfirmedInstall = true
    await screenshot(cdp, 'after-update.png')
    // Use update lifecycle shutdown to confirm API/Worker are reaped without
    // killing arbitrary processes or leaving the test port occupied.
    await cdp.api('/runtime/shutdown', 'POST')
    await waitFor(() => portFree(48765), 25_000)
    await cdp.evaluate('window.close()').catch(() => {})
    cdp.socket.close(); cdp = undefined
    evidence.checks.testPortReleased = true
    evidence.status = 'PASS'
  } catch (error) {
    evidence.status = 'FAIL'; evidence.error = error.message
    if (cdp) { await screenshot(cdp, 'failure.png').catch(() => {}); await cdp.evaluate('window.close()').catch(() => {}); cdp.socket.close() }
    throw error
  } finally {
    evidence.finishedAt = new Date().toISOString()
    fs.writeFileSync(path.join(work, 'installed-update-result.json'), JSON.stringify(evidence, null, 2))
    server.closeAllConnections(); server.close()
  }
  console.log('INSTALLED_UPDATE_ACCEPTANCE_PASS ' + JSON.stringify(evidence.checks))
}
module.exports = { connect, portFree, waitFor, screenshot }
if (require.main === module) main().catch(error => { console.error(error.message); process.exitCode = 1 })
