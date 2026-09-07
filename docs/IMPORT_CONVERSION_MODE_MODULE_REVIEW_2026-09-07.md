# 逐库导入转换策略模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 前置模块：[KAH 转换 checkpoint 与任务进度模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md)

## 已完成的代码

- `libraries` 增加 `import_conversion_mode`，支持 `reference`、`enhanced`、`off`；旧 `autoSummarizeImports` 仍读写，并映射到 `enhanced/reference`。
- 旧数据库迁移会把已有 `auto_summarize_imports=1` 记录映射为 `enhanced`，新建知识库默认 `reference`。
- 导入队列按逐库策略工作：`reference` 创建无模型 reference 草稿，`enhanced` 在有 Provider 时排队分批总结并先创建 fallback，没有 Provider 时保留 fallback，`off` 只完成原始文档导入。
- API、OpenAPI 和桌面隐私设置暴露同一三态策略；现有 Provider、远程授权、审核和 KAH 发布链路继续复用。

## 门禁一：代码质量审查

结论：通过。

- Go 全量：`go -C services/api test ./... -count=1` 通过；模式迁移、存储读写、转换队列测试通过。
- Go 静态检查：`go -C services/api vet ./...` 通过；相关 Go 文件 `gofmt -d` 无输出。
- Worker 全量：24 项运行，19 项通过，5 项因当前环境缺少既有 Office/LanceDB 依赖按约定跳过；Python compileall 通过。
- 桌面 TypeScript：`apps\desktop\node_modules\.bin\tsc.cmd --noEmit -p apps\desktop\tsconfig.json` 通过；Vitest `--configLoader runner` 的 5 个测试文件、32 项测试通过。
- OpenAPI Spectral 无 error 级问题，`git diff --check` 通过。

## 门禁二：代码连通性审查

结论：通过。

- 设置页面提交的 `importConversionMode` 经过 API 校验，写入同一 libraries 表，并在下一次导入读取；兼容字段不会产生第二套配置。
- `TestImportConversionModeOffSkipsKAHDraftCreation` 验证 `off` 策略仍完成源文档和 chunks 保存，但不创建 KAH 草稿或总结任务。
- 既有 reference 导入、自动总结、Provider、KAH review/publish 和稳定 revision 回归继续通过；策略切换没有切断 Worker→SQLite→KAH 链路。
- `reference` 和 `enhanced` 都沿用完整来源 locator、内容 hash、幂等键和 checkpoint；`off` 只跳过转换阶段，不影响原始文档检索。

当前边界：配置策略已贯通，但转换任务 payload 尚未完全保存运行时的模式快照；运行中的任务仍按现有总结 runner 读取库配置。下一步应把 extraction snapshot、转换 run 和配置快照写入持久化任务记录，并为 OCR 页级任务复用同一策略。
