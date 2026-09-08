const fs = require('node:fs')
const WebSocket = require('../node_modules/.pnpm/ws@8.21.3/node_modules/ws')

const port = Number(process.env.KAH_ACCEPTANCE_CDP_PORT || 9225)
const evidenceRoot = process.env.KAH_ACCEPTANCE_EVIDENCE || 'E:/AI/agent-knownledge-base/.run-data/ocr-complex-ui-acceptance'

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
    await delay(250)
  }
  throw new Error(`Timed out waiting for ${label}`)
}

async function screenshot(send, name) {
  const result = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
  const path = `${evidenceRoot}/${name}`
  fs.writeFileSync(path, Buffer.from(result.data, 'base64'))
  return path
}

async function trustedClick(send, evaluate, selector, label) {
  const point = await evaluate(`(() => {
    const element = [...document.querySelectorAll(${JSON.stringify(selector)})]
      .find((item) => item.textContent.includes(${JSON.stringify(label)}))
    if (!element || element.disabled) return null
    const rect = element.getBoundingClientRect()
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
  })()`)
  if (!point) throw new Error(`${label} control unavailable`)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 })
}

async function main() {
  fs.mkdirSync(evidenceRoot, { recursive: true })
  const { socket, send, evaluate, target } = await connect()
  await send('Page.enable')
  if (!await evaluate(`Boolean(document.querySelector('[role="dialog"]'))`)) {
    await trustedClick(send, evaluate, 'button', '设置')
  }
  await waitFor(evaluate, `document.querySelector('[role="dialog"]')`, 'settings dialog')
  await trustedClick(send, evaluate, '[role="tab"]', '可选插件')
  await waitFor(evaluate, `document.querySelector('[aria-label="OCR 插件"]')`, 'OCR plugin panel')
  await waitFor(
    evaluate,
    `document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('未安装') || document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('已安装')`,
    'OCR plugin state'
  )
  const initial = await evaluate(`document.querySelector('[aria-label="OCR 插件"]')?.innerText`)
  const firstScreenshot = await screenshot(send, '01-plugin-before-install.png')
  if (initial.includes('未安装')) {
    await trustedClick(send, evaluate, '[aria-label="OCR 插件"] button', '安装 OCR 离线包')
    console.log(JSON.stringify({ phase: 'native-install-dialog-opened', firstScreenshot }))
    await waitFor(evaluate, `document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('已安装并启用')`, 'plugin installation', 180000)
  } else if (!initial.includes('已安装并启用')) {
    throw new Error(`Unexpected plugin state: ${initial}`)
  }
  const installedScreenshot = await screenshot(send, '02-plugin-installed.png')
  const click = async (label) => {
    await trustedClick(send, evaluate, '[aria-label="OCR 插件"] button', label)
  }
  await click('验证 OCR 引擎')
  await waitFor(evaluate, `document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('OCR 引擎验证通过')`, 'engine verification', 30000)
  await click('停用 OCR')
  await waitFor(evaluate, `document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('已安装，已停用')`, 'plugin disable')
  await click('启用 OCR')
  await waitFor(evaluate, `document.querySelector('[aria-label="OCR 插件"]')?.innerText.includes('已安装并启用')`, 'plugin enable')
  const finalText = await evaluate(`document.querySelector('[aria-label="OCR 插件"]')?.innerText`)
  const finalScreenshot = await screenshot(send, '03-plugin-verified-reenabled.png')
  socket.close()
  console.log(JSON.stringify({
    rendererTitle: target.title,
    initialText: initial,
    finalText,
    installedScreenshot,
    finalScreenshot
  }, null, 2))
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1 })
