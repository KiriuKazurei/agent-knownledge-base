from PyInstaller.utils.hooks import collect_all

datas, binaries, hiddenimports = collect_all("lancedb")


def collect_optional(package):
    try:
        return collect_all(package)
    except Exception:
        # The base worker remains packageable without the optional OCR extra;
        # an OCR-enabled release build installs the extra before invoking this
        # spec and therefore collects its native DLLs and model adapters.
        return [], [], []


for optional_package in ("pypdfium2", "rapidocr", "onnxruntime"):
    optional_datas, optional_binaries, optional_hiddenimports = collect_optional(optional_package)
    datas += optional_datas
    binaries += optional_binaries
    hiddenimports += optional_hiddenimports

a = Analysis(
    ["src/knowledge_worker/__main__.py"],
    pathex=["src"],
    binaries=binaries,
    datas=datas,
    hiddenimports=hiddenimports + ["pypdf", "docx", "openpyxl", "pptx", "bs4", "pypdfium2", "rapidocr", "onnxruntime"],
    noarchive=False,
)
pyz = PYZ(a.pure)
exe = EXE(pyz, a.scripts, a.binaries, a.datas, [], name="kah-worker", console=True)
