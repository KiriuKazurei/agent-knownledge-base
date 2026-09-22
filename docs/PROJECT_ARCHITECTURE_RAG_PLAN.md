# 项目架构与本地语义 RAG 实施计划

状态：项目架构与桌面发布收口优先；RAG 扩展质量与 100 条人工标注查询暂列低优先级未完成。Phase3 已完成；Phase4 已取得架构、渲染层/交互、原生语义性能和打包健康证据，完整干净安装链路仍未完成；验收勾选只代表已取得的证据，不代表人工或发布验收。

## 已确认设计

- 独立项目架构模块，项目关联知识库，读取工作目录，保留未提交变更的内容哈希、代码证据和不可变版本。
- 静态识别 TS/JS、Go、Python、C#，未知栈由 Agent 分析；遵循忽略规则、排除秘密与产物、不越界跟随链接。
- 执行顺序 Codex CLI、在线 MCP Agent、配置生成模型；附带架构分析 Skill；所有远程路径遵循项目许可。
- 固定版本内置 archify 引擎及 Skill，禁用更新检查；文档、JSON、HTML、SVG、PNG 导出，不修改源项目。
- 手动或周期更新，退出暂停、重开合并补一次；内容无变化跳过，失败保留上一成功版本。
- CPU ONNX embedding 模型扩展，按需安装和离线导入，固定资源清单及哈希、无可执行安装脚本；首包 bge-small-zh-v1.5，目标 500MB 内。
- 关键词与语义融合，模型配置与索引版本绑定，后台重建原子切换；不可用明确降级为关键词。
- 桌面及 MCP 均支持，正式知识与原始证据分权；新增 evidence_search/evidence_read，不扩大旧令牌权限。

## 阶段与门禁

### 1. 数据与扫描
- [x] 数据契约、迁移、任务恢复与租约
- [x] 静态扫描、忽略/越界/秘密保护、内容变化检测
- [x] 架构分析 Skill 与结构化输出校验
- [x] 单元测试与存储/扫描链路

### 2. 生成与架构界面
- [x] Codex 能力探测与只读执行、MCP 领取/心跳/提交、模型回退（后端）
- [x] archify 固定版本、许可证、离线渲染与导出（后端）
- [x] 项目配置、任务进度/取消/错误、版本/证据/差异 API（后端）
- [x] 定时更新与重启恢复（后端调度与服务启动恢复）
- [x] 项目架构桌面界面、可见进度与交互自动化门禁

### 3. 本地语义检索
- [x] 模型安装/导入/启停/卸载/健康检查
- [x] CPU 本地推理、分词边界、索引代次与切换
- [x] 统一检索、原始证据权限、正式知识兼容
- [x] 桌面搜索与引用问答、MCP 全链路

### 4. 验收
- [x] Go/Python/TypeScript 测试与契约检查
- [x] 当前项目及四种语言样例的证据准确性
- [x] 变更/删除/取消/失联/过期租约/重启/模型切换回归
- [ ] 至少 100 条人工标注查询；语义 Recall@10 提高 10 个百分点，符号检索不退步
- [x] 2 万片段 CPU 性能：预热检索 p95 <= 1 秒，记录硬件/内存/索引耗时
- [x] 离线首次启动、无隐式下载、跨库隔离和旧令牌兼容
- [x] 可见桌面操作：已取得打包渲染层截图与交互证据；Windows `MainWindowHandle` 因自动化窗口站隔离仍为 `0`，用户侧人工肉眼确认单独记录。
- [ ] 打包 → 干净安装 → 完整使用 → 重启恢复

## 交付约束

保留既有未提交文件。阶段门禁失败先修复，不将静态检查等同真实运行、人工或发布验收。模型实测与人工标注缺失时如实记录，不伪造指标。数据迁移可重复执行，旧数据和上一成功产物保留。

## 实施与审查记录（2026-09-20）

- 用户显式启用 `call-subagent`：功能实现由 GPT-5.6 Luna/max 子代理承担，主进程做独立门禁，未通过退回原实施者；阶段通过前不进入下一阶段。
- 初始工作树仅有既存未跟踪 `.codex/` 与 `tmp/`；不得清理。
- 变更前基线：Go `go test ./...` 全部通过（GOCACHE 使用仓库 `.run-data/go-cache`）；Python 3.14.7 unittest 36 项通过、5 项跳过；桌面 TypeScript 检查通过、Vitest 38 项通过。
- 环境注意：默认 Python 缺少 ONNX/LanceDB；已有 `.run-data/ocr-plugin-env` 含 ONNX Runtime、LanceDB、PyInstaller，可用于后续真实运行与打包验证。默认 Go 缓存和 Vite 缓存遇到沙箱写权限，前者切换本地缓存、后者通过经批准的测试命令运行。
- 审查重点：正式知识搜索独立于原始文档索引；已有通用任务恢复会拒绝未知任务类型；Worker 单响应上限 16MB；打包资源与开发路径必须分别验证。
- 后续依赖锁定（远程只读核验）：archify 提交 `72c750bb070d95171dbb2244e5b62b1b7da69c12`；BAAI/bge-small-zh-v1.5 提交 `7999e1d3359715c523056ef9478215996d62a620`。实现者应记录下载资源的实际 SHA-256，不使用浮动 main 构建发布包。
- 基线 Go vet 与 OpenAPI Spectral 检查通过。旧检索基准仅 10 条查询，且标为 recall 的计算实际是是否命中任一正确文档；新验收必须分别计算真正 Recall@10、MRR@10，不复用旧指标名称制造通过。

### 阶段 1 独立门禁用例

1. 创建项目时不存在/已删除知识库、相对或不存在目录应被拒绝；有效目录保存后重开数据库仍能读取。
2. 相同字节重复扫描不生成重复快照；修改文件但保持大小/修改时间仍被哈希识别；删除文件产生变化；恢复旧内容可正确指向对应扫描状态。
3. 根与嵌套 `.gitignore`、否定规则、目录规则均有样例；已跟踪秘密文件也不得因 Git 跟踪状态而进入证据；链接不进入外部目录。
4. 四类语言的技术栈/版本/入口/依赖有真实清单样例，未知语言有显式覆盖记录；大文件与截断不能被计为完整读取。
5. 同项目两次并发提交最多一个活跃任务；取消、过期租约、代次变化后的旧完成请求不能更改当前快照。
6. 任务恢复必须经实际服务启动入口接通；HTTP 路由保持 desktop 授权，普通任务列表不暴露租约凭证。
7. 审查扫描输出体积、错误可见性、上下文取消、路径行号哈希可回溯；结构化分析不能引用扫描范围之外的文件。

阶段 1 门禁通过：完整 Go 测试、Go vet、OpenAPI Spectral、扫描稳定性 5 次及新增模块的竞态测试通过。全量竞态测试仅复现基线已有的 Windows `TestSourceWatchPollerQueuesChangedFile` SQLite 临时文件句柄清理偶发失败；单独重跑 HTTP 包通过，未归因于本阶段变更。

阶段 2B 桌面自动化门禁通过：桌面 TypeScript 检查、Vitest 49 项和 electron-vite build 通过；覆盖项目导航、空态、目录选择、创建配置、扫描/生成轮询、取消、失败与过期重试、版本/差异/证据、旧版本分析切换、分析 404 空态、导出、远程代码许可、键盘/ARIA 及目录/保存 IPC。可见窗口操作、真实服务联调和人工交互验收仍待主代理执行，未将本次自动化结果记为可见验收。

### 阶段 3A-1 实施记录（2026-09-20）

- 已实现本地 embedding 模型包 manifest、固定资源清单、逐文件 SHA-256/大小校验、500 MiB 包上限、ZIP Slip/绝对路径/链接/重复项/可执行脚本拒绝、原子安装与失败清理；模型存放在应用数据目录 `models/embedding_models`，运行时大小单独报告。
- 已实现 Worker 的 `model_load`、`model_unload`、`model_health`、`model_embed` RPC 及保留的 `embedding_*` 兼容别名；推理路径限定 CPU ONNX Runtime，使用固定本地 tokenizer 资源、attention/token type 输入、manifest pooling、前缀、显式 token 截断、tokenCount/truncated/configHash 和 L2 归一化。
- 已实现桌面授权的模型列表、ZIP 导入、固定目录在线安装、启用/停用、删除和健康检查 API，并更新 OpenAPI 与 Go Worker 客户端类型。删除前端不能声明活动索引；当前只允许禁用后的模型删除，索引注册表保护留到统一索引阶段。
- BGE 目录条目固定于 `BAAI/bge-small-zh-v1.5` 提交 `7999e1d3359715c523056ef9478215996d62a620`。阶段 3A-1 当时只记录了 512 维、CLS pooling 及 `1_Pooling/config.json` 的 SHA-256；完整 ONNX/tokenizer 转换包、资源清单和在线安装在下方阶段 4 记录中补齐，未使用浮动 URL 或假哈希。
- 自动证据：embedding Python unittest 7 项通过，Worker 全量 unittest 43 项通过（5 项按既有依赖条件跳过）；新增 API 路由与现有 HTTP 定向 Go 测试通过；OpenAPI Spectral 通过；Go 全量、race、vet 由主代理在本地缓存下复测。
- 阶段 3A-1 当时未声称真实 BGE 已安装、真实 ONNX 推理、统一索引切换或 Recall@10/MRR@10 指标；后续真实 BGE 离线包实测见下方阶段 4 记录。

### 阶段 4 BGE 离线包实测记录（2026-09-21）

- 已取得可审计的固定 ONNX 转换资源：转换仓库 `JasonYANG170/bge-small-zh-v1.5-ONNX` 提交 `88d1b438c9c44428e0f7578c6ac3cd8e6d4edeae`，其构建清单声明来源为 `BAAI/bge-small-zh-v1.5` 提交 `7999e1d3359715c523056ef9478215996d62a620`，转换工具为 `optimum.exporters.onnx.main_export`、opset 17、fp32、feature-extraction；转换仓库的 `SHA256SUMS` 与 `build-manifest.json` 已在 `.run-data/vendor` 核验。
- 固定离线包由 `scripts/build_bge_embedding_package.py` 生成，第二次生成得到相同 SHA-256。包为 `.run-data/vendor/bge-small-zh-v1.5-1.5-onnx-fp32.zip`，压缩后 54,223,760 bytes，SHA-256 `ad09cc98048a74aef30a4d2946a274499139120e433ce34428afbc08ac00c5f5`；不进入 git。包内资源逐项绑定：`model.onnx` 94,835,369 bytes / `ba917299b47626c45a420351c179948adb1b24354c89601c265570d43d8554ab`，`tokenizer.json` 439,125 bytes / `48cea5d44424912a6fd1ea647bf4fe50b55ab8b1e5879c3275f80e339e8fae26`，MIT `LICENSE` 1,065 bytes / `587a673933425dbc36ec61268d3b954051b2d3ef3c9b322ede357976055ffdd5`。许可证文本固定于 FlagEmbedding 提交 `fd1a2bdf69488ffebe0327999d4400d8c8058a0b`。
- manifest 绑定 512 维、512 token、CLS pooling、L2 归一化、查询前缀、CPU ONNX Runtime 最低版本 `1.20.0`、tokenizers 最低版本 `0.21.0`，并要求 tokenizer 配置哈希等于 tokenizer 文件 SHA-256；上游 `1_Pooling/config.json` 的固定 SHA-256 为 `aaa8861589f80c961a03cc86c7eeaef7605c1676b9ab55329d33a304738769c6`。
- 真实 Worker 证据写入 `.run-data/vendor/bge-real-validation.json`：离线环境变量开启且网络连接尝试为 0；ZIP 导入、哈希校验、健康检查、启用、CPU ONNX 加载和 tokenizer 加载均成功。当前验证环境为 ONNX Runtime `1.29.0`、tokenizers `0.23.2`，运行时大小 45,547,807 bytes；三条输入输出均为 512 维且 L2 范数为 1，512 token 长输入返回 `truncated=true`。以三条正式知识/原始资料/项目架构样例建立统一 generation 后，三类权限范围查询均返回语义结果，`semanticAvailable=true` 且未降级。
- 在线目录已切换为固定提交、固定 URL、固定大小与 SHA-256 的可安装条目；在线安装仅允许 HTTPS 到固定来源及其受信 CDN/Xet 域名的受限重定向，并执行逐资源校验。转换资源仍只在 `.run-data` 留存，应用安装使用 ZIP 导入路径即可完全离线运行。
- 另用真实 Worker 在线安装路径完成一次验证：固定 Hugging Face URL 的 HTTPS 重定向仅允许到 Hugging Face 自有 CDN/Xet 域名，最终文件仍逐项 SHA-256 校验；安装结果为 `installed=true`、包资源 95,275,559 bytes，健康检查 `available=true`、CPU ONNX Runtime `1.29.0`。在线下载仅用于显式安装操作，启动和离线导入验证没有隐式下载。

### 阶段 3A-2 实施记录（2026-09-21）

- [x] 统一检索 chunk 契约与 SQLite 语料汇总：原始资料、稳定知识、当前项目架构文档/结构化产物/事实/代码证据分别标注来源、审核状态、库/项目范围、URI、路径/行号和内容哈希；pending、删除、跨库记录在构建前排除。
- [x] 版本化 portable generation：generation 元数据绑定 modelId、modelVersion、configHash、dimension；临时目录验证后原子切换 active 指针，失败或 Worker 重启保留上一代，空范围由服务端拒绝，增量 raw 更新标注 needsRebuild，避免冒充完整统一语料。
- [x] 关键词/BM25 兼容路径、精确 symbol 加权、语义可用时的 RRF 融合，以及无模型/模型不匹配时明确 keywordOnly/degradedReason；没有把哈希特征作为新统一检索的语义结果。
- [x] 桌面检索与重建 API：服务端强制非空 library/project 范围，返回前按当前 SQLite 语料再次校验来源和权限；重建状态持久化为 queued/building/current/stale，自动刷新采用同库去重的后台任务，启动时恢复 queued/building/stale。
- [x] 可重复 benchmark harness：正确输出 Recall@10、MRR@10、机器信息、预热和 p95，并逐次记录 requested/effective retrieval mode、semanticAvailable、keywordOnly、degradedReason；真实 BGE 运行报告 `.run-data/phase4-semantic-benchmark-native-batched-20260921.json` 的 native LanceDB 路径对 20,000 个片段完成 CPU 语义压测，`nativeLanceRequired=true`，所有 measured `searchBackend=lancedb`，`semanticAvailable=true`、`keywordOnly=false`、`degraded=false`，lexical/vector/hybrid p95 分别为 `494.7578ms`/`426.6946ms`/`502.7305ms`，达到 `<=1s` 目标。该报告记录 `indexBuild=195441.8829ms`、预热 `22590.901ms`、模型后工作集 `281.75MB`、索引后 `1.4867GB`、套件结束约 `1.40GB`。portable JSON 旧报告 hybrid/vector p95 为 `2168.5813ms`/`2264.1152ms`，仅作路径对照；两者 `qualityComparable=false`，Recall/MRR 只作性能 harness 元数据，不代表 100 条人工查询验收通过。
- [x] 2026-09-21 Worker 完整发布依赖全量 `49 passed, 0 skipped`；Go 全量、Go vet、Go race、Python、桌面 TypeScript 与 OpenAPI Spectral 收口门禁通过。无启用模型时 LanceDB 中文关键词检索已通过 FTS 路径降级，明确 `semanticAvailable=false`/`keywordOnly=true`，不再把旧哈希相似度当语义向量；对应 Worker 回归覆盖 `test_native_lancedb_keyword_fallback_does_not_query_legacy_hash_vectors`。真实 BGE、三类语料语义检索、100 条人工标注 Recall/MRR 与发布链路的边界见下方 Phase4 记录。

### Phase4 验收记录（2026-09-21）

- 自动化门禁已通过：Go 全量、Go vet、Go race、Python Worker（完整发布依赖 `49 passed, 0 skipped`）、桌面 TypeScript、OpenAPI Spectral 均有共享工作区运行证据。Phase4 的自动化检查项可勾；这不替代人工窗口或发布验收。
- 架构运行证据已通过：`.run-data/phase4-runtime-20260921/architecture-scan.json` 记录当前项目与四语言 fixture（C#/Go/JavaScript/Python）的扫描快照；`.run-data/phase4-runtime-20260921/architecture-generate-mcp.json` 记录 MCP Agent 领取、提交与完成的真实链路；`.run-data/phase4-runtime-20260921/architecture-success-evidence.json` 记录重启后的分析/产物读回，以及 `diagram_json`、`document`、`html`、`png`、`svg` 五类架构产物的哈希匹配。第一次真实 Archify 运行因长 participant label 超出布局约束失败，随后按来源语义做确定性短标签处理；`TestArchifySequenceParticipantLabelsKeepArchifySourceInputAndRender` 与 vendored Archify 校验已通过。初次 PNG 浏览器 visual-check 因 Chrome/GPU 环境失败而使用本地 fallback；最终修复包的渲染层截图与交互证据见末段，并继续区分自动化窗口站和用户人工肉眼确认。
- 模型与语义检索已通过：固定 BGE 包的离线导入在 `HF_HUB_OFFLINE=1`、`TRANSFORMERS_OFFLINE=1` 下网络尝试为 0，并完成 CPU ONNX 健康检查；固定目录的在线安装路径也已完成真实安装、重定向限制与逐资源哈希校验。以正式知识、原始证据、项目架构三类语料建立完整 generation 后，三类权限范围查询均返回 `semanticAvailable=true` 且未降级；模型切换、索引代次、跨库范围和无模型 LanceDB 中文 FTS 降级均有测试证据。
- 2 万片段性能门禁已通过：`.run-data/phase4-semantic-benchmark-native-batched-20260921.json` 记录 `nativeLanceRequired=true`、`chunkCount=20000`，所有 measured `searchBackend=lancedb`，`semanticAvailable=true`、`keywordOnly=false`、`degraded=false`；lexical/vector/hybrid p95 分别为 `494.7578ms`/`426.6946ms`/`502.7305ms`，`indexBuild=195441.8829ms`，预热 `22590.901ms`，模型后工作集 `281.75MB`、索引后 `1.4867GB`、套件结束约 `1.40GB`，生产原生 LanceDB 路径达到 `<=1000ms`。portable 真语义旧报告 hybrid/vector p95 为 `2168.5813ms`/`2264.1152ms`，保留作为超时对照；该路径与 native 报告 `qualityComparable=false`。
- RAG 扩展质量与人工发布门禁仍未完全通过，且按当前优先级暂缓：100 条人工标注查询尚未执行。最终修复包位于 `.run-data/phase4-package-final-20260921`：`setup` 为 `386345197` bytes、SHA256 `4156e97be026fb69daa7f87b1017ea25f989777efc80334729cafc1bab10d6f4`；`portable` 为 `386073824` bytes、SHA256 `3e1919838173a348625a657f3d885444253a26e1eb3999f4e826d57d2f872fb1`。此前 `BrowserWindow ready-to-show` 未触发导致窗口隐藏，已增加 `did-finish-load` 幂等显示兜底；修复包 CDP `visibility=visible`，项目架构导航、空态和添加项目对话框的截图/交互通过，Archify resources passed。打包 API 与 Worker 使用同一 data root 连续两次启动，均通过 `/api/v1/health`：`status=ok`、`version=0.1.1`、`worker=ok`。最终 `Knowledge Agent Hub-0.1.1-x64-portable.exe` 已直接启动（非 `win-unpacked`），从 `%LOCALAPPDATA%\Temp\...\resources\app.asar` 自解压运行；其 `/api/v1/health` 返回 `status=ok`、`version=0.1.1`、`worker=ok`，CDP 文档为 `ready=complete`、`title=Knowledge Agent Hub`、`visibility=visible`、`1440x900`，截图为 `.run-data/phase4-package-final-20260921/portable-visible.png`；执行 `window.close` 后 `48761` 端口已释放。设置→检索模型真实服务崩溃根因是 API 返回 `{models: [...]}` 而客户端按数组处理，已归一化；打包真实点击显示 BGE 条目且无 alert/exception。上述渲染层与交互证据支持“可见桌面操作”勾选，但 Windows `MainWindowHandle=0` 源于自动化窗口站隔离，用户侧人工肉眼确认仍需单独区分；尚未在干净系统真实安装，NSIS clean install 仍未完成，因此不能勾选“打包 → 干净安装 → 完整使用 → 重启恢复”，该总项保持未完成。
