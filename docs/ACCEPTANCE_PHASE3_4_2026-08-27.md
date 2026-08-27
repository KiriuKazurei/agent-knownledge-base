# 阶段三/四严格验收与修复清单（2026-08-27）

## 结论

阶段三、阶段四的核心链路已经具备可运行基础，当前结论为“部分收口，严格验收未完全通过”。文件导入、来源同步、权限边界、反馈归属、任务恢复和 Worker 失败回传已经有代码与自动化证据；持续监听、真实 Provider 流式输出、Office/PDF 中文实文件验收、备份恢复和人工桌面验收仍未闭环。

## 本轮已修复并复验

- URL 导入的分块写入、Worker index_upsert、页面解析和数据库异常均会使文档与任务进入失败态，不再静默成功。
- 来源扫描会检查子任务创建、子任务导入和任务列表错误；来源删除会先执行 Worker index_delete，索引删除失败时不会提前把数据库标记为 source_missing。
- 文本编辑的分块替换或重新索引失败会使文档进入失败态并返回错误。
- Token 创建会校验显式 libraryIds，未知库和空白库 ID 会被拒绝。
- 反馈除校验 Token 与知识库权限外，还校验 requestId 与 chunkId 是否确实来自同一次查询，避免跨请求伪造反馈。
- 查询结果会持久化 requestId 到 evidence 的映射，为反馈归属校验提供依据。
- 桌面端启动握手改为验证当前会话 Token 的 /libraries 请求，并在退出时回收 Windows 后端进程树，避免旧 go-run 后端导致假健康、Token 不匹配和页面显示服务异常。

## 自动化验收证据

- Go：go test ./... 通过；go vet ./... 通过。
- Worker：Python 3.7 unittest 全部通过（10/10）。
- Desktop：TypeScript 检查通过；Vitest 8/8 通过；electron-vite build 通过。
- 阶段三/四定向用例通过：真实 Worker RPC 文件导入、查询去重、来源更新与缺失、URL index_upsert 失败回传、未关联 Skill 拒绝、Token/反馈范围与撤销。
- 真实 Electron 页面已加载，window.kah 存在，API health 返回 worker=ok，中文文档可见且当前运行态未发现替换字符。该证据证明了运行链路，不等同于人工验收。

## 未通过项与优先级

### P1：持续来源监听尚未实现

当前有来源扫描和手动触发链路，但尚未形成稳定的文件系统事件监听或周期调度。需要补充 fsnotify 或等价监听、去抖、队列串行化、取消、重启恢复和可观测状态，并复验新增、修改、重命名、删除及快速连续修改。

### P1：Provider 真流式输出尚未实现

当前 SSE 接口会先完成一次查询，再把完整答案切片为 answer_delta；这不是 Provider 到客户端的真实增量流。需要为 Provider 增加 GenerateStream，解析 OpenAI/Anthropic/本地兼容端点的 SSE，处理断线、取消、背压、首 token 超时和非流式降级，并保留 citations、done 和 error 事件契约。

### P1：中文 Office/PDF 实文件验收不足

目前已覆盖中文文本、Markdown、CSV、HTML 等路径，但 DOCX、XLSX、PPTX、PDF 的真实中文文件尚未形成稳定的 fixture 与端到端验收。需要验证编码、页码或工作表定位、表格文本、标题层级、解析失败状态和乱码检测；任何 U+FFFD 都应使验收失败。

### P1：人工桌面流程尚未闭环

当前已用 CDP、DOM 和真实 API 证明页面与后端运行，但原生文件选择器、真实鼠标键盘操作、导入进度、失败提示、结果点击和反馈提交尚未完成一次人工验收。需要在可见窗口中执行并保存截图或录屏证据。

### P2：真实外部 Provider 验收尚未完成

自动化使用 httptest 兼容端点验证协议和错误回传，尚未用用户配置的 OpenAI、Anthropic 或 LM Studio 端点完成人工验收。接入前应确认密钥不入库、不进日志，并验证超时、限流和错误提示。

### P2：URL robots 与外部页面边界仍需加固

当前 robots 处理覆盖常见 User-agent、Disallow 和 Allow，但 robots 获取失败时仍是可配置的 fail-open 语义，HTML 解析也需要更多编码、重定向、超大页面和恶意内容边界测试。上线前应明确策略并加入限制。

### P2：query_evidence 尚无清理策略

查询到 evidence 的映射目前用于反馈归属校验，但尚未实现 TTL、按时间清理和索引增长监控。需在后续阶段加入保留策略，并验证清理不会破坏近期反馈。

### P2：大规模检索基准仍未达目标

阶段二压力证据已覆盖 100,000 chunks，Recall@10 为 1.0，但 p95 约 3.58 秒；尚未达到 1,000,000 chunks、p95 不超过 2 秒的目标，且当前环境未具备 LanceDB 依赖和 Python 3.14 基准条件。该项不阻塞阶段三/四功能收口，但阻塞性能目标宣称。

## 后续修复顺序

1. 先补持续来源监听与可恢复任务状态，完成来源四态变更的端到端回归。
2. 再实现 Provider 真流式与统一 SSE 事件契约，补取消、超时、断线和降级测试。
3. 建立中文 Office/PDF fixture 矩阵，补解析、定位、乱码和失败态验收。
4. 完成可见窗口人工验收，记录原生文件选择器到反馈提交的全链路证据。
5. 最后处理外部 Provider、robots 策略、evidence 清理和百万级检索性能。

## 阶段三/四验收门槛

阶段三达到基本完成，需要：任务失败不静默、来源变更可恢复、Worker 与数据库状态一致、查询和反馈权限闭合、文件导入中文无乱码。

阶段四达到基本完成，需要：Provider 流式契约真实可用、Token/library scope 与反馈归属可审计、桌面端启动不会连接旧服务、可见窗口人工流程通过，并完成至少一轮中文 Office/PDF 实文件验收。

## 关联实现

- services/api/internal/httpapi/stage3.go
- services/api/internal/httpapi/server.go
- services/api/internal/storage/store.go
- apps/desktop/src/main/index.ts
- docs/PROJECT_PLAN.md
