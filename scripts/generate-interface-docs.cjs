/** Source-derived handoff. --check is read-only and rejects missing routes,
 * newly introduced calls and changed signatures rather than trusting counts. */
const fs = require('node:fs')
const path = require('node:path')
const { execFileSync } = require('node:child_process')
const ts = require('../apps/desktop/node_modules/typescript')
const root = path.resolve(__dirname, '..')
const rel = file => path.relative(root, file).replaceAll('\\', '/')
// Git may check Windows sources out as CRLF. Keep the source-derived artifact
// stable across local generation and a fresh CI checkout.
const read = file => fs.readFileSync(path.join(root, file), 'utf8').replace(/\r\n/g, '\n')
const dir = 'apps/desktop/src/renderer/src'
function sourceFiles(directory) {
  return fs.readdirSync(path.join(root, directory), { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? sourceFiles(`${directory}/${entry.name}`) : /\.(ts|tsx)$/.test(entry.name) && !/\.test\.|test-matchers/.test(entry.name) ? [`${directory}/${entry.name}`] : [])
}
const files = sourceFiles(dir)
const parsed = new Map(files.map(f => [f, ts.createSourceFile(f, read(f), ts.ScriptTarget.Latest, true, f.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)]))
const compilerOptions = { target: ts.ScriptTarget.ES2023, moduleResolution: ts.ModuleResolutionKind.Bundler, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.ReactJSX, skipLibCheck: true }
const host = ts.createCompilerHost(compilerOptions)
const originalReadFile = host.readFile.bind(host)
host.readFile = file => originalReadFile(file)?.replace(/\r\n/g, '\n')
const program = ts.createProgram([...files.map(f => path.join(root, f)), path.join(root, 'apps/desktop/src/shared/updates.ts')], compilerOptions, host)
const checker = program.getTypeChecker()
function walk(node, fn) { fn(node); ts.forEachChild(node, c => walk(c, fn)) }
function location(node, sf) { return `${sf.fileName}:${sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1}` }
function expr(node, sf) {
  if (!node) return ''
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
  if (ts.isConditionalExpression(node)) return expr(node.whenTrue, sf) || expr(node.whenFalse, sf)
  if (ts.isTemplateExpression(node)) return node.head.text + node.templateSpans.map(s => (ts.isConditionalExpression(s.expression) ? expr(s.expression, sf) : `{${s.expression.getText(sf)}}`) + s.literal.text).join('')
  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) return expr(node.left, sf) + expr(node.right, sf)
  if (ts.isCallExpression(node) && /encodeURIComponent$/.test(node.expression.getText(sf))) return `{${node.arguments[0].getText(sf)}}`
  return `{${node.getText(sf)}}`
}
function canonical(value) { return value.split('?')[0].replace(/\{[^}]*\}/g, ':param').replace(/:[^/]+/g, ':param') }
function group(route) {
  if (route.includes('/runtime')) return '应用维护'
  if (/\/architecture\/agent/.test(route)) return '架构 Agent 专用'
  if (/\/projects|\/architecture/.test(route)) return '项目架构'
  if (/\/graph/.test(route)) return '关系图谱'
  if (/\/skill/.test(route)) return 'Skills 与映射'
  if (/\/knowledge\/submissions/.test(route)) return '知识审核与修订'
  if (/\/query|\/retrieval|\/knowledge/.test(route)) return '检索与知识'
  if (/\/libraries|\/documents|\/imports|\/folders|\/sources|\/saved-searches/.test(route)) return '知识库与来源'
  if (/\/jobs/.test(route)) return '任务中心'
  if (/\/trash/.test(route)) return '回收站'
  return '设置与维护'
}
const serverSource = read('services/api/internal/httpapi/server.go')
const groups = { router: '' }
for (const m of serverSource.matchAll(/(\w+)\s*:=\s*(\w+)\.Group\("([^"]*)"\)/g)) groups[m[1]] = (groups[m[2]] ?? '') + m[3]
const backend = [...serverSource.matchAll(/(\w+)\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)",\s*([^\n]+)\)/g)].map(m => ({ method: m[2], path: (groups[m[1]] ?? '') + m[3], handler: m[4].replace(/\)$/, ''), authentication: m[1] === 'agent' ? 'architecture-agent-secret' : m[1] === 'v1' ? 'public' : /MCP/.test(m[1]) ? 'mcp-token' : 'desktop-token', location: `services/api/internal/httpapi/server.go:${serverSource.slice(0, m.index).split('\n').length}` }))
const callsites = new Map(), desktopCalls = new Map(), cacheContracts = [], components = []
const usageContracts = new Map()
const directContexts = new Map()
for (const [file, sf] of parsed) {
  const caches = [], polls = [], refreshes = []
  const aliases = new Map()
  walk(sf, node => { if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer?.getText(sf).startsWith('window.kah.')) aliases.set(node.name.text, node.initializer.getText(sf)) })
  walk(sf, node => {
    if (file !== `${dir}/api.ts` && ts.isCallExpression(node) && /^(fetch|axios(?:\..*)?)$/.test(node.expression.getText(sf))) throw new Error(`未归档的原始 HTTP 请求: ${location(node, sf)}，先补充扫描器契约`)
    if (ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'client') {
      const list = callsites.get(node.name.text) ?? []; list.push(location(node, sf)); callsites.set(node.name.text, [...new Set(list)])
      let scope = node.parent
      while (scope && !ts.isArrowFunction(scope) && !ts.isFunctionDeclaration(scope) && !ts.isFunctionExpression(scope)) scope = scope.parent
      if (scope) {
        const contexts = directContexts.get(node.name.text) ?? []
        contexts.push({ location: location(scope, sf), callerSource: scope.getText(sf).length <= 6000 ? scope.getText(sf) : '<大页面函数；以对应query/mutation回调和缓存表为准>' })
        directContexts.set(node.name.text, contexts)
      }
    }
    if (ts.isCallExpression(node)) {
      let target = node.expression.getText(sf).replaceAll('?.', '.')
      if (['useQuery', 'useMutation'].includes(target) && node.arguments[0] && ts.isObjectLiteralExpression(node.arguments[0])) {
        const props = node.arguments[0].properties.filter(ts.isPropertyAssignment)
        const value = name => props.find(p => p.name.getText(sf) === name)?.initializer
        const operation = value(target === 'useQuery' ? 'queryFn' : 'mutationFn')
        if (operation) walk(operation, ref => {
          const methodName = ts.isPropertyAccessExpression(ref) && ref.expression.getText(sf) === 'client' ? ref.name.text : ts.isCallExpression(ref) && ['api', 'apiBinary'].includes(ref.expression.getText(sf)) ? `${path.basename(file)}:direct:${sf.getLineAndCharacterOfPosition(ref.getStart()).line + 1}` : null
          if (!methodName) return
          const entries = usageContracts.get(methodName) ?? []
          entries.push({ location: location(node, sf), hook: target, queryKey: value('queryKey')?.getText(sf) ?? null, onSuccess: value('onSuccess')?.getText(sf) ?? null, onError: value('onError')?.getText(sf) ?? null })
          usageContracts.set(methodName, entries)
        })
      }
      for (const [name, value] of aliases) if (target.startsWith(name + '.')) target = value + target.slice(name.length)
      if (target.startsWith('window.kah.')) {
        const name = target.slice('window.kah.'.length), list = desktopCalls.get(name) ?? []
        list.push(location(node, sf)); desktopCalls.set(name, list)
      }
      if (/invalidateQueries|setQueryData|\.refetch$/.test(target)) refreshes.push({ location: location(node, sf), expression: node.getText(sf) })
    }
    if (ts.isPropertyAssignment(node) && node.name.getText(sf) === 'queryKey') caches.push({ location: location(node, sf), key: node.initializer.getText(sf) })
    if (ts.isPropertyAssignment(node) && node.name.getText(sf) === 'refetchInterval') polls.push({ location: location(node, sf), interval: node.initializer.getText(sf) })
    if (ts.isFunctionDeclaration(node) && node.name && /^[A-Z]/.test(node.name.text)) components.push({ name: node.name.text, location: location(node, sf), parameters: node.parameters.map(p => p.getText(sf)) })
  })
  if (caches.length || polls.length || refreshes.length) cacheContracts.push({ file, queryKeys: caches, polling: polls, refreshes })
}
function typeExample(type, name, depth = 0, seen = new Set()) {
  if (/secret|token|apiKey|authorization/i.test(name)) return '[REDACTED]'
  if (depth > 3 || seen.has(type)) return '<见类型定义>'
  if (type.isUnion()) return typeExample(type.types.find(t => !(t.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null))) ?? type.types[0], name, depth, seen)
  if (type.flags & ts.TypeFlags.StringLiteral) return type.value
  if (type.flags & ts.TypeFlags.NumberLiteral) return type.value
  if (type.flags & ts.TypeFlags.BooleanLiteral) return type.intrinsicName === 'true'
  if (type.flags & ts.TypeFlags.StringLike) return /path/i.test(name) ? 'E:/AKB-example/sample.md' : /url|uri/i.test(name) ? 'https://example.invalid/sample' : /id/i.test(name) ? 'example-id' : '示例'
  if (type.flags & ts.TypeFlags.NumberLike) return 1
  if (type.flags & ts.TypeFlags.BooleanLike) return true
  if (type.flags & (ts.TypeFlags.Void | ts.TypeFlags.Undefined)) return null
  if (checker.isArrayType(type)) return [typeExample(checker.getTypeArguments(type)[0], name, depth + 1, new Set([...seen, type]))]
  const result = {}
  for (const property of type.getProperties().slice(0, 35)) {
    if (property.flags & ts.SymbolFlags.Method) continue
    const declaration = property.valueDeclaration ?? property.declarations?.[0]
    if (declaration) result[property.name] = typeExample(checker.getTypeOfSymbolAtLocation(property, declaration), property.name, depth + 1, new Set([...seen, type]))
  }
  return result
}
function methodDetails(call, sf) {
  const helper = call.expression.getText(sf)
  const pathArgument = helper === 'architectureAgentApi' ? 1 : 0
  const route = '/api/v1' + expr(call.arguments[pathArgument], sf)
  const init = call.arguments[pathArgument + 1]
  const properties = init && ts.isObjectLiteralExpression(init) ? init.properties.filter(ts.isPropertyAssignment) : []
  const methodNode = properties.find(p => p.name.getText(sf) === 'method')?.initializer
  const method = methodNode ? ts.isStringLiteral(methodNode) ? methodNode.text : methodNode.getText(sf).includes('uninstall') ? ['DELETE', 'PATCH'] : '<dynamic>' : 'GET'
  return { method, path: route, pathExpression: call.arguments[pathArgument].getText(sf), requestBodyExpression: properties.find(p => p.name.getText(sf) === 'body')?.initializer.getText(sf) ?? null, responseType: call.typeArguments?.[0]?.getText(sf) ?? (helper === 'apiBinary' ? 'Blob' : 'unknown'), helper }
}
const http = []
const apiFile = `${dir}/api.ts`, apiSource = parsed.get(apiFile)
walk(apiSource, node => {
  if (!ts.isPropertyAssignment(node) || !ts.isArrowFunction(node.initializer)) return
  const name = node.name.getText(apiSource)
  const calls = []
  walk(node.initializer, child => { if (ts.isCallExpression(child) && ['api', 'apiBinary', 'architectureAgentApi'].includes(child.expression.getText(apiSource))) calls.push(child) })
  if (!calls.length) return
  const func = node.initializer
  const programSource = program.getSourceFile(path.join(root, apiFile))
  const matched = []
  walk(programSource, n => { if (ts.isPropertyAssignment(n) && n.name.getText(programSource) === name && ts.isArrowFunction(n.initializer)) matched.push(n.initializer) })
  const typedFunc = matched[0]
  const parameters = func.parameters.map((p, i) => ({ name: p.name.getText(apiSource), type: p.type?.getText(apiSource) ?? 'inferred', optional: Boolean(p.questionToken || p.initializer), default: p.initializer?.getText(apiSource) ?? null, example: typedFunc ? typeExample(checker.getTypeAtLocation(typedFunc.parameters[i]), p.name.getText(apiSource)) : null }))
  const endpoints = calls.map(c => methodDetails(c, apiSource))
  const first = endpoints[0]
  const returnType = typedFunc ? checker.getAwaitedType(checker.getReturnTypeOfSignature(checker.getSignatureFromDeclaration(typedFunc))) : undefined
  http.push({ name, group: group(first.path), status: callsites.has(name) ? 'used' : 'wrapped-unused', implementation: 'existing', location: location(node, apiSource), callsites: callsites.get(name) ?? [], parameters, signature: `(${func.parameters.map(p => p.getText(apiSource)).join(', ')})`, responseType: name === 'importSkill' ? 'Promise<Job> (poll until completed)' : `Promise<${first.responseType}>`, endpoints,
    authentication: first.helper === 'architectureAgentApi' ? 'architecture-agent-secret' : 'desktop-token', responseKind: first.helper === 'apiBinary' ? 'binary' : 'json-or-204', errorBehavior: name === 'importSkill' ? 'ApiError 或任务失败/600秒轮询超时' : 'ApiError(status, code, retryable); 204 -> undefined', refresh: cacheContracts.filter(c => (callsites.get(name) ?? []).some(s => s.startsWith(c.file + ':'))).map(c => c.file), requestExample: Object.fromEntries(parameters.map(p => [p.name, p.example])), responseExample: first.helper === 'apiBinary' ? '<Blob: 二进制产物>' : returnType ? typeExample(returnType, 'response') : '<见返回类型>', exampleNotice: '合成脱敏参数与响应结构示例，字段枚举及业务校验以类型和后端处理器为准' })
})
for (const item of http) item.refreshRules = usageContracts.get(item.name) ?? []
for (const [file, sf] of parsed) {
  if (file === apiFile) continue
  walk(sf, node => {
    if (ts.isCallExpression(node) && ['api', 'apiBinary'].includes(node.expression.getText(sf))) {
      const detail = methodDetails(node, sf)
      const typedSource = program.getSourceFile(path.join(root, file))
      let responseExample = '<见返回类型>'
      walk(typedSource, child => { if (ts.isCallExpression(child) && child.getStart(typedSource) === node.getStart(sf)) responseExample = typeExample(checker.getAwaitedType(checker.getTypeAtLocation(child)), 'response') })
      http.push({ name: `${path.basename(file)}:direct:${sf.getLineAndCharacterOfPosition(node.getStart()).line + 1}`, group: group(detail.path), status: 'used', implementation: 'existing', location: location(node, sf), callsites: [location(node, sf)], parameters: [], responseType: detail.responseType, endpoints: [detail], authentication: 'desktop-token', responseKind: 'json-or-204', errorBehavior: 'ApiError; OCR health.available=false -> Error(message)', refresh: [file], requestExample: detail.requestBodyExpression, exampleNotice: '请求表达式直接来自源码；取消选择文件不发出安装请求' })
      http.at(-1).responseExample = responseExample
    }
  })
}
const mismatches = []
for (const item of http) {
  item.refreshRules = usageContracts.get(item.name) ?? []
  item.directCallerContexts = item.refreshRules.length ? [] : directContexts.get(item.name) ?? []
}
for (const item of http) for (const endpoint of item.endpoints) {
  const methods = Array.isArray(endpoint.method) ? endpoint.method : [endpoint.method]
  endpoint.backendRoutes = methods.map(method => backend.find(route => route.method === method && canonical(route.path) === canonical(endpoint.path))).filter(Boolean)
  if (endpoint.backendRoutes.length !== methods.length) mismatches.push(`${item.name}: ${methods.join('/')} ${endpoint.path}`)
  if (endpoint.backendRoutes.some(r => r.authentication !== item.authentication && r.authentication !== 'public')) mismatches.push(`${item.name}: wrong authentication`)
}
if (mismatches.length) throw new Error('接口与实际路由不一致:\n' + mismatches.join('\n'))
const ipc = []
const desktopContracts = new Map()
for (const file of [`${dir}/env.d.ts`, 'apps/desktop/src/shared/updates.ts']) {
  const sf = ts.createSourceFile(file, read(file), ts.ScriptTarget.Latest, true)
  walk(sf, node => {
    if (ts.isMethodSignature(node)) desktopContracts.set(node.name.getText(sf), {
      parameters: node.parameters.map(p => ({ name: p.name.getText(sf), type: p.type?.getText(sf) ?? 'unknown', optional: Boolean(p.questionToken), default: p.name.getText(sf) === 'kind' ? 'undefined (普通导入文件筛选)' : null })),
      responseType: node.type?.getText(sf) ?? 'unknown'
    })
  })
}
const preloadFile = 'apps/desktop/src/preload/index.ts'
const preload = ts.createSourceFile(preloadFile, read(preloadFile), ts.ScriptTarget.Latest, true)
function bridgeProperties(object, prefix = '') {
  for (const property of object.properties) {
    if (!ts.isPropertyAssignment(property)) continue
    const name = prefix + property.name.getText(preload)
    if (ts.isObjectLiteralExpression(property.initializer)) { bridgeProperties(property.initializer, name + '.'); continue }
    const channels = [], subscriptions = []
    walk(property.initializer, n => {
      if (ts.isCallExpression(n) && n.arguments[0] && ts.isStringLiteral(n.arguments[0])) {
        if (n.expression.getText(preload) === 'ipcRenderer.invoke') channels.push(n.arguments[0].text)
        if (n.expression.getText(preload) === 'ipcRenderer.on') subscriptions.push(n.arguments[0].text)
      }
    })
    const contract = desktopContracts.get(name.split('.').at(-1)) ?? {}
    ipc.push({ name: `window.kah.${name}`, group: name.startsWith('updates.') ? '应用更新' : '桌面能力', status: desktopCalls.has(name) ? 'used' : 'wrapped-unused', implementation: 'existing', location: location(property, preload), signature: property.initializer.getText(preload), ...contract, channels, events: subscriptions, authentication: 'Electron contextBridge 暴露给应用渲染器；不接收更新源/任意安装文件', responseKind: subscriptions.length ? 'event-subscription' : 'ipc-promise', errorBehavior: name.startsWith('updates.') ? '检查/下载/预检失败返回 status=error 的 UpdateState；禁用模式返回 disabled；不可安装或重复操作不启动安装器' : name === 'restoreBackup' ? 'IPC拒绝则保留错误提示；成功会重启应用服务' : 'IPC Promise拒绝需展示；取消原生对话框返回空数组/空字符串；openPath 非空字符串为系统错误', refresh: name === 'restoreBackup' ? ['应用重启后重新读取全部服务数据'] : name.startsWith('updates.') ? ['updates:state-changed；getState 初始快照不能覆盖晚到事件'] : name === 'runtimeConfig' ? ['启动配置与底栏版本'] : ['成功后由调用页面执行操作并刷新对应资源'], requestExample: Object.fromEntries((contract.parameters ?? []).map(p => [p.name, /callback/.test(p.name) ? '<函数：卸载时取消订阅>' : /data/.test(p.name) ? '<字符串或Uint8Array>' : /url/.test(p.name) ? 'https://example.invalid/' : /path/i.test(p.name) ? 'E:/AKB-example/sample.md' : 'light'])), responseExample: name.startsWith('updates.') && !subscriptions.length ? { enabled: true, status: 'idle', currentVersion: '0.1.2' } : name === 'runtimeConfig' ? { apiBaseUrl: 'http://127.0.0.1:48761/api/v1', desktopToken: '[REDACTED]', dataRoot: 'E:/AKB-example/data', version: '0.1.2', applicationMode: 'installed', updatesEnabled: true } : '<见 responseType；合成契约示例不作为实测结果>', unsubscribe: subscriptions.length ? '调用返回的函数移除对应 ipcRenderer listener；组件卸载必须执行' : null })
    ipc.at(-1).callsites = desktopCalls.get(name) ?? []
  }
}
walk(preload, n => { if (ts.isCallExpression(n) && n.expression.getText(preload) === 'contextBridge.exposeInMainWorld') bridgeProperties(n.arguments[1]) })
const unseenBackend = backend.filter(route => !http.some(item => item.endpoints.some(endpoint => endpoint.backendRoutes.some(r => r.path === route.path && r.method === route.method)))).map(route => ({ ...route, status: route.path.includes('/runtime/shutdown') ? 'main-process-only' : 'backend-only' }))
const schemaFiles = [`${dir}/types.ts`, `${dir}/env.d.ts`, 'apps/desktop/src/shared/updates.ts']
const schemas = schemaFiles.map(file => ({ file, source: read(file) }))
for (const [file, sf] of parsed) if (!schemaFiles.includes(file)) {
  const localTypes = sf.statements.filter(node => ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node)).map(node => node.getText(sf))
  if (localTypes.length) schemas.push({ file, source: localTypes.join('\n\n') })
}
const persistence = []
for (const [file, sf] of parsed) {
  walk(sf, node => {
    if (ts.isVariableDeclaration(node) && /STORAGE_KEY/.test(node.name.getText(sf))) persistence.push({ file, name: node.name.getText(sf), value: node.initializer?.getText(sf) })
    if (ts.isCallExpression(node) && /localStorage\.(getItem|setItem)|persist$/.test(node.expression.getText(sf))) persistence.push({ file, expression: node.getText(sf), location: location(node, sf) })
  })
}
const inventoryPath = 'docs/frontend-interface-inventory.json'
const existing = fs.existsSync(path.join(root, inventoryPath)) ? JSON.parse(read(inventoryPath)) : undefined
const gitHead = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim()
const gitStatus = execFileSync('git', ['status', '--short'], { cwd: root, encoding: 'utf8' }).trim()
const inventory = { schemaVersion: 1, snapshot: { generatedAt: new Date().toISOString(), gitHead, dirty: Boolean(gitStatus), gitStatus, examplePolicy: 'synthetic redacted examples; no runtime credentials' }, counts: { clientMethods: http.filter(i => !i.name.includes(':direct:')).length, directCalls: http.filter(i => i.name.includes(':direct:')).length, desktopInterfaces: ipc.length, backendRoutes: backend.length }, http, desktop: ipc, backendOnly: unseenBackend, components, cacheContracts, persistence, schemas, plannedInterfaces: [], resolvedDifferences: [
  '补齐 GET /api/v1/skill-mapping-targets/:id；旧 client 调用缺少实际后端路由。',
  '4 个架构 Agent 租约方法显式接收专用 secret；不再使用 desktopToken。',
  'OCR install/PATCH/DELETE 均同步返回 OcrPlugin JSON，已补齐泛型类型；不是导入 Job 或204响应。',
  'embeddingModels 归一化 {models:[]} 与数组响应，重构应复用 client。',
  '更新桥接已实现，installation marker 仅 NSIS 安装时创建。'
] }
const checking = process.argv.includes('--check')
if (checking) {
  if (!existing) throw new Error('接口清单尚未生成')
  inventory.snapshot = existing.snapshot
  if (JSON.stringify(inventory) !== JSON.stringify(existing)) throw new Error('接口清单已过期，请执行 pnpm docs:interfaces')
  console.log(`INTERFACE_COVERAGE_PASS client=${inventory.counts.clientMethods} direct=${inventory.counts.directCalls} IPC=${ipc.length} route/auth mismatches=0`)
}
if (!checking) fs.writeFileSync(path.join(root, inventoryPath), JSON.stringify(inventory, null, 2) + '\n')
const text = []
text.push('# AKB 前端接口与重构交接', '', `生成时间：${inventory.snapshot.generatedAt}。基线提交：\`${inventory.snapshot.gitHead}\`。工作区：${inventory.snapshot.dirty ? '有未提交实现；以下内容依据当前源码生成' : '干净'}。精确文件状态保存在机器清单 snapshot.gitStatus。`, '', '## 接入原则', '',
  '这是功能重构契约，不固定视觉方案或内部组件结构。必须保留每项操作入口、快捷键、选择上下文、原生文件操作、权限边界和未保存草稿保护。导航重组时提供旧入口到新入口的映射。',
  'HTTP 基址由 window.kah.runtimeConfig() 返回，默认 http://127.0.0.1:48761/api/v1。桌面接口使用本次进程 desktopToken；不得硬编码、写入文档或本地存储。架构 Agent lease 接口必须传入注册时返回的专用 secret；MCP token 不可替代。',
  '所有示例为合成、脱敏参数；不是业务验收数据。类型字段见末尾完整 TypeScript 定义，请求体表达式和实际后端处理器位于机器清单。不要依据方法存在推断已有页面入口。',
  '普通请求通过 api<T>；204 返回 undefined；失败抛 ApiError(status,code,retryable)，JSON 错误包括 requestId 时消息附加请求 ID。apiBinary 返回 Blob，其错误消息不附加 requestId。更新及原生系统能力通过 IPC，不是 REST。',
  '现有 hash 路由仅通配 App，功能切换通过 activeSection，不存在独立的 /skills 或 /settings 页面路由。知识管理、图谱、项目架构、Skills、审核、回收站和设置入口都需保留。', '',
  `覆盖：${inventory.counts.clientMethods} 个 client 方法、${inventory.counts.directCalls} 处直接 HTTP 调用、${ipc.length} 个桌面接口。所有前端调用已核对实际路由及鉴权，差异 0。`, '',
  '## 页面调用流程与状态约束', '',
  '| 功能 | 调用与行为 | 验收 |', '|---|---|---|',
  '| 知识库/来源 | libraries → documents/folders；selectFiles → importFiles → jobs 轮询；取消文件选择不发请求；文档编辑保存后刷新列表和详情 | 导入、标签/标题/正文编辑、收藏、批量、目录归属、源重连 |',
  '| 成型知识/检索 | knowledgeSearch 目录 → knowledge 详情；retrievalSearch/query → 引用跳转；createKnowledgeRevision → 审核，不直接写正式知识 | 来源与知识切换、筛选、跨库范围、引用回跳、空态与错误 |',
  '| 审核 | submissions → submission → approveSubmission/rejectSubmission；批准后刷新相关知识和任务，拒绝需理由 | 版本对照、来源、人工批准/拒绝 |',
  '| 图谱 | graph → 选择节点/Inspector；saveGraphPositions 与 autoLayoutGraph 保存后刷新 graph | 库 A→B→A、布局保存、知识/文档跳转 |',
  '| 项目架构 | projects → snapshots → analysis/diff/artifacts；scan → tasks 轮询 → generate；产物 Blob → Uint8Array → saveFile | 生成失败保留旧快照；取消、重试、版本切换、导出 |',
  '| Skills | selectFiles → importSkill(内部250ms轮询，最多600秒) → skills/manifest；links 修改后刷新 skills/detail | 导入错误、入口文件、关联库、删除 |',
  '| 外部映射 | targets → target；选择目录 → 创建；验证/修复/移除后刷新列表与详情 | 真正符号链接、移除与忘记记录区别 |',
  '| 设置 | provider 保存/模型枚举/测试；embeddingModels 归一化响应；OCR 组件直接 API | 设置各分类功能均保留；本地资源不静默下载 |',
  '| 备份恢复 | backup → backups → verifyBackup；selectBackupCandidate 复制到临时备份相对路径 → restoreBackup → 服务重启/页面重载 | 数据路径合法；失败回滚；不能与安装更新并发 |',
  '| 更新 | updates 状态订阅 → 后台检查/下载 → 用户确认 → 草稿保护 → install → 主进程 shutdown 预检 | 安装版显示；其他模式不检查、不下载、不显示入口 |',
  '| 回收站/活动 | trash → restore/purge/empty；jobs → cancel/retry → 刷新 | 软删除/永久删除区分，取消是协作状态 |', '',
  '未保存草稿：knowledgeDraftDirty/documentDraftDirty 与 beforeunload、confirmUnsavedDraftLeave 联动；切换选择、页面和更新安装必须保留保护。',
  '默认 QueryClient：retry=1、staleTime=10000、refetchOnWindowFocus=false。完整 queryKey、轮询和 invalidate/refetch 表见下方；修改资源后不要只更新视觉状态。旧库请求响应不得覆盖新库选择。',
  '外观：kah-ui 存储 theme(light/dark/system)、density(comfortable/compact) 和旧本地 savedSearches；服务器 saved-searches 是独立资源，不可混为一个状态。详情栏宽度和侧栏收起状态单独存储。',
  '详情栏默认420px，最小330px、最大720px，并随当前窗口可用宽度夹取；支持鼠标拖动及ArrowLeft/ArrowRight/Home/End键盘调整。知识修订草稿使用按知识URI隔离的本机键，关闭与页面切换不能绕过未保存确认。',
  '桌面窗口默认1440×900、最小1040×680。检查亮色/暗色/系统主题、两种密度、键盘焦点、加载/空态/错误、Windows125%/150%缩放。仅静态截图不能替代实际接口操作。', '',
  '## 前端 Agent 的逐功能验收记录', '',
  '| 功能组 | 重构验收要求 | 重构后状态 |', '|---|---|---|',
  '| 应用框架 | 所有旧入口、命令面板、快捷键、底栏版本、异常提示；提交旧入口→新入口映射 | 待重构验收 |',
  '| 知识库与来源 | 管理、导入/编辑、批量、目录、收藏、标签、监视和源重连 | 待重构验收 |',
  '| 检索与知识 | 来源/知识切换、统一检索、回答与引用、修订、固定搜索；切换库不串数据 | 待重构验收 |',
  '| 审核与回收站 | 提交详情、版本比较、批准/拒绝、恢复和永久删除确认 | 待重构验收 |',
  '| 图谱 | 选择/取消、布局、位置、视口、跳转；A→B→A无旧选择残留 | 待重构验收 |',
  '| 项目架构 | 配置、扫描/生成、轮询与取消、快照/差异、产物查看/原生导出 | 待重构验收 |',
  '| Skills与映射 | 导入/详情/关联库、目标验证/修复/移除；专用Agent凭证隔离 | 待重构验收 |',
  '| 设置与维护 | 外观、模型/检索模型/OCR、令牌、备份恢复、索引和审计 | 待重构验收 |',
  '| 更新与桌面 | 原生选择/保存/打开、监听释放、后台退出；仅安装版更新、草稿保护及任务预检 | 待重构验收 |',
  '| 共通状态 | 三主题×两密度；键盘/焦点、加载/空/错误、取消/重试、窗口尺寸及125%/150%缩放 | 待重构验收 |', '',
  '## HTTP 方法清单', '')
for (const category of [...new Set(http.map(i => i.group))]) {
  text.push(`### ${category}`, '')
  for (const item of http.filter(i => i.group === category)) {
    text.push(`#### ${item.name}`, '', `- 状态：${item.status === 'used' ? '正在使用' : '已封装但未使用'}；鉴权：${item.authentication}；定义：\`${item.location}\`。`, `- 参数：\`${item.signature ?? item.endpoints[0].requestBodyExpression ?? '见请求表达式'}\`。`, `- 返回：\`${item.responseType}\`；传输：${item.responseKind}；错误：${item.errorBehavior}。`, `- 调用位置：${item.callsites.length ? item.callsites.map(s => `\`${s}\``).join('、') : '无现有页面调用；不得擅自当作已接入功能'}。`)
    for (const endpoint of item.endpoints) text.push(`- 路由：\`${Array.isArray(endpoint.method) ? endpoint.method.join('/') : endpoint.method} ${endpoint.path}\`；请求体：\`${endpoint.requestBodyExpression ?? '无'}\`；后端：${endpoint.backendRoutes.map(r => `\`${r.location}\` (${r.handler})`).join('、')}。`)
    if (item.refreshRules?.length) text.push('', '对应页面查询键/成功刷新回调（onSaved等回调由父组件提供，重构时必须继续执行）：', '', '```json', JSON.stringify(item.refreshRules, null, 2), '```')
    else if (item.directCallerContexts?.length) text.push('', '直接操作的调用/成功刷新/本地状态流程：', '', '```json', JSON.stringify(item.directCallerContexts, null, 2), '```')
    text.push(`- 刷新上下文：${item.refresh.map(f => `\`${f}\``).join('、') || '无页面调用；调用者负责刷新'}；精确刷新表达式见缓存表。`, '', '合成脱敏参数示例：', '', '```json', JSON.stringify(item.requestExample, null, 2), '```', '', '合成响应结构示例（不是实测响应；复杂嵌套字段见完整类型定义）：', '', '```json', JSON.stringify(item.responseExample ?? '<见返回类型与后端处理器>', null, 2), '```', '')
  }
}
text.push('## 桌面 IPC 与事件', '')
for (const item of ipc) text.push(`### ${item.name}`, '', `状态：${item.status === 'used' ? '正在使用' : '已封装但未使用'}。通道：${item.channels.join(', ') || '事件订阅'}。事件：${item.events.join(', ') || '无'}。定义：\`${item.location}\`。`, `鉴权：${item.authentication}。返回：\`${item.responseType}\`；传输：${item.responseKind}。`, `错误：${item.errorBehavior}。刷新：${item.refresh.join('；')}。`, '', '```ts', item.signature, '```', '', '合成脱敏参数/响应契约：', '', '```json', JSON.stringify({ parameters: item.parameters, requestExample: item.requestExample, responseExample: item.responseExample }, null, 2), '```', '', `调用：${item.callsites.join('、') || '尚无页面调用'}。${item.unsubscribe ?? ''}`, '')
text.push('## 后端存在但未通过普通 client 接入', '', '| 方法 | 路径 | 鉴权/归属 | 定义 |', '|---|---|---|---|')
for (const route of unseenBackend) text.push(`| ${route.method} | \`${route.path}\` | ${route.authentication}; ${route.status} | ${route.location} |`)
text.push('', '此表包含 SSE /query/stream、Skill 文件流及 MCP；未接入状态不授权新增功能。runtime/shutdown 专供主进程，前端不得直接调用。', '', '## 组件输入和回调', '', '| 组件 | 定义 | 输入参数与回调契约 |', '|---|---|---|')
for (const item of components) text.push(`| ${item.name} | ${item.location} | \`${item.parameters.join('; ').replaceAll('|', '&#124;').replaceAll('\n', ' ')}\` |`)
text.push('', '## 缓存、轮询与刷新', '')
for (const item of cacheContracts) {
  text.push(`### ${item.file}`, '', '```json', JSON.stringify(item, null, 2), '```', '')
}
text.push('## 持久化键', '', '```json', JSON.stringify(persistence, null, 2), '```', '', '## 完整共享数据类型', '')
for (const item of schemas) text.push(`### ${item.file}`, '', '```ts', item.source.trim(), '```', '')
text.push('## 已处理的接口差异与交接门禁', '', ...inventory.resolvedDifferences.map(s => `- ${s}`), '', '新增更新 IPC 和 runtime/shutdown 已实现，plannedInterfaces=[]；真实升级/人工界面验收状态另见 AKB优化计划.md。', '', '重构前后运行 pnpm verify:interfaces、TypeScript 和前端/Go 测试；逐项验证上方功能表。对原有接口签名或状态做改变时同步重新生成清单与交接文档，不能只改机器清单。', '')
const markdown = text.join('\n')
if (checking) {
  if (read('docs/前端接口与重构交接.md') !== markdown) throw new Error('交接 Markdown 与接口清单不同步，请执行 pnpm docs:interfaces')
  console.log('HANDOFF_MARKDOWN_PASS')
} else {
  fs.writeFileSync(path.join(root, 'docs/前端接口与重构交接.md'), markdown)
  console.log(`Generated handoff: client=${inventory.counts.clientMethods}, direct=${inventory.counts.directCalls}, IPC=${ipc.length}, backend=${backend.length}`)
}
