# 完整来源读取与基础 KAH 转换模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 前置模块：[OCR 资源包模块审查](OCR_RESOURCE_MODULE_REVIEW_2026-09-07.md)

## 已完成的代码

- `services/api/internal/storage/store.go` 增加稳定 ordinal 分页的 `ListDocumentChunks`；保留文档详情的 500 块预览边界，转换读取不再复用预览查询。
- `services/api/internal/httpapi/knowledge_summarize.go` 在模型转换开始时分页读取完整 chunk；分页错误会使任务失败，不会把截断内容标记成成功。
- `createImportedDocumentDraft` 现在读取完整来源，并按最多 24,000 Unicode 字符分成有序 KAH `reference` sections；每节保留 `[^source]` 引用，source locator 记录完整 chunk ID 和数量。
- 长文分段仍使用现有 KAH schema、来源快照、幂等 submission、审核和稳定 revision，不创建平行入库链路。
- 新增存储分页和长来源分段测试；现有导入、重复导入和 KAH 草稿回归继续覆盖实际 API→Worker→SQLite→草稿路径。

## 门禁一：代码质量审查

结论：通过。

- Go 全量：`go -C services/api test ./... -count=1` 通过。
- Go 静态检查：`go -C services/api vet ./...` 通过；`gofmt -d` 对本模块涉及文件无输出。
- 新增 `TestListDocumentChunksReadsBeyondPreviewPage`、`TestImportedReferenceSectionsCoverLongSource` 与既有导入/幂等测试通过。
- Worker 全量：24 项运行，19 项通过，5 项因环境缺少既有 Office/LanceDB 依赖按约定跳过；`compileall` 通过。
- 桌面 TypeScript 检查和 OpenAPI Spectral lint 通过；`git diff --check` 通过。

## 门禁二：代码连通性审查

结论：通过。

- 文件导入创建 ready 文档后，现有 `queueImportedKnowledgeProcessing` 进入完整来源读取；无 Provider 时仍生成待审核 reference 草稿，Provider 路径读取同一完整 chunk 集合。
- 转换产物继续通过 `Store.CreateKnowledgeDraft`，服务端验证 document URI、当前 content hash、source citation、幂等和审核状态；没有直接写稳定知识或绕过索引发布。
- `TestFileImportCreatesTraceablePendingKAHDraft` 和 `TestDuplicateFileImportDoesNotCreateSecondKAHDraft` 通过，证明分页读取改造未切断 Worker 解析、SQLite chunks、KAH 来源快照或重复导入防重。
- 全量 API 测试通过，说明 source watch、检索、KAH/MCP、审核发布和其他存储模块仍能与新分页方法共存。

当前边界：增强模式仍沿用现有单次 48,000 字符模型输入上限；本模块只保证无 Agent 的基础 reference 转换覆盖完整来源，尚未实现模型分批提炼、批次 checkpoint、转换任务状态和 UI 进度。分批提炼已由后续模块完成，checkpoint 与 UI 进度已由 [checkpoint 与任务进度模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md) 接续完成；提取快照和 OCR 任务恢复仍待实施。
