# PDF 离线 OCR 提取模块门禁审查

> 复验更正（2026-09-07）：本页保留为实施阶段历史记录，“通过”仅对应当时测试覆盖，不再代表模块验收通过。最新总体验收不通过，本模块缺口、门禁结果与修复要求以 [复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md) 为准。

> 日期：2026-09-07\
> 对应方案：[离线 OCR 与 KAH 转换升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)\
> 模块范围：Worker PDF 页级 OCR 适配、解析定位信息、Worker RPC 参数和 API 导入调用。

## 已完成的代码

- `services/worker/src/knowledge_worker/ocr.py` 增加 RapidOCR 兼容适配层：只接受本地模型目录和显式模型文件，缺资源时不会尝试联网下载；兼容当前 RapidOCR 对象结果和旧版 tuple 结果。
- `services/worker/src/knowledge_worker/parsers.py` 的 PDF 解析增加 `off/auto/force` 三种模式。`auto` 只对空页或含 U+FFFD/NUL 的不可信文本层触发 OCR；正常文字层保持原文。
- OCR 结果按页写入 `pdf-ocr` 定位块，带 `page`、`extraction`、`ocrConfidence` 和识别框；原文字层写入 `pdf-text` 定位块。页级摘要写入 Worker 返回的 `extraction` 元数据。
- `services/worker/src/knowledge_worker/server.py` 接收并转发 `ocrMode`；`services/api/internal/httpapi/server.go` 的实际文件导入默认传递 `ocrMode=auto`，OCR 结果仍沿现有 chunks → SQLite → index_upsert → KAH 草稿链路进入系统。
- `services/worker/pyproject.toml` 增加独立 `ocr` 可选依赖组；`services/worker/ocr_models/README.md` 固定离线模型目录、环境变量和资源包要求。
- `services/worker/tests/test_pdf_ocr.py` 覆盖 OCR 结果归一化、空 PDF 自动 OCR、`off` 模式、页级定位和 RPC 参数转发。

## 门禁一：代码质量审查

结论：通过。

- Python Worker 全量：`python -m unittest discover -s services/worker/tests -v`，21 项运行，16 项通过，5 项因当前解释器未安装既有 Office/LanceDB 依赖而按仓库测试约定跳过；新增 4 项 OCR 测试全部通过。
- Python 语法：`python -m compileall -q services/worker/src services/worker/tests` 通过。
- Go API 全量：`go -C services/api test ./... -count=1` 重跑通过。第一次运行的 Windows 临时数据库删除失败是偶发文件锁，单测单独重跑和完整重跑均通过。
- Go 静态检查：`go -C services/api vet ./internal/httpapi ./internal/worker` 通过；`gofmt -d services/api/internal/httpapi/server.go` 无输出。
- 桌面类型检查：`apps\desktop\node_modules\.bin\tsc.cmd --noEmit -p apps\desktop\tsconfig.json` 通过。
- `git diff --check` 通过。没有清理或覆盖工作区中其他未提交改动。

## 门禁二：代码连通性审查

结论：通过现有链路；真实 OCR 引擎运行仍待离线资源验收。

- Worker RPC 测试确认 `parse` 接收 `ocrMode` 并传入解析器；模拟 OCR 页结果经过同一 `parse_document`、chunk 稳定 ID 和页定位路径。
- API→Worker→SQLite/KAH 回归：`TestFileImportCreatesTraceablePendingKAHDraft` 和 `TestDuplicateFileImportDoesNotCreateSecondKAHDraft` 通过，证明新增 parse 参数不会破坏导入、索引前置处理、可追溯草稿及幂等去重。
- API `go test ./...` 通过，现有 source watch、knowledge、storage 和 worker 包回归保持连通。
- OCR 失败仍沿现有错误路径返回，文件不会以空内容或乱码内容进入 chunks/index；正常文本 PDF 不要求 OCR 资源即可继续导入。

尚未宣称的证据：当前环境没有安装 `pypdf`、`pypdfium2`、RapidOCR 模型和 ONNX Runtime，因此没有在本机运行真实扫描 PDF 的识别准确率、冷启动、内存和离线断网验收。下一模块开始前，必须先在 P0 离线构建环境安装并锁定 `ocr` extra，导入带 SHA-256 manifest 的实际模型包，完成真实中文扫描 PDF 测试；这项限制不影响本模块的接口和现有导入链路回归。

## 后续状态

“离线 OCR 资源包与健康检查”模块已完成，见 [OCR 资源模块审查](OCR_RESOURCE_MODULE_REVIEW_2026-09-07.md)。转换批次 checkpoint 已完成，见 [KAH 转换 checkpoint 模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md)。本模块仍缺少真实 RapidOCR 模型包和扫描 PDF 的离线运行证据；OCR 页级任务恢复尚未接入。
