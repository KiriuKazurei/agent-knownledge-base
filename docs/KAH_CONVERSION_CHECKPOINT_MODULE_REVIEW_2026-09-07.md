# KAH 转换 checkpoint 与任务进度模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 前置模块：[分批增强转换模块审查](KAH_BATCHED_CONVERSION_MODULE_REVIEW_2026-09-07.md)

## 已完成的代码

- `jobs` 表增加 `stage`、`outcome`、`completed_units`、`total_units`、`error_code` 和 `checkpoint_json`，启动迁移会为旧数据库补齐列；重试任务默认从空 checkpoint 开始，原任务记录保留。
- `Store.UpdateJobCheckpoint` 以单次数据库更新提交状态、阶段、批次计数、错误码和 checkpoint；取消请求优先级高于迟到的 Provider 结果。
- `runKnowledgeSummarize` 在每个增强批次完成后保存候选结果和下一批编号；服务重启后由既有 `ResumeQueuedJobs` 重新调度，并从同一文档 hash、Provider 和提示词版本的 checkpoint 继续。
- 任务失败会保留已完成批次并写入稳定错误码；`GET /api/v1/jobs` 返回阶段和计数，桌面活动中心展示阶段、`完成/总数` 和错误码。
- 新增存储 checkpoint、恢复选择和 KAH 总结终态测试；OpenAPI `Job` 契约同步扩展。

## 门禁一：代码质量审查

结论：通过。

- Go 全量：`go -C services/api test ./... -count=1` 通过。
- Go 静态检查：`go -C services/api vet ./...` 通过；checkpoint、总结和服务端文件 `gofmt -d` 无输出。
- Worker 全量：24 项运行，19 项通过，5 项因当前环境缺少既有 Office/LanceDB 依赖按约定跳过；`compileall` 通过。
- 桌面 TypeScript：`apps\desktop\node_modules\.bin\tsc.cmd --noEmit -p apps\desktop\tsconfig.json` 通过；Vitest 使用 `node_modules\.bin\vitest.cmd run --configLoader runner`，5 个测试文件、32 项测试全部通过。
- OpenAPI Spectral 无 error 级问题，`git diff --check` 通过。

## 门禁二：代码连通性审查

结论：通过。

- 总结任务沿用现有 `CreateJob`、`ResumeQueuedJobs`、Store 和 Provider 链路；checkpoint 不创建旁路任务，也不绕过 KAH 草稿、审核或发布。
- `TestJobCheckpointPersistsStageCountersAndOutcome` 验证数据库保存、旧任务查询映射和终态结果；`TestRestoreImportedSummaryCheckpointSkipsCompletedBatches` 验证来源 hash、Provider 和提示词版本匹配后才恢复已完成批次。
- `TestImportedDocumentSummaryReviewAndPublishUsesKAHWorkflow` 通过真实 HTTP Provider、KAH review/publish 和稳定 revision 读取，并额外验证总结任务终态为 `completed`、`1/1`。
- API 返回的阶段和批次字段已经被桌面 `ActivityCenter` 读取显示；取消、重试继续使用既有 jobs API。

当前边界：目前只有 `knowledge_summarize` 的模型批次持久化候选结果；PDF 页级 OCR、完整提取快照和独立 `document_extract` 任务尚未接入同一 checkpoint 表。逐库转换策略随后已接入，但运行中任务尚未保存完整配置快照。真实 RapidOCR 模型包、断网运行、冷启动、性能及可见窗口验收仍未完成。
