import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const desktopRoot = path.resolve(scriptDirectory, '..')
const repositoryRoot = path.resolve(desktopRoot, '..', '..')
const packageJson = JSON.parse(fs.readFileSync(path.join(desktopRoot, 'package.json'), 'utf8'))
const resource = packageJson.build?.extraResources?.find((entry) => entry?.to === 'archify')
if (!resource || resource.from !== '../../third_party/archify/upstream') {
  throw new Error('electron-builder extraResources must copy third_party/archify/upstream to archify')
}

const required = [
  'bin/archify.mjs',
  'schemas/architecture.schema.json',
  'schemas/sequence.schema.json',
  'schemas/dataflow.schema.json',
  'assets/template.html',
  'LICENSE',
  'THIRD_PARTY_NOTICES.md',
  'SKILL.md'
]
const sourceRoot = path.resolve(desktopRoot, resource.from)
for (const relative of required) {
  const candidate = path.join(sourceRoot, relative)
  if (!fs.existsSync(candidate)) throw new Error(`missing vendored Archify resource: ${candidate}`)
}

const packagedRoot = process.argv[2] ? path.resolve(process.argv[2], 'archify') : undefined
if (packagedRoot) {
  for (const relative of required) {
    const candidate = path.join(packagedRoot, relative)
    if (!fs.existsSync(candidate)) throw new Error(`missing packaged Archify resource: ${candidate}`)
  }
}

console.log(JSON.stringify({ ok: true, sourceRoot, packagedRoot: packagedRoot ?? null, files: required }, null, 2))
