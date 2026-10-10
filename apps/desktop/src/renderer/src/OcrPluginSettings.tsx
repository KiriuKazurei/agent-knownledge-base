import './OcrPluginSettings.css'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'

type OcrPlugin = { installed: boolean; enabled: boolean; version: string; sizeBytes: number }
export function OcrPluginSettings() {
  const query = useQueryClient()
  const plugin = useQuery({queryKey:['ocr-plugin'], queryFn:() => api<OcrPlugin>('/plugins/ocr')})
  const action = useMutation({
    mutationFn:async (operation:'install'|'enable'|'disable'|'uninstall'|'check') => {
      if (operation === 'install') {
        const files = await window.kah.selectFiles('ocr-plugin')
        if (!files.length) return
        return api<OcrPlugin>('/plugins/ocr/install', {method:'POST',body:JSON.stringify({path:files[0]})})
      }
      if (operation === 'check') {
        const health = await api<{available:boolean;message?:string}>('/ocr/health')
        if (!health.available) throw new Error(health.message || 'OCR 引擎尚不可用，请安装运行依赖后重试。')
        return health
      }
      return api<OcrPlugin>('/plugins/ocr', {method:operation === 'uninstall' ? 'DELETE':'PATCH', body:operation === 'uninstall' ? undefined:JSON.stringify({enabled:operation === 'enable'})})
    },
    onSuccess:() => query.invalidateQueries({queryKey:['ocr-plugin']})
  })
  const busy = action.isPending || plugin.isPending
  return <section className="ocr-plugin-settings" aria-label="OCR 插件"><h3>离线 PDF OCR</h3>
    <p className="settings-lead">为扫描 PDF 和图文混合页面提取文字。安装本地 ZIP 资源包后在本机识别，不发送文档到云端。</p>
    {plugin.isPending && <p role="status">正在读取插件状态…</p>}
    {plugin.data && <p role="status">{plugin.data.installed ? (plugin.data.enabled ? '已安装并启用':'已安装，已停用'):'未安装'}{plugin.data.installed && ` · ${plugin.data.version} · ${(plugin.data.sizeBytes/1024/1024).toFixed(1)} MB`}</p>}
    <div className="ocr-plugin-actions">
      {!plugin.data?.installed && <button type="button" disabled={busy || plugin.isError} onClick={() => action.mutate('install')}>安装 OCR 离线包</button>}
      {plugin.data?.installed && <><button type="button" disabled={busy} onClick={() => action.mutate(plugin.data!.enabled ? 'disable':'enable')}>{plugin.data.enabled ? '停用 OCR':'启用 OCR'}</button>
      <button type="button" disabled={busy || !plugin.data.enabled} onClick={() => action.mutate('check')}>验证 OCR 引擎</button>
      <button type="button" disabled={busy} onClick={() => action.mutate('uninstall')}>卸载 OCR 插件</button></>}
    </div>
    <p className="muted">卸载会释放模型资源；已导入的文档和知识保留。需要时可以重新安装。OCR 运行依赖由应用运行环境提供。</p>
    {action.isPending && <p role="status">正在处理，请稍候…</p>}
    {action.isSuccess && action.variables === 'check' && <p role="status">OCR 引擎验证通过。</p>}
    {(action.error || plugin.error) && <p className="form-error" role="alert">{(action.error || plugin.error)?.message}</p>}
    {plugin.isError && <button type="button" onClick={() => plugin.refetch()}>重试读取插件状态</button>}
  </section>
}
