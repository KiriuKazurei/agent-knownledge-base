import type { AgentToken, AuditEntry, BackupRecord, BackupVerification, Document, DocumentBatchRequest, DocumentBatchResult, DocumentDetail, GraphPositionUpdate, GraphSnapshot, GraphWorkspace, Job, KAHSubmission, KnowledgeRevision, KnowledgeSearchResponse, Library, Provider, SavedSearch, Skill, SkillManifest, SkillMappingTarget, SourceWatch, TrashEntry, VirtualFolder } from './types'

let runtime: RuntimeConfig | undefined
export async function getRuntime(): Promise<RuntimeConfig> { runtime ??= await window.kah.runtimeConfig(); return runtime }

const skillImportPollIntervalMs = 250
const skillImportMaxPolls = 2400

async function waitForJob(id: string): Promise<Job> {
  let last: Job | undefined
  for (let attempt = 0; attempt < skillImportMaxPolls; attempt += 1) {
    const jobs = await api<Job[]>('/jobs')
    last = jobs.find((item) => item.id === id)
    if (last?.status === 'completed') return last
    if (last?.status === 'failed') {
      throw new Error(last.message || 'Skill 导入失败')
    }
    await new Promise((resolve) => setTimeout(resolve, skillImportPollIntervalMs))
  }
  throw new Error(last?.message ? `Skill 导入超时：${last.message}` : 'Skill 导入超时，请检查任务状态')
}
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const config = await getRuntime()
  const response = await fetch(`${config.apiBaseUrl}${path}`, { ...init, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${config.desktopToken}`, ...init.headers } })
  if (!response.ok) {
    const problem = await response.json().catch(() => ({ detail: response.statusText }))
    const message = problem.detail ?? problem.title ?? `Request failed: ${response.status}`
    throw new Error(problem.requestId ? `${message}（请求 ID：${problem.requestId}）` : message)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const client = {
  libraries: () => api<Library[]>('/libraries'),
  createLibrary: (name: string, description = '') => api<Library>('/libraries', { method: 'POST', body: JSON.stringify({ name, description }) }),
  updateLibrary: (id: string, patch: Partial<Library>) => api<Library>(`/libraries/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  deleteLibrary: (id: string) => api<void>(`/libraries/${id}`, { method: 'DELETE' }),
  documents: (libraryId?: string, folderId?: string, favorite = false) => api<Document[]>(`/documents?libraryId=${encodeURIComponent(libraryId ?? '')}${folderId ? `&folderId=${encodeURIComponent(folderId)}` : ''}${favorite ? '&favorite=true' : ''}`),
  document: (id: string) => api<DocumentDetail>(`/documents/${id}`),
  updateDocument: (id: string, patch: { title?: string; tags?: string[]; content?: string; favorite?: boolean }) => api<Document>(`/documents/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  deleteDocument: (id: string) => api<void>(`/documents/${id}`, { method: 'DELETE' }),
  batchDocuments: (input: DocumentBatchRequest) => api<DocumentBatchResult>('/documents/batch', { method: 'POST', body: JSON.stringify(input) }),
  reindexDocument: (id: string) => api<{job: Job; result: DocumentBatchResult}>(`/documents/${id}/reindex`, { method: 'POST' }),
  relinkDocument: (id: string, sourcePath: string) => api<DocumentDetail>(`/documents/${id}/relink`, { method: 'POST', body: JSON.stringify({ sourcePath }) }),
  importFiles: (libraryId: string, paths: string[]) => api<Job[]>('/imports/files', { method: 'POST', body: JSON.stringify({ libraryId, paths, watchSource: false }) }),
  importUrl: (libraryId: string, url: string) => api<Job>('/imports/url', { method: 'POST', body: JSON.stringify({ libraryId, url, maxDepth: 0, maxPages: 1 }) }),
  skills: () => api<Skill[]>('/skills'),
  skill: (id: string) => api<Skill>(`/skills/${id}`),
  importSkill: async (path: string, replace = false) => {
    const job = await api<Job>('/skills/import', { method: 'POST', body: JSON.stringify({ path, replace }) })
    return waitForJob(job.id)
  },
  updateSkillLinks: (id: string, usesLibraryIds: string[], requiresLibraryIds: string[]) => api<Skill>(`/skills/${id}/links`, { method: 'PUT', body: JSON.stringify({ usesLibraryIds, requiresLibraryIds }) }),
  deleteSkill: (id: string) => api<void>(`/skills/${id}`, { method: 'DELETE' }),
  skillManifest: (id: string) => api<SkillManifest>(`/skills/${id}/manifest`),
  skillMappingTargets: () => api<SkillMappingTarget[]>('/skill-mapping-targets'),
  skillMappingTarget: (id: string) => api<SkillMappingTarget>(`/skill-mapping-targets/${id}`),
  createSkillMappingTarget: (input: { name: string; kind: 'agent'|'project'; directoryPath: string; skillIds: string[] }) => api<SkillMappingTarget>('/skill-mapping-targets', { method: 'POST', body: JSON.stringify(input) }),
  updateSkillMappingTarget: (id: string, input: { name: string; kind: 'agent'|'project' }) => api<SkillMappingTarget>(`/skill-mapping-targets/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  addSkillMappings: (id: string, skillIds: string[]) => api<SkillMappingTarget>(`/skill-mapping-targets/${id}/skills`, { method: 'POST', body: JSON.stringify({ skillIds }) }),
  verifySkillMappingTarget: (id: string) => api<SkillMappingTarget>(`/skill-mapping-targets/${id}/verify`, { method: 'POST' }),
  repairSkillMapping: (targetId: string, skillId: string) => api<SkillMappingTarget>(`/skill-mapping-targets/${targetId}/skills/${skillId}/repair`, { method: 'POST' }),
  removeSkillMapping: (targetId: string, skillId: string) => api<void>(`/skill-mapping-targets/${targetId}/skills/${skillId}`, { method: 'DELETE' }),
  forgetSkillMapping: (targetId: string, skillId: string) => api<void>(`/skill-mapping-targets/${targetId}/skills/${skillId}/record`, { method: 'DELETE' }),
  deleteSkillMappingTarget: (id: string) => api<void>(`/skill-mapping-targets/${id}`, { method: 'DELETE' }),
  jobs: () => api<Job[]>('/jobs'),
  cancelJob: (id: string) => api<Job>(`/jobs/${id}/cancel`, { method: 'POST' }),
  retryJob: (id: string) => api<Job>(`/jobs/${id}/retry`, { method: 'POST' }),
  savedSearches: () => api<SavedSearch[]>('/saved-searches'),
  folders: (libraryId: string) => api<VirtualFolder[]>(`/folders?libraryId=${encodeURIComponent(libraryId)}`),
  createFolder: (libraryId: string, name: string, parentId?: string) => api<VirtualFolder>('/folders', { method: 'POST', body: JSON.stringify({ libraryId, name, parentId }) }),
  updateFolder: (id: string, name: string, parentId = '') => api<VirtualFolder>(`/folders/${id}`, { method: 'PATCH', body: JSON.stringify({ name, parentId }) }),
  deleteFolder: (id: string) => api<void>(`/folders/${id}`, { method: 'DELETE' }),
  assignFolder: (documentId: string, folderId: string) => api<void>(`/documents/${documentId}/folders/${folderId}`, { method: 'PUT' }),
  removeFolder: (documentId: string, folderId: string) => api<void>(`/documents/${documentId}/folders/${folderId}`, { method: 'DELETE' }),
  watches: (libraryId: string) => api<SourceWatch[]>(`/sources/watches?libraryId=${encodeURIComponent(libraryId)}`),
  createWatch: (libraryId: string, rootPath: string) => api<{watch: SourceWatch; job: Job}>('/sources/watches', { method: 'POST', body: JSON.stringify({ libraryId, rootPath, recursive: true }) }),
  updateWatch: (id: string, patch: { enabled?: boolean; recursive?: boolean }) => api<SourceWatch>(`/sources/watches/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  scanWatch: (id: string) => api<Job>(`/sources/watches/${id}/scan`, { method: 'POST' }),
  deleteWatch: (id: string) => api<void>(`/sources/watches/${id}`, { method: 'DELETE' }),
  createSavedSearch: (name: string, query: string, libraryIds: string[] = [], tags: string[] = [], view: SavedSearch['view'] = 'knowledge', filters: SavedSearch['filters'] = { tags, tagMode: 'all', favorite: false }, sort: SavedSearch['sort'] = { field: 'updatedAt', direction: 'desc' }) => api<SavedSearch>('/saved-searches', { method: 'POST', body: JSON.stringify({ name, query, view, libraryIds, tags, filters, sort }) }),
  updateSavedSearch: (id: string, input: Omit<SavedSearch, 'id'>) => api<SavedSearch>(`/saved-searches/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  deleteSavedSearch: (id: string) => api<void>(`/saved-searches/${id}`, { method: 'DELETE' }),
  graph: (libraryId: string, workspaceId = '', centerNodeId = '', depth = 1, limit = 250) => api<GraphSnapshot>('/graph?' + new URLSearchParams({ libraryId, ...(workspaceId ? { workspaceId } : {}), ...(centerNodeId ? { centerNodeId } : {}), depth: String(depth), limit: String(limit) }).toString()),
  createGraphWorkspace: (libraryId: string, name = '默认图谱') => api<GraphWorkspace>('/graph/workspaces', { method: 'POST', body: JSON.stringify({ libraryId, name }) }),
  updateGraphWorkspace: (id: string, patch: { name?: string; filters?: Record<string, unknown>; viewport?: Record<string, unknown> }) => api<GraphWorkspace>(`/graph/workspaces/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  saveGraphPositions: (id: string, positions: GraphPositionUpdate[]) => api<{positions: GraphPositionUpdate[]}>(`/graph/workspaces/${id}/positions`, { method: 'PUT', body: JSON.stringify({ positions }) }),
  autoLayoutGraph: (id: string, libraryId: string, overwrite = false) => api<{positions: GraphPositionUpdate[]}>(`/graph/workspaces/${id}/auto-layout`, { method: 'POST', body: JSON.stringify({ libraryId, overwrite }) }),
  knowledgeSearch: (query: string, libraryIds: string[] = []) => api<KnowledgeSearchResponse>('/knowledge/search', { method: 'POST', body: JSON.stringify({ query, libraryIds, limit: 20 }) }),
  knowledge: (uri: string) => api<KnowledgeRevision>('/knowledge/resolve?uri=' + encodeURIComponent(uri)),
  submissions: (libraryId?: string) => api<KAHSubmission[]>('/knowledge/submissions?' + new URLSearchParams({ ...(libraryId ? { libraryId } : {}) }).toString()),
  submission: (id: string) => api<KAHSubmission>('/knowledge/submissions/' + encodeURIComponent(id)),
  approveSubmission: (id: string, reason = '') => api<KnowledgeRevision>('/knowledge/submissions/' + encodeURIComponent(id) + '/approve', { method: 'POST', body: JSON.stringify({ reason }) }),
  rejectSubmission: (id: string, reason: string) => api<KAHSubmission>('/knowledge/submissions/' + encodeURIComponent(id) + '/reject', { method: 'POST', body: JSON.stringify({ reason }) }),
  createKnowledgeRevision: (input: { libraryId: string; mode: 'create'|'propose_revision'; knowledgeUri: string; baseRevision: number; clientSubmissionId: string; payload: KnowledgeRevision['payload'] }) => api<KAHSubmission>('/knowledge/submissions', { method: 'POST', body: JSON.stringify(input) }),
  providers: () => api<Provider[]>('/providers'),
  saveProvider: (provider: Provider) => api<Provider>('/providers', { method: 'POST', body: JSON.stringify(provider) }),
  providerModels: (id: string) => api<{models:string[]}>(`/providers/${id}/models`),
  testProvider: (id: string) => api<{models:string[]}>(`/providers/${id}/test`, { method: 'POST' }),
  deleteProvider: (id: string) => api<void>(`/providers/${id}`, { method: 'DELETE' }),
  tokens: () => api<AgentToken[]>('/tokens'),
  createToken: (name: string, scopes: string[], libraryIds: string[]) => api<AgentToken>('/tokens', { method: 'POST', body: JSON.stringify({ name, scopes, libraryIds }) }),
  revokeToken: (id: string) => api<void>(`/tokens/${id}`, { method: 'DELETE' }),
  backup: () => api<{path:string;sha256:string}>('/backups', { method: 'POST', body: JSON.stringify({ includeIndexes: true }) }),
  backups: () => api<BackupRecord[]>('/backups'),
  verifyBackup: (path: string) => api<BackupVerification>('/backups/verify', { method: 'POST', body: JSON.stringify({ path }) }),
  rebuildIndex: (libraryId: string) => api<Job>('/indexes/rebuild', { method: 'POST', body: JSON.stringify({ libraryId }) }),
  audit: () => api<AuditEntry[]>('/audit'),
  trash: () => api<TrashEntry[]>('/trash'),
  restoreTrash: (kind: TrashEntry['kind'], id: string) => api<void>(`/trash/${kind}/${id}/restore`, { method: 'POST' }),
  purgeTrash: (kind: TrashEntry['kind'], id: string) => api<void>(`/trash/${kind}/${id}`, { method: 'DELETE' }),
  emptyTrash: () => api<void>('/trash', { method: 'DELETE' })
}
