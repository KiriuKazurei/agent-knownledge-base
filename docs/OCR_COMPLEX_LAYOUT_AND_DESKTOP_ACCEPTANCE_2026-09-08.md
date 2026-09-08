# OCR 复杂版式与真实桌面窗口模拟验收（2026-09-08）

## 验收结论

初始真实窗口验收结论为 **有条件不通过**；P0 版面恢复修复后的最终结论为 **通过**。

- 离线 OCR 插件的安装、引擎验证、停用、启用和卸载流程通过真实桌面窗口验收。
- 扫描 PDF 从窗口导入后，来源文档、索引任务和无 Agent 的 reference KAH 草稿能够完整连通，插件卸载后已导入数据仍然保留。
- 修复后，复杂版式 OCR 的文字召回、双栏阅读顺序和表格行列关联均通过，真实本地模型回归为 10/10 项。
- 审核详情的差异视图会显示字面量 `\n`，影响候选内容的人工审阅体验；该问题不破坏数据，但应在下一轮 UI 质量修复中处理。

## 验收边界与环境

| 项目 | 实际值 |
| --- | --- |
| 工作区 | `E:\AI\agent-knownledge-base` |
| 隔离数据目录 | `.run-data/ocr-complex-ui-acceptance/data` |
| OCR 运行环境 | `.run-data/ocr-plugin-env/Scripts/python.exe` |
| OCR 资源包 | `.run-data/ocr-downloads/kah-ocr-ppocrv4.zip` |
| 测试 PDF | `output/pdf/ocr-complex-layout-acceptance.pdf` |
| 页面组成 | 4 页纯图片 PDF：双栏、表格、低对比度与 90 度旋转、标题层级与脚注 |
| 桌面窗口 | PID `38020`，HWND `8263910`，类名 `Chrome_WidgetWin_1`，标题 `Knowledge Agent Hub` |
| 原生文件对话框 | 标题 `打开`，类名 `#32770`，由同一 Electron PID 创建 |

测试数据只写入隔离数据目录，没有使用默认用户数据。

## 复杂版式 OCR 结果

初始真实 RapidOCR 测试耗时为 `14.819s`；修复后复跑耗时为 `13.673s`。提取结果为 `status=complete`，`pageCount=4`，`ocrPages=[1,2,3,4]`，没有缺页、空白页或不可信页。

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| 全部页面完整分类 | 通过 | 4/4 页面为 OCR 页面，状态完整 |
| P1-P4 页尾标记 | 通过 | 四个 `P*-END` 均保留 |
| 双栏文字召回 | 通过 | L1-L3 与 R1-R3 均识别 |
| 双栏人类阅读顺序 | 通过 | 修复后为 `L1 → L2 → L3 → R1 → R2 → R3` |
| 表格单元格召回 | 通过 | T01-T04、类型、页数和状态内容均识别 |
| 表格行列关联 | 通过 | 保留表头 `编号、类型、页数、状态` 与 T01-T04 四行对应关系 |
| 低对比度文本 | 通过 | `C1低对比度正文` 完整识别 |
| 90 度旋转文本 | 通过 | `ROT90旋转九十度文字` 完整识别 |
| 侧栏顺序 | 通过 | S1、S2、S3 顺序正确 |
| 标题层级与脚注顺序 | 通过 | H1、H1.1、H1.2、脚注顺序正确 |

初始机器可读结果见 [`evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results.json`](evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results.json)，修复后结果见 [`evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results-after-layout.json`](evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results-after-layout.json)。

复杂版式的初始阻断点是版面分析而不是字符识别。P0 已加入保守双栏分区与规则表格恢复，并将 `ocrLayout`（行、坐标、列与表格元数据）写入文档块位置；P1 将使 KAH 草稿消费这些结构化块。

## 真实窗口操作记录

按真实窗口交互执行了以下步骤：

1. 在“设置 → 可选插件”确认 OCR 离线包已安装并启用。
2. 点击“验证 OCR 引擎”，验证通过。
3. 点击“停用 OCR”，确认状态变为已安装且停用。
4. 点击“启用 OCR”，确认状态恢复为已安装并启用。
5. 在窗口中创建“复杂版式 OCR 验收库”。
6. 点击“导入”，通过 Windows 原生文件选择器选择复杂版式 PDF。
7. 等待后台 OCR、文档写入、分块、索引和 KAH 草稿生成完成。
8. 在“来源文档”中确认 PDF 为“已索引”，详情显示 `4 个片段` 和页级定位。
9. 在“待审核”中确认自动生成的 reference KAH 草稿和 `kah://document/...` 来源连接。
10. 返回“设置 → 可选插件”，点击“卸载 OCR 插件”，确认状态为未安装。
11. 通过同一桌面会话 API 复核：已导入文档和 KAH 草稿均保留。

对应截图：

- [`plugin-verified-reenabled.png`](evidence/ocr-complex-acceptance-2026-09-08/plugin-verified-reenabled.png)
- [`complex-pdf-imported.png`](evidence/ocr-complex-acceptance-2026-09-08/complex-pdf-imported.png)
- [`complex-pdf-detail.png`](evidence/ocr-complex-acceptance-2026-09-08/complex-pdf-detail.png)
- [`kah-pending-review.png`](evidence/ocr-complex-acceptance-2026-09-08/kah-pending-review.png)
- [`plugin-uninstalled.png`](evidence/ocr-complex-acceptance-2026-09-08/plugin-uninstalled.png)

窗口、导入对象和卸载后保留状态的机器可读摘要见 [`desktop-flow-results.json`](evidence/ocr-complex-acceptance-2026-09-08/desktop-flow-results.json)。

## 无 Agent 转换与来源连通性

导入完成后的关键对象如下：

| 对象 | 状态与标识 |
| --- | --- |
| 导入任务 | `41563d82-26fc-4837-b9b9-5106f3074048`，`completed` |
| 来源文档 | `3de8f1d1-36f1-4c58-ae7f-82d7e9f7ccf3`，`ready`，4 个 chunk |
| 内容哈希 | `26b792d2e9f6e0300c837c4d216b01e8ad8ea54b4d6810fe5b6dc9517edd658d` |
| KAH 提交 | `344dbc04-925b-4ffc-a07b-d64090f10ffc`，`pending_review` |
| KAH URI | `kah://knowledge/0b9921c6-2930-419b-ad60-1e13b558d430` |
| 来源 URI | `kah://document/3de8f1d1-36f1-4c58-ae7f-82d7e9f7ccf3` |
| KAH 校验 | `valid=true`，无 errors、无 warnings |
| 生成方式 | `desktop/import`，`document-import` |

KAH 快照中的 `content_hash` 与来源文档哈希完全一致。该草稿由导入管线直接生成，没有 Agent 操作，证明 PDF 原始知识到 reference KAH 待审核项的连接已经成立。

## 插件卸载后的保留性

卸载后的插件接口返回：

```json
{
  "installed": false,
  "enabled": false,
  "version": "",
  "sizeBytes": 0
}
```

同一时刻：

- 来源文档仍为 `ready`，内容哈希不变。
- KAH 提交仍为 `pending_review`，知识 URI 不变。
- 插件模型资源已释放，界面恢复为“安装 OCR 离线包”。

因此插件卸载不会删除已导入文档或已生成知识。

## 启动异常说明

验收中一次重新启动出现 `Backend rejected the current desktop token: 401`。端口检查确认旧验收后端 PID `51280` 仍占用 `127.0.0.1:48761`，新桌面会话连接到了旧令牌的后端。只终止本轮隔离验收的旧后端和 Electron 进程后，单一会话正常启动，API、渲染器、窗口和 OCR 导入全部通过。

该异常属于验收进程清理问题，不计入 OCR 产品功能失败；它也说明桌面启动时可以进一步增加“端口占用进程归属”诊断，避免只向用户显示 401。

## 两道门禁

### 代码质量门禁

- 4 页 PDF 均完成渲染目检；表格边界修正后无裁切、重叠或溢出。
- Worker 插件生命周期标准库测试通过：`3 tests / OK`，覆盖安装、停用、重启、卸载、重装、哈希篡改和 ZIP 路径穿越拒绝。
- Go API 定向测试通过：`go test ./internal/httpapi -run "OCR|Import" -count=1`。
- 桌面 OCR 设置测试通过：`1 test / 1 passed`，覆盖卸载请求和资源移除后的安装入口恢复。
- 桌面 TypeScript `tsc --noEmit` 通过。
- `node --check` 通过：`accept-ocr-desktop-ui.cjs`、`accept-ocr-desktop-import.cjs`、`accept-ocr-desktop-uninstall.cjs`、`probe-ocr-desktop-state.cjs`。
- `py_compile` 通过：`generate-complex-ocr-fixture.py`、`verify-complex-ocr.py`。
- `git diff --check` 通过。
- P0 双栏和表格修复后的 Worker 测试为 `15 tests / OK`，`py_compile` 与 `git diff --check` 通过。
- P0-2 的 API 真实模型连通性测试 `TestRealComplexOCRImportRetainsLayoutMetadata` 通过（16.75s），确认 API 文档端点保留双栏和表格元数据，并确认 KAH 草稿仍固定到来源文档和内容哈希。

### 代码连通性门禁

| 链路 | 结果 |
| --- | --- |
| 设置 UI → 原生文件选择器 → OCR 插件安装 | 通过 |
| 设置 UI → OCR 健康检查 | 通过 |
| 设置 UI → 停用/启用/卸载 | 通过 |
| 桌面导入 → API → worker → RapidOCR | 通过 |
| OCR 文本 → 文档分块 → 索引 | 通过 |
| 文档导入 → 无 Agent reference KAH 提交 | 通过 |
| KAH 提交 → `kah://document` 来源与快照哈希 | 通过 |
| 插件卸载 → 已有文档和知识保留 | 通过 |
| 复杂版式文字 → 人类阅读结构 | 通过：双栏与表格结构均已恢复并保留在 `ocrLayout` |

## 后续修复优先级

1. **P1：结构化 KAH 输入。** 让转换器优先消费段落、栏、表格、标题、脚注等块类型，而不是只消费平面文本。
4. **P2：审核差异视图。** 将当前字面量 `\n` 改为真实换行或逐行块展示，提升人工审核可读性。
5. **P2：桌面启动诊断。** 端口被旧会话占用时显示旧进程与数据根信息，避免泛化为令牌 401。

## 复现命令

```powershell
.run-data\ocr-plugin-env\Scripts\python.exe scripts\generate-complex-ocr-fixture.py
.run-data\ocr-plugin-env\Scripts\python.exe scripts\verify-complex-ocr.py `
  --package .run-data\ocr-downloads\kah-ocr-ppocrv4.zip `
  --pdf output\pdf\ocr-complex-layout-acceptance.pdf `
  --output .run-data\ocr-complex-acceptance
```

桌面操作脚本为：

- `scripts/accept-ocr-desktop-ui.cjs`
- `scripts/accept-ocr-desktop-import.cjs`
- `scripts/accept-ocr-desktop-uninstall.cjs`
- `scripts/probe-ocr-desktop-state.cjs`
