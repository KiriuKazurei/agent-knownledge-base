# OCR 与原始知识到 KAH 实施状态

> 2026-09-08 更新：已完成可卸载 OCR 资源插件、真实扫描/混合 PDF 识别及无 Agent 导入 KAH 链验证，见 [实际验证记录](OCR_PLUGIN_AND_REAL_VALIDATION_2026-09-08.md)。复杂版式的双栏与规则表格已由真实本地模型回归为 10/10，并已进入结构化 KAH 草稿；本轮模块记录见 [双栏](OCR_COLUMN_LAYOUT_MODULE_REVIEW_2026-09-08.md)、[表格](OCR_TABLE_LAYOUT_MODULE_REVIEW_2026-09-08.md)、[结构化 KAH](OCR_STRUCTURED_KAH_MODULE_REVIEW_2026-09-08.md) 与 [桌面审阅/启动诊断](OCR_DESKTOP_REVIEW_AND_STARTUP_MODULE_REVIEW_2026-09-08.md)。干净机器发行验收与本轮 P2 的人工可读性确认仍未完成。

> 后续修复：A1–A9 已做针对性修复，原失败回归已转绿；本页保留此前验收历史。最新结果与未验收边界见 [阻断项修复与双门禁](OCR_KAH_BLOCKER_FIXES_2026-09-07.md)。

> 更新时间：2026-09-07

方案主文档：[离线 PDF OCR 与原始资料到 KAH 的全链路升级方案](OFFLINE_OCR_AND_KAH_CONVERSION_PLAN.md)

## 历史复验结论：不通过（已由 2026-09-08 模块复验部分替代）

2026-09-07 复验发现 OCR 完整性误报、任务失败无法写回、新库增强设置不通和恢复策略等阻断问题。此前“六模块均通过两道门禁”的结论撤回。完整证据与修复顺序见 [OCR 与 KAH 转换复验记录](OCR_KAH_ACCEPTANCE_2026-09-07.md)。

## 已实现、待修复复验的模块

| 顺序 | 模块 | 审查记录 |
| --- | --- | --- |
| 1 | PDF 页级离线 OCR 适配与导入连通 | [PDF 离线 OCR 模块审查](OFFLINE_OCR_MODULE_REVIEW_2026-09-07.md) |
| 2 | OCR 资源 manifest、哈希校验与健康检查 | [OCR 资源模块审查](OCR_RESOURCE_MODULE_REVIEW_2026-09-07.md) |
| 3 | 完整来源分页与 reference KAH 转换 | [完整来源转换审查](KAH_FULL_SOURCE_CONVERSION_MODULE_REVIEW_2026-09-07.md) |
| 4 | 分批增强转换与 reference fallback | [分批增强转换审查](KAH_BATCHED_CONVERSION_MODULE_REVIEW_2026-09-07.md) |
| 5 | 转换 checkpoint、重启恢复与桌面任务进度 | [checkpoint 模块审查](KAH_CONVERSION_CHECKPOINT_MODULE_REVIEW_2026-09-07.md) |
| 6 | 逐库 `reference / enhanced / off` 转换策略 | [转换策略模块审查](IMPORT_CONVERSION_MODE_MODULE_REVIEW_2026-09-07.md) |

以上审查保留为实施阶段历史记录，其通过结论由最新复验记录取代。真实 OCR 引擎、资源包和可见窗口仍未验收。

## 当前验证结果

- Go API：全量测试失败，新增两项验收用例分别复现失败状态未持久化、off 重放仍创建草稿；`go -C services/api vet ./...` 通过。
- Worker：26 项运行，19 项通过，5 项跳过，新增两项验收用例分别 failure/error，复现空 OCR 输出被标记完整、文字层异常不进入 OCR。
- 桌面：TypeScript 检查通过；Vitest 使用 `--configLoader runner`，5 个测试文件、32 项测试通过。
- OpenAPI Spectral 无 error 级问题，`git diff --check` 通过。

## 尚未宣称完成的证据

- 当前机器没有 `pypdf`、`pypdfium2`、RapidOCR、ONNX Runtime 或实际 OCR 模型包，因此扫描 PDF 的真实识别、中文准确率、断网首次运行、资源 DLL 加载、冷启动、内存和性能尚未验收。
- OCR 适配目前运行在现有 Worker 进程内；方案中的独立 `kah-ocr` 子进程、Python 3.12 专用打包和真实离线资源包仍是 P0/P1 后续工作。
- 提取快照、`document_extract`/`knowledge_convert` 独立任务、批量处理接口、OCR 页级 checkpoint、桌面导入/重试/定位的完整可见窗口验收仍未完成。

下一步先修复复验报告中的阻断项并逐模块重过两道门禁，再推进真实离线包、提取快照等未完成工作。当前 checkpoint/进度链路尚未通过恢复与失败验收。
