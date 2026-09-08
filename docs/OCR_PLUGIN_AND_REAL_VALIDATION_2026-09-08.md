# OCR 可卸载插件与真实识别验证（2026-09-08）

## 插件操作

桌面入口：设置 → 可选插件 → 离线 PDF OCR。

- 安装 OCR 离线包：选择 `kah-ocr-ppocrv4.zip`，通过 manifest/哈希校验后复制到当前数据根 `plugins/ocr/models`。
- 停用/启用：状态写入 `plugins/ocr/state.json`，重启后保留。停用后释放引擎引用，后续 OCR 请求明确失败；普通文字资料仍可使用。
- 验证 OCR 引擎：实际初始化运行库和本地模型，显示失败原因。
- 卸载 OCR 插件：先保存禁用状态，再释放引擎引用并删除受管理模型目录。已导入的文件、提取文本和 KAH 知识保留；卸载后不会自动回退到环境变量模型目录。可重新安装。

这是可移除的 OCR **资源/功能插件**：ONNX Runtime、RapidOCR、PDFium 由 Worker 可选运行依赖提供，卸载模型不会卸载共享 Python 运行环境或应用二进制。此边界在设置界面明确显示。当前不是独立进程发行包。

## 来源与复现

资源下载自 [RapidAI 官方 GitHub Release](https://github.com/RapidAI/RapidOCR/releases/download/v1.1.0/required_for_whl_v3.0.0.zip)。包名中的 v3.0.0 是资源发布名称，实际选用其中配套的 PP-OCRv4 模型，不声称为 PP-OCRv5。

源 ZIP SHA-256：`e050aa8cf29cdbea04550204336859069dcbf7bb86761112d2008ae93db13296`。该值是本次下载固定的校验值，不是上游签名证明。

| 安装文件 | 上游模型 | SHA-256 |
| --- | --- | --- |
| det.onnx | ch_PP-OCRv4_det_infer.onnx | d2a7720d45a54257208b1e13e36a8479894cb74155a5efe29462512d42f49da9 |
| rec.onnx | ch_PP-OCRv4_rec_infer.onnx | 48fc40f24f6d2a207a2b1091d3437eb3cc3eb6b676dc3ef9c37384005483683b |
| cls.onnx | ch_ppocr_mobile_v2.0_cls_infer.onnx | e47acedf663230f8863ff1ab0e64dd2d82b838fceb5957146dab185a89d6215c |
| dict.txt | ppocr_keys_v1.txt | 28b2362ad4ab2dc38769aa72feb535e3a9ddb3fd2a7585a05920e6393b1dc7f7 |

构建安装包：

```powershell
python scripts/prepare-ocr-plugin.py --output .run-data/ocr-downloads
```

开发环境使用 Python 3.14 隔离环境安装 Worker OCR extra；桌面开发启动时将 `KAH_PYTHON` 指向该环境的 `Scripts/python.exe`。资源插件安装不会自动联网执行 pip。RapidOCR 已锁定为实际核对配置的 3.9.2，避免模型默认版本漂移。

真实验证：

```powershell
.run-data/ocr-plugin-env/Scripts/python.exe scripts/verify-real-ocr.py --package .run-data/ocr-downloads/kah-ocr-ppocrv4.zip --output .run-data/ocr-real-validation
```

测试资料由脚本使用本机微软雅黑生成，不下载第三方图片。包含中英扫描页和文字页眉加扫描正文的混合页；先确认扫描 PDF 没有文字层，再真实渲染和推理。执行中拦截 Python socket 连接，检查模型初始化/识别是否依赖联网；这不等于操作系统防火墙断网或干净机器安装包验收。

## 双门禁

质量门禁：Worker 单元测试覆盖校验失败、ZIP 路径穿越、安装/停用/卸载/重装和资料保留；Go 全量测试与 vet；桌面类型检查、34 项 Vitest；OpenAPI Spectral 校验。

连通性门禁：新增真实 API→Worker RPC 生命周期测试，覆盖管理员鉴权、安装、状态读取、停用、启用、卸载。桌面测试确认卸载触发 DELETE，刷新后恢复安装入口。桌面主进程新增 OCR ZIP 文件过滤器，避免入口只能选知识文件。

## 实际运行结果

**实际识别与无 Agent 导入链通过。** 本次为开发机 CPU、2 个合成页面的实证，不是完整发行验收。

| 场景 | 结果 | 耗时 |
| --- | --- | --- |
| 首次引擎初始化 | resourcesVerified / engineReady 均为 true | 2.480 秒 |
| 无文字层的中文/英文扫描 PDF | 五行预期文字全部命中（忽略空白），无缺页，complete | 3.807 秒 |
| 同页文字页眉＋扫描正文 | 页眉及五行正文识别，未因已有文字层跳过正文，complete | 2.717 秒 |
| 真引擎停用→启用→卸载→重装 | 状态和可用性符合预期，原始 PDF 保留 | 通过 |
| API 安装插件→扫描 PDF 导入→reference KAH→卸载 | 无 Agent/Provider 生成一条 reference 草稿；卸载后草稿仍在 | 集成测试 6.77 秒 |

平台 Windows 11 build 26200、Python 3.14.7；RapidOCR 3.9.2、ONNX Runtime 1.29.0、PDFium Python 4.30.0、pypdf 6.17.0、NumPy 2.5.3、OpenCV 5.0.0.93。完整依赖版本固定在 `services/worker/requirements-ocr-validation.txt`。

真实运行发现并修复：RapidOCR 的模型版本/类型参数必须传 `OCRVersion`/`ModelType` 枚举；移除了会掩盖原始错误的旧构造器回退。包内模型版本显式设为 PP-OCRv4，关闭不需要的字符框输出，CPU 推理线程固定为 2/1。

[原始验证 JSON](evidence/ocr-real-validation-2026-09-08.json) 保留识别文本、预期文本、页状态、版本及耗时。安装后模型资源约 15.48 MiB，ZIP 约 14.20 MiB；不含共享运行依赖体积。

实际环境运行 Worker 测试：33 项、29 通过、4 项跳过（Office/LanceDB 依赖）；真实 PDF fixture 测试已不再跳过。另有完整 Go 测试/vet、桌面 34 项测试和构建通过，Spectral 无 error。

真实 API 验证可复现命令（路径按工作区调整）：

```powershell
$env:KAH_TEST_PYTHON = 'E:\AI\agent-knownledge-base\.run-data\ocr-plugin-env\Scripts\python.exe'
$env:KAH_TEST_REAL_OCR_PACKAGE = 'E:\AI\agent-knownledge-base\.run-data\ocr-downloads\kah-ocr-ppocrv4.zip'
$env:KAH_TEST_REAL_OCR_PDF = 'E:\AI\agent-knownledge-base\.run-data\ocr-real-validation\scan.pdf'
go -C services/api test ./internal/httpapi -run TestRealOCRPluginImportCreatesKAHReference -count=1 -v
```

## 尚未验收

真实可见窗口人工验收、复杂版式、手写、30 页 CER 基准和干净 Windows 离线发行包仍需独立验收。
