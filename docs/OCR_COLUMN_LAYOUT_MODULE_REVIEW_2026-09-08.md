# OCR 双栏版面分析模块验收（2026-09-08）

## 目标

修复扫描 PDF 中简单双栏正文按同一水平线交错读取的问题，同时不把表格误判为双栏正文。

## 实现

- 新增 `order_reading_lines`，仅在两组稳定、分离、各至少三行的左边界簇存在时采用列优先顺序。
- 页首横跨文本保持在两栏前，页脚保持在两栏后。
- 三列以上、列间距过小、缺失坐标或正文横跨文本时保留原有行优先顺序，避免错误重排。
- OCR 页面位置增加 `ocrLayout`；其中保留阅读顺序、列数、分栏线和逐行文本、置信度、坐标、列号。该数据可供后续表格恢复和结构化 KAH 转换使用。

## 代码质量门禁

通过：

```text
python -m unittest services.worker.tests.test_pdf_ocr \
  services.worker.tests.test_ocr_acceptance \
  services.worker.tests.test_ocr_plugin -v

14 tests / OK
```

新增单元测试覆盖：

- 双栏页顺序为标题 → 左栏全部 → 右栏全部 → 页脚。
- 四列表格保持行优先顺序，不触发双栏重排。
- `py_compile` 与 `git diff --check` 通过。

## 连通性门禁

真实复杂版式 PDF 使用本地 RapidOCR 重跑：

- 总检查从 `8/10` 提升到 `9/10`。
- `two_column_human_reading_order=true`。
- OCR 完整状态、页尾、低对比度、旋转文字、侧栏、层级均保持通过。
- `table_row_association_order=false`，留给 P0-2 表格结构恢复模块。

真实 API 导入测试通过：

```text
go test ./internal/httpapi -run TestRealOCRPluginImportCreatesKAHReference -count=1 -v
PASS (6.33s)
```

该测试覆盖 OCR 插件安装、扫描 PDF 导入、Worker 解析、文档分块、reference KAH 草稿创建和来源保留。

## 结论

双栏版面分析模块通过两道门禁，可以进入表格结构恢复。复杂版式整体仍未完成，因为表格尚未恢复为有行列关联的结构化数据。
