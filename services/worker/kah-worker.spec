from PyInstaller.utils.hooks import collect_all

datas, binaries, hiddenimports = collect_all("lancedb")

a = Analysis(
    ["src/knowledge_worker/__main__.py"],
    pathex=["src"],
    binaries=binaries,
    datas=datas,
    hiddenimports=hiddenimports + ["pypdf", "docx", "openpyxl", "pptx", "bs4"],
    noarchive=False,
)
pyz = PYZ(a.pure)
exe = EXE(pyz, a.scripts, a.binaries, a.datas, [], name="kah-worker", console=True)
