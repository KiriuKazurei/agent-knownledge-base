# KAH 分批增强转换模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 前置模块：[完整来源转换审查](KAH_FULL_SOURCE_CONVERSION_MODULE_REVIEW_2026-09-07.md)

## 已完成的代码

- `runKnowledgeSummarize` 现在基于完整 chunk 集合按 48,000 Unicode 字符分批调用已配置 Provider，不再只处理首批截断文本。
- 每批响应先执行现有 JSON/KAH 字段/来源引用校验；任意批次超时、错误、响应过大或缺少引用都会使任务失败，并保留可重试的失败状态。
- 多批候选合并时为后续 section 加上稳定 batch 前缀，合并完整 source locator、chunkCount、content hash 和 `model-synthesis-batched` derivation；同一文档/Provider 的幂等版本升级到 `kah-import-summary-v2-batched`。
- 单批和多批仍只生成一个待审核 submission，继续使用现有审核、信度门槛、发布和索引链路。
- 自动总结入队前先创建无模型的 reference fallback；增强任务失败、取消或 Provider 不可用时，原始资料仍有可追溯的待审核草稿。

## 门禁一：代码质量审查

结论：通过。

- Go 全量：`go -C services/api test ./... -count=1` 通过。
- Go 静态检查：`go -C services/api vet ./...` 通过；转换文件 `gofmt -d` 无输出。
- 新增批次完整性、候选合并和完整来源定位测试通过；现有自动总结、缺少引用和 KAH 审核发布测试通过。
- Worker 全量 24 项运行，19 项通过，5 项因当前环境缺少既有 Office/LanceDB 依赖按约定跳过；Python compileall、桌面 TypeScript、OpenAPI Spectral 和 diff 检查通过。

## 门禁二：代码连通性审查

结论：通过。

- 批次仍由已有 `knowledge_summarize` job 调度，使用同一 Store、Provider、Secrets、KAH draft、自动审核和发布任务；没有新建孤立提交入口。
- 文件导入配置了自动总结时，队列先调用同一 `createImportedDocumentDraft`，再创建增强任务；fallback 与增强候选都使用同一文档 hash 和审核隔离规则。
- 自动总结真实回归 `TestImportedDocumentSummaryReviewAndPublishUsesKAHWorkflow` 通过，证明 Provider 响应仍可进入 KAH review/publish，并能读取稳定 revision。
- 文件导入、重复导入、完整 chunk 分页和 KAH 来源 hash 回归均通过；分批改造没有切断 Worker 解析或数据库到审核队列的连接。
- 任务取消检查位于每个 batch 前和合并前；未完成批次不会创建草稿，已创建草稿仍遵循现有幂等键。

当前边界：本模块完成时模型增强路径仍沿现有 goroutine job runner 执行，尚未持久化每批 checkpoint，也未在设置界面展示 batch 进度；这些缺口已由后续 [checkpoint 与任务进度模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md) 补齐。当前仍未把同一恢复机制扩展到 PDF 页级 OCR 和提取快照任务。
