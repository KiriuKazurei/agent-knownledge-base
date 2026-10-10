// Generates isolated NSIS A/B fixtures. Does not edit tracked version files or
// install into the user's application. A test feed is compiled in, not a runtime
// override available to production copies.
const fs = require('node:fs')
const path = require('node:path')
const { spawnSync } = require('node:child_process')
const root = path.resolve(__dirname, '..')
const work = path.join(root, '.run-data/updater-acceptance')
const appDir = path.join(root, 'apps/desktop')
const python = process.env.KAH_BUILD_PYTHON || path.join(root, '.run-data/ocr-plugin-env/Scripts/python.exe')
const sourceVersion = require('../package.json').version
const nextVersion = sourceVersion.replace(/\d+$/, n => String(Number(n) + 1))
const run = (command, args, cwd = root, env = process.env) => {
  const result = spawnSync(command, args, { cwd, env, stdio: 'inherit', windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) throw Error(`${command} failed (${result.status})`)
}
fs.mkdirSync(work, { recursive: true })
const backendB = path.join(work, 'backend-b')
fs.mkdirSync(backendB, { recursive: true })
const workerB = path.join(work, 'worker-source-b')
fs.mkdirSync(workerB, { recursive: true })
fs.cpSync(path.join(root, 'services/worker/src'), path.join(workerB, 'src'), { recursive: true, filter: file => !file.includes('__pycache__') })
fs.copyFileSync(path.join(root, 'services/worker/kah-worker.spec'), path.join(workerB, 'kah-worker.spec'))
const init = path.join(workerB, 'src/knowledge_worker/__init__.py')
fs.writeFileSync(init, fs.readFileSync(init, 'utf8').replace(`__version__ = "${sourceVersion}"`, `__version__ = "${nextVersion}"`))
run(python, ['-m', 'PyInstaller', '--noconfirm', '--distpath', backendB, '--workpath', path.join(work, 'worker-build-b'), 'kah-worker.spec'], workerB)
run('go', ['build', '-C', 'services/api', '-ldflags', `-X github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config.BuildVersion=${nextVersion}`, '-o', path.join(backendB, 'kah-api.exe'), './cmd/server'])
const env = { ...process.env, KAH_UPDATE_TEST_URL: 'http://127.0.0.1:48766/', ELECTRON_BUILDER_CACHE: path.join(root, '.run-data/updater-builder-cache') }
run('node', ['node_modules/electron-vite/bin/electron-vite.js', 'build'], appDir, env)
for (const [letter, version] of [['a', sourceVersion], ['b', nextVersion]]) {
  const config = JSON.parse(JSON.stringify(require('../apps/desktop/package.json').build))
  config.appId = 'io.github.kiriukazurei.knowledge-agent-hub.updater-acceptance'
  config.productName = 'Knowledge Agent Hub Update Test'
  config.directories.output = path.join(work, `package-${letter}`)
  config.extraMetadata = { version }
  config.nsis.createDesktopShortcut = false
  config.nsis.createStartMenuShortcut = false
  config.nsis.runAfterFinish = false
  config.nsis.artifactName = 'Knowledge-Agent-Hub-Update-Test-${version}-${arch}-setup.${ext}'
  config.portable.artifactName = 'Knowledge-Agent-Hub-Update-Test-${version}-${arch}-portable.${ext}'
  config.extraResources[0].from = letter === 'b' ? path.join(backendB, 'kah-api.exe') : path.join(root, 'services/api/kah-api.exe')
  config.extraResources[1].from = letter === 'b' ? path.join(backendB, 'kah-worker.exe') : path.join(root, 'services/worker/dist/kah-worker.exe')
  config.extraResources[2].from = path.join(root, 'third_party/archify/upstream')
  const configPath = path.join(work, `config-${letter}.json`)
  fs.writeFileSync(configPath, JSON.stringify(config, null, 2))
  run('node', ['node_modules/electron-builder/cli.js', '--win', 'nsis', ...(letter === 'b' ? ['portable'] : []), '--config', configPath, '--publish', 'never'], appDir, env)
}
fs.writeFileSync(path.join(work, 'fixtures.json'), JSON.stringify({ sourceVersion, nextVersion, feed: 'http://127.0.0.1:48766/', applicationPort: 48765, appId: 'io.github.kiriukazurei.knowledge-agent-hub.updater-acceptance', production: false }, null, 2))
console.log('UPDATE_FIXTURE_BUILD_PASS (test only; not for publication)')
