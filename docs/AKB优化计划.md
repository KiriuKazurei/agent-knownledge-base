# AKB 前端重构交接与安装版自动更新

状态核对日期：2026-10-10（Asia/Shanghai）。本文对应当前源码版本 **0.1.2**，也是首个带安装版更新器的版本。

对应实现提交：[d4fa708](https://github.com/KiriuKazurei/agent-knownledge-base/commit/d4fa708e4fb8e40dcc126fda9b8e46a261520bf7)，已推送到 `develop`；本次文档同步不改变该实现基线。[该实现提交的 GitHub CI](https://github.com/KiriuKazurei/agent-knownledge-base/actions/runs/38045902179) 已完成且全部通过。当前公开正式 Release 仍为 [v0.1.1](https://github.com/KiriuKazurei/agent-knownledge-base/releases/tag/v0.1.1)，0.1.2 源码推送与 0.1.2 安装包发布是两个独立状态。

接口文档的历史生成快照基线为 `1b9207aa9110f493fa01f6533b8b5d0db806081a`，生成时存在未提交实现。机器清单 `snapshot` 保留当时的生成时间和工作区状态，不代表当前实现未提交。已使用 `pnpm verify:interfaces` 核对当前源码：109 个 client 方法、4 处组件直接 HTTP 调用、15 个桌面接口、121 条实际 Go 路由均匹配，路由/鉴权差异 0；`plannedInterfaces=[]`，本计划列出的新增更新接口均已实现。

## 当前阶段与剩余工作

| 阶段 | 0.1.2 当前状态 | 后续工作 |
|---|---|---|
| 接口盘点与差异修复 | 已完成；类型、实际路由、专用凭证和调用流程已核对 | 前端契约变化时重新生成并检查文档 |
| 前端重构交接 | 三份交付文档已进入远程源码；功能与状态约束齐备 | 用户另行安排前端 Agent 进行视觉重构与逐功能验收 |
| 安装版更新器与发布流程 | 已实现并通过本地测试及远程 CI；正式 Windows 包已在本地生成 | 执行 Windows Release 草稿构建并验收其产物 |
| 安装升级与隔离验收 | 真实隔离 A→B 和三种禁用模式验收已完成；本轮补充 Skill 持久化验收通过 | 完成人工界面及真实异常故障注入；最终发布构建如有变化须重新验证升级 |
| 0.1.2 正式发布 | 尚未发布；不能将 develop 推送或普通 CI 成功当作已发布安装包 | 完成待验收项后发布经过校验的正式 Release |

## 目标与边界

前端交付为完整功能接口与状态文档，视觉重构由用户另行安排的 Agent 执行。本次界面仅新增安装版应用更新设置和下载就绪提示，保留导航、快捷键、主题、密度及原有功能。

安装版自动检查并下载 GitHub 正式稳定版本，只有用户确认后才重启安装。源码版、便携版和直接运行的解包应用不初始化更新器，不发更新网络请求，不显示更新操作。

## 已落地的交付

- [前端接口与重构交接](前端接口与重构交接.md)：HTTP、桌面 IPC/事件、类型、参数、脱敏示例、页面流程、缓存刷新、轮询、组件属性/回调和持久化状态。
- [机器可读清单](frontend-interface-inventory.json)：基线提交、生成时间和工作区状态；正在使用/已封装未使用/后端未接入分类；实际路由与鉴权核对。
- `pnpm docs:interfaces` 重新生成文档，`pnpm verify:interfaces` 只读检查覆盖及漂移。CI 同步运行检查。
- 安装版更新器、NSIS 安装标记、共享更新状态与受控 IPC；独立 Windows Release 草稿工作流。
- 完整打包与 Release 验证脚本；隔离 A/B 更新包构建和真实 NSIS 安装验收脚本。

接口核对修复了 `GET /api/v1/skill-mapping-targets/:id` 路由缺失；4 个架构 Agent lease 方法改为显式接收专用 secret，不再错误复用 desktopToken。既有 embeddingModels 客户端响应归一化继续保留。

## 更新器行为与数据保护

NSIS 安装钩子在实际安装目录的 resources 写入 `kah-installation.json`。该文件不进入共享解包产物或便携包；判断优先级为开发态 → 现有便携标识 → 有效安装标记 → 解包应用。更新源固定为 `KiriuKazurei/agent-knownledge-base`，客户端不接受渲染器提供的源地址或安装文件。

后台与窗口就绪后延迟 30 秒检查，之后每 6 小时检查，支持手动检查与重试。仅接受更高的稳定 SemVer，忽略预发布、草稿、同版本及低版本。下载使用 electron-updater 的清单 SHA-512 校验；完成后显示“重启并更新”和“稍后”。`autoInstallOnAppQuit=false`，普通退出及下次启动均不自动安装。

UI 安装操作先确认重启，再复用未保存草稿保护。主进程再次检查模式、已校验包、备份恢复维护状态，然后调用仅桌面凭证可用的 `POST /api/v1/runtime/shutdown`。API 在同一调度门禁下原子检查请求、queued/running/cancellation_requested 任务及索引重建：忙碌返回409，关闭不可用返回503，接受返回202并关闭新任务入口。来源监视与架构调度共享门禁。

更新关闭先等待 HTTP 退出、Worker stdin EOF 和进程退出，最后关闭数据存储。20 秒内未确认 API 正常退出则中止安装；不强制覆盖运行中的程序。确认后运行 NSIS 安装器并自动启动新版本，沿用安装目录、appId 和独立的数据根；桌面/API/Worker同版更新。此版不增加数据库迁移或自动降级。

所有错误写入现有脱敏、轮转日志。检查/下载失败不阻止知识库使用。关闭预检忙碌可继续使用并稍后重试；实际关闭超时需按日志重新启动旧版后重试。
更新缓存位于应用用户配置目录中，避免共享系统缓存并支持独立测试配置。备份恢复从首次异步验证前就占用维护门禁，完整结束后释放；更新关闭与恢复操作不能同时进入。显式确认更新且后台已正常退出时允许跨过残留编辑器 beforeunload；普通关闭和失败路径继续保留草稿保护。

## 构建与发布流程

1. 同步根包、桌面包、Worker包和Worker运行时版本；API构建通过ldflags注入根版本，底栏读取实际运行版本。
2. `pnpm package:win` 按 Worker → API → 桌面构建；普通本地打包明确 `--publish never`。目标机不要求安装开发运行时。
3. `pnpm verify:release` 验证版本、NSIS安装包/latest.yml大小及SHA-512、捆绑后端字节一致性、GitHub源和安装标记隔离，输出build-manifest.json及SHA256SUMS.txt。
4. Windows Release工作流仅上传草稿：NSIS安装包、对应blockmap、便携包、latest.yml、清单和校验信息。不得修改已发布Release；CI凭证只存在于CI。
5. 真实A→B升级与人工桌面验收通过后再人工发布草稿，避免用户下载未验收版本。若最终发布构建与已有 A→B 验收包不同，需针对最终构建重新核对升级保留、健康、进程和端口释放。

普通 CI 在 `main`、`develop` 推送或 pull request 时验证源码；Windows Release 草稿工作流仅在 `v*` 标签推送或手动触发时构建和上传，推送 develop 本身不生成远程安装包。使用 `workflow_dispatch` 手动触发之前，工作流文件须进入默认分支，当前默认分支为 `main`；这是 [GitHub 的触发要求](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)。

旧0.1.1无更新器，必须手动安装一次0.1.2，后续才自动更新。首版沿用未签名包，HTTPS和哈希验证只证明传输内容与清单一致，不等于发布者身份验证。保留旧安装包和已有备份恢复流程。

技术依据：[electron-builder v26 Auto Update](https://www.electron.build/v26/docs/features/auto-update/)、[发布配置](https://www.electron.build/v26/docs/publish/)。依赖为electron-updater 6.8.9，现有Electron/Builder主版本保持不变。

## 隔离验收操作

先完成普通正式包构建，再运行 `node scripts/build-update-fixtures.cjs`、`node scripts/accept-installed-update.cjs` 和 `node scripts/accept-mode-isolation.cjs`。顺序执行；产物目录、端口和解包目录不能被两个打包/运行任务同时占用。

隔离构建仅在编译时嵌入回环测试源，运行时环境变量不能改写正式版源。测试使用独立appId、无桌面/开始菜单快捷方式、工作区安装/data/profile目录、API端口48765、更新源48766及CDP9228。测试B使用复制到工作区的Worker源码注入下一版本，不改写跟踪源码版本。测试包不能发布为正式版本；验收后重新构建正式包并运行verify:release。

验收脚本拒绝占用已有测试端口。使用真实NSIS安装A、导入与审核专用资料、创建和验证备份、下载B、选择稍后、普通退出并再次启动A、显式安装B、确认版本/资料/知识检索/偏好保留及端口释放。截图和脱敏结果保留在 `.run-data/updater-acceptance`。
NSIS通过Explorer自动启动B后，先独立确认新版本健康和原生窗口可见；若没有测试CDP端口，再正常关闭并以调试参数重开B完成数据/页面检查。这次验收需要该检查重启，不能将其写成同一进程的连续CDP采样。Windows临时空间不足时将本轮TEMP/TMP/GOTMPDIR指向工作区专用目录，不清理用户其他临时文件。

## 验收记录

| 项目 | 当前状态 |
|---|---|
| 接口覆盖 | 109个client方法、4处组件直接HTTP调用、15个桌面接口；路由/鉴权核对通过 |
| 前端与更新测试 | 82项通过；包含隔离、检查时机、重复请求、校验失败、忙碌/超时、订阅释放、稍后、晚到快照保护、草稿确认和安装器失败事件 |
| TypeScript | 通过 |
| Go全部测试与静态检查 | 全量测试和 go vet 通过；包含关闭门禁、鉴权、映射详情及子目录 Git 范围回归 |
| Worker unittest | 49项通过；真实本地LanceDB/文档依赖 |
| 正式完整打包 | PASS：已重新构建0.1.2 Worker/API/NSIS/便携包；latest.yml SHA-512、兼容字段、实际ASAR/后端字节一致性与标记隔离验证通过 |
| 真实安装A→B | 此前隔离验收 PASS：0.1.2→0.1.3；真实NSIS下载/校验/确认/安装/自动重启、资料ID/知识检索/偏好保留及API端口释放；包含后续架构扫描修复的最终包尚未重跑 A→B |
| 模式隔离 | 此前隔离验收 PASS：真实解包应用、便携包、源码版；手动方法均禁用，越过30秒检查时限，更新网络请求0、无更新入口；本轮未重跑 |
| Skill 持久化 | 最新打包 API/Worker 验收 PASS：专用 zip 导入、备份校验、正常关闭并重启后 Skill ID/状态/文件哈希保留，测试端口释放 |
| 原生窗口/自动化外观 | 原生可见窗口已确认；18组主题/密度/尺寸组合通过，宿主DPR=1.5；请求1040×680实际1041×681、默认1440×900、最大可用1708×1020 |
| 人工界面验收 | 未完成；自动化窗口/截图不能替代用户肉眼确认 |
| 源码远程同步 | 0.1.2 实现提交 d4fa708 已推送 develop |
| GitHub普通CI | 已执行且全部通过；对应提交 d4fa708，运行编号 38045902179 |
| Windows Release草稿工作流 | 尚未执行本版本的远程打包与产物验收；不能用普通 CI 结果替代 |
| 正式Release | 0.1.2 尚未发布；当前公开最新版为 v0.1.1 |

前端重构验收须覆盖全部交接功能表，亮/暗/系统主题、两种密度、最小1040×680、默认1440×900、大窗口、Windows125%/150%缩放、键盘/焦点、加载/空态/错误及页面A→B→A。功能回归、打包升级、可见渲染和人工体验是不同验收项，必须分别报告。

自动化证据：`.run-data/updater-acceptance/installed-update-result.json`、`mode-isolation-result.json`、`before-update.png`、`after-update.png`和18张ui截图。Go真实Skill符号链接管理生命周期独立执行通过，无跳过。业务功能回归由前端、Go、Worker测试提供；打包端到端实测覆盖导入、审核、检索、备份创建/校验及升级保留。完整人工业务操作、系统125%缩放和全部键盘/焦点场景仍待人工验收。

异常验证的边界：无更新/同版/降版/预发布、断网、清单或校验失败、重复调用、忙碌/超时及安装启动权限错误都有自动化状态/接口验证；真实NSIS异常权限和关闭超时故障注入尚未单独执行。成功升级实测不能代替这些故障注入。正式发布仍须人工完成待验收项。

## 2026-10-10 再检查与提交结果

本轮审查覆盖更新器/IPC/草稿保护、API 调度与 Worker 退出、Skills 数据持久化、接口文档/真实路由/OpenAPI、版本与锁文件、CI 和最终 Windows 产物。

发现并修复以下问题：

- 架构扫描子目录时，Git porcelain 返回父仓库相对路径，可能将父目录的同名文件误计入当前项目。现在用路径范围限制查询，并转换为扫描目录相对路径；新增父目录变更隔离、子目录变更及中文文件名回归测试。
- 上次中断留下的接口清单同步未完成；重新生成全部交接文件，所有在用 HTTP 方法均有对应刷新规则或直接操作流程。生成器统一 LF，并对 TypeScript 编译器读取采用同一规范，避免 Windows CRLF 检出造成清单漂移。
- CI 增加 develop 分支，推送开发分支也触发验证。
- OpenAPI 补齐已实现的映射目标详情 GET 与仅桌面会话可用的 runtime/shutdown。

本轮结果：前端 82 项、Worker 49 项、Go 全量测试与 go vet、TypeScript、OpenAPI lint、离线 frozen-lockfile 安装、接口覆盖均通过；API 与 NSIS/便携包已重新构建，最终 ASAR/API/Worker 一致性和 SHA-512 检查通过。真实 A→B 和模式隔离证据沿用本次此前完成的验收，本轮没有重新执行 NSIS A→B。

新增真实打包 API/Worker 验收使用独立数据目录和专用 Skill zip：导入完成、入口及附属文件可读、备份 SHA-256/清单校验通过；正常关闭 API/Worker 后重启，Skill ID、状态和所有文件哈希一致，测试端口释放。证据位于 `.run-data/review-20261010-aCljiU/result.json`。

数据丢失排查的边界：真实安装目录覆盖前的 SQLite 备份和覆盖后数据库均只有内置 formatter Skill，没有 taste 记录；35 个表的记录数量和数据库完整性保持一致。这能证明此次覆盖未清理已有数据库记录，不能证明用户更早的 taste 导入记录来自何处，也不能把内置 Skill 计为用户导入记录已保留。旧导入来源尚未定位。本轮验收使用专用测试包，不写入用户数据目录。

此前用户安装目录的手动覆盖验收为 0.1.2、API/Worker 健康和原生窗口可见。本轮包含架构扫描修复的安装包已在 release 重新生成，没有再次覆盖用户正在使用的安装目录。

0.1.2 实现已提交并推送远程 develop；本地与远程的实现提交均为 d4fa708。GitHub CI 的依赖安装、TypeScript、接口覆盖、前端、Go、Worker 和 OpenAPI 检查均成功，最终结论为 success；结果保存于 `.run-data/review-20261010-aCljiU/remote-ci-result.json`，公开运行链接见本文开头。

运行数据、日志、测试数据库、临时验收包和 release 二进制不进入 Git。正式 Release、人工界面及真实故障注入仍待完成，不能将源码提交成功理解为这些验收已经通过。

## 后续版本的文档同步规则

1. 以根 `package.json`、桌面包、API 构建版本和 Worker 包/运行时版本为准更新本文版本；写明对应实现提交与分支，不把历史生成快照当作当前工作区状态。
2. 对接口或页面行为的变更，先核对真实实现，再执行 `pnpm docs:interfaces` 和 `pnpm verify:interfaces`；同步本文接口数量、已实现能力、参数和状态约束。纯状态文档修改不改写无变化的接口生成快照。
3. 验收结果标明对应提交或测试构建。分开记录本地测试、远程普通 CI、本地打包、真实升级、人工验收和正式发布；沿用旧证据时注明范围，不自动提升为新构建已验收。
4. 更新版本或发布前重新核对本地/远程提交、GitHub CI 和实际 Release 状态；待验收项只有取得对应证据后才能改为完成。随实现提交本文，防止优化计划落后于代码。
