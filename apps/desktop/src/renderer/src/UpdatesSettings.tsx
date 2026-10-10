import { useEffect, useState } from 'react'
import type { UpdateState } from '../../shared/updates'
import { getRuntime } from './api'

export function useDesktopUpdates(): UpdateState {
  const [state, setState] = useState<UpdateState>({ enabled: false, status: 'disabled', currentVersion: '…' })
  useEffect(() => {
    let active = true
    let revision = 0
    const bridge = window.kah.updates
    const unsubscribe = bridge?.onStateChanged((next) => { revision++; if (active) setState(next) })
    const snapshot = bridge ? bridge.getState() : getRuntime().then((runtime) => ({ enabled: false, status: 'disabled' as const, currentVersion: runtime.version }))
    void snapshot.then((next) => { if (active && revision === 0) setState(next) }).catch(() => {})
    return () => { active = false; unsubscribe?.() }
  }, [])
  return state
}

const labels: Record<UpdateState['status'], string> = {
  disabled: '此运行模式不启用自动更新', idle: '等待检查更新', checking: '正在检查更新…',
  'not-available': '已是最新正式版本', available: '发现新版本', downloading: '正在下载更新…',
  ready: '更新已下载，等待确认安装', installing: '正在准备重启更新…', error: '更新未完成'
}

export function UpdatesSettings({ state, onInstall }: { state: UpdateState; onInstall: () => void }) {
  const busy = ['checking', 'downloading', 'installing'].includes(state.status)
  return <section aria-label="应用更新"><h3>应用更新</h3>
    <p className="settings-lead">自动检查并下载 GitHub 上的正式版本。只有确认后才重启安装；稍后更新或正常退出不会安装。</p>
    <p>当前版本：v{state.currentVersion}{state.targetVersion && ` · 新版本：v${state.targetVersion}`}</p>
    <p role="status">{labels[state.status]}</p>
    {state.lastCheckAt && <p className="field-hint">最近检查：{new Date(state.lastCheckAt).toLocaleString()}</p>}
    {state.status === 'downloading' && <progress aria-label="更新下载进度" max={100} value={state.progress?.percent ?? 0}/>}
    {state.error && <p className="form-error" role="alert">{state.error}</p>}
    {state.releaseNotes && <details><summary>版本说明</summary><pre className="update-release-notes">{state.releaseNotes}</pre></details>}
    <div className="settings-actions">
      <button className="button secondary" disabled={!state.enabled || busy || state.status === 'ready'} onClick={() => void window.kah.updates.check()}>检查更新</button>
      {state.status === 'error' && state.targetVersion && <button className="button secondary" onClick={() => void window.kah.updates.download()}>重试下载</button>}
      {(state.canInstall || state.status === 'ready' || state.status === 'installing') && <button className="button primary" disabled={busy} onClick={onInstall}>重启并更新</button>}
    </div>
  </section>
}

export function UpdateNotice({ state, onInstall }: { state: UpdateState; onInstall: () => void }) {
  const [dismissedVersion, setDismissedVersion] = useState('')
  if (!state.enabled || state.status !== 'ready' || dismissedVersion === state.targetVersion) return null
  return <aside className="update-notice" aria-label="更新已就绪"><span role="status">v{state.targetVersion} 已下载，可重启更新。</span>
    <button className="button primary" onClick={onInstall}>重启并更新</button>
    <button className="button ghost" onClick={() => setDismissedVersion(state.targetVersion ?? '')}>稍后</button>
  </aside>
}
