# Knowledge Agent Hub 完整 V1 实施计划

> 状态：阶段 1/2 开发与自动验收完成，可见窗口验收待 Electron runtime（截至 2026-08-25）  
> 项目目录：`E:\AI\agent-knownledge-base`  
> 远程仓库：`https://github.com/KiriuKazurei/agent-knownledge-base`  
> 许可证：MIT

> 迁移说明（2026-08-30）：本计划中的旧 Agent HTTP 查询、SSE、Skill discovery 和 Markdown submission 接口已由 KAH + MCP 重构取代。当前 Agent 入口是 `/mcp/read` 与 `/mcp/manage`；`/api/v1` 仅保留桌面管理接口。具体当前契约和未完成门槛见 `docs/KAH_MCP_REFACTOR_PLAN.md`。

## 1. 总体方案

- 所有开发内容固定在 `E:\AI\agent-knownledge-base`，不移动项目目录；源码、配置和持久化记录中禁止硬编码盘符或绝对路径。
- 初始化 MIT Git 仓库并关联 `https://github.com/KiriuKazurei/agent-knownledge-base`。
- 采用单仓库结构：
  - Electron + React + TypeScript 桌面端。
  - Go + Gin 主后台。
  - Python 文档处理与检索工作进程。
  - OpenAPI/JSON Schema 共享契约。
- 首发支持 Windows 10/11 x64，生成 NSIS 安装包和可复制迁移的便携包。
- 工具链固定为 Node.js 24.14.0、pnpm 11.21.0、Go 1.26.1、Python 3.14 x64；依赖全部写入锁文件。发布包内置 Gin 和 Python worker 可执行文件，不要求目标机预装 Go/Python/Node。
- 默认单用户、本地优先、零遥测；Agent API 只监听 `127.0.0.1`，局域网访问必须显式启用。

```text
Electron React ───────┐
本机/局域网 Agent ────┼─> Gin API ──> SQLite + 文件对象库
                      │            ├─> Python Worker ──> LanceDB 索引
                      │            └─> OpenAI / Anthropic / LM Studio
                      └─ MCP Streamable HTTP（Read / Manage）
```

## 2. 架构与主要功能

### 桌面端

- 使用 Vite、React、TypeScript、React Router、TanStack Query、Zustand、Radix UI、Tailwind CSS、TanStack Table 和 CodeMirror。
- 采用专业三栏工作台：
  - 左栏：多知识库、虚拟目录、标签、收藏、固定搜索。
  - 中栏：文档列表、搜索结果、导入及索引任务。
  - 右栏：文档预览、来源、标签、索引状态与引用定位。
- 提供浅色/深色主题、完整键盘操作、清晰焦点、减少动画模式和 WCAG 2.2 AA 对比度；图标使用统一 SVG，不用 Emoji 代替功能图标。
- 内置 Markdown/TXT 编辑；PDF、DOCX、XLSX、PPTX、HTML 和代码文件提供结构化只读预览及“外部打开”。
- PDF 保留页码定位，DOCX 保留标题/段落/表格，XLSX 保留工作表和单元格范围，PPTX 保留幻灯片与文本层级，不承诺 Office 像素级还原。
- 设置中心包含模型端点、LM Studio、Agent 令牌、数据目录、备份恢复、日志与隐私设置。

### 后台和数据层

- Gin 是唯一对桌面端和 Agent 暴露的后台；Electron 启动 Gin，Gin 再以长驻子进程方式启动 Python worker。
- Gin 与 Python worker 使用基于标准输入输出的 JSON-RPC 2.0 通信，避免额外暴露内部端口；实现健康检查、超时、取消、崩溃重启和请求关联 ID。
- SQLite WAL 保存知识库、文档、来源、标签、固定搜索、任务、索引版本、提供商配置、反馈和审计记录。
- 原文件复制到内容寻址对象库，以 SHA-256 去重；数据库只存相对 `dataRoot` 的路径。
- Python worker 负责格式解析、分块、结构定位、LanceDB 写入和混合搜索；Gin 持有模型密钥并负责嵌入/生成端点调用。
- 每个知识库拥有独立、版本化的索引目录。更换嵌入模型时建立新索引，完成校验后原子切换，失败则继续使用旧索引。
- 检索流程为全文候选 + 向量候选 + RRF 融合重排；可选增加模型重排。嵌入端点不可用时自动降级为全文检索并明确返回降级状态。
- 导入支持 Markdown、TXT、PDF、DOCX、XLSX、PPTX、HTML、常见源码文件、单个 URL 和受控站点采集。
- 站点采集限定域名、深度、页数、内容类型、超时和并发，遵守 robots；V1 不处理登录态、复杂反爬或必须执行 JavaScript 的网页。
- 源目录监视采用增量任务：新增和修改自动同步，改名更新来源；源文件删除后保留入库副本并标记“源已丢失”，等待用户确认。
- SQLite 持久任务队列保存导入、解析、嵌入、重建和备份进度，异常退出后可恢复，不引入 RabbitMQ/NATS。外部消息队列仅保留未来适配接口。

### 模型与安全

- 生成模型适配器支持：
  - OpenAI Responses 和 Chat Completions。
  - Anthropic Messages。
  - 自定义兼容 Base URL。
- 嵌入模型单独配置为 OpenAI-compatible 或 LM Studio Embeddings；Anthropic 仅用于生成，因为其官方目前不提供自有嵌入模型。[Anthropic Embeddings](https://platform.claude.com/docs/en/build-with-claude/embeddings)
- LM Studio 优化包括自动检测、连接诊断、模型枚举、下载状态、加载/卸载、显存估算及 OpenAI/Anthropic 兼容协议选择；不静默安装 LM Studio。[兼容端点](https://lmstudio.ai/docs/developer/openai-compat)、[模型管理 API](https://lmstudio.ai/docs/developer/rest/list)
- 即使没有生成模型，检索、引用和全文管理仍可工作。
- 每个知识库默认禁止把内容发送到远程模型，必须逐库授权；本地 LM Studio 不视为远程外发。
- API Key 存 Windows Credential Manager；Agent MCP 令牌只保存哈希，可撤销并限定 `mcp_read`、`mcp_manage` 权限及可访问知识库。
- 启用局域网模式时配置监听地址、允许网段和独立令牌，并提示使用 TLS 反向代理；不直接暴露 Python worker、SQLite 或 LanceDB。
- Electron 启用 `contextIsolation`、sandbox 和 CSP，关闭 renderer Node 集成；preload 仅公开最小类型化接口。

## 3. 公共接口与数据契约

- 所有公开接口使用 `/api/v1`，以 OpenAPI 3.1 作为唯一契约来源并生成 TypeScript 客户端。
- 主要接口：
  - 知识库、文档、目录、标签、固定搜索 CRUD。
  - 文件/目录/URL 导入、任务状态、取消和重新索引。
  - `POST /mcp/read`：Agent 通过 MCP `knowledge_search`、`knowledge_get` 和 resources 读取稳定 KAH 知识。
  - `POST /mcp/manage`：Agent 通过 MCP 校验并提交 review-only KAH draft，不允许发布或删除。
  - `/api/v1/knowledge/*`：桌面端 KAH 目录、revision 和审核管理。
  - 提供商连接测试、模型选择、LM Studio 管理、令牌管理、备份恢复和健康状态。
- `QueryRequest` 包含查询文本、知识库范围、标签/目录/时间过滤、`topK`、检索模式及 `evidence|answer` 响应模式。
- 每条证据包含文档/分块 ID、来源标题、文本、可解析位置、内容哈希，以及全文、向量、融合和最终分数。
- 流事件固定为 `retrieval`、`citation`、`answer_delta`、`complete`、`error`；生成答案中的引用必须能解析回具体分块和原文位置。
- 错误统一使用 RFC 9457 Problem Details，附稳定错误码、请求 ID 和可重试标志。
- Provider、Retriever、Reranker、AgentTransport 使用内部接口隔离；MCP transport 与文档 Worker 检索核心解耦，后续可增加其他 transport 而不改变 KAH revision 契约。

## 4. 分阶段实施

### 阶段 1：工程骨架（基本完成，未严格判定为完成）

- 初始化 Git、MIT、pnpm workspace、Go module、Python 项目、共享契约、相对路径配置和 Windows 开发脚本。
- 打通 Electron → Gin → Python 的启动、健康检查、退出和日志链路。

### 阶段 2：首个检索闭环（部分完成，未判定为完成）

- 完成知识库、对象存储、Markdown/TXT/PDF 导入、SQLite 任务和 LanceDB 索引。
- 交付三栏工作台、全文/向量检索、引用预览及只读 Agent API。

### 当前进度判定（2026-08-25）

本节记录基于当前工作树、实际入口注册、持久化实现和自动化验证结果的进度；本计划中的目标描述不等同于已完成实现。

#### 阶段 1：开发完成（可见窗口验收待环境）

已验证的内容：

- Electron 主进程能够启动 Gin API，并等待健康检查；Gin 能启动并管理 Python worker。
- 已具备健康检查、进程退出、日志和请求关联的基础链路。
- Go、Python、Electron/React 工程入口、共享 OpenAPI 契约、相对数据根目录和 Windows 开发/测试脚本均已存在。
- Go API 单元/API 测试、Python worker 测试和桌面端 TypeScript 类型检查已通过；Go 服务可构建。

尚未形成严格完成证据的内容：

- 当前工作树根目录未能确认存在可用的 Git 仓库，因此“初始化 Git 并关联远程仓库”不能标记为已完成。
- 发布包在干净 Windows 环境中的启动、安装和迁移验收尚未完成，这属于阶段 5 的交付强化，也尚未作为阶段 1 的完整验收证据。

结论：阶段 1 的开发骨架已完成，按严格验收口径暂记为“基本完成”，待 Git 状态和独立环境验收补证后再改为“完成”。

#### 阶段 2：首个检索闭环完成（可见窗口验收待环境）

已实现的内容：

- 知识库、SQLite 文档/分块/任务记录、内容寻址对象存储和 Markdown/TXT/PDF 导入链路已存在。
- Python worker 已具备文档解析、分块及 JSON-RPC/LanceDB 索引接口。
- 桌面端已具备三栏工作台、文档预览/引用定位和导入任务展示。
- 文档检索仍由桌面 API/Worker 链路提供；Agent 的当前只读入口已切换为 Read MCP，返回稳定 KAH 目录和 revision-pinned 资源。

严格验收仍保留的边界：

- API 已通过 worker 执行 lexical/vector/hybrid 检索；vector 使用可移植确定性文本向量，hybrid 使用两路排名的 RRF 融合，并由 SQLite 补齐可回溯引用。
- TypeScript、Vitest 和 Electron 构建现已通过；真实可见窗口仍因 Electron runtime 未下载而未完成。
- Recall@10、检索 p95、崩溃恢复、索引重建失败等阶段验收指标尚未产生可复核结果。

结论：阶段 2 的首个检索闭环开发和自动验收完成；Recall@10/p95 等基准及真实可见窗口验收仍待补证。

#### 阶段 1/2 验证记录

- Go：`services/api` 执行 `go test ./...` 通过，`go build ./cmd/server` 通过。
- Python：`services/worker` 执行 `py -3.7 -m unittest discover -s tests -v`，4/4 通过。
- 桌面端：TypeScript 类型检查、Vitest 1/1 和 Electron main/preload/renderer 构建均通过。
- 当前结论不包含真实可见窗口手工验收，因此自动化通过、进程启动成功和界面交互通过未混为同一项。

### 阶段 3：完整知识管理

- 加入 DOCX/XLSX/PPTX/HTML/代码解析、结构化预览、文本编辑、标签、虚拟目录、收藏、固定搜索和来源监视。
- 加入受控网页采集、去重、失败重试和索引版本切换。

### 阶段 4：模型与 Agent 能力

- 完成 OpenAI、Anthropic、自定义端点和 LM Studio 管理。
- 完成可选带引用答案、SSE、逐库隐私授权、Agent 令牌、反馈及审计。

### 阶段 5：交付强化

- 实现一致性备份：导出知识文件、数据库、配置、清单和可选索引为带哈希的 `.kahbackup`；恢复时先在临时目录校验和迁移，再原子启用。
- 生成 NSIS 安装包和便携 ZIP，验证干净 Windows 环境启动、迁移、升级和卸载。
- 建立 GitHub Actions Windows CI；V1 采用手动 GitHub Release，不实现自动更新或代码签名。

### 横向能力：Skill 外部 Agent/项目映射

规划文档：[Skill 外部 Agent/项目映射计划](SKILL_MAPPING_PLAN.md)

状态：规划已形成，代码实现待后续执行。

- 依赖现有全局 Skill 管理、`dataRoot/skills/<skill-name>` 标准目录和桌面会话权限。
- 允许用户选择 Agent 或项目实际使用的 Skills 目录，并创建指向知识库 Skill 的真实 Windows 目录软链接。
- 支持映射目标管理、多个 Skill 映射、状态校验、缺失链接修复、解除映射、外部变更遗忘、审计和备份元数据。
- 不改变阶段 1、阶段 2 当前的“基本完成/部分完成”判断；该能力作为现有 Skill 管理之上的横向 Agent 集成能力单独验收。
- 不使用 junction 或复制回退，不覆盖目标目录中的同名文件、目录或外部链接。

## 5. 测试与验收

- Go 使用单元及 API 集成测试；Python 使用 pytest 和固定格式样本文档；React 使用 Vitest/Testing Library；Electron 使用 Playwright 做真实桌面流程。
- 契约测试验证 OpenAPI、SSE 事件、错误码、Agent 权限和 TypeScript 客户端一致。
- 建立中英混合检索评测集，目标为 Recall@10 不低于 0.80，所有展示引用必须可回到原始文档位置。
- 在当前 RTX 3070 Laptop 8GB 机器、百万分块预热数据集上，排除外部模型网络时间后，候选检索 p95 不超过 2 秒；生成模型耗时单独展示，不计入检索 SLA。
- 验证重复导入、源改名/删除、程序崩溃、索引重建失败、模型不可用、磁盘不足、损坏备份、令牌撤销、路径穿越和局域网误配置。
- 验证 Skill 外部映射：指定目录成功创建真实软链接，其他 Agent/项目可读取 `SKILL.md` 和附属文件；Skill 替换后链接继续有效；同名对象、权限不足、路径穿越、源目录嵌套和外部篡改被拒绝或标记；被映射 Skill 删除被阻止；备份恢复不自动写入外部目录，映射需显式验证或修复。
- 验证便携目录整体复制后可启动，数据库和索引中不存在 `E:\AI\agent-knownledge-base` 等开发机绝对路径。
- 每个阶段均进行真实可见窗口手工验收，并将“自动测试通过”“进程启动成功”“界面交互通过”分别记录。
- V1 明确不包含多用户账户系统、完整 Office 编辑、图片 OCR、音视频理解、复杂登录网站爬取、外部消息队列和跨平台安装包。

## 2026-08-25 阶段1/2复核与阶段3启动记录

本轮复核以当前源码、入口、持久化和新鲜验证结果为准：

- 阶段1：已初始化本地 Git 仓库并配置 `origin`；Electron 主进程→Gin→Python worker 的启动、健康检查和退出链路已存在。Go API 全量测试、worker 测试、桌面端类型检查和 Electron 构建均通过。
- 阶段2：worker 现在执行 lexical/vector/hybrid 三种模式，vector 使用可移植的确定性文本向量，hybrid 使用 lexical/vector 两路排名的 RRF 融合；API 通过 worker 排名后回 SQLite 补齐标题、位置和内容哈希，返回可回溯证据与评分。
- 阶段2真实联调：`worker=ok`，导入 README 后 hybrid 查询返回 3 条证据且 `degraded=false`；lexical、vector、hybrid 的最终分数分别按对应模式变化。
- 阶段3已启动：固定搜索从 Zustand 临时状态接入 SQLite、桌面管理 API、OpenAPI 契约和桌面端查询，Go 生命周期测试已通过。


## 2026-08-26 阶段3/4完成与复核记录

本节是对上方历史记录的更新，依据当前工作区源码和本轮新鲜验证结果。

- 阶段3复核完成：保存搜索、文件夹和来源监控的库归属校验拒绝未知库 ID；文本内容更新同步对象路径、内容哈希、文档状态、分块和索引任务；URL 导入统一使用受控抓取入口，并移除旧的不可达实现。
- 阶段4完成：支持 OpenAI 兼容、Anthropic、LM Studio 和自定义提供商；提供商模型列表；带证据的回答生成；检索、引用、回答增量和失败错误的 SSE 事件；逐库远程模型授权；哈希存储、撤销和 scope 校验的 Agent 令牌；引用反馈和审计查询。
- 桌面端已接入回答模型选择、回答展示、生成状态、错误提示和逐条证据反馈，按钮具备可见标签、焦点样式、禁用/选中状态。
- 自动验证：API `go test ./...` 和 `go build ./cmd/server` 通过；worker 使用本机已安装的 Python 3.7 执行测试 6/6 通过；桌面 TypeScript、Vitest 1/1、Electron main/preload/renderer 构建通过。
- 可见运行验证：干净启动取得 Electron 窗口句柄 `1511318`，标题 `Knowledge Agent Hub`，`Responding=True`；`KAH_API_READY`、`KAH_RENDERER_READY` 存在，stderr 为空，API 健康检查返回 worker `ok`。
- 环境边界：本机没有 Python 3.14；pnpm 因依赖安装策略阻止 Electron/esbuild 构建脚本，本轮使用仓库已安装的直接工具入口完成等价类型检查、单测和构建验证。Recall@10、百万分块 p95、崩溃恢复等性能/故障指标仍属于后续阶段验收，不在本轮虚报完成。

## 2026-08-26 中文 Markdown 链接兼容修复

- 已修复中文 Markdown 内容和链接：解析器支持 GB18030 等常见编码，桌面预览会解码中文链接并解析相对路径。
- 库内已导入文档直接切换预览；外部 `http/https/mailto` 链接通过 Electron 安全桥接打开；不存在的目标显示错误。
- 回归验证：worker 7/7、桌面测试 5/5、TypeScript 和 Electron 构建通过；最新可见启动窗口句柄为 `921732`。

## 2026-08-26 历史中文预览修复复核

- 旧数据库中已存在的 U+FFFD 中文乱码分块会在文档详情读取时自动重新解析，避免侧栏继续显示历史脏数据。
- 同哈希重复导入也会触发相同修复；新增 API 回归测试验证中文预览和持久化分块均恢复。
- 同一 Electron 数据目录实测 `README.zh.md` 返回正常中文预览，3 个分块均不含 U+FFFD。
- 阶段3/4 当前验证：API `go test ./...`、`go vet ./...`、Worker 7/7、桌面 TypeScript、Vitest 5/5、Electron 构建均通过。

## 2026-08-26 阶段二基本完成补强记录

本节为当前状态的最新补充；与上方历史阶段标题冲突时，以本节和 [阶段二审校文档](REVIEW_2026-08-26_PHASE2.md) 为准。

- 文件导入任务和源目录扫描子任务现在保存可回放的 `libraryId`、源文件 `path` 等 payload。
- API 启动时恢复 `file_import`、`url_import`、`skill_import`、`source_scan` 和 `index_rebuild`；损坏 payload 或未知任务只会单独失败。
- 新增重启回归测试，验证运行中任务经数据库重开后可恢复为 queued，最终文档状态为 ready 且存在分块。
- 阶段2现判定为“基本完成”；Recall@10、检索 p95、全任务崩溃矩阵和当前变更后的可见窗口复验仍是严格完成前置条件。
- 后续优先级：先完成阶段二性能/故障/可见交互验收，再推进阶段三持续监视与备份恢复、阶段五安装包与迁移，以及 Skill 外部映射实现。

## 2026-08-27 阶段二质量收口：检索、恢复与中文解析

本节补充当前阶段二的最新证据；阶段二维持“基本完成”，尚不改写为“严格完成”。

- 固定双语检索评测已落盘：18 个文档、10 个查询、每种模式预热 10 次后执行 1000 次查询。lexical、vector 和 hybrid 的 Recall@10 均为 1.00；p95 分别为 1.1432 ms、1.1189 ms、1.1285 ms。结果明确标记为 referenceScale=false，不能替代百万分块或真实 LanceDB 查询 SLA。
- 重启恢复矩阵已使用真实 SQLite data root 与真实 Worker 覆盖 url_import、skill_import、source_scan、index_rebuild；所有目标任务完成，URL 与来源监视文档为 ready、Skill 可读取、来源扫描状态已持久化。损坏 payload 与未知任务类型会单独失败并保留诊断信息。
- 中文解析优先保证不产生乱码：Worker 按 BOM、显式 HTML charset、严格 UTF-8、UTF-16/GB18030/Big5 等候选的顺序处理；无 BOM UTF-16、GB18030 CSV、声明 GBK 的 HTML 均已做 Unicode 等值回归。API 在 Worker 不可用时使用同样的 UTF-8/UTF-16/GB18030/Big5 安全解码，不再直接把原始字节转换为字符串。
- 文件兼容范围新增 CSV、TSV、RST、LOG、INI、CONF 和 PROPERTIES；text/html 改为优先走结构化 HTML 提取，避免被通用 text/* 分支吞掉。
- 严格完成剩余前置条件：百万分块/目标硬件的 p95 与容量证据、真实 LanceDB 查询质量对比，以及本轮变更后的 Electron 可见窗口与人工导入/查询/预览验收。

## 2026-08-27 阶段二严格验收结论

- 新增 `benchmark_retrieval.py --stress-chunks`，用固定双语夹具复制到指定 chunk 数并一次性重建索引，压测脚本与结果分别位于 `services/worker/benchmarks/benchmark_retrieval.py`、`docs/PHASE2_RETRIEVAL_STRESS_2026-08-27.json`。
- 100,000 chunks 的 portable-json 结果为 Recall@10 1.00；hybrid p95 3582.91 ms，lexical/vector p95 分别为 3558.78/3509.56 ms，超过计划中的 2 秒参考线，且未达到 1,000,000 chunks 目标规模。
- 当前环境为 Python 3.7.8，Python 3.14 未安装，LanceDB 不可导入；因此本轮只证明 portable fallback 的行为与瓶颈，不宣称真实 LanceDB 查询或百万分块 SLA 已通过。
- 阶段二严格验收项中的固定双语 Recall@10、恢复矩阵和中文无乱码已通过；百万分块性能、真实 LanceDB 对比、当前变更后的 Electron 可见人工流程仍未验收。
- 判定维持：阶段二“基本完成”，严格完成“未通过/待补证”。下一步先准备 Python 3.14 + LanceDB 的可复现环境和百万分块压测，再进行最新构建的窗口级导入、查询、预览、引用全流程验收。

## 2026-08-27 当前构建可见流程复核

- 最新 `apps/desktop/out` 启动成功，Electron 窗口标题为 Knowledge Agent Hub，窗口句柄非零（`8917360`），并输出 `KAH_RENDERER_READY`；真实 DOM 根节点与 `window.kah` 均存在。
- 同一桌面运行时完成 `PROJECT_PLAN.md` 导入：任务 completed、文档 ready；刷新后页面显示 1 个项目。
- 页面输入中文查询“中文乱码”并点击搜索，显示 20 条证据，存在证据反馈入口，查询为 `degraded=false`；点击结果后文档预览显示中文且未发现 U+FFFD。
- 该流程证明当前构建的渲染器/API、查询和预览链路；原生文件选择器人工导入尚未执行，故仍保留该项验收边界。

## 2026-08-27 阶段三/四并行收口复核

- 阶段三已补强来源修改、改名、删除后的状态与索引同步、文本编辑失败可见化；阶段四已补强受限 Agent 的 Skill 查询、Manifest 和文件读取边界，并新增回归测试。
- 保存搜索的非必要库 ID 前置校验曾造成既有生命周期测试失败，现已移除；文件夹、来源监视和 Agent 权限的真实库边界仍保留。
- 修复后 `go test ./...` 与 `go vet ./...` 均通过，阶段三/四暂定“部分收口/基本能力可用”。
- 后续仍需真实外部 Provider、桌面端回答流、人工 SSE 流程和备份恢复演练；这些工作不替代阶段二百万分块性能、真实 LanceDB 和人工文件选择器硬门槛。

### 最终重启窗口复核

- 全部代码修复后重新启动最新 Electron 构建，窗口句柄为非零值 `57019152`；页面显示已导入的 1 个项目，`window.kah` 存在且页面中文正常。
- 最终实例再次完成中文查询和结果点击，显示 20 条证据并进入预览；原生文件选择器人工操作仍未完成。

## 阶段三/四严格验收与修复清单（2026-08-27）

阶段三、四当前为“部分收口，严格验收未完全通过”：核心导入、来源变更、权限边界、反馈归属、任务恢复与失败回传已有自动化证据；持续来源监听、Provider 真流式、中文 Office/PDF 实文件、人工桌面流程和备份恢复仍需完成。

详细问题分级、验收证据、修复顺序与门槛见：[阶段三/四严格验收与修复清单](ACCEPTANCE_PHASE3_4_2026-08-27.md)。
