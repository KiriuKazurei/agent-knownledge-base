const fs = require('node:fs')
const WebSocket = require('../node_modules/.pnpm/ws@8.21.3/node_modules/ws')

const port = Number(process.env.KAH_ACCEPTANCE_CDP_PORT || 9225)
const evidenceRoot = process.env.KAH_ACCEPTANCE_EVIDENCE || 'E:/AI/agent-knownledge-base/.run-data/workspace-switch-acceptance'
const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds))

async function main() {
  fs.mkdirSync(evidenceRoot, { recursive: true })
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
  const waitFor = async (expression, label) => {
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      if (await evaluate(`Boolean(${expression})`)) return
      await delay(200)
    }
    throw new Error(`Timed out waiting for ${label}`)
  }
  const click = async (label) => {
    const point = await evaluate(`(() => {
      const element = [...document.querySelectorAll('button')].find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)} || item.textContent.replace(/\\s+/g, '').includes(${JSON.stringify(label.replace(/\s+/g, ''))}))
      if (!element || element.disabled) return null
      const rect = element.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
    })()`)
    if (!point) throw new Error(`${label} button is unavailable`)
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', clickCount: 1 })
  }
  const screenshot = async (name) => {
    const image = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    const output = `${evidenceRoot}/${name}`
    fs.writeFileSync(output, Buffer.from(image.data, 'base64'))
    return output
  }

  const results = []
  await click('关系图谱稳定知识与证据关系')
  await waitFor(`document.querySelector('[aria-label="关系图谱"]')`, 'graph workspace')
  if (await evaluate(`document.body.innerText.includes('请先选择或创建知识库')`)) {
    const graphScreenshot = await screenshot('workspace-graph-empty.png')
    await click('从关系图谱新建知识库')
    await waitFor(`document.querySelector('[role="dialog"]')?.textContent.includes('新建知识库')`, 'create-library dialog')
    results.push({ button: '关系图谱稳定知识与证据关系', state: 'empty-library-guidance', screenshot: graphScreenshot, dialogOpened: true })
    socket.close()
    console.log(JSON.stringify({ title: target.title, results }, null, 2))
    return
  }
  for (const item of [
    { button: 'SkillsAgent能力包', expected: 'Skills', image: 'workspace-skills.png' },
    { button: '待审核Agent提交的知识', expected: '待审核知识', image: 'workspace-review.png' },
    { button: '回收站恢复或永久清理', expected: '回收站', image: 'workspace-trash.png' },
    { button: '知识管理文档、网页与检索', expected: '当前知识库', image: 'workspace-knowledge.png' },
  ]) {
    await click(item.button)
    await waitFor(`document.body.innerText.includes(${JSON.stringify(item.expected)})`, item.expected)
    results.push({ button: item.button, expected: item.expected, active: await evaluate(`document.querySelector('.library-row.active')?.innerText`), screenshot: await screenshot(item.image) })
  }
  socket.close()
  console.log(JSON.stringify({ title: target.title, results }, null, 2))
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1 })
