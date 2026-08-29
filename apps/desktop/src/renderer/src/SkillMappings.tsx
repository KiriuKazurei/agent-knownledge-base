import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, FolderSearch2, Link2, LoaderCircle, Plus, RefreshCw, Trash2, Wrench, X } from 'lucide-react'
import { client } from './api'
import type { Skill, SkillMapping, SkillMappingTarget } from './types'

function targetStatusLabel(status: SkillMappingTarget['status']): string {
  return ({ pending: '处理中', ready: '已就绪', partial: '部分缺失', missing: '目标不存在', conflict: '存在冲突', invalid: '无效目标', permission_required: '需要权限' })[status]
}

function mappingStatusLabel(status: SkillMapping['status']): string {
  return ({ pending: '处理中', ready: '已链接', missing: '链接缺失', conflict: '外部冲突', invalid: '链接无效', permission_required: '需要权限' })[status]
}

export function SkillMappingsWorkspace({ skills, selectedTargetId, onTargetSelect }: { skills: Skill[]; selectedTargetId: string; onTargetSelect: (id: string) => void }) {
  const queryClient = useQueryClient()
  const targets = useQuery({ queryKey: ['skill-mapping-targets'], queryFn: client.skillMappingTargets })
  const [formOpen, setFormOpen] = useState(false)
  const [name, setName] = useState('')
  const [kind, setKind] = useState<'agent'|'project'>('agent')
  const [directoryPath, setDirectoryPath] = useState('')
  const [skillIds, setSkillIds] = useState<string[]>([])
  const [directoryError, setDirectoryError] = useState('')
  const create = useMutation({
    mutationFn: () => {
      if (!name.trim()) throw new Error('请填写映射目标名称')
      if (!directoryPath) throw new Error('请选择一个已存在的 Skills 目录')
      return client.createSkillMappingTarget({ name: name.trim(), kind, directoryPath, skillIds })
    },
    onSuccess: (target) => {
      queryClient.invalidateQueries({ queryKey: ['skill-mapping-targets'] })
      setFormOpen(false)
      setName('')
      setDirectoryPath('')
      setSkillIds([])
      onTargetSelect(target.id)
    }
  })
  useEffect(() => {
    if (!selectedTargetId && targets.data?.[0]) onTargetSelect(targets.data[0].id)
  }, [selectedTargetId, targets.data, onTargetSelect])

  async function chooseDirectory() {
    setDirectoryError('')
    try {
      const value = await window.kah.selectDirectory()
      if (value) setDirectoryPath(value)
    } catch (error) {
      setDirectoryError(error instanceof Error ? error.message : String(error))
    }
  }

  function toggleSkill(id: string) {
    setSkillIds((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id])
  }

  return <>
    {formOpen && <form className="mapping-create-form" onSubmit={(event) => { event.preventDefault(); create.mutate() }} aria-describedby="mapping-create-help">
      <div className="mapping-form-heading"><div><span className="eyebrow">新建外部映射</span><h2>连接一个 Agent 或项目</h2></div><button type="button" className="icon-button" aria-label="关闭新建映射表单" title="关闭" onClick={() => setFormOpen(false)}><X size={17}/></button></div>
      <p id="mapping-create-help" className="settings-lead">系统只会在已选择的目录中创建真实目录符号链接，不会复制 Skill 内容或自动创建目录。</p>
      <div className="form-grid">
        <label htmlFor="mapping-name">目标名称<input id="mapping-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：本机 Codex" autoFocus/></label>
        <label htmlFor="mapping-kind">目标类型<select id="mapping-kind" value={kind} onChange={(event) => setKind(event.target.value as 'agent'|'project')}><option value="agent">Agent</option><option value="project">项目</option></select></label>
        <label className="span-2" htmlFor="mapping-directory">Skills 目录<input id="mapping-directory" value={directoryPath} readOnly placeholder="请选择一个已存在的绝对路径"/><button type="button" className="button secondary mapping-directory-button" onClick={() => void chooseDirectory()}><FolderSearch2 size={16}/>选择目录</button></label>
      </div>
      <fieldset className="mapping-skill-picker"><legend>要映射的 Skill（可选）</legend>{skills.length ? skills.map((skill) => <label className="switch-row" key={skill.id}><span><strong>{skill.name}</strong><small>{skill.description}</small></span><input type="checkbox" checked={skillIds.includes(skill.id)} onChange={() => toggleSkill(skill.id)} /></label>) : <p className="muted">请先安装至少一个 Skill。</p>}</fieldset>
      {(directoryError || create.error) && <div role="alert" className="inline-error"><CircleAlert size={17}/>{directoryError || (create.error as Error).message}</div>}
      <div className="preview-actions"><button type="submit" className="button primary" disabled={create.isPending || !directoryPath}><Link2 size={16}/>{create.isPending ? '正在创建链接…' : '创建映射'}</button><button type="button" className="button ghost" onClick={() => setFormOpen(false)}>取消</button></div>
    </form>}
    <div className="content-heading"><div><h2>外部映射目标</h2><span>{targets.data?.length ?? 0} 个目标</span></div><button className="button secondary" onClick={() => setFormOpen(true)}><Plus size={16}/>新建映射</button></div>
    {targets.error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{(targets.error as Error).message}</div>}
    <section className="result-list" aria-live="polite" aria-label="外部映射目标列表">
      {targets.isLoading && <div className="preview-loading"><LoaderCircle className="spin"/>正在载入外部映射</div>}
      {targets.data?.map((target) => <button className={`document-row ${selectedTargetId === target.id ? 'active' : ''}`} key={target.id} onClick={() => onTargetSelect(target.id)}><span className="file-icon"><Link2 size={19}/></span><span className="document-copy"><strong>{target.name}</strong><small>{target.kind === 'agent' ? 'Agent' : '项目'} · {target.directoryPath}</small></span><span className={`status-pill ${target.status}`}>{targetStatusLabel(target.status)}</span><span className="mapping-count">{target.mappings.length} 个 Skill</span></button>)}
      {!targets.isLoading && !targets.data?.length && <EmptyMappingTargets onCreate={() => setFormOpen(true)} />}
    </section>
  </>
}

function EmptyMappingTargets({ onCreate }: { onCreate: () => void }) {
  return <div className="empty"><div className="empty-icon"><Link2 size={25}/></div><strong>还没有外部映射</strong><p>选择一个现有 Agent 或项目 Skills 目录，让它直接发现全局 Skill。</p><button className="button secondary" onClick={onCreate}><Plus size={15}/>新建映射</button></div>
}

export function SkillMappingDetailPane({ targetId, onDeleted }: { targetId: string; onDeleted: () => void }) {
  const queryClient = useQueryClient()
  const target = useQuery({ queryKey: ['skill-mapping-target', targetId], queryFn: () => client.skillMappingTarget(targetId), enabled: Boolean(targetId) })
  const refresh = () => { queryClient.invalidateQueries({ queryKey: ['skill-mapping-targets'] }); queryClient.invalidateQueries({ queryKey: ['skill-mapping-target', targetId] }) }
  const verify = useMutation({ mutationFn: () => client.verifySkillMappingTarget(targetId), onSuccess: refresh })
  const repair = useMutation({ mutationFn: (skillId: string) => client.repairSkillMapping(targetId, skillId), onSuccess: refresh })
  const remove = useMutation({ mutationFn: (skillId: string) => client.removeSkillMapping(targetId, skillId), onSuccess: refresh })
  const forget = useMutation({ mutationFn: (skillId: string) => client.forgetSkillMapping(targetId, skillId), onSuccess: refresh })
  const deleteTarget = useMutation({ mutationFn: () => client.deleteSkillMappingTarget(targetId), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['skill-mapping-targets'] }); onDeleted() } })
  if (!targetId) return <EmptyMappingDetail />
  if (target.error) return <div role="alert" className="inline-error"><CircleAlert size={17}/>{(target.error as Error).message}</div>
  if (target.isLoading || !target.data) return <div className="preview-loading"><LoaderCircle className="spin"/>正在载入映射详情</div>
  const error = verify.error || repair.error || remove.error || forget.error || deleteTarget.error
  return <div className="preview-shell mapping-detail-shell"><header className="preview-header"><div className="preview-icon"><Link2 size={20}/></div><div><span>{target.data.kind === 'agent' ? 'Agent 映射' : '项目映射'}</span><h2>{target.data.name}</h2></div></header><div className="preview-meta"><span className={`status-pill ${target.data.status}`}>{targetStatusLabel(target.data.status)}</span><span><Link2 size={14}/>{target.data.mappings.length} 个链接</span></div><div className="mapping-path-card"><strong>目标 Skills 目录</strong><code>{target.data.directoryPath}</code></div>{target.data.error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{target.data.error}</div>}{error && <div role="alert" className="inline-error"><CircleAlert size={17}/>{(error as Error).message}</div>}<div className="preview-actions"><button className="button primary" onClick={() => verify.mutate()} disabled={verify.isPending}><RefreshCw size={16}/>{verify.isPending ? '正在验证…' : '验证链接'}</button><button className="button ghost" onClick={() => window.kah.openPath(target.data!.directoryPath)}><FolderSearch2 size={16}/>打开目录</button><button className="button danger" onClick={() => { if (window.confirm(`确定删除映射目标“${target.data!.name}”？系统会只清理仍确认归属的链接。`)) deleteTarget.mutate() }} disabled={deleteTarget.isPending}><Trash2 size={16}/>删除目标</button></div><div className="mapping-list"><h3>已映射 Skill</h3>{target.data.mappings.length ? target.data.mappings.map((mapping) => <MappingRow key={mapping.skillId} mapping={mapping} onRepair={() => repair.mutate(mapping.skillId)} onRemove={() => { if (window.confirm(`解除 ${mapping.skillName} 的外部链接？`)) remove.mutate(mapping.skillId) }} onForget={() => { if (window.confirm(`只遗忘 ${mapping.skillName} 的映射记录，不触碰外部对象？`)) forget.mutate(mapping.skillId) }} busy={repair.isPending || remove.isPending || forget.isPending}/>) : <p className="muted">此目标还没有映射 Skill。</p>}</div><div className="mapping-detail-note" role="status" aria-live="polite">最后校验：{target.data.lastVerifiedAt ? new Date(target.data.lastVerifiedAt).toLocaleString() : '尚未校验'}</div></div>
}

function MappingRow({ mapping, onRepair, onRemove, onForget, busy }: { mapping: SkillMapping; onRepair: () => void; onRemove: () => void; onForget: () => void; busy: boolean }) {
  const conflict = mapping.status === 'conflict' || mapping.status === 'invalid'
  return <article className="mapping-row"><div className="mapping-row-heading"><div><strong>{mapping.skillName}</strong><span className={`status-pill ${mapping.status}`}>{mappingStatusLabel(mapping.status)}</span></div><span className="mapping-link-name">{mapping.linkName}</span></div><dl><div><dt>源目录</dt><dd><code>{mapping.sourcePath}</code></dd></div><div><dt>外部链接</dt><dd><code>{mapping.linkPath}</code></dd></div></dl>{mapping.error && <p className="form-error" role="alert">{mapping.error}</p>}<div className="mapping-row-actions">{mapping.status === 'missing' && <button className="button secondary" onClick={onRepair} disabled={busy}><Wrench size={15}/>修复链接</button>}{conflict && <button className="button ghost" onClick={onForget} disabled={busy}>遗忘记录</button>}<button className="button ghost" onClick={onRemove} disabled={busy || conflict}><Trash2 size={15}/>解除映射</button></div></article>
}

function EmptyMappingDetail() {
  return <Empty icon={<Link2 size={25}/>} title="选择一个映射目标" text="目标目录、链接状态和修复操作会显示在这里。" />
}

function Empty({ icon, title, text }: { icon: React.ReactNode; title: string; text: string }) {
  return <div className="empty"><div className="empty-icon">{icon}</div><strong>{title}</strong><p>{text}</p></div>
}
