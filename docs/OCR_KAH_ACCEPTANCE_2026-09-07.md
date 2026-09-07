# OCR 与 KAH 转换复验记录（2026-09-07）

> 后续修复：A1–A9 已做针对性修复，原失败回归已转绿；本页保留此前验收历史。最新结果与未验收边界见 [阻断项修复与双门禁](OCR_KAH_BLOCKER_FIXES_2026-09-07.md)。

## 结论与范围

**验收不通过。暂停扩展模块，先修复已复现的阻断问题，再重新执行两道门禁。**

本次针对当前工作区已经实现的六个子模块进行代码审查、回归测试和跨模块调用链核查。此前模块记录中的“通过”仅说明当时已有测试覆盖范围通过，不能作为功能验收通过的结论；以本记录为准。工作区还存在关系图等既有变更，本次没有修改这些功能，也没有修改 OCR/KAH 生产代码，仅增加回归测试和修正文档。

本次未执行真实 OCR 模型识别、断网安装包首次启动或真实 Electron 可见窗口操作，不把单元测试或接口接通等同于这些验收。

## 两道门禁结果

| 模块 | 代码质量门禁 | 连通性门禁 | 复验结论 |
| --- | --- | --- | --- |
| 1. PDF 离线 OCR | 不通过：空 OCR 输出被标记完整；文字层异常未进入 OCR | 不通过：提取质量未传递到转换/审核，具体错误被 API 覆盖 | 不通过，且真实引擎尚未验收 |
| 2. 资源与健康检查 | 未通过完整验收：实际模型路径可偏离 manifest 校验对象 | 资源文件检查已接 API，但不证明运行库/模型可初始化 | 部分实现，不能宣布离线可用 |
| 3. 全量来源与 reference | 原有分页/长文测试通过；转换仍直接拼接带 overlap 的检索块 | 读取与草稿创建已连接，但没有不可变提取快照及完整质量信息 | 部分通过，不满足无重复与完整来源要求 |
| 4. 分批增强与 fallback | 原有测试通过；跨批次来源定位、崩溃边界仍有缺口 | 草稿和审核入队之间存在恢复断点 | 部分通过，需补恢复与来源验收 |
| 5. checkpoint 与进度 | 不通过：工作 context 失效时失败状态无法持久化 | 不通过：运行配置/提取版本未完整绑定恢复点，进度可长期停留 running | 不通过 |
| 6. 逐库转换策略 | 原有策略测试通过；新增重放用例失败 | 不通过：新库无法在设置中完成首次启用增强；off 重放仍创建草稿 | 不通过 |

## 阻断项与证据

### A1 / P1：无法可靠识别提取遗漏

`services/worker/src/knowledge_worker/parsers.py:198` 仅按文字是否为空、是否含替换字符/NUL 或 force 决定 OCR。有页眉文字、正文为扫描图像的同页混合 PDF 会跳过 OCR。OCR 无结果的页面没有形成可供后续阻断的缺页状态，而 `parse_document` 在第 369 行无条件设置 `extraction.status=complete`。

新增 `test_unrecognized_scan_is_not_reported_complete` 已复现空结果仍 complete。此用例模拟控制流，不声称模拟文件是真实扫描语料；空页也必须有明确分类，不能将未分类空结果直接作为完整提取证据。API 的 `parseDocument` 返回 chunks，未将整体 extraction 质量送入转换和审核，因此现有完整性状态不足以阻止缺页资料被继续转换。

修复验收：区分空白、非文字内容、识别失败和成功；同页混合资料不漏正文；质量状态持久化并传递到转换/审核，缺页不能自动发布。

### A2 / P1：文字层异常没有 OCR 补救

`parsers.py:198` 的 `page.extract_text()` 抛异常后直接退出，尚未尝试渲染/OCR。新增 `test_broken_text_layer_attempts_ocr` 以可恢复文字层异常复现，测试报 `ValueError: broken text layer`。

修复验收：auto/force 对可渲染但文字层损坏的页面执行 OCR；不可渲染/加密等失败保留明确原因和页码。

### A3 / P1：任务失败无法写回，桌面进度不可信

`knowledge_summarize.go:108` 使用有限时长 context；`server.go:1167` 的 `failJob` 沿用该 context 查询 checkpoint 和更新任务，最后一次写入错误被忽略。context 失效时写回全部失败，任务仍显示 running。新增 `TestAcceptanceExpiredSummaryContextPersistsFailure` 用已取消 context 复现，实际状态为 `running`。该用例验证失效 context 的持久化路径，没有等待真实十分钟超时。

修复验收：使用独立、有限时长的收尾 context 写入终态，并处理持久化失败；超时/取消后的 API 与桌面状态一致。

### A4 / P1：新库无法首次启用增强

静态贯通核查：`apps/desktop/src/renderer/src/App.tsx:1148` 只在 `autoSummarizeImports=true` 时显示“总结模型”选择器；选择 enhanced 只提交转换策略。`services/api/internal/httpapi/server.go:370` 又要求启用时已经存在 summaryProviderId，否则返回 `summary_provider_required`。新库没有预配置模型时，用户无法先选模型，也无法先启用。这是前后端约束冲突，现有桌面测试没有覆盖此路径；本次未做真实窗口复现。

修复验收：默认新库可选择本地 Provider 并保存 enhanced；错误可恢复，保存后重启设置仍一致；无 Provider 时 reference 可独立使用。

### A5 / P1：恢复策略与配置快照不一致

`knowledge_summarize.go:129` 将所有非 enhanced 状态都转为 reference，包含 off。新增 `TestAcceptanceRecoveredSummaryHonorsOffMode` 在 off 库重放总结任务，实际生成一条 KAH submission。

方案要求任务沿用创建时配置，但当前任务没有完整配置快照。因此当前行为既没有恢复原任务配置，也没有正确按当前 off 跳过。不能仅增加一个 off 分支便宣称达到方案中的恢复语义。

此外，`restoreImportedSummaryCheckpoint` 只比较文档 ID、文件 hash、Provider ID、提示版本等；相同 Provider ID 下更换模型/地址，或同文件重新提取，仍可能复用旧候选。需要将实际模型配置、提取版本和批次计划纳入运行标识。

### A6 / P2：OCR 具体错误在 API 丢失

`server.go:1080-1103` 丢弃 Worker parse 的原始错误，PDF 最终统一返回 `document worker is unavailable for this format`。模型缺失、资源校验失败和解析错误无法准确展示，新增的错误码分类也无法恢复被丢失的信息。

修复验收：从 Worker 经过 API、任务持久化到桌面均保留安全的错误码、原因与页级定位；不能将运行中的 Worker 误报成不可用。

### A7 / P2：健康检查的资源与实际加载资源可能不同

`services/worker/src/knowledge_worker/ocr.py` 先验证 manifest，再由 `_model_file` 接受 `KAH_OCR_DET_MODEL` 等覆盖路径；覆盖路径只检查存在，没有校验其是否为 manifest 中的同一文件及同一 hash。健康检查通过不足以证明实际加载文件已验证，也不足以证明依赖导入及引擎初始化成功。

修复验收：对最终实际加载的模型和字典校验；拆分资源完整、依赖可用、引擎可启动三个状态，用真实离线资源验证。

### A8 / P2：全文与引用尚未达到方案要求

`parsers.py:311` 的检索切块默认 overlap=120；reference 与增强材料按 chunks 拼接，没有从非重叠提取块生成，边界文字会重复。增强合并后的统一 source 也不能表达每个生成章节对应的具体批次证据。分页读到尾部的测试通过不等于全文无重复和章节引用精确。

修复验收：转换基于非重叠、不可变的提取内容；跨块/跨批次唯一证据不丢失、不重复，章节引用可回溯具体位置。

### A9 / P1：草稿写入后崩溃可能漏掉自动审核

`knowledge_summarize.go:231-248` 先创建草稿，再在 `!duplicate` 时创建审核任务。若进程在两步之间终止，重放复用草稿后会跳过审核入队。此项为代码路径发现，尚未执行进程级故障注入，不表述为已运行复现。

修复验收：草稿写入与审核调度具有事务性或可补偿的幂等机制；在写草稿后、入队前强制退出并恢复，最终恰有一次有效审核。

## 本次执行结果

| 检查 | 实际结果 | 证据边界 |
| --- | --- | --- |
| Go `go -C services/api test ./... -count=1` | 失败：新增两个验收用例失败，未见其他用例失败 | 覆盖真实测试库的任务状态与 KAH submission，不是桌面人工验收 |
| Go `go -C services/api vet ./...` | 通过 | 静态检查不能消除上述逻辑问题 |
| Worker `python -m unittest discover -s services/worker/tests -v` | 26 项：19 通过、5 跳过、1 failure、1 error | 两个新增用例暴露 OCR 分支问题；跳过项涉及 Office、PDF fixture、LanceDB 依赖 |
| 桌面 `vitest.cmd run --configLoader runner` | 5 文件、32 项通过 | 原有测试，未覆盖新库首次启用增强；测试输出仍有 query data undefined 提示 |
| 桌面 TypeScript 检查 | 通过 | 类型检查 |
| OpenAPI Spectral | 无 error 级问题 | 契约静态检查 |
| 真实 OCR / 断网首次运行 / 中文准确率 | 未验收 | 当前缺少实际 OCR 运行依赖和模型包，不能宣称可用离线方案已交付 |
| Electron 可见窗口全流程 / 人工确认 | 未验收 | 本次为代码及自动化复验，没有截图或窗口交互证据 |

新增回归文件：`services/api/internal/httpapi/ocr_conversion_acceptance_test.go`、`services/worker/tests/test_ocr_acceptance.py`。失败用例保留为后续修复门禁，没有跳过、弱化断言或修改生产代码使其表面通过。当前测试套件因此为红灯，不可作为通过版本交付。

## 后续顺序

1. 修复 A1/A2/A6，贯通提取质量与错误；完成真实文本、扫描、混合、坏文字层 PDF 回归。
2. 修复 A3/A5/A9，落实运行配置、恢复一致性和审核补偿；执行取消、超时与进程中断验收。
3. 修复 A4，执行新库首次配置到导入生成草稿的真实桌面流程。
4. 修复 A7/A8，完成实际资源校验、非重叠全文及准确引用；再验收干净 Windows 断网包。

每组修复均先通过质量门禁，再通过跨模块链路门禁；未完成的运行证据继续单列，不恢复“六模块全部通过”的表述。
