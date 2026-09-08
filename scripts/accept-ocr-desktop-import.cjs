const fs = require('node:fs')
const WebSocket = require('../node_modules/.pnpm/ws@8.21.3/node_modules/ws')

const port = Number(process.env.KAH_ACCEPTANCE_CDP_PORT || 9225)
const evidenceRoot = process.env.KAH_ACCEPTANCE_EVIDENCE || 'E:/AI/agent-knownledge-base/.run-data/ocr-complex-ui-acceptance'
const libraryName = '复杂版式 OCR 验收库'
const documentTitle = 'ocr-complex-layout-acceptance.pdf'

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function connect() {
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  const target = targets.find((item) => item.type === 'page' && item.title === 'Knowledge Agent Hub')
  if (!target) throw new Error('Knowledge Agent Hub renderer target was not found')
  const socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { socket.once('open', resolve); socket.once('error', reject) })
  let sequence = 0
  const pending = new Map()
  socket.on('message', (data) => {
    const message = JSON.parse(String(data))
    const operation = pending.get(message.id)
    if (!operation) return
    pending.delete(message.id)
    message.error ? operation.reject(new Error(message.error.message)) : operation.resolve(message.result)
  })
  const send = (method, params = {}) => {
    const id = ++sequence
    const result = new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
    socket.send(JSON.stringify({ id, method, params }))
    return result
  }
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true })
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
    return result.result.value
  }
  return { socket, send, evaluate, target }
}

async function waitFor(evaluate, expression, label, timeout = 45000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await evaluate(`Boolean(${expression})`)) return
    await delay(300)
  }
  throw new Error(`Timed out waiting for ${label}`)
}

async function screenshot(send, name) {
  const result = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
  const path = `${evidenceRoot}/${name}`
  fs.writeFileSync(path, Buffer.from(result.data, 'base64'))
  return path
}

async function pointFor(evaluate, selector, label, exact = false) {
  return evaluate(`(() => {
    const element = [...document.querySelectorAll(${JSON.stringify(selector)})]
      .find((item) => ${exact ? 'item.textContent.trim() ===' : 'item.textContent.includes('}${JSON.stringify(label)}${exact ? '' : ')'})
    if (!element || element.disabled) return null
    const rect = element.getBoundingClientRect()
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
  })()`)
}

async function trustedClick(send, evaluate, selector, label, exact = false) {
  const point = await pointFor(evaluate, selector, label, exact)
  if (!point) throw new Error(`${label} control unavailable`)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 })
}

async function typeInto(send, evaluate, selector, value) {
  const point = await evaluate(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)})
    if (!element) return null
    const rect = element.getBoundingClientRect()
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
  })()`)
  if (!point) throw new Error(`Input unavailable: ${selector}`)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 })
  await evaluate(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)})
    const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, ${JSON.stringify(value)})
    element.dispatchEvent(new Event('input', { bubbles: true }))
    return element.value
  })()`)
}

const apiExpression = (path) => `(async () => {
  const config = await window.kah.runtimeConfig()
  const response = await fetch(config.apiBaseUrl + ${JSON.stringify(path)}, {
    headers: { Authorization: 'Bearer ' + config.desktopToken }
  })
  if (!response.ok) throw new Error(await response.text())
  return response.json()
})()`

async function main() {
  fs.mkdirSync(evidenceRoot, { recursive: true })
  const { socket, send, evaluate, target } = await connect()
  await send('Page.enable')

  if (await evaluate(`Boolean(document.querySelector('[role="dialog"]'))`)) {
    const closePoint = await evaluate(`(() => {
      const dialog = document.querySelector('[role="dialog"]')
      const button = [...dialog.querySelectorAll('button')].find((item) => item.getAttribute('aria-label') === '关闭')
      if (!button) return null
      const rect = button.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
    })()`)
    if (closePoint) {
      await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...closePoint, button: 'left', clickCount: 1 })
      await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...closePoint, button: 'left', clickCount: 1 })
    } else {
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape' })
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape' })
    }
    await waitFor(evaluate, `!document.querySelector('[role="dialog"]')`, 'dialog close')
  }

  let libraries = await evaluate(apiExpression('/libraries'))
  let library = libraries.find((item) => item.name === libraryName)
  if (!library) {
    const createPoint = await evaluate(`(() => {
      const button = document.querySelector('button[aria-label="新建知识库"]')
      if (!button) return null
      const rect = button.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
    })()`)
    if (!createPoint) throw new Error('新建知识库 control unavailable')
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...createPoint, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...createPoint, button: 'left', clickCount: 1 })
    await waitFor(evaluate, `document.querySelector('[role="dialog"]')?.textContent.includes('新建知识库')`, 'create library dialog')
    await typeInto(send, evaluate, '[role="dialog"] input', libraryName)
    await typeInto(send, evaluate, '[role="dialog"] textarea', '真实窗口复杂版式 PDF OCR 验收，使用隔离数据目录。')
    await trustedClick(send, evaluate, '[role="dialog"] button', '创建知识库', true)
    await waitFor(evaluate, `!document.querySelector('[role="dialog"]')`, 'library creation', 30000)
    await waitFor(evaluate, `[...document.querySelectorAll('.library-row')].some((item) => item.textContent.includes(${JSON.stringify(libraryName)}))`, 'new library sidebar row', 30000)
    libraries = await evaluate(apiExpression('/libraries'))
    library = libraries.find((item) => item.name === libraryName)
  }
  if (!library) throw new Error('Acceptance library was not found after creation')

  await trustedClick(send, evaluate, '.library-row', libraryName)
  await waitFor(evaluate, `document.querySelector('.workspace-title-line h1')?.textContent === ${JSON.stringify(libraryName)}`, 'library selection')
  await trustedClick(send, evaluate, '[aria-label="知识库内容视图"] button', '来源文档', true)

  const existing = await evaluate(apiExpression(`/documents?libraryId=${encodeURIComponent(library.id)}`))
  if (!existing.some((item) => item.title === documentTitle)) {
    await trustedClick(send, evaluate, '.header-actions button', '导入', true)
    console.log(JSON.stringify({ phase: 'native-import-dialog-opened', libraryId: library.id }))
  }

  await waitFor(evaluate, `[...document.querySelectorAll('.result-card')].some((item) => item.textContent.includes(${JSON.stringify(documentTitle)}))`, 'imported document card', 240000)
  await waitFor(evaluate, `!document.querySelector('.pipeline-status')?.textContent.match(/处理中|等待中|正在/)`, 'pipeline completion', 240000)
  const documents = await evaluate(apiExpression(`/documents?libraryId=${encodeURIComponent(library.id)}`))
  const submissions = await evaluate(apiExpression(`/knowledge/submissions?libraryId=${encodeURIComponent(library.id)}`))
  const jobs = await evaluate(apiExpression('/jobs'))
  const document = documents.find((item) => item.title === documentTitle)
  if (!document) throw new Error('Imported document missing from API')
  const relatedSubmissions = submissions.filter((item) => JSON.stringify(item).includes(document.id) || JSON.stringify(item).includes(documentTitle))
  if (!relatedSubmissions.length) throw new Error('No source-linked KAH submission was generated')

  await trustedClick(send, evaluate, '.result-card', documentTitle)
  await waitFor(evaluate, `document.body.innerText.includes('OCR') || document.body.innerText.includes('扫描')`, 'document detail content', 30000)
  const screenshotPath = await screenshot(send, '04-complex-pdf-imported.png')
  socket.close()
  console.log(JSON.stringify({
    rendererTitle: target.title,
    library,
    document,
    relatedSubmissions,
    relatedJobs: jobs.filter((item) => JSON.stringify(item).includes(document.id) || JSON.stringify(item).includes(documentTitle)),
    screenshotPath
  }, null, 2))
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1 })
