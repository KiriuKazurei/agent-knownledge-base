# 2026-08-26 项目审校与阶段二基本完成评估

> 本文是基于当前工作树源码、路由注册、持久化实现、自动化测试和既有运行记录形成的审校结论。历史计划不作为完成证据；没有本轮实测的指标保持为待验收。

## 结论

当前项目已经越过“能运行的原型”阶段：阶段一工程骨架和阶段二首个检索闭环具备可维护的源码实现，阶段三、四的主要能力也已进入代码与测试体系。

本轮将阶段二从“开发完成、严格验收待补证”推进为“基本完成”。关键补强是持久化任务的重启恢复：任务保存完整回放参数，数据库重启会把遗留运行任务重新置为 queued，API 启动时原子认领并恢复任务；单个损坏或不支持的任务会被标记失败，不会阻断其他任务。

阶段二尚不能标记为严格完成，原因是 Recall@10、百万分块检索 p95、故障矩阵和本轮代码变更后的真实可见窗口复验还没有形成新鲜、可复核的证据。

## 当前进度

| 范围 | 当前判断 | 已有证据 | 尚未闭合 |
| --- | --- | --- | --- |
| 阶段一：工程骨架 | 开发完成，交付验收待补 | Electron → Gin → worker、配置、日志、API 和自动化测试均已存在 | 干净环境安装/迁移和发布包验收 |
| 阶段二：首个检索闭环 | 基本完成 | 导入、分块、全文/vector/hybrid、RRF、引用回溯、降级状态、流式查询、任务恢复；Go 全量测试通过 | Recall@10、检索 p95、全 job 崩溃矩阵、当前变更后的可见窗口复验 |
| 阶段三：完整知识管理 | 主要代码已存在，需按验收项收口 | 文件夹、收藏、固定搜索、文本更新、来源扫描、受控 URL、索引重建已有实现 | 真正持续监视、源删除/改名语义、备份恢复等仍需独立验收 |
| 阶段四：模型与 Agent | 主要代码已存在 | Provider、模型列表、回答/SSE、权限、token、反馈和审计已有实现 | 外部模型和真实 Agent 使用场景需继续做环境验收 |
| 横向 Skill 映射 | 规划阶段 | 计划文档已形成 | 软链接映射、校验、修复、审计尚未实现 |
| 阶段五：交付强化 | 未完成 | 有 Windows CI 基础配置 | 便携/安装包、恢复演练、干净环境迁移和发布流程 |

## 本轮审校发现

### 已确认的有效能力

- API 路由、SQLite 存储、对象库和 worker JSON-RPC 链路已注册且有测试覆盖。
- worker 支持 Markdown、TXT、代码、PDF、DOCX、XLSX、PPTX、HTML 的解析/分块；检索支持 lexical、vector 和 hybrid。
- hybrid 检索由 lexical/vector 两路排名执行 RRF 融合；API 再从 SQLite 补齐标题、位置、内容哈希，证据可以回溯到具体分块。
- worker 不可用时 API 可降级到 SQLite lexical 检索，并返回 degraded 状态；这条降级路径不能被误报为向量检索。
- 桌面端已有三栏工作台、文档预览、导入任务、回答选择/展示和证据反馈入口；自动化类型检查、单测和 Electron 构建可运行。

### 本轮修复的阶段二缺口

- `file_import` 任务现在持久化 `libraryId`、源文件 `path` 和名称；源目录扫描产生的子任务也保存完整回放参数。
- 新增内部 queued job 读取和原子 Claim 机制，避免两个恢复入口重复执行同一个任务。
- API 启动后自动恢复 `file_import`、`url_import`、`skill_import`、`source_scan` 和 `index_rebuild`；payload 缺失、损坏、字段类型错误或任务类型未知时只失败当前任务。
- `CreateJob` 不再静默吞掉 payload 序列化错误。
- 新增重启回归测试，真实验证了“运行中任务模拟中断 → 数据库重新打开 → migration 回置 queued → API 恢复 → 文档 ready 且产生分块”。

## 阶段二“基本完成”验收口径

阶段二可以维持“基本完成”，但在下列项目全部有证据前，不改成“严格完成”：

1. 功能闭环：知识库、文件导入、解析/分块、索引、全文/vector/hybrid 查询、引用回溯和 lexical 降级均可用。
2. 任务可靠性：新建任务带完整回放 payload；异常退出后至少文件导入已通过重启回归，其他任务类型完成恢复或明确失败。
3. 数据正确性：导入文档状态为 ready，分块和对象库一致，查询证据的文档、分块和内容哈希可回溯。
4. 性能质量：在固定双语评测集上记录 Recall@10；在固定规模数据集上记录预热后的检索 p95，生成模型耗时单独统计。
5. 交互证据：在最新构建上分别记录自动测试、进程健康、可见窗口和人工导入/查询/预览结果。
6. 契约一致性：OpenAPI、SSE 事件、错误码、桌面客户端和 Agent 权限回归通过。

## 后续开发方向

### P0：先闭合阶段二严格验收

- 建立最小双语评测集和固定数据生成脚本，输出 Recall@10、候选数、模式（lexical/vector/hybrid）和运行环境；没有结果前不宣称达到 0.80。
- 建立可重复的检索 p95 基准，至少覆盖冷启动/预热后边界、不同 topK 和 worker 不可用降级路径；把结果写入验收记录。
- 建立崩溃恢复测试到 URL、Skill、源扫描、索引重建和损坏 payload；重点检查“任务不丢失、不会双跑、失败可见”。
- 在当前变更重新构建后做一次 Electron 可见窗口验收，确认任务恢复状态、导入、查询、预览和引用交互没有被后台改动破坏。
- 评估 worker 的索引查询实现：当前 LanceDB 负责持久写入，但搜索仍使用可移植 JSON/确定性向量路径；阶段二基本完成可接受该降级，但后续应补真实 LanceDB 查询与质量对比。

### P1：阶段三/四收口

- 把源目录扫描从手动扫描入口推进到真正的增量监视，并定义修改、改名、删除和源已丢失的状态转换。
- 完成 `.kahbackup` 的校验、临时目录迁移和原子恢复演练；恢复不得默认写入外部 Skill 映射目录。
- 对 Provider、远程授权、Agent token、SSE 和引用回答补充真实配置下的人工验收，保留本地优先和隐私边界。

### P2：交付与横向能力

- 补齐 worker 可执行文件、NSIS/portable 包和干净 Windows 安装/复制/迁移验收，再把 CI 从验证扩展到可下载构件。
- 按 `docs/SKILL_MAPPING_PLAN.md` 实现真实 Windows 目录软链接映射；先做路径、同名对象、权限、外部篡改和解除映射测试。

## 本轮验证

- `services/api`：`go test ./...` 通过，包含新增重启恢复回归；此前 `go vet ./...` 也通过。
- 新增测试使用真实 SQLite data root、真实 Markdown 源文件和文本 fallback，不依赖 worker 进程。
- 代码已执行 `gofmt` 和 `git diff --check`；本轮没有修改与阶段二无关的用户文件。
- Python 3.14、pnpm 依赖脚本和完整干净安装仍受本机环境限制；旧记录中的 worker/桌面/可见窗口证据继续保留，但不替代本轮阶段二恢复测试。

## 文档关联

- 总计划：[docs/PROJECT_PLAN.md](PROJECT_PLAN.md)
- 验收记录：[docs/ACCEPTANCE.md](ACCEPTANCE.md)
- 架构说明：[docs/ARCHITECTURE.md](ARCHITECTURE.md)
- Skill 映射计划：[docs/SKILL_MAPPING_PLAN.md](SKILL_MAPPING_PLAN.md)

## 2026-08-27 质量收口证据

### 检索质量（小规模基准）

- 基准脚本：services/worker/benchmarks/benchmark_retrieval.py；固定夹具为 18 文档、10 个中英混合查询。
- 条件：warmup=10、iterations=100、topK=10，每种模式得到 1000 次查询样本；结果见 docs/PHASE2_RETRIEVAL_BENCHMARK_2026-08-27.json。
- 结果：lexical/vector/hybrid 的 Recall@10 均为 1.00；p95 分别为 1.1432 ms、1.1189 ms、1.1285 ms。
- 边界：报告已标为 referenceScale=false、backend=portable-json；它证明固定夹具上的可重复性，不证明百万分块 SLA，也不证明真实 LanceDB 查询质量。

### 任务恢复

- 真实恢复矩阵覆盖 URL 导入、Skill 导入、来源扫描和索引重建；模拟任务运行中断、重开 SQLite、启动真实 Worker 后恢复。
- 每个目标任务均达到 completed；URL 与来源扫描生成的文档为 ready，Skill 成功导入，来源扫描状态已写回。
- 损坏 payload 和未知 kind 会变为 failed 并保留诊断；ClaimJob 保持 queued 到 running 的原子认领，避免重复回放。

### 中文文件兼容性

- 文本解码优先级为：BOM、HTML 显式 charset、严格 UTF-8、UTF-16/GB18030/Big5 等候选；无有效安全解码时拒绝解析，不用替换字符伪装成功。
- Worker 已回归 UTF-8 BOM Markdown、GB18030 Markdown/CSV、无 BOM UTF-16LE 和声明 GBK 的 HTML；断言比较实际 Unicode 内容，并检查不存在 U+FFFD 和 NUL。
- API 的无 Worker fallback 同样覆盖 UTF-8 BOM、GB18030 和无 BOM UTF-16LE，避免直接 bytes 转 string 造成中文乱码。
- 导入范围新增 CSV、TSV、RST、LOG、INI、CONF、PROPERTIES；HTML 优先走结构化提取。

### 本轮新鲜验证

- services/api：go test ./... 通过；目标恢复矩阵和 API 解码回归单独通过。
- services/worker：py -3.7 -m unittest discover -s services/worker/tests -v 通过，10/10。
- 待完成的严格验收只保留：目标硬件/百万分块 p95 与容量、真实 LanceDB 查询对比、以及本轮变更后的 Electron 可见窗口和人工全流程验收。

## 2026-08-27 阶段二严格验收结论

### 规模化检索证据

- 新增可复现压测参数 `--stress-chunks`，基于同一固定双语夹具复制文档，使用一次性索引重建，避免逐条写入掩盖查询开销。
- 100,000 chunks 压测结果已落盘到 `docs/PHASE2_RETRIEVAL_STRESS_2026-08-27.json`；portable-json 后端、10 个查询、每种模式 1 次迭代，Recall@10 均为 1.00。
- 100,000 chunks 下 p95 为 lexical 3558.78 ms、vector 3509.56 ms、hybrid 3582.91 ms；已明显超过计划中的 p95 不超过 2 秒参考线，且尚未达到 1,000,000 chunks 目标规模。
- 固定 18 文档评测的最终报告仍为 referenceScale=false；100 次迭代、预热 10 次后，Recall@10 三种模式均为 1.00，p95 分别为 0.8835 ms、0.8711 ms、0.7071 ms。
- 该结果确认当前 portable JSON 路径随数据规模线性变慢；不能外推为百万分块通过，也不能把小规模毫秒级结果当作生产容量证据。

### 严格验收判定

| 验收项 | 结果 | 判定 |
| --- | --- | --- |
| 固定双语集 Recall@10 | 18 文档、10 查询、1,000 次/模式，均为 1.00 | 通过 |
| 任务崩溃恢复矩阵 | URL、Skill、来源扫描、索引重建及损坏 payload 回归通过 | 通过 |
| 中文无乱码 | Worker 与 API fallback 覆盖 BOM、UTF-16、GB18030、声明 charset，拒绝 U+FFFD/NUL | 通过 |
| 100,000 chunks 压测 | Recall@10 1.00，但 hybrid p95 3582.91 ms | 未通过参考线 |
| 真实 LanceDB 查询 | 当前 Python 3.7 环境未安装 LanceDB；搜索实际使用 portable-json | 未验收 |
| 当前变更后的可见桌面全流程 | 尚未取得本轮新鲜窗口、导入、查询、预览及引用交互证据 | 未验收 |

结论：阶段二继续保持“基本完成”，严格完成不通过。后续 P0 是在 Python 3.14 与真实 LanceDB 运行时完成索引查询对比，并在目标硬件/百万分块数据集上重新测量；同时补齐当前构建的可见桌面人工流程证据。阶段三/四收口不得替代这些阶段二硬门槛。

## 2026-08-27 当前构建可见流程复核

- 最新 `apps/desktop/out` 启动成功：Electron 窗口标题为 Knowledge Agent Hub，窗口句柄为非零值 `8917360`，渲染器输出 `KAH_RENDERER_READY`，DOM 根节点和 `window.kah` 均存在。
- 在该实例内通过桌面运行时认证调用导入 `docs/PROJECT_PLAN.md`，任务状态为 completed，文档状态为 ready；页面刷新后真实 DOM 显示 1 个项目。
- 通过真实页面输入中文查询“中文乱码”并点击搜索，页面显示“搜索结果”和 20 条证据；结果卡片及“标记证据相关性”入口存在，查询响应 `degraded=false`。
- 点击结果卡片后页面进入文档预览，DOM 包含中文内容且未发现 U+FFFD；本次验证覆盖渲染器/API 导入、页面搜索和预览点击。
- 边界：原生文件选择器的人工鼠标/键盘导入尚未执行，因此“可见窗口 + 渲染器全流程”通过，“人工文件选择器验收”仍待补，不据此标记阶段二严格完成。

## 2026-08-27 阶段三/四并行收口复核

- 子代理复核并收口了阶段三来源修改、改名、删除后的状态与索引同步，阶段四受限 Agent 的 Skill 查询、Manifest 和文件读取边界，并补充对应回归测试。
- 收口期间发现保存搜索接口对不存在的库 ID做了非必要前置校验，与既有保存搜索生命周期契约冲突；已移除该校验，保留文件夹、来源监视和 Agent 权限等真正需要的库边界。
- 修复后 `go test ./...`、`go vet ./...` 均通过；阶段三/四受影响测试覆盖来源变更与 source_missing、编辑重索引失败、Skill 跨库拒绝、Provider/SSE/Agent token 等路径。
- 阶段三/四当前判定为“部分收口/基本能力可用”，尚未严格完成：真实外部 Provider、桌面端回答流和人工 SSE 全流程仍待环境验收；阶段二的百万分块性能与真实 LanceDB 硬门槛保持独立。

### 最终重启窗口复核

- 在全部代码修复后重新启动最新 Electron 构建，窗口句柄为非零值 `57019152`；页面仍显示已导入的 1 个中文项目，`window.kah` 存在。
- 最终实例再次执行中文查询并点击结果：20 个结果卡片、证据反馈入口存在，预览 DOM 含中文且无 U+FFFD；原生文件选择器人工操作仍未完成。
