import type { AgentToken, Document, DocumentDetail, Job, KAHSubmission, KnowledgeRevision, KnowledgeSearchResponse, Library, Provider, SavedSearch, Skill, SkillManifest, SourceWatch, VirtualFolder } from './types'

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
    throw new Error(problem.detail ?? problem.title ?? `Request failed: ${response.status}`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const client = {
  libraries: () => api<Library[]>('/libraries'),
  createLibrary: (name: string, description = '') => api<Library>('/libraries', { method: 'POST', body: JSON.stringify({ name, description }) }),
  updateLibrary: (id: string, patch: Partial<Library>) => api<Library>(`/libraries/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  documents: (libraryId?: string, folderId?: string, favorite = false) => api<Document[]>(`/documents?libraryId=${encodeURIComponent(libraryId ?? '')}${folderId ? `&folderId=${encodeURIComponent(folderId)}` : ''}${favorite ? '&favorite=true' : ''}`),
  document: (id: string) => api<DocumentDetail>(`/documents/${id}`),
  updateDocument: (id: string, patch: { title?: string; tags?: string[]; content?: string; favorite?: boolean }) => api<Document>(`/documents/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
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
  jobs: () => api<Job[]>('/jobs'),
  savedSearches: () => api<SavedSearch[]>('/saved-searches'),
  folders: (libraryId: string) => api<VirtualFolder[]>(`/folders?libraryId=${encodeURIComponent(libraryId)}`),
  createFolder: (libraryId: string, name: string, parentId?: string) => api<VirtualFolder>('/folders', { method: 'POST', body: JSON.stringify({ libraryId, name, parentId }) }),
  deleteFolder: (id: string) => api<void>(`/folders/${id}`, { method: 'DELETE' }),
  assignFolder: (documentId: string, folderId: string) => api<void>(`/documents/${documentId}/folders/${folderId}`, { method: 'PUT' }),
  watches: (libraryId: string) => api<SourceWatch[]>(`/sources/watches?libraryId=${encodeURIComponent(libraryId)}`),
  createWatch: (libraryId: string, rootPath: string) => api<{watch: SourceWatch; job: Job}>('/sources/watches', { method: 'POST', body: JSON.stringify({ libraryId, rootPath, recursive: true }) }),
  scanWatch: (id: string) => api<Job>(`/sources/watches/${id}/scan`, { method: 'POST' }),
  deleteWatch: (id: string) => api<void>(`/sources/watches/${id}`, { method: 'DELETE' }),
  createSavedSearch: (name: string, query: string, libraryIds: string[] = [], tags: string[] = []) => api<SavedSearch>('/saved-searches', { method: 'POST', body: JSON.stringify({ name, query, libraryIds, tags }) }),
  deleteSavedSearch: (id: string) => api<void>(`/saved-searches/${id}`, { method: 'DELETE' }),
  knowledgeSearch: (query: string, libraryIds: string[] = []) => api<KnowledgeSearchResponse>('/knowledge/search', { method: 'POST', body: JSON.stringify({ query, libraryIds, limit: 20 }) }),
  knowledge: (uri: string) => api<KnowledgeRevision>('/knowledge/resolve?uri=' + encodeURIComponent(uri)),
  submissions: (libraryId?: string) => api<KAHSubmission[]>('/knowledge/submissions?' + new URLSearchParams({ ...(libraryId ? { libraryId } : {}) }).toString()),
  submission: (id: string) => api<KAHSubmission>('/knowledge/submissions/' + encodeURIComponent(id)),
  approveSubmission: (id: string, reason = '') => api<KnowledgeRevision>('/knowledge/submissions/' + encodeURIComponent(id) + '/approve', { method: 'POST', body: JSON.stringify({ reason }) }),
  rejectSubmission: (id: string, reason: string) => api<KAHSubmission>('/knowledge/submissions/' + encodeURIComponent(id) + '/reject', { method: 'POST', body: JSON.stringify({ reason }) }),
  providers: () => api<Provider[]>('/providers'),
  saveProvider: (provider: Provider) => api<Provider>('/providers', { method: 'POST', body: JSON.stringify(provider) }),
  providerModels: (id: string) => api<{models:string[]}>(`/providers/${id}/models`),
  tokens: () => api<AgentToken[]>('/tokens'),
  createToken: (name: string, scopes: string[], libraryIds: string[]) => api<AgentToken>('/tokens', { method: 'POST', body: JSON.stringify({ name, scopes, libraryIds }) }),
  revokeToken: (id: string) => api<void>(`/tokens/${id}`, { method: 'DELETE' }),
  backup: () => api<{path:string;sha256:string}>('/backups', { method: 'POST', body: JSON.stringify({ includeIndexes: true }) })
}
