const { spawnSync } = require('node:child_process')
const path = require('node:path')
const version = require('../package.json').version
const result = spawnSync('go', ['build', '-C', 'services/api', '-ldflags', `-X github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config.BuildVersion=${version}`, '-o', 'kah-api.exe', './cmd/server'], { cwd: path.resolve(__dirname, '..'), stdio: 'inherit', windowsHide: true })
if (result.error) throw result.error
process.exit(result.status ?? 1)
