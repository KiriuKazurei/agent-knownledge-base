# 关系图谱阶段 D 验收记录

日期：2026-09-06\
范围：Knowledge Agent Hub v1 关系图谱（阶段 A-D）\
隔离数据：`.run-data/graph-acceptance-user/data`。该数据根只用于验收，不代表默认用户数据。

## 自动化门禁

- API：`go test ./... -count=1` 通过；`go vet ./...` 通过。
- 桌面端：`tsc --noEmit` 通过；Vitest 5 个测试文件、32 项通过。
- 打包：`electron-vite build` 通过，2001 个模块完成构建。
- 合同：`spectral lint contracts/openapi.yaml` 无 error。
- 差异：`git diff --check` 退出码为 0；输出仅为仓库既有的 LF/CRLF 转换提醒。

## 隔离窗口与交互证据

通过 Electron 可见窗口、CDP DOM 和截图复核：

1. 图谱一级工作区加载隔离库，显示 3 个稳定知识节点、3 条关系，其中包含 `derived-from` 派生关系；节点 `aria-label` 暴露小数百分比坐标。
2. 节点详情显示固定 URI（例如 `kah://knowledge/<id>?revision=1`）、修订号和逻辑位置 `(19.374%, 19.779%)`。
3. 放大与“重置视口”前后，节点百分比坐标完全不变；画布尺寸变化只影响屏幕变换。
4. 键盘 `ArrowRight` 将坐标从 `19.374%` 更新为 `19.624%`，保存后无错误提示。
5. 关系类型筛选 `derived-from` 将可见边收敛为 1 条，节点仍保持可定位。
6. 原验收夹具的旧布局存在近邻节点；显式执行“重新布局”后，碰撞规避生效，节点标签分离。视觉截图：`.run-data/graph-acceptance-user/kah-graph-stage-d-final-relayout-1440.png`。

## 审查结论

阶段 D 的实现门禁通过，没有发现需要阻断发布的代码问题。坐标、内容版本、视口状态和布局历史已分离；跨库隔离、稳定知识过滤、来源/派生证据、原子位置保存、备份恢复与版本刷新均有测试覆盖。

本记录包含 Agent 可见窗口的实证；仍需用户在自己的运行环境中进行最终人工确认。确认后可进入常规 GUI 优化迭代（字体层级、密集图谱的可读性和更大数据集的交互微调）。
