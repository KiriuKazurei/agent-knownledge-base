export interface Library { id: string; name: string; description: string; icon: string; color: string; tags: string[]; favorite: boolean; allowRemoteModels: boolean; autoSummarizeImports: boolean; importConversionMode?: 'reference'|'enhanced'|'off'; summaryProviderId?: string; autoReviewAgentSubmissions: boolean; reviewProviderId?: string; deletedAt?: string; createdAt: string; updatedAt: string }
export interface Chunk { id: string; documentId: string; text: string; location: Record<string, unknown>; contentHash: string }
export interface Document { id: string; libraryId: string; title: string; mediaType: string; sourcePath?: string; sourceUrl?: string; objectPath?: string; contentHash?: string; status: 'pending'|'pending_review'|'approved_pending_index'|'rejected'|'ready'|'failed'|'source_missing'; error?: string; tags: string[]; folderIds?: string[]; favorite: boolean; deletedAt?: string; createdAt: string; updatedAt: string }
export interface DocumentDetail extends Document { preview: Chunk[] }
export interface KnowledgeDirectoryEntry { uri: string; revision: number; title: string; description: string; type: string; subtype?: string; language: 'zh-CN'|'en'; primaryPath: string[]; classifications?: Record<string, string[]>; tags: string[]; matchedSectionIds: string[]; matchReason: string; flags: string[]; trust: string; conflictUris?: string[] }
export interface KnowledgeSearchResponse { results: KnowledgeDirectoryEntry[]; nextCursor?: string }
export interface GraphPosition { xPct: number; yPct: number }
export interface GraphViewport { scale: number; panX: number; panY: number }
export interface GraphWorkspace { id: string; libraryId: string; name: string; filters: Record<string, unknown>; viewport: Record<string, unknown>; createdAt: string; updatedAt: string }
export interface GraphNode { id: string; kind: 'knowledge'|'document'|'external_source'|'tag_cluster'; label: string; description?: string; uri: string; revision?: number; status?: string; trust?: string; tags: string[]; position: GraphPosition|null }
export interface GraphEdge { id: string; kind: 'relation'|'source'|'derivation'|'cluster'; relationType: string; source: string; target: string; targetRevision?: number; sourceId?: string; locator?: Record<string, unknown>; snapshot?: Record<string, unknown> }
export interface GraphSnapshot { workspace: GraphWorkspace; nodes: GraphNode[]; edges: GraphEdge[]; truncated: boolean; hiddenNodeCount: number; limits: { requestedDepth: number; appliedDepth: number; maxNodes: number } }
export interface GraphPositionUpdate extends GraphPosition { nodeId: string }
export interface KnowledgeSection { id: string; heading: string; content: string }
export interface KnowledgeRevision { libraryId: string; knowledgeId: string; uri: string; revision: number; payload: { schema: string; id: string; revision: number; title: string; description: string; type: string; subtype?: string; language: 'zh-CN'|'en'; aliases?: string[]; primaryPath?: string[]; classifications?: Record<string, string[]>; tags?: string[]; sections: KnowledgeSection[]; sources?: { id: string; resource: string; title?: string; locator?: Record<string, unknown>; snapshot?: { status?: string; content_hash?: string; captured_at?: string } }[]; relations?: { type: string; target: string; target_revision?: number }[]; derivation?: Record<string, unknown> }; markdown: string; contentHash: string; status: 'draft'|'approved_pending_index'|'rejected'|'stable'|'deprecated'; flags: string[]; stable: boolean; createdAt: string; submittedBy?: string }
export interface Job { id: string; kind: string; status: string; progress: number; message?: string; stage?: string; outcome?: string; completedUnits?: number; totalUnits?: number; errorCode?: string; originJobId?: string; canCancel?: boolean; canRetry?: boolean; createdAt: string; updatedAt: string }
export type ArchitectureScheduleKind = 'manual'|'daily'|'weekly'|'interval'|'fixed'
export type ArchitectureExecutor = 'auto'|'codex'|'mcp'|'provider'|'none'
export interface ProjectExecutionConfig { providerId?: string; executor: ArchitectureExecutor; codexPath?: string; allowExternal: boolean; codexModel?: string; timeoutSeconds: number }
export interface ArchitectureProject { id: string; libraryId: string; name: string; description?: string; rootPath: string; excludePatterns: string[]; scanPeriodSeconds: number; scheduleKind: ArchitectureScheduleKind; scheduleAnchor?: string; scheduleNextAt?: string; allowRemoteCode: boolean; execution: ProjectExecutionConfig; lastSnapshotId?: string; lastSnapshotHash?: string; lastScanChanged?: boolean; analysisStatus?: string; analysisError?: string; currentAnalysisSnapshotId?: string; createdAt: string; updatedAt: string; deletedAt?: string }
export interface ArchitectureFile { path: string; language: string; size: number; lineStart: number; lineEnd: number; lineCount: number; contentHash: string; gitStatus?: string; ignored?: boolean; entryPoint: boolean; dependencyFiles?: string[]; containerFiles?: string[]; detection?: string }
export interface ArchitectureModule { name: string; language: string; rootPath: string; version?: string; dependencies: string[]; dependencyVersions?: Record<string, string>; entryPoints: string[]; dependencyFiles: string[]; containerFiles: string[]; unknown: boolean; evidencePaths: string[] }
export interface ArchitectureCoverage { totalFiles: number; includedFiles: number; ignoredFiles: number; secretFiles: number; binaryFiles: number; outsideLinks: number; unknownFiles: number; byLanguage: Record<string, number>; ignoredReasons: Record<string, number> }
export interface ArchitectureSnapshot { id: string; projectId: string; generation: number; status: string; contentHash: string; gitRevision?: string; gitDirty: boolean; gitChangedPaths: string[]; fileCount: number; moduleCount: number; unknownCount: number; coverage: ArchitectureCoverage; files: ArchitectureFile[]; modules: ArchitectureModule[]; createdAt: string }
export interface ArchitectureAnalysisRequest { project: ArchitectureProject; snapshot: ArchitectureSnapshot; incremental: ArchitectureIncremental; prompt?: string }
export interface ArchitectureTask { id: string; projectId: string; kind: 'scan'|'analysis'|'mcp'|string; input?: ArchitectureAnalysisRequest; status: 'queued'|'running'|'completed'|'failed'|'cancelled'|string; generation: number; snapshotId?: string; cancelRequested: boolean; errorCode?: string; message?: string; executor?: string; createdAt: string; updatedAt: string }
export interface ArchitectureArtifact { id: string; projectId: string; snapshotId: string; kind: 'document'|'diagram_json'|'html'|'svg'|'png'|string; fileName: string; contentType: string; renderer?: string; rendererError?: string; relativePath: string; contentHash: string; size: number; createdAt: string }
export interface AnalysisFact { id: string; subject: string; predicate: string; object: string; evidence: string[] }
export interface AnalysisInference { id: string; statement: string; evidence: string[]; confidence: number }
export interface AnalysisEvidence { id: string; path: string; lineStart: number; lineEnd: number; contentHash: string; kind: string }
export interface ArchitectureIncremental { baseSnapshotId?: string; added: string[]; changed: string[]; deleted: string[]; unchanged: string[] }
export interface ArchitectureAnalysis { schemaVersion: string; projectId: string; snapshotId: string; facts: AnalysisFact[]; inferences: AnalysisInference[]; evidence: AnalysisEvidence[]; incremental: ArchitectureIncremental; coverage: ArchitectureCoverage }
export interface ArchitectureDiff { projectId: string; fromSnapshotId?: string; toSnapshotId: string; added: string[]; changed: string[]; deleted: string[]; unchanged: string[]; analysis?: ArchitectureAnalysis }
export interface ArchitectureAgent { id: string; name: string; capabilities: string[]; projectIds: string[]; status: string; lastHeartbeat: string; createdAt: string }
export interface ArchitectureAgentCredential { agent: ArchitectureAgent; secret: string }
export interface ArchitectureAgentLease { agentId: string; task: ArchitectureTask; leaseToken: string; leaseExpires: string }
export interface ArchitectureArtifactInput { kind: string; fileName: string; contentType: string; renderer?: string; rendererError?: string; data: string }
export interface Skill { id: string; name: string; systemRole?: string; description: string; compatibility?: string; license?: string; metadata?: Record<string, string>; allowedTools?: string; rootPath: string; entryPoint: string; contentHash: string; status: 'ready'|'failed'|'invalid'; error?: string; fileCount: number; usesLibraryIds: string[]; requiresLibraryIds: string[]; createdAt: string; updatedAt: string }
export interface SkillMappingTarget { id: string; name: string; kind: 'agent'|'project'; directoryPath: string; status: 'pending'|'ready'|'partial'|'missing'|'conflict'|'invalid'|'permission_required'; error?: string; mappings: SkillMapping[]; createdAt: string; updatedAt: string; lastVerifiedAt?: string }
export interface SkillMapping { targetId: string; skillId: string; skillName: string; sourcePath: string; linkName: string; linkPath: string; status: 'pending'|'ready'|'missing'|'conflict'|'permission_required'|'invalid'; error?: string; createdAt: string; updatedAt: string; lastVerifiedAt?: string }
export interface SkillFile { path: string; size: number; sha256: string; mediaType: string; url?: string; content?: string }
export interface SkillManifest { skill: Skill; root: string; entryPoint: SkillFile; files: SkillFile[] }
export interface Provider { id: string; name: string; kind: 'openai'|'anthropic'|'lmstudio'|'custom'; baseUrl: string; model: string; embeddingModel: string; local: boolean; apiKey?: string }
export interface EmbeddingModelHealth {
  available: boolean
  modelId: string
  version?: string
  dimension?: number
  runtime?: string
  runtimeVersion?: string
  runtimeBytes?: number
  expectedRuntimeVersion?: string
  tokenizerRuntimeVersion?: string
  expectedTokenizerRuntimeVersion?: string
  runtimeMismatch?: string[]
  error?: string
}
export interface EmbeddingModel {
  id: string
  version?: string
  upstream?: string
  upstreamCommit?: string
  license?: string
  licenseUrl?: string
  dimension?: number
  maxTokens?: number
  tokenizer?: { configHash?: string; type?: string; runtimeVersion?: string }
  runtime?: { name?: string; version?: string; device?: string }
  pooling?: { type?: string; normalize?: boolean }
  files?: { path: string; size: number; sha256: string; role: string; url?: string }[]
  onlineInstall?: boolean
  onlineDisabledReason?: string
  installed: boolean
  enabled: boolean
  sizeBytes?: number
  runtimeBytes?: number
  health?: EmbeddingModelHealth | null
}
export interface RetrievalScores { lexical: number; vector: number; fusion: number; final: number; symbolMatch?: boolean }
export interface RetrievalResult {
  id: string
  documentId?: string
  text: string
  sourceType: 'knowledge'|'raw'|'architecture'|string
  reviewStatus: 'stable'|'unreviewed'|'derived'|'deprecated'|string
  libraryId: string
  projectId?: string
  sourceURI?: string
  title?: string
  filePath?: string
  lineStart?: number
  lineEnd?: number
  contentHash: string
  symbol?: string
  language?: string
  permissionScope?: 'library'|'project'|string
  location?: Record<string, unknown>
  tokenCount?: number
  tokenLimit?: number
  truncated?: boolean
  scores: RetrievalScores
}
export type RetrievalSourceType = 'all'|'knowledge'|'raw'|'architecture'
export interface RetrievalSearchResponse {
  results: RetrievalResult[]
  source?: string
  generation?: string
  semanticAvailable: boolean
  keywordOnly: boolean
  degraded: boolean
  degradedReason?: string
  modelId?: string
  modelVersion?: string
  configHash?: string
  dimension?: number
  corpusComplete?: boolean
  needsRebuild?: boolean
  retrievalMode: 'lexical'|'vector'|'hybrid'|string
}
export interface RetrievalGeneration {
  libraryId: string
  generation?: string
  status?: 'current'|'stale'|'queued'|'building'|'failed'|'cancelled'|string
  count?: number
  semanticAvailable: boolean
  keywordOnly?: boolean
  degraded?: boolean
  degradedReason?: string
  modelId?: string
  modelVersion?: string
  configHash?: string
  dimension?: number
  corpusComplete?: boolean
  needsRebuild?: boolean
}
export interface RetrievalProgress {
  libraryId: string
  generation?: string
  status: 'idle'|'running'|'completed'|'failed'|'cancelled'|'cancellation_requested'|'unknown'|string
  completed: number
  total: number
  error?: string
}
export interface QueryResponse {
  requestId?: string
  evidence?: RetrievalResult[]
  results?: RetrievalResult[]
  requiredSkills?: Skill[]
  answer?: string
  degraded?: boolean
  degradedReason?: string
}
export interface AgentToken { id: string; name: string; scopes: ('mcp_read'|'mcp_manage')[]; libraryIds: string[]; secret?: string; createdAt: string; revokedAt?: string }
export interface KAHReviewRecord { id: string; submissionId: string; reviewerType: 'model'|'agent'|'human'; reviewer: string; decision: 'approve'|'reject'|'needs_human'; confidence?: number; reason: string; createdAt: string }
export interface KAHSubmission { id: string; libraryId: string; knowledgeUri: string; revision: number; mode: 'create'|'propose_revision'; reviewStatus: 'pending_review'|'reviewing'|'approved_pending_index'|'rejected'|'published'; validation: Record<string, unknown>; title: string; summary: string; tags: string[]; provenance: Record<string, unknown>; clientSubmissionId: string; submittedByTokenId?: string; markdown?: string; reviews?: KAHReviewRecord[]; createdAt: string; updatedAt: string }
export interface SavedSearch { id: string; name: string; query: string; view: 'sources'|'knowledge'; libraryIds: string[]; tags: string[]; filters: { tags: string[]; tagMode: 'all'|'any'; favorite: boolean; folderIds?: string[]; statuses?: string[]; mediaTypes?: string[]; sourceKinds?: string[]; favoritesOnly?: boolean }; sort: { field: string; direction: 'asc'|'desc' } }
export interface VirtualFolder { id: string; libraryId: string; name: string; parentId?: string; createdAt?: string; updatedAt?: string }
export interface SourceWatch { id: string; libraryId: string; rootPath: string; recursive: boolean; enabled: boolean; lastScanAt?: string; lastMessage?: string; createdAt?: string; updatedAt?: string }
export interface TrashEntry { kind: 'library'|'document'; id: string; libraryId?: string; name: string; description: string; itemCount: number; deletedAt: string }
export type DocumentBatchAction = 'favorite'|'add_tags'|'remove_tags'|'add_folders'|'remove_folders'|'trash'|'reindex'
export interface DocumentBatchRequest { documentIds: string[]; action: DocumentBatchAction; tags?: string[]; folderIds?: string[]; favorite?: boolean }
export interface DocumentBatchResult { libraryId: string; documentIds: string[]; updatedCount: number; jobIds?: string[] }
export interface BackupVerification { path: string; sha256: string; manifest: Record<string, unknown> }
export interface BackupRecord { path: string; size: number; createdAt: string }
export interface AuditEntry { id: string; actor: string; action: string; target: string; details: Record<string, unknown>; createdAt: string }
