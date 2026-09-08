# OCR 结构化 KAH 渲染模块验收（2026-09-08）

## 目标

使无 Agent 的导入转换消费 OCR 已验证的版面元数据，保留原始来源文本、页级定位和内容哈希，同时为规则表格提供可审核的结构化 KAH 内容。

## 实现

- reference KAH 保持 `reference` 类型、`pending_review` 状态和原有 `kah://document/<id>` 来源快照。
- 每个带页号的 OCR 块在 KAH 正文中显示页级标题；不带页号的其他文件仍走原有内容路径。
- 规则表格只在 `ocrLayout.tables` 包含完整表头、行数和等宽单元格时渲染为 Markdown 表格，并保留“发布前请核对原件”说明。
- 原始 OCR 文本始终先保留。坐标缺失、表格不规则或字段不完整时不渲染表格，不虚构结构。

## 代码质量门禁

```text
go test ./internal/httpapi -run 'TestImportedReferenceSections(CoverLongSourceInOrder|RenderRetainedOCRTable)$' -count=1 -v
PASS

go vet ./internal/httpapi
PASS
```

单元测试确认原文、中文页标题、表格表头和行内容、来源引用同时存在。

## 连通性门禁

```text
go test ./internal/httpapi -run TestRealComplexOCRImportRetainsLayoutMetadata -count=1 -v
PASS (16.63s)
```

该真实模型测试从插件 ZIP 安装开始，经过 PDF 导入、Worker 解析、API 文档读回和自动 KAH 草稿创建。它确认第 2 页 KAH 内容包含 `编号 | 类型 | 页数 | 状态` 以及 T01-T04 的表格行，同时草稿仍是待审核 reference，来源 URI 和快照哈希保持一致。

## 结论

P1 通过两道门禁。无 Agent 的文件导入现在能够把经验证的规则 OCR 表格转换成 KAH 中可审核的结构化内容；该转换仍不做语义总结，也不绕过人工审核。
