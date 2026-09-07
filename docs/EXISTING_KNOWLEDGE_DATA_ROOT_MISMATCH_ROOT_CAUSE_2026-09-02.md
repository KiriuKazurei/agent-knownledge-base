# 新构建无法读取已有知识库根因分析（2026-09-02）

## 结论

“新构建无法读取已有知识库”的首要根因是数据根目录不一致，而不是新旧数据库 schema 天然不兼容。桌面主进程根据启动模式、环境变量、可执行文件位置和 Electron `userData` 动态计算 `dataRoot`；新构建只会把计算结果通过 `KAH_DATA_ROOT` 传给 API，不会自动发现旧构建的数据目录。

当新构建指向一个新的空目录时，存储层会创建目录并打开该位置的 `knowledge.db`。SQLite 在文件不存在时会创建新数据库，迁移随后建立空结构。用户看到的是“应用正常启动但知识库为空”，容易误判为旧库无法读取；实际上旧 `knowledge.db`、对象和索引通常仍留在另一个目录。

## 数据路径决定链

当前 `apps/desktop/src/main/index.ts` 的 `dataRoot()` 按以下优先级决定目录：

1. 存在 `PORTABLE_EXECUTABLE_DIR`：使用该目录下的 `data`。
2. `KAH_PORTABLE=1`：使用 Electron 可执行文件所在目录下的 `data`。
3. 可执行文件旁存在 `portable.flag`：同样使用可执行文件所在目录下的 `data`。
4. 其他情况：使用 Electron `app.getPath('userData')/data`。

主进程随后把结果设置为 API 的 `KAH_DATA_ROOT`。API 将其规范化为绝对路径，存储层固定在该目录下打开 `knowledge.db`。

## 容易发生路径变化的启动组合

| 启动方式 | 数据目录来源 | 与旧库断开的典型原因 |
| --- | --- | --- |
| NSIS 安装版 | Electron `userData/data` | 与便携版的邻接 `data` 不是同一路径 |
| electron-builder 便携 exe | `PORTABLE_EXECUTABLE_DIR/data` | exe 被复制到新目录后，数据根随 exe 位置变化 |
| `KAH_PORTABLE=1` 或 `portable.flag` | 当前 Electron exe 目录下的 `data` | 启动了另一份 exe 或 flag 未随构建复制 |
| `release/win-unpacked` 中的 exe | 通常走 installed/userData 分支 | `win-unpacked` 不等同于 portable target，不会自然读取 `release/data` |
| 源码开发/直接启动 `out/main/index.js` | Electron 开发态 `userData/data` | 与安装版、便携版使用不同应用位置/配置 |
| 带 `--user-data-dir=<隔离目录>` 的验收启动 | 指定 profile 下的 `data` | 隔离测试刻意不读取正式知识库 |
| 直接启动 API | 显式 `KAH_DATA_ROOT`，否则 `.run-data` | 绕过 Electron 后不会采用桌面版目录规则 |

## 最可能的事故链

本项目先前的 Windows 输出同时存在：

- `release/Knowledge Agent Hub-0.1.0-x64-portable.exe`
- `release/win-unpacked/...`
- `release/data`

便携 exe 启动时，electron-builder 会提供 `PORTABLE_EXECUTABLE_DIR`，因此它能读取 `release/data`。如果随后为了唤起“新构建”而直接运行 `release/win-unpacked` 中的 exe、开发态 Electron 或另一个目录里的便携 exe，`dataRoot()` 会落到 `userData/data`、验收 profile 或新 exe 旁的 `data`。新进程不会回看原来的 `release/data`，于是打开了另一套库。

这也解释了为什么程序可以显示“本地服务已连接”却没有已有知识：服务健康只证明新路径下的数据库成功打开，不证明打开的是用户期望的那一个数据库。

## 促成因素

### 1. 数据目录与启动位置耦合

便携模式把数据放在可执行文件旁，便于整体复制，但也意味着“新构建放在哪”会直接改变知识库位置。构建目录、便携运行目录和用户数据目录没有被流程严格分离。

### 2. 新空库会静默创建

存储层会创建 `objects`、`indexes`、`logs`、`backups`、`staging` 和 `skills`，然后打开 `knowledge.db` 并运行迁移。新目录不是错误条件，因此启动不会失败，也不会提示“未找到旧库”。

### 3. 没有稳定数据根标识

当前没有独立于构建位置的用户选择配置，也没有记录“上次成功使用的数据根、库 UUID、数据库哈希或实例标识”。新构建无法判断当前空库是否是用户主动创建，还是路径漂移造成。

### 4. 没有启动时迁移/发现流程

应用设置页能显示并打开“当前数据目录”，也支持备份恢复，但这些能力发生在应用已经用某个根目录启动之后。启动阶段没有：

- 检测当前根为空而旧已知根存在。
- 展示本次与上次 dataRoot 的差异。
- 让用户选择继续空库、连接旧目录或从备份恢复。

### 5. 构建验收使用隔离 profile 时容易产生认知偏差

使用 `--user-data-dir` 验证新 UI 是正确的隔离做法，但只能证明新构建可启动，不能证明它读取正式数据。若验收报告没有单独记录 `runtime.dataRoot`，就可能把“隔离实例为空”误报为数据丢失，或把“测试数据可见”误报为正式库继承成功。

## 证据

- `apps/desktop/src/main/index.ts:52-56` 定义启动模式相关的 `dataRoot()`。
- `apps/desktop/src/main/index.ts:74` 把计算结果传给 API 的 `KAH_DATA_ROOT`。
- `apps/desktop/src/main/index.ts:238` 通过 runtime config 向界面暴露当前数据目录。
- `services/api/internal/config/config.go:25` 读取 `KAH_DATA_ROOT`，未提供时默认 `.run-data`。
- `services/api/internal/storage/store.go:57-68` 创建数据子目录并打开 `<dataRoot>/knowledge.db`。
- `docs/ARCHITECTURE.md:11-13` 记录 installed 与 portable 使用不同数据根的设计。
- 清理旧构建时，`release/data` 被识别为潜在正式便携知识库并明确保留。

## 当前数据的安全处理原则

在确认根因和迁移前：

1. 不删除、覆盖或直接编辑任何候选 `knowledge.db`。
2. 不只复制 SQLite 文件；对象、索引、Skills、备份和日志均属于同一数据根。
3. 若数据库正在使用，先通过应用创建 `.kahbackup`，不要复制活动中的单个数据库文件。
4. 记录旧版和新版设置页显示的完整 `dataRoot`。
5. 校验候选根是否包含 `knowledge.db`、`objects` 和预期库记录。
6. 通过现有备份预检和原子恢复流程迁移，不做直接 SQLite 合并。

## 建议修复

### 1. 建立稳定的数据根契约

- 安装版始终使用同一个按 `appId` 确定的用户数据目录。
- 便携版只在明确 portable 标识存在时使用邻接数据。
- 开发/验收必须显式使用 `.run-data/<purpose>`，不得含糊继承正式库。
- 不把 `win-unpacked` 当成便携版运行入口。

### 2. 启动时记录并展示路径身份

后端启动日志和界面状态应同时显示：

- 当前启动模式：installed、portable、development、test。
- 规范化后的 dataRoot。
- 数据库是否为首次创建。
- 数据实例 ID 和 schema 版本。

“本地服务已连接”不能代替“已连接预期知识库”。

### 3. 增加路径漂移保护

若当前 dataRoot 是新空库，而上次记录的数据根仍存在，应暂停自动进入空工作台，展示可恢复选择：

- 使用当前空库。
- 连接旧数据目录。
- 从 `.kahbackup` 恢复。
- 仅打开旧目录进行核验。

应用可以发现候选路径，但不应未经确认自动移动或合并用户数据。

### 4. 将数据迁移纳入发布验收

至少覆盖以下矩阵：

1. 旧安装版 → 新安装版。
2. 旧便携版原地替换 → 新便携版。
3. 旧便携版移动到新目录。
4. 便携版 → 安装版。
5. 安装版 → 便携版。
6. `win-unpacked`、开发态和隔离 profile，确认不会被误认为正式数据继承。

每项都要分别记录旧/新 `dataRoot`、库数量、稳定知识数量、MCP 回读结果和备份哈希。

## 验收标准

问题不能仅以“新窗口能看见知识”判定解决。合格标准是：

- 启动前后 `dataRoot` 符合所选模式的稳定规则。
- 原数据根未被破坏且有可校验备份。
- 新实例读取的是预期的 `knowledge.db` 和对象目录。
- 数据库迁移成功，稳定知识、来源文档、Skills、审核记录和令牌范围符合预期。
- 服务重启后仍能通过桌面 API 与 MCP Read 检索同一稳定 revision。
- 构建清理脚本不会删除任何活动或候选数据根。
