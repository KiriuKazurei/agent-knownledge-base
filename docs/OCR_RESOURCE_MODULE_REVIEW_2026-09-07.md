# 离线 OCR 资源包与健康检查模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 前置模块：[PDF 离线 OCR 提取模块门禁审查](OFFLINE_OCR_MODULE_REVIEW_2026-09-07.md)

## 已完成的代码

- `services/worker/src/knowledge_worker/ocr_resources.py` 定义 `kah-ocr-resource/v1` manifest，拒绝绝对路径和目录穿越，逐文件校验 SHA-256，并解析检测/识别/方向/字典模型路径。
- RapidOCR 初始化现在必须先通过 manifest 和哈希校验；缺目录、缺 manifest、缺文件、哈希不一致或模型未声明时，不加载引擎，也不会尝试联网下载。
- Worker 新增 `ocr_health` JSON-RPC，只做确定性的资源检查，不加载大模型；`/api/v1/ocr/health` 将检查结果接入桌面 API，并在 OpenAPI 中登记。
- `services/worker/ocr_models/README.md` 记录资源目录、环境变量和发布包必须附带的版本、哈希与许可证要求。
- `services/worker/kah-worker.spec` 在 OCR extra 存在时收集 pypdfium2、RapidOCR 和 ONNX Runtime 的 Python 模块及原生文件；基础 Worker 在未安装 OCR extra 时仍可打包。

## 门禁一：代码质量审查

结论：通过。

- Worker 全量：`python -m unittest discover -s services/worker/tests -v`，24 项运行，19 项通过，5 项因当前环境没有既有 Office/LanceDB 依赖而按测试约定跳过；资源 manifest、哈希、缺失资源 RPC 和 OCR 回归全部通过。
- Python 语法：`python -m compileall -q services/worker/src services/worker/tests` 通过。
- API 定向回归：`go -C services/api test ./internal/httpapi ./internal/worker -run 'TestFileImportCreatesTraceablePendingKAHDraft|TestDuplicateFileImportDoesNotCreateSecondKAHDraft' -count=1` 通过。
- `go -C services/api vet ./internal/httpapi ./internal/worker` 通过；`gofmt -d services/api/internal/httpapi/server.go` 无输出。
- `node_modules\\.bin\\spectral.cmd lint contracts/openapi.yaml` 通过，没有 error 级问题；`git diff --check` 通过。

## 门禁二：代码连通性审查

结论：通过。

- Worker 的 `ocr_health` 通过同一 JSON-RPC dispatch 表注册，资源检查不会触发模型加载；解析器只有在页面确实需要 OCR 时才读取资源并创建引擎。
- API `/api/v1/ocr/health` 复用现有桌面认证、请求上下文和 Worker 客户端，Worker 不可用时返回统一 `worker_unavailable`/`ocr_health_failed` 问题，而不是伪造资源可用。
- 文件导入仍调用 `parse`，解析产生的 OCR chunk 继续使用现有 `ReplaceChunks`、`index_upsert`、来源草稿和 KAH 审核路径；资源健康检查未创建平行数据链路。
- API 导入/KAH 草稿回归已通过；OpenAPI lint 确认新增端点与契约可解析。

真实资源包仍未在当前机器安装，因此 `available=true`、模型 DLL 加载、断网识别准确率和资源包升级回滚尚未宣称通过。后台 checkpoint 已由后续 [checkpoint 与任务进度模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md) 接入 `knowledge_summarize`；OCR 页级任务尚未接入恢复调度。下一步仍需在构建环境生成真实 manifest，并以 `ocr_health` 作为导入前检查完成离线资源验收。
