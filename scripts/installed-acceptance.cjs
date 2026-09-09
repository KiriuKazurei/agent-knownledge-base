// Runs against the installed desktop's local CDP endpoint. Never prints credentials.
const fs = require('node:fs')
const path = require('node:path')
const root = path.resolve(__dirname, '../.run-data/installed-acceptance-20260908')
fs.mkdirSync(root, { recursive: true })
const pause = ms => new Promise(r => setTimeout(r, ms))
async function main() {
 let target; for(let attempt=0;attempt<90;attempt++){try{const targets=await(await fetch('http://127.0.0.1:9227/json/list')).json();target=targets.find(x=>x.type==='page');if(target)break}catch{}await pause(1000)}; if(!target)throw Error('Installed renderer unavailable after 90 seconds')
 const socket = new WebSocket(target.webSocketDebuggerUrl)
 await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject})
 let seq=0;const pending=new Map()
 socket.onmessage=e=>{const m=JSON.parse(e.data);const p=pending.get(m.id);if(p){pending.delete(m.id);m.error?p.reject(m.error):p.resolve(m.result)}}
 const send=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}))})
 const evaluate=async expression=>{const r=await send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true,userGesture:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value}
 const api=async (route,method='GET',body)=>evaluate(`(async()=>{const c=await window.kah.runtimeConfig();const r=await fetch(c.apiBaseUrl+${JSON.stringify(route)},{method:${JSON.stringify(method)},headers:{Authorization:'Bearer '+c.desktopToken,'Content-Type':'application/json'},body:${body===undefined?'undefined':JSON.stringify(JSON.stringify(body))}});const text=await r.text();if(!r.ok)throw Error(r.status+' '+text);return text?JSON.parse(text):null})()`)
 const mode=process.argv[2]||'snapshot'
 try {
 if(mode==='eval'){ console.log(JSON.stringify(await evaluate(fs.readFileSync(process.argv[3],'utf8')),null,2));return }
 if(mode==='click'){
  const label=process.argv[3]
  const point=await evaluate(`(()=>{const b=[...document.querySelectorAll('button')].reverse().find(x=>x.textContent.trim()===${JSON.stringify(label)}||x.getAttribute('aria-label')===${JSON.stringify(label)});if(!b||b.disabled)throw Error('Control unavailable');b.scrollIntoView({block:'center'});const r=b.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}})()`)
  await send('Input.dispatchMouseEvent',{type:'mousePressed',...point,button:'left',clickCount:1});await send('Input.dispatchMouseEvent',{type:'mouseReleased',...point,button:'left',clickCount:1});console.log('Clicked '+label);return
 }
 if(mode==='snapshot') {
  console.log(JSON.stringify(await evaluate(`(async()=>{const c=await window.kah.runtimeConfig();return {dataRoot:c.dataRoot,version:c.version,url:location.href,text:document.body.innerText.slice(0,9000)}})()`),null,2))
 }
 if(mode==='flow') {
  const lib=(await api('/libraries')).find(x=>x.name==='安装验收 20260908') || await api('/libraries','POST',{name:'安装验收 20260908',description:'可保留或删除的验收样例'})
  fs.writeFileSync(path.join(root,'sample.md'),'# 安装验收知识\n\n验收标记 KAHINSTALL0908。知识库日志位于数据目录 logs，错误可按时间、请求 ID 和任务 ID 追踪。\n')
  if(!(await api('/plugins/ocr')).installed) await api('/plugins/ocr/install','POST',{path:path.resolve(__dirname,'../.run-data/ocr-downloads/kah-ocr-ppocrv4.zip')})
  const jobs=await api('/imports/files','POST',{libraryId:lib.id,paths:[path.join(root,'sample.md'),path.resolve(__dirname,'../output/pdf/ocr-complex-layout-acceptance.pdf')],watchSource:false})
  const completed=[]
  for(const initial of jobs){let j;for(let i=0;i<120;i++){j=(await api('/jobs')).find(x=>x.id===initial.id);if(['completed','failed'].includes(j.status))break;await pause(1000)}if(j.status!=='completed')throw Error(JSON.stringify(j));completed.push(j)}
  const docs=await api('/documents?libraryId='+lib.id)
  if(docs.length!==2||docs.some(x=>x.status!=='ready'))throw Error('Imported documents not ready')
  const submissions=await api('/knowledge/submissions?libraryId='+lib.id)
  if(submissions.length!==2)throw Error('Expected two reference drafts')
  const stable=[];for(const s of submissions)stable.push(s.reviewStatus==='published'?await api('/knowledge/resolve?uri='+encodeURIComponent(s.knowledgeUri)):await api('/knowledge/submissions/'+s.id+'/approve','POST',{reason:'Installed build acceptance using known generated fixtures'}))
  for(const old of await api('/tokens')) if(old.name==='安装验收临时只读'&&!old.revokedAt)await api('/tokens/'+old.id,'DELETE')
  const token=await api('/tokens','POST',{name:'安装验收临时只读',scopes:['mcp_read'],libraryIds:[lib.id]})
  // Keep the temporary token inside the renderer, never write it to evidence.
  await evaluate('window.__acceptanceToken='+JSON.stringify(token.secret))
  const mcp=await evaluate(`(async()=>{const r=await fetch('http://127.0.0.1:48761/mcp/read',{method:'POST',headers:{Authorization:'Bearer '+window.__acceptanceToken,'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'knowledge_search',arguments:{query:'KAHINSTALL0908',libraryIds:[${JSON.stringify(lib.id)}]}}})});return r.json()})()`)
  const entries=mcp.result?.structuredContent?.results
  if(mcp.error||mcp.result?.isError||entries?.length!==1)throw Error('MCP directory did not find published fixture')
  const read=await evaluate(`(async()=>{const r=await fetch('http://127.0.0.1:48761/mcp/read',{method:'POST',headers:{Authorization:'Bearer '+window.__acceptanceToken,'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:2,method:'tools/call',params:{name:'knowledge_get',arguments:{uri:${JSON.stringify(entries[0].uri)},sectionIds:['overview']}}})});return r.json()})()`)
  if(read.error||read.result?.isError||!JSON.stringify(read).includes('KAHINSTALL0908'))throw Error('MCP content read failed')
  await api('/tokens/'+token.id,'DELETE');await evaluate('delete window.__acceptanceToken')
  const backup=await api('/backups','POST',{includeIndexes:true});const verified=await api('/backups/verify','POST',{path:backup.path})
  const result={libraryId:lib.id,documents:docs.map(x=>({id:x.id,title:x.title,status:x.status})),jobs:completed.map(x=>({id:x.id,status:x.status})),stable:stable.map(x=>({uri:x.uri,revision:x.revision})),mcp,read,backup,verified}
  fs.writeFileSync(path.join(root,'flow.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result,null,2))
 }
 if(mode==='restart-check'){
  const old=JSON.parse(fs.readFileSync(path.join(root,'flow.json'),'utf8'))
  const docs=await api('/documents?libraryId='+old.libraryId)
  const found=await api('/knowledge/search','POST',{query:'KAHINSTALL0908',libraryIds:[old.libraryId],limit:20})
  if(docs.length!==2||found.results?.length!==1)throw Error('Restart persistence failed')
  const health=await api('/health');const r={health,documents:docs.length,knowledge:found}
  fs.writeFileSync(path.join(root,'restart.json'),JSON.stringify(r,null,2));console.log(JSON.stringify(r,null,2))
 }
 if(mode==='fault'){
  const result=await evaluate(`(async()=>{const c=await window.kah.runtimeConfig();const r=await fetch(c.apiBaseUrl+'/plugins/ocr/install',{method:'POST',headers:{Authorization:'Bearer '+c.desktopToken,'Content-Type':'application/json','X-Request-ID':'acceptance-ocr-missing-0908'},body:JSON.stringify({path:'${root.replaceAll('\\','/').replaceAll("'","\\'")}/intentionally-missing.zip'})});return {status:r.status,problem:await r.json()}})()`)
  if(result.status!==400||result.problem.code!=='ocr_plugin_failed')throw Error('Expected diagnostic fault not observed')
  fs.writeFileSync(path.join(root,'fault.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result,null,2))
 }
 const shot=await send('Page.captureScreenshot',{format:'png'});fs.writeFileSync(path.join(root,mode+'.png'),Buffer.from(shot.data,'base64'))
 }finally{socket.close()}
}
main().catch(e=>{console.error(e);process.exit(1)})
