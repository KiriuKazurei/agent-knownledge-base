import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { BookOpen, CircleAlert, Code2, FileText, Layers3, LoaderCircle, MessageSquareText, Search, Sparkles } from 'lucide-react'
import { client } from './api'
import type { ArchitectureProject, Library, QueryResponse, RetrievalResult, RetrievalSearchResponse, RetrievalSourceType } from './types'

const sourceLabels: Record<string, string> = { all: '全部', knowledge: '正式知识', raw: '原始资料', architecture: '项目架构' }
const reviewLabels: Record<string, string> = { stable: '已审核', unreviewed: '未审核', derived: '架构产物', deprecated: '已弃用' }

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value ?? '未知错误')
}

function resultSourceIcon(type: string) {
  if (type === 'architecture') return <Layers3 size={16} aria-hidden="true" />
  if (type === 'raw') return <FileText size={16} aria-hidden="true" />
  return <BookOpen size={16} aria-hidden="true" />
}

function resultLocation(item: RetrievalResult) {
  if (item.filePath) return `${item.filePath}${item.lineStart ? `:${item.lineStart}${item.lineEnd && item.lineEnd !== item.lineStart ? `-${item.lineEnd}` : ''}` : ''}`
  if (item.sourceURI) return item.sourceURI
  if (item.documentId) return `kah://document/${item.documentId}`
  return '来源位置未提供'
}

function citationResults(value?: QueryResponse | null): RetrievalResult[] {
  const candidates = value?.evidence ?? value?.results ?? []
  return Array.isArray(candidates) ? candidates : []
}

export function RetrievalSearchPanel({
  query,
  response,
  filters,
  loading,
  error,
  libraries,
  selectedLibrary,
  projects,
  onSearch,
  onRetry,
}: {
  query: string
  response: RetrievalSearchResponse | null
  filters: { sourceType: RetrievalSourceType; libraryIds?: string[]; projectIds?: string[] }
  loading: boolean
  error?: unknown
  libraries: Library[]
  selectedLibrary: string
  projects: ArchitectureProject[]
  onSearch: (input: { query: string; filters: { sourceType: RetrievalSourceType; libraryIds?: string[]; projectIds?: string[] } }) => void
  onRetry: () => void
}) {
  const [sourceType, setSourceType] = useState<RetrievalSourceType>('all')
  const [libraryId, setLibraryId] = useState(selectedLibrary)
  const [projectId, setProjectId] = useState('')
  const [expandedId, setExpandedId] = useState('')
  const [providerId, setProviderId] = useState('')
  const providers = useQuery({ queryKey: ['retrieval-answer-providers'], queryFn: client.providers, enabled: Boolean(response?.results.length), retry: false })
  const answer = useMutation({
    mutationFn: async () => {
      if (typeof client.query !== 'function') throw new Error('当前桌面后端没有提供引用问答接口')
      const answerFilters = { sourceType, ...(libraryId ? { libraryIds: [libraryId] } : {}), ...(projectId ? { projectIds: [projectId] } : {}) }
      return client.query({ query, libraryIds: libraryId ? [libraryId] : [], filters: answerFilters, responseMode: 'answer', providerId: providerId || undefined, topK: 10, retrievalMode: response?.retrievalMode === 'lexical' ? 'lexical' : 'hybrid' })
    },
  })

  useEffect(() => {
    setSourceType(filters.sourceType)
    setLibraryId(filters.libraryIds?.[0] ?? selectedLibrary)
    setProjectId(filters.projectIds?.[0] ?? '')
  }, [filters, selectedLibrary])
  useEffect(() => {
    if (projectId && !projects.some((item) => item.id === projectId)) setProjectId('')
  }, [projects, projectId])
  const visibleProjects = useMemo(() => projects.filter((item) => !libraryId || item.libraryId === libraryId), [projects, libraryId])
  const answerCitations = citationResults(answer.data)
  const standaloneCitation = answerCitations.find((item) => item.id === expandedId && !response?.results.some((result) => result.id === item.id))

  function applyFilters() {
    if (!query.trim()) return
    onSearch({ query, filters: { sourceType, ...(libraryId ? { libraryIds: [libraryId] } : {}), ...(projectId ? { projectIds: [projectId] } : {}) } })
  }

  return <div className="retrieval-search-panel">
    <div className="retrieval-filter-panel" role="region" aria-label="统一检索筛选">
      <label><span>来源类型</span><select aria-label="检索来源类型" value={sourceType} onChange={(event) => setSourceType(event.target.value as RetrievalSourceType)}>
        <option value="all">全部</option><option value="knowledge">正式知识</option><option value="raw">原始资料</option><option value="architecture">项目架构</option>
      </select></label>
      <label><span>知识库</span><select aria-label="检索知识库" value={libraryId} onChange={(event) => { setLibraryId(event.target.value); setProjectId('') }}>
        <option value="">全部知识库</option>{libraries.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
      </select></label>
      <label><span>项目范围</span><select aria-label="检索项目" value={projectId} onChange={(event) => setProjectId(event.target.value)} disabled={!visibleProjects.length}>
        <option value="">全部项目</option>{visibleProjects.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
      </select></label>
      <button type="button" className="button secondary" onClick={applyFilters} disabled={loading || !query.trim()}><Search size={15}/>{loading ? '检索中…' : '应用筛选'}</button>
    </div>
    {Boolean(error) && <div className="inline-error" role="alert"><CircleAlert size={16}/><span>{errorText(error)}</span><button type="button" className="text-button" onClick={onRetry}>重试检索</button></div>}
    {loading && <div className="preview-loading" role="status" aria-live="polite"><LoaderCircle size={17} className="spin"/>正在检索本地语料…</div>}
    {response && <div className="retrieval-response-status" role="status" aria-live="polite">
      <span><strong>{response.results.length}</strong> 个结果</span>
      <span>范围：{sourceLabels[sourceType]}</span>
      <span className={response.keywordOnly || response.degraded ? 'warning-text' : 'success-text'}>{response.keywordOnly || response.degraded ? `关键词降级${response.degradedReason ? `：${response.degradedReason}` : ''}` : `语义${response.retrievalMode === 'hybrid' ? '融合' : '检索'}可用`}</span>
      {response.modelId && <span>模型：{response.modelId} {response.modelVersion || ''}</span>}
      {response.generation && <span>代次：{response.generation}</span>}
    </div>}
    {response?.needsRebuild && <div className="retrieval-index-note warning" role="status"><CircleAlert size={15}/><span>当前语料有更新，索引待重建；结果仍来自上一代或关键词路径。</span></div>}
    {response && <div className="retrieval-result-list" role="list" aria-label="统一检索结果">
      {response.results.map((item, index) => <article key={item.id} className={`retrieval-result-card ${expandedId === item.id ? 'expanded' : ''}`} role="listitem">
        <button type="button" className="retrieval-result-toggle" aria-expanded={expandedId === item.id} onClick={() => setExpandedId((value) => value === item.id ? '' : item.id)}>
          <span className="retrieval-result-rank">{String(index + 1).padStart(2, '0')}</span>
          <span className="retrieval-result-icon">{resultSourceIcon(item.sourceType)}</span>
          <span className="retrieval-result-main"><strong>{item.title || item.symbol || item.filePath || '未命名证据'}</strong><small>{sourceLabels[item.sourceType] || item.sourceType} · {reviewLabels[item.reviewStatus] || item.reviewStatus} · {resultLocation(item)}</small></span>
          <span className="retrieval-result-score">排序分 {(item.scores.final || item.scores.fusion || item.scores.lexical || 0).toFixed(4)}</span>
        </button>
        {expandedId === item.id && <div className="retrieval-result-detail">
          <div className="retrieval-citation-label"><span>可定位引用</span><code>{resultLocation(item)}</code></div>
          <p>{item.text}</p>
          <div className="retrieval-result-meta"><span>来源状态：{reviewLabels[item.reviewStatus] || item.reviewStatus}</span>{item.language && <span>语言：{item.language}</span>}{item.contentHash && <code>内容哈希：{item.contentHash}</code>}</div>
          {item.sourceURI && <code className="retrieval-uri">{item.sourceURI}</code>}
        </div>}
      </article>)}
      {!response.results.length && <div className="retrieval-empty"><Search size={22}/><strong>没有找到匹配证据</strong><span>尝试改写问题、扩大来源范围，或检查索引状态。</span></div>}
    </div>}
    {response && <section className="retrieval-answer-panel" aria-labelledby="retrieval-answer-heading">
      <div className="retrieval-answer-heading"><div><span className="eyebrow">可选生成</span><h3 id="retrieval-answer-heading">引用问答</h3><p>先由统一检索召回证据，再由配置的生成模型组织答案。搜索结果始终独立可用。</p><p className="field-hint">答案范围：{sourceLabels[sourceType]} · {libraryId ? (libraries.find((item) => item.id === libraryId)?.name || libraryId) : '全部知识库'} · {projectId ? (projects.find((item) => item.id === projectId)?.name || projectId) : '全部项目'}</p></div><MessageSquareText size={19} aria-hidden="true"/></div>
      <div className="retrieval-answer-controls">
        <label><span>生成模型</span><select aria-label="引用问答生成模型" value={providerId} onChange={(event) => setProviderId(event.target.value)} disabled={answer.isPending}>
          <option value="">选择已配置模型</option>{(providers.data ?? []).map((provider) => <option value={provider.id} key={provider.id}>{provider.name} · {provider.local ? '本地' : '远程'}</option>)}
        </select></label>
        <button type="button" className="button primary" onClick={() => answer.mutate()} disabled={answer.isPending || !providerId || !response.results.length}>{answer.isPending ? <><LoaderCircle size={15} className="spin"/>生成中…</> : <><Sparkles size={15}/>生成引用答案</>}</button>
      </div>
      {!providers.isLoading && !providers.data?.length && <p className="field-hint">尚未配置生成模型；你仍可以直接查看和复制检索结果。</p>}
      {answer.error && <div className="inline-error" role="alert"><CircleAlert size={16}/><span>答案生成失败：{errorText(answer.error)}。搜索结果仍可用。</span><button type="button" className="text-button" onClick={() => answer.reset()}>关闭</button></div>}
      {answer.data?.answer && <div className="retrieval-answer-output"><div className="retrieval-answer-copy"><Code2 size={16}/><p>{answer.data.answer}</p></div>{answer.data.degraded && <p className="field-hint">生成前的检索已降级：{answer.data.degradedReason || '关键词检索'}</p>}{answerCitations.length > 0 && <div className="retrieval-answer-citations"><strong>答案引用</strong>{answerCitations.map((item, index) => <button type="button" key={item.id || index} onClick={() => setExpandedId(item.id)}><span>[{index + 1}]</span><span>{item.title || item.filePath || '证据'}</span><code>{resultLocation(item)}</code><small>{reviewLabels[item.reviewStatus] || item.reviewStatus}</small></button>)}</div>}{standaloneCitation && <article className="retrieval-result-detail retrieval-standalone-citation" aria-label="答案引用内容"><div className="retrieval-citation-label"><span>答案引用内容</span><code>{resultLocation(standaloneCitation)}</code></div><p>{standaloneCitation.text}</p><div className="retrieval-result-meta"><span>来源类型：{sourceLabels[standaloneCitation.sourceType] || standaloneCitation.sourceType}</span><span>来源状态：{reviewLabels[standaloneCitation.reviewStatus] || standaloneCitation.reviewStatus}</span>{standaloneCitation.language && <span>语言：{standaloneCitation.language}</span>}{standaloneCitation.contentHash && <code>内容哈希：{standaloneCitation.contentHash}</code>}</div>{standaloneCitation.sourceURI && <code className="retrieval-uri">{standaloneCitation.sourceURI}</code>}</article>}</div>}
    </section>}
  </div>
}
