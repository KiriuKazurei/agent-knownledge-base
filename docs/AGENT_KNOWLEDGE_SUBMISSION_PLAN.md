# Agent 主动提交知识与审核发布方案

## 概要

新增一条独立的“Agent 提交 → 待审核 → 审核批准 → 索引发布”链路。Agent 必须持有带 `submit` scope 且限定知识库的输入 Token，先读取系统内置的全局格式化 Skill，再提交符合规范的 Markdown。待审核、已驳回或发布失败的内容绝不参与检索，只有状态为 `ready` 的知识可被调用。

## 核心实现

- 新增受保护的系统 Skill `knowledge-submission-formatter`：
  - 启动时幂等安装或升级，使用稳定系统角色标识，不覆盖同名用户 Skill。
  - 用户可以查看，但不能删除、替换或修改关联。
  - Skill 要求输出 UTF-8 Markdown，禁止 NUL 和 U+FFFD，最大 1 MiB。
  - YAML frontmatter 必须包含 `title`、`summary`、`tags`、`language`、`provenance`。
  - `provenance.type` 为 `external`、`internal` 或 `agent_observation`；外部来源必须提供引用，其他类型必须填写依据说明。
  - 正文必须包含与 `title` 一致的 H1，以及“核心内容、适用范围、限制与不确定性”三个章节。
- 新增 `submit` Token scope：
  - 可与现有 scope 组合，但桌面端默认创建独立输入 Token。
  - 带 `submit` scope 时必须指定至少一个 `libraryId`；空列表不代表全库。
  - 该 Token 不能管理知识库、审核内容或查询正式知识，除非另行授予 `query`。
- Agent 先调用 `POST /knowledge-submissions/prepare`，传入 `libraryId`：
  - 返回格式化 Skill 完整内容、Skill ID、内容哈希、格式约束和一次性提交凭证。
  - 凭证保存哈希，绑定 Token、知识库和 Skill 版本，15 分钟过期。
  - Skill 更新后，旧凭证自动失效。
- Agent 使用 `POST /knowledge-submissions` 提交：
  - 请求包含 `libraryId`、`ticket`、`clientSubmissionId`、`markdown`、可选 `supersedesSubmissionId`。
  - `clientSubmissionId` 在 Token 范围内唯一；重试返回原提交，避免重复知识。
  - 同库相同内容哈希返回 `409 submission_duplicate`；同标题不同内容允许提交。
  - 驳回后的修改必须创建新修订版，原内容不可覆盖。
- 增加查询接口：
  - `GET /knowledge-submissions`：Agent 只看到自己创建的提交，桌面端看到全部。
  - `GET /knowledge-submissions/{id}`：返回状态、审核意见、修订关系和发布时间；Agent 仍受提交者及知识库范围限制。
  - 桌面专用 `approve`、`reject`、`retry-review` 接口；驳回必须填写原因，人工覆盖模型驳回时也必须填写原因。

## 数据、审核与发布

- 扩展文档状态：`pending_review`、`approved_pending_index`、`rejected`、`ready`；保留现有导入状态。
- 新增：
  - `knowledge_submissions`：提交者、幂等 ID、格式化 Skill 版本、修订关系和当前状态。
  - `submission_tickets`：一次性凭证、绑定范围、过期和消费时间。
  - `knowledge_reviews`：不可变的人工或模型审核记录。
- Markdown 在提交时完成规范校验、对象存储和分块，但使用独立方法保存分块，不调用 `index_upsert`，也不把文档改成 `ready`。
- Markdown frontmatter 不进入检索正文；解析器保留原始行号，使引用定位仍对应原文件。
- 知识库新增 `autoReviewAgentSubmissions` 与 `reviewProviderId`：
  - 仅显式启用的知识库执行自动审核。
  - 远程 Provider 必须同时满足现有 `allowRemoteModels=true`；界面明确提示新稿和相关库内证据将发送到远程服务。
- 自动审核使用可恢复的 `knowledge_review` 任务：
  - 根据标题和摘要从同库正式知识中检索最多 8 条相关证据。
  - 审查模型检查格式、来源声明、重复、冲突和内部一致性。
  - 返回严格 JSON：`decision`、`confidence`、`reason`、`issues`。
  - 仅当 `decision=approve`、`confidence>=0.85` 且无 error 级问题时自动批准。
  - `reject` 标记为驳回；`needs_human`、低置信度、超时或无效响应继续保持待审核。
- 批准后进入 `approved_pending_index` 并创建可恢复的 `knowledge_publish` 任务：
  - Worker `index_upsert` 成功后才改为 `ready`。
  - 索引失败保持待发布并允许重试，不产生“已正式但不可检索”的状态。
- 人工拥有最终权限，可以覆盖模型驳回；所有提交、模型决定、人工覆盖、发布和失败均写入审计日志。
- 查询和证据水合继续硬性过滤 `documents.status='ready'`，即使 Worker 存在异常旧索引也不能返回待审核内容。

## 桌面端与验收

- Agent Token 设置改为 scope 和知识库复选框，突出“输入 Token”最小权限和一次性密钥。
- 增加待审核队列、数量提示、状态筛选、Markdown 预览、来源信息、修订链、模型意见及批准/驳回操作。
- 系统格式化 Skill 显示“系统管理”标记并禁用替换和删除。
- 自动化覆盖：
  - Token 越权、撤销、跨库提交及空知识库范围。
  - 凭证过期、重放、跨 Token、跨库和 Skill 版本过期。
  - 中文 UTF-8、frontmatter、来源规则、章节规则、乱码和体积限制。
  - 幂等重试、重复内容、新修订及不可变内容。
  - 待审和驳回内容在 SQLite、Worker 和最终证据水合中均不可检索。
  - 模型批准、驳回、低置信度、异常、远程隐私拒绝及人工覆盖。
  - 索引失败、发布重试、进程重启后的审核与发布任务恢复。
  - OpenAPI、TypeScript 类型、桌面交互和状态样式。
- 最终进行真实 Electron 人工验收：创建输入 Token、读取 Skill、提交中文 Markdown、确认检索不可见、人工及模型两种批准路径、发布后可检索、驳回后新建修订版。

## 默认边界

- V1 只接受 Markdown 字符串，不接受附件、URL 或二进制文件。
- 格式化 Skill 为全局系统 Skill，暂不支持每库或每 Token 覆盖。
- 提交凭证只能证明 Agent 获取了当前 Skill，不能证明其内部推理过程；服务端格式校验是最终保障。
- 自动审查不访问外部网络，只使用提交内容和同库正式知识；Provider 自身为远程时遵循现有隐私授权。
- Agent 提交内容及修订版不可原地编辑；任何正文变化都创建新修订。
- 现有 `query`、`feedback` Token 和普通人工导入流程保持兼容。
