# KAH + MCP 重构方案（当前实施版）

> 更新时间：2026-08-30
> 状态：核心链路与 Skill 外部映射已落地，正在进行契约收口与发布前验收

## 1. 重构目标

KAH（Knowledge Artifact Hub）负责保存可审计的知识条目、不可变 revision、来源、关系和审核状态；MCP 是 Agent 的唯一访问协议。桌面端继续使用 `/api/v1` 管理资料、审核队列和运行状态，但不再把旧的通用 query/submit HTTP API 暴露给 Agent。

```text
Electron/React ── desktop token ──> Gin /api/v1 ──> SQLite + objects + Worker
Agent ── mcp_read ────────────────> POST /mcp/read
Agent ── mcp_manage ──────────────> POST /mcp/manage
```

文档导入和 chunk/vector 检索仍由 Worker 负责；KAH 目录的稳定 revision、审核隔离和 MCP 读取以 SQLite KAH 表为权威源。两条数据链路不互相越权：未发布 KAH revision 不会进入 Read MCP 的目录。

## 2. KAH 数据与生命周期

- Canonical payload 使用 `kah-knowledge/v1`，支持 `concept`、`claim`、`procedure`、`decision`、`policy`、`reference` 六类。
- `kah://knowledge/<uuid>` 是条目标识；`?revision=N` 固定历史 revision；`#section-id` 固定段落。
- revision 只追加，不覆盖历史内容；同一知识库按语义内容 hash 做精确去重，跨知识库不误判为重复。
- Agent 提交必须有来源、正文引用、精确的 KAH source revision 或已导入文档的 content hash，以及 `idempotencyKey`；HTTPS 来源会做大小限制和公网地址校验，无法快照时保留 `source-unverified` 标记。
- `create` 和 `propose_revision` 都先进入 `pending_review`。人工批准后进入 `approved_pending_index`，当前实现随后原子切换稳定指针并标记为 `stable`；旧稳定 revision 变为 `deprecated`。驳回 revision 永不进入 Read MCP。

## 3. MCP 接口

### 3.1 传输与权限

- `POST /mcp/read` 需要 `mcp_read`；只能读取 token 允许的 library。
- `POST /mcp/manage` 需要 `mcp_manage`；可以发现、读取、比较、验证、提交和审核绑定 library 范围内的 submission。Agent 审核批准必须通过服务端信度门槛，不能删除知识或绕过不可变 revision。
- Agent token 只保存哈希，创建 MCP token 必须绑定至少一个 library；桌面 token 仅用于本地管理接口。
- MCP Origin 只允许空值、`null`、`file://`、localhost 和 loopback IP；请求体上限为 4 MiB。
- `initialize` 返回协商后的 `protocolVersion`，并通过 `MCP-Protocol-Version` response header 回显。
- JSON-RPC 错误使用标准错误码；业务拒绝在工具结果中返回 `isError: true`、稳定业务码和可读消息。

### 3.2 工具与资源

Read/Manage 都提供：

- `knowledge_search`：只返回紧凑目录项和 resource links，不直接返回完整正文。
- `knowledge_get`：读取稳定或明确指定的已发布 revision，可限制 `sectionIds`，并按需包含 sources/relations。

Manage 额外提供：

- `knowledge_validate`：校验 KAH payload，不写入。
- `knowledge_submission_list`：按 library scope 和审核状态列出待处理 submission。
- `knowledge_submit`：创建草稿或提议 revision，幂等返回同一 submission；不直接批准或发布。
- `knowledge_submission_get`：读取 candidate、来源、校验、审核历史和发布状态。
- `knowledge_compare`：比较 submission 与指定 URI、另一个 submission 或上一 revision 的元数据、章节、来源和关系。
- `knowledge_review`：记录 Agent 的 `approve`、`reject` 或 `needs_human` 决策；只有 `confidence > 0.95` 且来源/校验满足发布条件时，批准才会原子发布，否则保持 `pending_review`。

固定资源包括 `kah://schema/kah-knowledge/v1`、Read Skill 和 Manage Skill；知识正文可通过 `resources/read` 读取，支持 revision 和 section fragment。`resources/list` 还会按 token scope 列出已完成索引的导入文档，文档使用不暴露文件系统路径的 `kah://document/<uuid>` URI；`resources/read` 返回索引正文，Manage 校验会确认文档属于目标 library、状态为 `ready` 且 `sources[].snapshot.content_hash` 与当前文档一致。

## 4. 桌面端契约

OpenAPI 只描述桌面管理接口：

- `/knowledge/search`：KAH 稳定目录搜索。
- `/knowledge/resolve`：桌面读取 KAH revision。
- `/knowledge/submissions` 及其审核子路径：审核、批准并发布、驳回。
- `/tokens`：只接受 `mcp_read`、`mcp_manage` scope。

旧 `/api/v1/query`、`/api/v1/query/stream`、`/api/v1/skills/query` 和 `/api/v1/knowledge-submissions*` 已从路由移除；保留的旧表和旧实现只用于兼容已有本地数据库迁移，不构成当前公共契约。

## 5. 实施与验收矩阵

| 领域 | 当前状态 | 证据/剩余工作 |
| --- | --- | --- |
| KAH schema、revision、来源、关系 | 已实现 | storage 单元测试与 MCP 端到端用例 |
| 精确/近似去重、幂等提交 | 已实现 | 同库/跨库和 cursor 回归 |
| Read/Manage MCP、Origin、scope | 已实现 | `mcp_test.go` 与 2026-08-30 原始 HTTP MCP client 烟测已覆盖 Manage 队列、比较和信度审核；Codex 宿主需重载新令牌后完成目标客户端互操作验收 |
| 导入文档 MCP 资源与来源快照 | 已实现 | 2026-08-30 真实 Electron 会话列出 3 个 ready Markdown 文档；读取 `README.zh.md`/`dsh-ecosystem-spec/README.md` 并完成 hash/chunk 引用的 KAH 草稿提交 |
| 桌面/Agent 审核并发布 | 已实现 | 桌面审核回归；Agent 比较与 `>0.95` 信度门槛由 MCP 回归覆盖；可见窗口人工流程仍待完成 |
| Worker/LanceDB 文档检索 | 保持原链路 | 不把它伪装成 KAH 目录索引；需单独完成百万 chunks 性能门槛 |
| 外部 Skill 目录映射 | 已实现 | 自动化测试与 2026-08-30 临时外部目录真实 HTTP/文件系统验收通过；可见窗口人工流程仍待完成 |
| 安装包、真实 Provider、可见窗口 | 尚未严格验收 | 属于发布前门槛，不由 MCP 自动化测试替代 |

## 6. 验收命令

```powershell
$env:GOCACHE = (Join-Path (Get-Location) '.run-data\go-cache')
go -C services/api test ./... -count=1
go -C services/api vet ./...
apps\desktop\node_modules\.bin\tsc.cmd --noEmit -p apps\desktop\tsconfig.json
apps\desktop\node_modules\.bin\vitest.cmd run --config apps\desktop\vitest.config.ts
Push-Location apps\desktop
& .\node_modules\.bin\electron-vite.cmd build
Pop-Location
```

通过这些命令只能证明自动化和构建状态；进程健康、可见窗口、人工交互、真实外部 Provider 和安装包仍分别记录，不能合并为一个“发布完成”结论。
