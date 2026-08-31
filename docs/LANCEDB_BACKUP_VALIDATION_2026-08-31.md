# LanceDB 与备份验证（2026-08-31）

## 范围与结论

- 检索不再因“已同步 LanceDB 表”而误报为原生检索。`HybridIndex.search` 现在对每个库实际执行 LanceDB FTS 和向量查询；任一库不可用时明确回退为 `searchBackend: portable-json`。
- 基准新增 `--require-lancedb`。该开关会在任一查询没有使用原生 LanceDB 时失败，避免以 JSON 全表扫描的延迟冒充 LanceDB 结果。
- 备份归档具备结构、路径和逐文件 SHA-256 校验；恢复只能写入一个不存在的新目录，不能覆盖正在运行的数据根目录。

## 可复现环境

运行时为 Codex 本机 Python 3.12.13，隔离依赖中安装 LanceDB 0.37.1 与 PyArrow 25.0.1。项目声明的目标 Python 3.14 在本机仍不可用，因此这是一份真实 LanceDB 功能与性能样本，不是 Python 3.14 兼容性验收。

```powershell
$python = (Get-Command python).Source
$env:PYTHONPATH = (Resolve-Path '.run-data\lancedb-py312').Path
& $python services\worker\benchmarks\benchmark_retrieval.py --require-lancedb --iterations 10 --warmup 2 --stress-chunks 20000 --stress-iterations 2
```

## 原生查询样本

所有模式均返回 `backend: lancedb+portable` 和 `searchBackend: lancedb`；其中后者是实际查询引擎的证据。

| 数据规模 | 模式 | Recall@10 | p50 | p95 |
| --- | --- | ---: | ---: | ---: |
| 18 chunk / 100 查询 | lexical | 0.70 | 20.34 ms | 30.74 ms |
| 18 chunk / 100 查询 | vector | 1.00 | 16.22 ms | 27.31 ms |
| 18 chunk / 100 查询 | hybrid | 1.00 | 26.01 ms | 31.87 ms |
| 20,000 chunk / 20 查询 | lexical | 0.70 | 25.81 ms | 33.83 ms |
| 20,000 chunk / 20 查询 | vector | 1.00 | 38.28 ms | 47.56 ms |
| 20,000 chunk / 20 查询 | hybrid | 1.00 | 50.72 ms | 68.08 ms |

该数据集的 lexical Recall@10 为 0.70，未达到既定 0.80 目标；向量与混合模式为 1.00。20,000 chunk 也不是 1,000,000 chunk 的发布级压力验收。受本机可用磁盘约 5.83 GiB、无 Python 3.14 运行时限制，未执行会造成资源风险的百万级构建。百万级 p95、目标 Python 3.14 和生产嵌入模型仍是待完成的发布门槛。

## 备份与恢复演练

`go test ./... -count=1` 覆盖并通过以下链路：

- `POST /api/v1/backups` 创建 `.kahbackup`，返回的 archive SHA-256 与重新校验的值一致。
- 归档 manifest 包含格式版本、是否包含索引以及每个数据文件的 SHA-256；篡改后的 payload 被拒绝。
- 恢复先验证归档，再解压到同级临时目录，最后原子改名为一个此前不存在的目标目录。
- 演练恢复了对象文件、索引文件和 SQLite 数据库，并成功重新打开数据库读取原有知识库。

当前 API 没有“覆盖当前数据根目录”的恢复端点；这是有意的安全边界。生产切换应在服务停止后，由桌面端或运维流程选择已验证的新恢复目录并重启服务。
