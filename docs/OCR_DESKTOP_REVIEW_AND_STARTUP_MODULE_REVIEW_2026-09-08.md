# OCR 桌面审阅与启动诊断模块验收（2026-09-08）

## 目标

修复审核差异视图中的换行显示，并在桌面会话连接到其他 KAH 后端时给出可操作的启动错误。

## 实现

- `RevisionDiff` 使用 JSX 字符串换行而不是显示为字面量 `\n`，稳定版和候选修订逐行显示。
- 后端健康检查成功但管理请求返回 401 时立即结束等待；错误包含端口、Windows 监听 PID（若可读取）、期望数据根和关闭其他 KAH 会话后的重试动作。
- 其他 HTTP 状态和真正的启动超时保持原有重试路径。

## 代码质量门禁

```text
apps/desktop/node_modules/.bin/tsc.cmd --noEmit -p tsconfig.json
PASS

apps/desktop/node_modules/.bin/vitest.cmd run src/renderer/src/OcrPluginSettings.test.tsx --configLoader runner
1 passed

git diff --check
PASS
```

## 连通性门禁

本地 Electron 开发构建实际完成了主进程与预加载编译，渲染开发服务器也成功启动。OCR 设置的现有交互回归通过，覆盖卸载后安装入口恢复。

本轮隔离窗口复跑没有产生隔离数据库、可用窗口句柄或后端监听，因此不能把它作为可见窗口验收；启动的命令与 Electron 进程已仅按 PID 清理，端口复核为干净。此前复杂版式验收的真实可见窗口证据和原生文件选择器证据保留在 [`OCR_COMPLEX_LAYOUT_AND_DESKTOP_ACCEPTANCE_2026-09-08.md`](OCR_COMPLEX_LAYOUT_AND_DESKTOP_ACCEPTANCE_2026-09-08.md)。

## 结论

P2 代码质量和模块连接门禁通过。新的 401 提示需要在拥有可见 Windows 桌面会话的环境中再做一次人工可读性确认；当前环境的无窗口结果不被计为该人工验收。
