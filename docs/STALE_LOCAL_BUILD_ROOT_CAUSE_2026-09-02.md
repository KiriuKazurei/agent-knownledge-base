# 本地旧版构建未及时清理根因分析（2026-09-02）

## 结论

旧版构建残留的直接原因不是 Git 异常，而是项目没有定义构建产物生命周期：现有命令只负责编译和打包，没有构建前清理、构建后清单、版本隔离或过期产物回收。与此同时，所有构建目录都被 `.gitignore` 忽略，Git 状态不会提示旧文件存在。只要开发者连续执行开发构建、后端编译和 Windows 打包，多个目录中的产物就会长期并存。

本轮已清理旧构建，并保留 `release/data`。保留该目录是必要的，因为便携版知识库可能位于其中，不能把用户数据当成构建垃圾删除。

## 清理前的实际残留

清理前确认存在以下生成物：

- `apps/desktop/out`：Electron main、preload 和 renderer 构建输出。
- `services/api/kah-api.exe`：Go API 本地可执行文件。
- `services/worker/dist`：PyInstaller Worker 输出。
- `release/win-unpacked`：electron-builder 解包目录。
- `release/Knowledge Agent Hub-0.1.0-x64-setup.exe`。
- `release/Knowledge Agent Hub-0.1.0-x64-portable.exe`。
- 对应 `.blockmap` 和 `builder-debug.yml`。
- `release/data`：与便携版相邻的数据目录，不属于可无条件删除的构建产物。

## 直接原因

### 1. 构建命令没有 clean 阶段

根目录脚本：

```text
build       = desktop build + Go API build
package:win = Go API build + desktop package:win
```

桌面脚本：

```text
build       = electron-vite build
package:win = electron-vite build && electron-builder --win nsis portable
```

这些命令均没有在构建前清理 `out`、API exe、Worker `build/dist` 或 `release` 中的旧产物，也没有在成功后清理临时解包目录。

### 2. 产物分散在多个工具和目录

- electron-vite 写入 `apps/desktop/out`。
- Go 写入 `services/api/kah-api.exe`。
- PyInstaller 写入 `services/worker/build` 和 `services/worker/dist`。
- electron-builder 写入根目录 `release`。

没有一个命令拥有全部四类产物的清单，因此单独重跑某个工具，只能更新自己的输出，不能处理其他工具留下的旧版文件。

### 3. Git 忽略规则隐藏了残留

`.gitignore` 明确忽略：

```text
dist/
out/
release/
services/api/kah-api.exe
services/worker/build/
services/worker/dist/
```

这能防止二进制进入仓库，但也意味着 `git status` 即使显示干净，也不能证明本地没有旧构建。提交完成与构建目录干净是两项独立状态。

### 4. 包版本长期固定为 0.1.0

安装包和便携包使用 `${productName}-${version}-${arch}` 命名，而当前版本仍为 `0.1.0`。同名目标可能被覆盖，但以下内容仍可能残留：

- 上一次未成功完成的 `win-unpacked`。
- 已不再生成的目标或架构文件。
- builder 调试文件和 blockmap。
- 其他工具生成、electron-builder 不负责管理的 API/Worker 文件。

固定名称也让使用者难以仅凭文件名判断产物来自哪次提交。

### 5. CI 不构建 Windows 安装包

当前 CI 执行类型检查、Web/API/Worker 测试和 OpenAPI lint，但不执行 `package:win`，也不验证 release 目录是否从空目录生成。因此 CI 无法发现本机打包目录混有上一轮文件。

## 为什么新构建完成后没有触发清理

现有流程把“构建成功”定义为编译器或打包器返回 0。这个定义只证明本轮目标文件被生成，未要求：

- 构建开始前目录为空。
- 输出目录只包含本轮清单。
- 旧 exe 不再存在。
- `win-unpacked` 与安装包来自同一提交。
- 打包后开发构建和中间二进制被删除。

因此新构建成功与旧构建残留可以同时成立，流程不会报错。

## 证据

- 根目录 `package.json` 的 `build`、`build:api` 和 `package:win` 没有调用清理脚本。
- `apps/desktop/package.json:10-15` 的 dev/build/package 脚本没有 clean 或 fresh 阶段。
- `apps/desktop/package.json:55` 将 electron-builder 输出固定到根目录 `release`。
- `apps/desktop/package.json:58-59` 从另外两个生成位置读取 API 和 Worker exe，证明产物跨越多个工具目录。
- `.gitignore:11-19` 忽略 `dist`、`out`、`release`、API exe 及 Worker build/dist。
- `.github/workflows/ci.yml` 只执行类型检查、测试和契约 lint，没有执行 Windows 打包或干净输出检查。
- 清理前的目录清单同时发现 Electron out、Worker dist、API exe、win-unpacked、setup、portable、blockmap 与 builder 调试文件。
- 本地提交完成后 `git status` 为干净，但上述被忽略产物仍然存在，直接证明 Git 干净不等于构建目录干净。

## 本轮清理结果

已删除或确认不存在：

- `apps/desktop/out`
- `services/worker/build`
- `services/worker/dist`
- `services/api/kah-api.exe`
- `release/win-unpacked`
- `release` 下旧 setup、portable、blockmap 和 builder 调试文件

已保留：

- `release/data`

清理后 Git 工作树仍保持干净，因为上述内容均为被忽略的本地生成物。

## 整改建议

### 1. 增加受控清理脚本

新增 `scripts/clean-build.ps1`，使用精确白名单删除生成物，并明确禁止删除：

- `release/data`
- 仓库根目录
- 任意未解析或越出仓库的路径

脚本应在同一 PowerShell 会话中解析、校验并使用 `Remove-Item -LiteralPath` 删除，避免跨 shell 和字符串拼接造成误删。

### 2. 将 clean 接入打包生命周期

建议提供：

```text
clean:build
build:fresh
package:win:fresh
```

正式交付只能使用 fresh 命令；普通增量 `build` 可继续用于开发速度。

### 3. 使用临时输出目录并原子发布

打包到一次性 staging 目录，全部校验通过后再将本轮产物移动到 `release/artifacts/<version>-<commit>/`。不要直接在长期复用的 `release` 根目录中边生成边覆盖。

### 4. 写入构建清单

每次打包生成 manifest，至少记录：

- Git commit。
- 应用版本和架构。
- 构建时间。
- API/Worker/renderer 哈希。
- 产物文件名及 SHA-256。

这样可以识别 `win-unpacked`、setup 和 portable 是否来自同一版本。

### 5. 将“数据”和“产物”物理分离

不应长期把便携数据放在同时承担构建输出的 `release` 根目录中。开发验收数据应放在 `.run-data`；正式便携包及其数据应放在独立、不会被 clean 的验收目录。至少要继续坚持 `release/data` 永不进入无条件递归删除范围。

## 验收标准

一次合格的干净打包应满足：

1. 构建前白名单产物不存在，数据目录存在且未变化。
2. 从当前 commit 构建 API、Worker 和 Electron。
3. 输出清单与实际文件完全一致。
4. 安装包、便携包和解包应用版本/哈希可追溯。
5. 产物通过启动、窗口、后端和核心流程验证。
6. 清理中间产物后再次确认 `release/data` 及其哈希未变化。
