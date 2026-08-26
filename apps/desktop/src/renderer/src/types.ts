export interface Library { id: string; name: string; description: string; allowRemoteModels: boolean; createdAt: string; updatedAt: string }
export interface Chunk { id: string; documentId: string; text: string; location: Record<string, unknown>; contentHash: string }
export interface Document { id: string; libraryId: string; title: string; mediaType: string; sourcePath?: string; sourceUrl?: string; objectPath?: string; contentHash?: string; status: 'pending'|'ready'|'failed'|'source_missing'; error?: string; tags: string[]; favorite: boolean; createdAt: string; updatedAt: string }
export interface DocumentDetail extends Document { preview: Chunk[] }
export interface Evidence extends Chunk { libraryId: string; title: string; scores: { lexical: number; vector: number; fusion: number; final: number } }
export interface QueryResponse { requestId: string; evidence: Evidence[]; requiredSkills?: Skill[]; answer?: string; degraded: boolean; degradedReason?: string }
export interface Job { id: string; kind: string; status: string; progress: number; message?: string; createdAt: string; updatedAt: string }
export interface Skill { id: string; name: string; description: string; compatibility?: string; license?: string; metadata?: Record<string, string>; allowedTools?: string; rootPath: string; entryPoint: string; contentHash: string; status: 'ready'|'failed'|'invalid'; error?: string; fileCount: number; usesLibraryIds: string[]; requiresLibraryIds: string[]; createdAt: string; updatedAt: string }
export interface SkillFile { path: string; size: number; sha256: string; mediaType: string; url?: string; content?: string }
export interface SkillManifest { skill: Skill; root: string; entryPoint: SkillFile; files: SkillFile[] }
export interface SkillMatch extends Skill { score: number }
export interface SkillQueryResponse { requestId: string; skills: SkillMatch[] }
export interface Provider { id: string; name: string; kind: 'openai'|'anthropic'|'lmstudio'|'custom'; baseUrl: string; model: string; embeddingModel: string; local: boolean; apiKey?: string }
export interface AgentToken { id: string; name: string; scopes: string[]; libraryIds: string[]; secret?: string; createdAt: string; revokedAt?: string }
export interface SavedSearch { id: string; name: string; query: string; libraryIds: string[]; tags: string[] }
export interface VirtualFolder { id: string; libraryId: string; name: string; parentId?: string }
export interface SourceWatch { id: string; libraryId: string; rootPath: string; recursive: boolean; enabled: boolean; lastScanAt?: string; lastMessage?: string }
