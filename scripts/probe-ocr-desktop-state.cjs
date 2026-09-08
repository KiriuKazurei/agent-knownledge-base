const WebSocket = require('../node_modules/.pnpm/ws@8.21.3/node_modules/ws')
const fs = require('node:fs')

async function main() {
  const port = Number(process.env.KAH_ACCEPTANCE_CDP_PORT || 9225)
  const libraryId = process.argv[2] || ''
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
  const send = (method, params) => new Promise((resolve, reject) => {
    const next = ++id
    pending.set(next, { resolve, reject })
    socket.send(JSON.stringify({ id: next, method, params }))
  })
  if (process.env.KAH_CLICK_DOCUMENT === '1') {
    const pointResult = await send('Runtime.evaluate', {
      expression: `(() => {
        const element = [...document.querySelectorAll('.document-row')].find((item) => item.textContent.includes('ocr-complex-layout-acceptance.pdf'))
        if (!element) return null
        const rect = element.getBoundingClientRect()
        return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
      })()`,
      returnByValue: true
    })
    const point = pointResult.result.value
    if (!point) throw new Error('Imported document card is unavailable')
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', clickCount: 1 })
    await new Promise((resolve) => setTimeout(resolve, 4000))
  }
  const expression = `(async () => {
    const config = await window.kah.runtimeConfig()
    const get = async (path) => {
      const response = await fetch(config.apiBaseUrl + path, { headers: { Authorization: 'Bearer ' + config.desktopToken } })
      return { status: response.status, body: await response.json().catch(() => null) }
    }
    return {
      bodyText: document.body.innerText.slice(0, 1200),
      documents: await get('/documents?libraryId=${encodeURIComponent(libraryId)}'),
      submissions: await get('/knowledge/submissions?libraryId=${encodeURIComponent(libraryId)}'),
      jobs: await get('/jobs')
    }
  })()`
  const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text)
  if (process.argv[3]) {
    const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    fs.writeFileSync(process.argv[3], Buffer.from(shot.data, 'base64'))
  }
  console.log(JSON.stringify(result.result.value, null, 2))
  socket.close()
}

main().catch((error) => { console.error(error); process.exitCode = 1 })
