// Exercise real Electron source/unpacked/portable launchers against the
// isolated compiled feed. Requires build-update-fixtures.cjs first.
const fs = require('node:fs')
const path = require('node:path')
const http = require('node:http')
const { spawn } = require('node:child_process')
const { connect, portFree, waitFor } = require('./accept-installed-update.cjs')
const root = path.resolve(__dirname, '..')
const work = path.join(root, '.run-data/updater-acceptance')
const fixtures = JSON.parse(fs.readFileSync(path.join(work, 'fixtures.json')))
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function main() {
  for (const port of [48765, 48766, 9228]) if (!await portFree(port)) throw Error(`Test port occupied: ${port}`)
  const requests = []
  const server = http.createServer((req, res) => { requests.push(req.url); res.writeHead(500); res.end('disabled modes must not request updates') })
  await new Promise(resolve => server.listen(48766, '127.0.0.1', resolve))
  const evidence = { startedAt: new Date().toISOString(), testOnly: true, modes: {}, requests }
  let cdp
  try {
    for (const mode of ['unpacked', 'portable', 'development']) {
      const profile = path.join(work, `profile-${mode}`)
      fs.mkdirSync(path.join(profile, 'user'), { recursive: true })
      const env = { ...process.env, KAH_UPDATE_TEST_PROFILE: profile, KAH_PYTHON: path.join(root, '.run-data/ocr-plugin-env/Scripts/python.exe'), GOCACHE: path.join(root, '.run-data/go-cache') }
      delete env.KAH_PORTABLE; delete env.PORTABLE_EXECUTABLE_DIR
      const executable = mode === 'unpacked' ? path.join(work, 'package-a/win-unpacked/Knowledge Agent Hub Update Test.exe') : mode === 'portable' ? path.join(work, `package-b/Knowledge-Agent-Hub-Update-Test-${fixtures.nextVersion}-x64-portable.exe`) : path.join(root, 'apps/desktop/node_modules/electron/dist/electron.exe')
      const child = spawn(executable, mode === 'development' ? [path.join(root, 'apps/desktop')] : [], { env, windowsHide: false, stdio: 'ignore' })
      let launchError
      child.once('error', error => { launchError = error })
      cdp = await connect()
      if (launchError) throw launchError
      const config = await cdp.evaluate('window.kah.runtimeConfig().then(({desktopToken,...safe})=>safe)')
      if (config.applicationMode !== mode || config.updatesEnabled) throw Error(`${mode}: runtime mode gate failed`)
      if (!config.dataRoot.startsWith(work + path.sep)) throw Error(`${mode}: data escaped test root`)
      for (const method of ['getState', 'check', 'download', 'install']) {
        const state = await cdp.evaluate(`window.kah.updates.${method}()`)
        if (state.enabled || state.status !== 'disabled') throw Error(`${mode}: ${method} was enabled`)
      }
      await cdp.evaluate(`Array.from(document.querySelectorAll('button')).find(b=>b.textContent.trim().startsWith('设置')).click()`)
      await pause(32_000) // crosses the installed-mode automatic check deadline
      const controls = await cdp.evaluate(`({notice:!!document.querySelector('.update-notice'),settingsOpen:!!document.querySelector('[role=tab]'),updateTab:Array.from(document.querySelectorAll('[role=tab]')).some(e=>e.textContent==='应用更新')})`)
      if (!controls.settingsOpen || controls.notice || controls.updateTab || requests.length) throw Error(`${mode}: update UI/network leaked`)
      evidence.modes[mode] = { config, controls, manualMethodsDisabled: true, beyondAutomaticDeadline: true, updateRequests: 0 }
      await cdp.evaluate('window.close()').catch(() => {})
      cdp.socket.close(); cdp = undefined
      await waitFor(() => portFree(48765), 30_000)
      await waitFor(() => portFree(9228), 15_000)
    }
    evidence.status = 'PASS'
    console.log('MODE_ISOLATION_PASS unpacked portable development; update requests=0')
  } catch (error) {
    evidence.status = 'FAIL'; evidence.error = error.message
    if (cdp) { await cdp.evaluate('window.close()').catch(() => {}); cdp.socket.close() }
    throw error
  } finally {
    evidence.finishedAt = new Date().toISOString()
    fs.writeFileSync(path.join(work, 'mode-isolation-result.json'), JSON.stringify(evidence, null, 2))
    server.closeAllConnections(); server.close()
  }
}
main().catch(error => { console.error(error.message); process.exitCode = 1 })
