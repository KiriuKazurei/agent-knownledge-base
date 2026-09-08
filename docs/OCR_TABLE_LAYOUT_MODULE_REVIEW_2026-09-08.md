# OCR 表格结构恢复模块验收（2026-09-08）

## 目标

将扫描 PDF 中规则表格恢复为可验证的表头、行和单元格对应关系，并把该结构随 OCR 文档块跨 Worker、API 与无 Agent KAH 草稿链路保留。

## 实现

- 以 OCR 检测框基线聚合行；小幅垂直偏差在同一行带内处理。
- 仅把连续三行以上、每行 3 至 8 个单元格、列数一致且列起点稳定的区域认定为表格。
- 识别结果保存表头、数据行、列数和表格边界；缺行、列数不一致或不规则区域保持普通行优先文本，不虚构关系。
- 将表格元数据写入每个 PDF 块的 `location.ocrLayout.tables`。来源文本仍完整保存，KAH 草稿继续固定 `kah://document/<id>` 与原文件内容哈希。

## 代码质量门禁

通过：

```text
python -m unittest services.worker.tests.test_pdf_ocr \
  services.worker.tests.test_ocr_acceptance \
  services.worker.tests.test_ocr_plugin -v

15 tests / OK
```

新增覆盖包括四列表格、表头与两行数据的恢复，以及单元格上下偏移三像素时仍按行归属。`py_compile` 和 `git diff --check` 均通过。

## 连通性门禁

真实本地 RapidOCR 对四页复杂 PDF 的独立回归为 **10/10**，耗时 `13.673s`：

- 双栏读取顺序为左栏完成后再读取右栏；
- 页 2 的表格元数据为表头 `编号、类型、页数、状态`，以及 T01 至 T04 四个数据行；
- 低对比度、90 度旋转、侧栏、标题和脚注检查继续通过。

机器可读实测结果：[`evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results-after-layout.json`](evidence/ocr-complex-acceptance-2026-09-08/complex-ocr-results-after-layout.json)。

真实 Worker 到 API 测试也通过：

```text
go test ./internal/httpapi -run TestRealComplexOCRImportRetainsLayoutMetadata -count=1 -v
PASS (16.75s)
```

它实际安装离线包、导入复杂 PDF、等待 Worker 完成，随后从 `GET /documents/:id` 验证页 1 的双栏元数据和页 2 的表格结构，并验证自动创建的 KAH 草稿保持待审核状态、`kah://document` 来源和内容哈希。

## 结论

P0 表格结构恢复通过两道门禁。规则表格已能作为有来源定位的结构化输入进入下一优先级的 KAH 渲染模块；复杂、不规则或缺失单元格的版式会保守降级为普通 OCR 文本。
