const fs = require('node:fs')
const WebSocket = require('../node_modules/.pnpm/ws@8.21.3/node_modules/ws')

const port = Number(process.env.KAH_ACCEPTANCE_CDP_PORT || 9225)
const evidenceRoot = process.env.KAH_ACCEPTANCE_EVIDENCE || 'E:/AI/agent-knownledge-base/.run-data/ocr-complex-ui-acceptance'
const libraryId = 'c57e0fde-6128-4d28-aa10-58d191d45586'
const libraryName = '复杂版式 OCR 验收库'
const documentTitle = 'ocr-complex-layout-acceptance.pdf'
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function main() {
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  const target = targets.find((item) => item.type === 'page' && item.title === 'Knowledge Agent Hub')
  if (!target) throw new Error('Renderer target not found')
  const socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { socket.once('open', resolve); socket.once('error', reject) })
  let id = 0
  const pending = new Map()
  socket.on('message', (data) => {
    const message = JSON.parse(String(data))
    const operation = pending.get(message.id)
    if (!operation) return
    pending.delete(message.id)
    message.error ? operation.reject(message.error) : operation.resolve(message.result)
  })
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const next = ++id
    pending.set(next, { resolve, reject })
    socket.send(JSON.stringify({ id: next, method, params }))
  })
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true })
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text)
    return result.result.value
  }
  const waitFor = async (expression, label, timeout = 45000) => {
    const deadline = Date.now() + timeout
    while (Date.now() < deadline) {
      if (await evaluate(`Boolean(${expression})`)) return
      await delay(250)
    }
    throw new Error(`Timed out waiting for ${label}`)
  }
  const click = async (selector, label) => {
    const point = await evaluate(`(() => {
      const element = [...document.querySelectorAll(${JSON.stringify(selector)})].find((item) => item.textContent.includes(${JSON.stringify(label)}))
      if (!element || element.disabled) return null
      const rect = element.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
    })()`)
    if (!point) throw new Error(`${label} control unavailable`)
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', clickCount: 1 })
  }
  const screenshot = async (name) => {
    const result = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    const path = `${evidenceRoot}/${name}`
    fs.writeFileSync(path, Buffer.from(result.data, 'base64'))
    return path
  }
  const api = (path) => evaluate(`(async () => {
    const config = await window.kah.runtimeConfig()
    const response = await fetch(config.apiBaseUrl + ${JSON.stringify(path)}, { headers: { Authorization: 'Bearer ' + config.desktopToken } })
    if (!response.ok) throw new Error(await response.text())
    return response.json()
  })()`)

  await click('.library-row', '待审核')
  await waitFor(`[...document.querySelectorAll('.document-row')].some((item) => item.textContent.includes('ocr-complex-layout-acceptance'))`, 'pending KAH row')
  await click('.document-row', 'ocr-complex-layout-acceptance')
  await waitFor(`document.body.innerText.includes('kah://document/3de8f1d1-36f1-4c58-ae7f-82d7e9f7ccf3')`, 'KAH provenance detail')
  const pendingReviewScreenshot = await screenshot('06-kah-pending-review.png')

  await click('button', '设置')
  await waitFor(`document.querySelector('[role="dialog"]')?.textContent.includes('设置')`, 'settings dialog')
  await click('[role="tab"]', '可选插件')
  await waitFor(`document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('已安装')`, 'installed OCR plugin')
  await click('[aria-label="OCR 插件"] button', '卸载 OCR 插件')
  await waitFor(`document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('未安装')`, 'plugin uninstall', 60000)
  const uninstalledScreenshot = await screenshot('07-plugin-uninstalled.png')

  const plugin = await api('/plugins/ocr')
  const documents = await api(`/documents?libraryId=${libraryId}`)
  const submissions = await api(`/knowledge/submissions?libraryId=${libraryId}`)
  const document = documents.find((item) => item.title === documentTitle)
  const submission = submissions.find((item) => item.title === 'ocr-complex-layout-acceptance')
  if (plugin.installed || plugin.enabled) throw new Error('OCR plugin still reports installed or enabled')
  if (!document || document.status !== 'ready') throw new Error('Imported document was not preserved after uninstall')
  if (!submission || submission.reviewStatus !== 'pending_review') throw new Error('KAH submission was not preserved after uninstall')

  socket.close()
  console.log(JSON.stringify({
    rendererTitle: target.title,
    plugin,
    preservedDocument: { id: document.id, status: document.status, contentHash: document.contentHash },
    preservedSubmission: { id: submission.id, knowledgeUri: submission.knowledgeUri, reviewStatus: submission.reviewStatus },
    pendingReviewScreenshot,
    uninstalledScreenshot
  }, null, 2))
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1 })
