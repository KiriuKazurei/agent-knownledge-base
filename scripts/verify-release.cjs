const fs = require('node:fs')
const path = require('node:path')
const crypto = require('node:crypto')
const { createRequire } = require('node:module')
const requireDesktop = createRequire(path.resolve(__dirname, '../apps/desktop/package.json'))
const yaml = createRequire(requireDesktop.resolve('electron-updater'))('js-yaml')
const requireBuilder = createRequire(requireDesktop.resolve('electron-builder'))
const asar = createRequire(requireBuilder.resolve('app-builder-lib'))('@electron/asar')
const root = path.resolve(__dirname, '..')
const output = path.resolve(root, process.argv[2] ?? 'release')
const version = require('../package.json').version
if (process.env.KAH_UPDATE_TEST_URL) throw Error('隔离更新验收构建不可作为正式 Release 上传')
if (/http:\/\/127\.0\.0\.1:\d+\//.test(fs.readFileSync(path.join(root, 'apps/desktop/out/main/index.js'), 'utf8'))) throw Error('主进程包含隔离更新源；重新构建正式包')
const desktop = require('../apps/desktop/package.json')
const worker = fs.readFileSync(path.join(root, 'services/worker/pyproject.toml'), 'utf8').match(/^version = "([^"]+)"/m)?.[1]
if (desktop.version !== version || worker !== version) throw Error('桌面/API构建版本/Worker版本不一致')
if (!fs.readFileSync(path.join(root, 'services/worker/src/knowledge_worker/__init__.py'), 'utf8').includes(`__version__ = "${version}"`)) throw Error('Worker运行时版本不一致')
const setupName = `Knowledge-Agent-Hub-${version}-x64-setup.exe`
const names = [setupName, `${setupName}.blockmap`, `Knowledge-Agent-Hub-${version}-x64-portable.exe`, 'latest.yml']
const files = names.map(name => {
  const file = path.join(output, name)
  const data = fs.readFileSync(file)
  return { path: name, sizeBytes: data.length, sha256: crypto.createHash('sha256').update(data).digest('hex'), sha512: crypto.createHash('sha512').update(data).digest('base64') }
})
const metadata = yaml.load(fs.readFileSync(path.join(output, 'latest.yml'), 'utf8'))
const installer = files[0]
if (metadata.version !== version || metadata.files?.length !== 1 || metadata.files[0].url !== setupName || metadata.files[0].sha512 !== installer.sha512 || metadata.files[0].size !== installer.sizeBytes) throw Error('更新清单未绑定同一次构建的 NSIS 安装包')
if (metadata.path !== setupName || metadata.sha512 !== installer.sha512) throw Error('兼容更新清单与安装包不一致')
const resources = path.join(output, 'win-unpacked/resources')
const packagedMain = asar.extractFile(path.join(resources, 'app.asar'), path.join('out', 'main', 'index.js'))
if (!packagedMain.equals(fs.readFileSync(path.join(root, 'apps/desktop/out/main/index.js')))) throw Error('捆绑主进程与本轮正式构建不一致')
if (fs.existsSync(path.join(resources, 'kah-installation.json'))) throw Error('NSIS安装标记泄漏到共享解包产物')
const internal = yaml.load(fs.readFileSync(path.join(resources, 'app-update.yml'), 'utf8'))
if (internal.provider !== 'github' || internal.owner !== 'KiriuKazurei' || internal.repo !== 'agent-knownledge-base') throw Error('更新源不正确')
for (const name of ['kah-api.exe', 'kah-worker.exe']) {
  const bundled = fs.readFileSync(path.join(resources, 'backend', name))
  const source = fs.readFileSync(path.join(root, name === 'kah-api.exe' ? 'services/api/kah-api.exe' : 'services/worker/dist/kah-worker.exe'))
  if (!bundled.equals(source)) throw Error(`后端打包文件与本轮构建不一致: ${name}`)
}
const manifest = { generatedAt: new Date().toISOString(), version, platform: 'win32', arch: 'x64', updaterProvider: 'github', unsigned: true, integrityOnly: true, artifacts: files }
fs.writeFileSync(path.join(output, 'build-manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
fs.writeFileSync(path.join(output, 'SHA256SUMS.txt'), files.map(f => `${f.sha256}  ${f.path}`).join('\n') + '\n')
console.log(`RELEASE_ARTIFACT_PASS version=${version} files=${files.length} SHA512=verified marker=absent bundled-backend=matching`)
