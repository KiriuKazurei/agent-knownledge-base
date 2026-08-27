# V1 acceptance record

Acceptance evidence is kept in three separate categories:

1. Automated: unit, contract, integration, accessibility, and package tests.
2. Process: Electron, Gin, and Python worker health and clean shutdown.
3. Visible UI: window rendering, keyboard navigation, import, search, preview, settings, backup, and Agent-token workflows.

Performance is measured after warm-up and excludes remote generation latency. The target is p95 candidate retrieval under two seconds on the reference one-million-chunk dataset and Recall@10 of at least 0.80 on the bilingual evaluation fixture.


## 2026-08-25 fresh evidence

- Go: `go test ./...` in `services/api` passed.
- Python: `py -3.7 -m unittest discover -s tests -v` passed, 4/4.
- Desktop: direct TypeScript check passed; Vitest passed, 1/1; Electron renderer/main/preload build passed.
- API/worker: local integration returned `worker=ok`, imported README, and returned three hybrid evidence items with `degraded=false`.
- Visible UI: not accepted yet because the locked Electron package has no downloaded `electron.exe`; the runtime download attempt did not complete.

## 2026-08-26 阶段3/4复核记录

本记录对应当前工作区代码，不替代用户对可见界面和交互路径的最终确认。

- 阶段3复核：保存搜索、文件夹和来源监控的库归属校验已覆盖未知库 ID；文本内容更新会同步对象路径、内容哈希、文档状态、分块和索引任务；URL 导入保留受控抓取入口并移除旧的不可达实现。
- 阶段4完成：OpenAI 兼容、Anthropic、LM Studio 和自定义提供商；模型列表；引用回答；SSE 检索/引用/回答/错误事件；按知识库远程模型授权；哈希存储、撤销和 scope 校验的 Agent token；引用反馈和审计记录。
- 自动证据：API Go 测试、worker Python 测试、桌面 TypeScript/Vitest、Electron main/preload/renderer 构建和 API 构建均通过；当前 worker 健康为 `ok`。
- 运行态证据：最新构建干净可见启动取得窗口句柄 `1511318`，标题为 `Knowledge Agent Hub`，`Responding=True`；`KAH_API_READY`、`KAH_RENDERER_READY` 输出存在，干净启动 stderr 为空。
- 环境边界：本机没有 Python 3.14，worker 测试使用已安装的 Python 3.7；pnpm 脚本因依赖安装策略阻止 Electron/esbuild 构建脚本，桌面验证改用仓库已安装的 TypeScript、Vitest 和 electron-vite 直接入口完成。

## 2026-08-26 中文 Markdown 链接兼容修复

- Markdown 读取保留 UTF-8、UTF-8 BOM、GB18030 和 UTF-16 兼容；新增 GB18030 中文链接内容回归测试。
- 预览中的相对链接支持中文百分号解码、Windows `./`/`../` 路径解析、库内文档切换和外部 URL 安全打开。
- 最新验证：worker 7/7、桌面测试 5/5、TypeScript、Electron 构建通过；最新可见窗口句柄 `921732`，API `worker=ok`，stderr 为空。

## 2026-08-26 历史中文预览修复

- 发现同一源文件正常 UTF-8，但旧数据库分块含 U+FFFD 替换字符，导致侧栏预览乱码。
- 文档详情读取和同哈希重复导入现在会自动重新解析并替换历史错误分块。
- 回归测试覆盖“旧分块 -> 中文源文件 -> 中文预览和数据库恢复”；API 全量测试、go vet、Worker 7/7、桌面 5/5 均通过。
- 使用 Electron 同一数据目录实测 README.zh.md 返回 3 个中文预览分块且不含 U+FFFD；可见窗口句柄 `1836862`，API `worker=ok`，stderr 为空。

## 阶段三/四严格验收（2026-08-27）

阶段三、四已完成核心链路复验并修复多项失败静默、权限归属和桌面旧服务问题，但严格验收尚未完全通过。问题清单与后续修复顺序见：[阶段三/四严格验收与修复清单](ACCEPTANCE_PHASE3_4_2026-08-27.md)。