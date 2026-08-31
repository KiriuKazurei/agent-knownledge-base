# Agent Knowledge Base（Knowledge Agent Hub）

Agent Knowledge Base 是一款本地优先的 Windows 桌面知识库，用于导入、整理、审核和检索个人或项目资料，并通过 MCP 为 Codex 等 Agent 提供受控的知识读写能力。

项目采用 Electron + React 桌面界面、Go/Gin API 和 Python 文档/索引 Worker。知识正文、索引、令牌和模型配置默认保存在本机，不需要把资料上传到远程服务。

## 下载

当前版本：`v0.1.0`

- [Windows NSIS 安装版](https://github.com/KiriuKazurei/agent-knownledge-base/releases/download/v0.1.0/Knowledge.Agent.Hub-0.1.0-x64-setup.exe)
- [Windows Portable 便携版](https://github.com/KiriuKazurei/agent-knownledge-base/releases/download/v0.1.0/Knowledge.Agent.Hub-0.1.0-x64-portable.exe)
- [版本说明与 SHA-256](https://github.com/KiriuKazurei/agent-knownledge-base/releases/tag/v0.1.0)

当前 Windows 安装包尚未进行商业代码签名，首次运行时可能出现 SmartScreen 提示。

## 核心能力

- 导入 Markdown、文本、PDF、Word、Excel、PowerPoint、HTML 和常见代码文件。
- 将资料解析为可定位、可引用的文档片段，并使用 LanceDB 与便携 JSON 索引进行混合检索。
- 可将普通资料自动整理为待审核的知识草稿；审核通过后才进入正式知识索引。
- 使用不可变知识修订、来源记录和审核日志保留完整变更历史。
- 导入和管理 Agent Skill，并映射到外部 Agent/工具目录。
- 提供职责分离的 MCP Read 与 MCP Manage 接口。
- 支持 Agent 搜索、比较、验证、提交和审核知识。
- Agent 只有在判断可信度严格高于 95% 时才能自动批准知识，否则必须保留为人工待审核状态。
- 支持内容寻址对象存储、索引重建、备份校验和安全恢复。

## MCP 与 Codex

仓库内置 `akb-mcp` 插件，Codex 界面显示名为 **Agent Knowledge Base MCP**。

- MCP Read：按授权知识库搜索和读取已发布知识。
- MCP Manage：比较资料、提交知识草稿、验证证据，并按可信度门槛审核知识。
- Agent 令牌经过哈希保存，并可限制到指定知识库和权限范围。
- `AKB_MCP_TOKEN` 只应通过环境变量提供，不应写入仓库或配置文件。

MCP 协议与工具说明见 [KAH MCP 重构方案](docs/KAH_MCP_REFACTOR_PLAN.md)。

## 项目结构

- `apps/desktop`：Electron、React 和 TypeScript 桌面应用。
- `services/api`：Gin API、SQLite 元数据、认证、导入、知识审核、备份和任务编排。
- `services/worker`：JSON-RPC 文档解析器、LanceDB 与混合检索 Worker。
- `plugins/akb-mcp`：Agent Knowledge Base MCP 插件与知识管理 Skill。
- `contracts/openapi.yaml`：桌面端 `/api/v1` 接口契约。
- `docs`：架构、开发、安全、重构方案和验收记录。

应用保存的文件路径均相对于配置的数据根目录，不依赖开发者机器上的绝对磁盘路径。

## 本地开发

环境要求：

- Node.js 24.14+
- pnpm 11.21+
- Go 1.26+
- Python 3.14 x64

```powershell
pnpm install
py -3.14 -m venv .venv
.\.venv\Scripts\python -m pip install -e ".\services\worker[dev,index]"
pnpm dev
```

运行测试：

```powershell
pnpm test
pnpm typecheck
```

构建 Windows 安装包：

```powershell
pnpm package:win
```

开发运行数据写入 `.run-data/`；构建产物写入 `release/`。这些目录、数据库、索引、日志和本地令牌均被 Git 忽略。更多命令见 [开发说明](docs/DEVELOPMENT.md)。

## 安全默认值

- API 默认仅监听 `127.0.0.1`。
- Electron 启用上下文隔离与渲染器沙箱。
- MCP 令牌按权限范围和知识库授权，并以哈希形式保存。
- 模型服务密钥使用 Windows Credential Manager 保存。
- 每个知识库默认禁止远程模型读取资料，必须由用户明确启用。
- 未通过审核的知识不会进入正式知识索引。

## 许可证

本项目使用 [MIT License](LICENSE)。
