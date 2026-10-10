import type { AgentToken, ArchitectureAgent, ArchitectureAgentCredential, ArchitectureAgentLease, ArchitectureAnalysis, ArchitectureArtifact, ArchitectureArtifactInput, ArchitectureDiff, ArchitectureProject, ArchitectureSnapshot, ArchitectureTask, AuditEntry, BackupRecord, BackupVerification, Document, DocumentBatchRequest, DocumentBatchResult, DocumentDetail, EmbeddingModel, EmbeddingModelHealth, GraphPositionUpdate, GraphSnapshot, GraphWorkspace, Job, KAHSubmission, KnowledgeRevision, KnowledgeSearchResponse, Library, Provider, QueryResponse, RetrievalGeneration, RetrievalProgress, RetrievalSearchResponse, SavedSearch, Skill, SkillManifest, SkillMappingTarget, SourceWatch, TrashEntry, VirtualFolder } from './types'

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
export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  readonly retryable?: boolean
  constructor(message: string, status: number, code?: string, retryable?: boolean) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryable = retryable
  }
}

function apiHeaders(config: RuntimeConfig, init: RequestInit): HeadersInit {
  return { 'Content-Type': 'application/json', Authorization: `Bearer ${config.desktopToken}`, ...init.headers }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const config = await getRuntime()
  const response = await fetch(`${config.apiBaseUrl}${path}`, { ...init, headers: apiHeaders(config, init) })
  if (!response.ok) {
    const problem = await response.json().catch(() => ({ detail: response.statusText }))
    const message = problem.detail ?? problem.title ?? `Request failed: ${response.status}`
    throw new ApiError(problem.requestId ? `${message}（请求 ID：${problem.requestId}）` : message, response.status, problem.code, problem.retryable)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export async function apiBinary(path: string, init: RequestInit = {}): Promise<Blob> {
  const config = await getRuntime()
  const response = await fetch(`${config.apiBaseUrl}${path}`, { ...init, headers: apiHeaders(config, init) })
  if (!response.ok) {
    const problem = await response.json().catch(() => ({ detail: response.statusText }))
    const message = problem.detail ?? problem.title ?? `Request failed: ${response.status}`
    throw new ApiError(message, response.status, problem.code, problem.retryable)
  }
  return response.blob()
}

/** Dedicated architecture-agent credentials are not desktop or MCP tokens. */
export async function architectureAgentApi<T>(secret: string, path: string, init: RequestInit = {}): Promise<T> {
  if (!secret.trim()) throw new Error('需要专用架构 Agent 凭证')
  return api<T>(path, { ...init, headers: { ...init.headers, Authorization: `Bearer ${secret}` } })
}

type EmbeddingModelsResponse = { models: EmbeddingModel[] } | EmbeddingModel[]

function unwrapEmbeddingModels(response: EmbeddingModelsResponse): EmbeddingModel[] {
  return Array.isArray(response) ? response : response.models
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
  /** Unified desktop retrieval. Source and permission filters are enforced again by the API. */
  retrievalSearch: (input: { query: string; filters?: { sourceType?: 'all'|'knowledge'|'raw'|'architecture'; libraryIds?: string[]; projectIds?: string[] }; topK?: number; retrievalMode?: 'lexical'|'vector'|'hybrid' }) => api<RetrievalSearchResponse>('/retrieval/search', { method: 'POST', body: JSON.stringify(input) }),
  retrievalIndexStatus: (libraryId: string) => api<RetrievalGeneration>(`/retrieval/index?libraryId=${encodeURIComponent(libraryId)}`),
  retrievalIndexProgress: (libraryId: string) => api<RetrievalProgress>(`/retrieval/index/progress?libraryId=${encodeURIComponent(libraryId)}`),
  rebuildRetrievalIndex: (libraryIds: string[], modelId?: string) => api<{ generations: RetrievalGeneration[] }>('/retrieval/index/rebuild', { method: 'POST', body: JSON.stringify({ libraryIds, ...(modelId ? { modelId } : {}) }) }),
  cancelRetrievalIndex: (libraryId: string) => api<RetrievalProgress>('/retrieval/index/cancel', { method: 'POST', body: JSON.stringify({ libraryId }) }),
  query: (input: { query: string; libraryIds?: string[]; filters?: { sourceType?: 'all'|'knowledge'|'raw'|'architecture'; libraryIds?: string[]; projectIds?: string[] }; responseMode?: 'evidence'|'answer'; providerId?: string; topK?: number; retrievalMode?: 'lexical'|'vector'|'hybrid' }) => api<QueryResponse>('/query', { method: 'POST', body: JSON.stringify(input) }),
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
  embeddingModels: async () => unwrapEmbeddingModels(await api<EmbeddingModelsResponse>('/embedding-models')),
  installEmbeddingModel: (path: string) => api<EmbeddingModel>('/embedding-models/install', { method: 'POST', body: JSON.stringify({ path }) }),
  installEmbeddingModelOnline: (modelId: string) => api<EmbeddingModel>('/embedding-models/install-online', { method: 'POST', body: JSON.stringify({ modelId }) }),
  setEmbeddingModelEnabled: (modelId: string, enabled: boolean) => api<EmbeddingModel>('/embedding-models', { method: 'PATCH', body: JSON.stringify({ modelId, enabled }) }),
  deleteEmbeddingModel: (modelId: string) => api<EmbeddingModel>('/embedding-models', { method: 'DELETE', body: JSON.stringify({ modelId }) }),
  embeddingModelHealth: (modelId: string) => api<EmbeddingModelHealth>(`/embedding-models/health?modelId=${encodeURIComponent(modelId)}`),
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
  emptyTrash: () => api<void>('/trash', { method: 'DELETE' }),

  architectureProjects: (libraryId?: string) => api<ArchitectureProject[]>(`/projects${libraryId ? `?libraryId=${encodeURIComponent(libraryId)}` : ''}`),
  architectureProject: (id: string) => api<ArchitectureProject>(`/projects/${encodeURIComponent(id)}`),
  createArchitectureProject: (input: Omit<ArchitectureProject, 'id'|'createdAt'|'updatedAt'|'deletedAt'|'lastSnapshotId'|'lastSnapshotHash'|'lastScanChanged'|'analysisStatus'|'analysisError'|'currentAnalysisSnapshotId'|'scheduleNextAt'>) => api<ArchitectureProject>('/projects', { method: 'POST', body: JSON.stringify(input) }),
  updateArchitectureProject: (id: string, input: Partial<Pick<ArchitectureProject, 'name'|'description'|'rootPath'|'excludePatterns'|'scanPeriodSeconds'|'scheduleKind'|'scheduleAnchor'|'allowRemoteCode'|'execution'>>) => api<ArchitectureProject>(`/projects/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  deleteArchitectureProject: (id: string) => api<void>(`/projects/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  scanArchitectureProject: (id: string) => api<ArchitectureTask>(`/projects/${encodeURIComponent(id)}/scan`, { method: 'POST' }),
  generateArchitectureProject: (id: string) => api<ArchitectureTask>(`/projects/${encodeURIComponent(id)}/generate`, { method: 'POST' }),
  architectureTasks: (id: string) => api<ArchitectureTask[]>(`/projects/${encodeURIComponent(id)}/tasks`),
  cancelArchitectureTask: (taskId: string) => api<ArchitectureTask>(`/architecture-tasks/${encodeURIComponent(taskId)}/cancel`, { method: 'POST' }),
  architectureSnapshots: (id: string) => api<ArchitectureSnapshot[]>(`/projects/${encodeURIComponent(id)}/snapshots`),
  architectureSnapshot: (projectId: string, snapshotId: string) => api<ArchitectureSnapshot>(`/projects/${encodeURIComponent(projectId)}/snapshots/${encodeURIComponent(snapshotId)}`),
  architectureAnalysis: (projectId: string, snapshotId: string) => api<ArchitectureAnalysis>(`/projects/${encodeURIComponent(projectId)}/snapshots/${encodeURIComponent(snapshotId)}/analysis`),
  architectureDiff: (projectId: string, snapshotId: string, fromSnapshotId?: string) => api<ArchitectureDiff>(`/projects/${encodeURIComponent(projectId)}/versions/${encodeURIComponent(snapshotId)}/diff${fromSnapshotId ? `?fromSnapshotId=${encodeURIComponent(fromSnapshotId)}` : ''}`),
  architectureArtifacts: (projectId: string, snapshotId: string) => api<ArchitectureArtifact[]>(`/projects/${encodeURIComponent(projectId)}/snapshots/${encodeURIComponent(snapshotId)}/artifacts`),
  downloadArchitectureArtifact: (projectId: string, snapshotId: string, kind: string) => apiBinary(`/projects/${encodeURIComponent(projectId)}/snapshots/${encodeURIComponent(snapshotId)}/artifacts/${encodeURIComponent(kind)}`),
  architectureAgents: () => api<ArchitectureAgent[]>('/architecture/agents'),
  registerArchitectureAgent: (input: { name: string; capabilities: string[]; projectIds: string[] }) => api<ArchitectureAgentCredential>('/architecture/agents', { method: 'POST', body: JSON.stringify(input) }),
  claimArchitectureAgentTask: (secret: string, leaseSeconds?: number) => architectureAgentApi<ArchitectureAgentLease>(secret, '/architecture/agent/tasks/claim', { method: 'POST', body: JSON.stringify({ leaseSeconds }) }),
  heartbeatArchitectureAgentTask: (secret: string, input: { taskId: string; leaseToken: string }) => architectureAgentApi<void>(secret, '/architecture/agent/tasks/heartbeat', { method: 'POST', body: JSON.stringify(input) }),
  submitArchitectureAgentResult: (secret: string, input: { taskId: string; generation: number; leaseToken: string; analysis: ArchitectureAnalysis; artifacts: ArchitectureArtifactInput[] }) => architectureAgentApi<ArchitectureTask>(secret, '/architecture/agent/tasks/result', { method: 'POST', body: JSON.stringify(input) }),
  submitArchitectureAgentFailure: (secret: string, input: { taskId: string; leaseToken: string; message: string }) => architectureAgentApi<ArchitectureTask>(secret, '/architecture/agent/tasks/failure', { method: 'POST', body: JSON.stringify(input) })
}
