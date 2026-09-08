"""Run real, network-blocked OCR against generated Chinese/English scanned PDFs."""
import argparse
import json
import importlib.metadata
import platform
import os
import socket
import sys
import time
from pathlib import Path
from unittest.mock import patch

ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/"services/worker/src"))

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--package",required=True)
    parser.add_argument("--output",required=True)
    args=parser.parse_args()
    root=Path(args.output).resolve();root.mkdir(parents=True,exist_ok=True)
    os.environ["KAH_DATA_ROOT"]=str(root/"data")
    from PIL import Image,ImageDraw,ImageFont
    from reportlab.pdfgen import canvas
    from pypdf import PdfReader
    from knowledge_worker import ocr_plugin
    from knowledge_worker.parsers import parse_document
    from knowledge_worker.ocr_resources import health_report
    lines=["知识库离线文字识别测试", "扫描文件转换为可追溯知识", "安装和卸载不会删除原始资料", "Offline OCR verification 2026", "Total 12345.67"]
    image=Image.new("RGB",(1654,2339),"white")
    draw=ImageDraw.Draw(image)
    font=ImageFont.truetype("C:/Windows/Fonts/msyh.ttc",54)
    for i,line in enumerate(lines):draw.text((120,180+i*110),line,font=font,fill="black")
    image.save(root/"scan.png")
    image.save(root/"scan.pdf","PDF",resolution=200)
    c=canvas.Canvas(str(root/"mixed.pdf"),pagesize=(595,842))
    c.drawString(42,810,"KAH HEADER")
    c.drawImage(str(root/"scan.png"),0,0,width=595,height=780)
    c.save()
    assert not PdfReader(root/"scan.pdf").pages[0].extract_text().strip(), "fixture must have no text layer"
    if ocr_plugin.status()["installed"]:ocr_plugin.uninstall()
    installed=ocr_plugin.install(str(Path(args.package).resolve()))
    results={"platform":platform.platform(),"python":platform.python_version(),"dependencies":{name:importlib.metadata.version(name) for name in ("rapidocr","onnxruntime","pypdfium2","pypdf","numpy","pillow","opencv-python")},"plugin":installed,"networkBlocked":True,"expectedLines":lines,"cases":[]}
    # Intercept socket connections before engine initialization to catch hidden downloads.
    with patch.object(socket.socket,"connect",side_effect=RuntimeError("network disabled for OCR acceptance")), patch("socket.create_connection",side_effect=RuntimeError("network disabled")):
        cold=time.perf_counter()
        health=health_report();results["coldInitSeconds"]=round(time.perf_counter()-cold,3);results["health"]=health
        if not health["available"]:raise RuntimeError(health)
        for name in ("scan.pdf","mixed.pdf"):
            start=time.perf_counter()
            result=parse_document(root/name,name,"application/pdf",name)
            text="\n".join(chunk["text"] for chunk in result["chunks"])
            normalized="".join(text.split())
            missing=[line for line in lines if "".join(line.split()) not in normalized]
            results["cases"].append({"file":name,"seconds":round(time.perf_counter()-start,3),"text":text,"extraction":result["extraction"],"missingLines":missing})
            (root/"results.json").write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding="utf-8")
            assert result["extraction"]["status"]=="complete" and not missing, results["cases"][-1]
        ocr_plugin.set_enabled(False)
        assert not health_report()["available"]
        ocr_plugin.set_enabled(True)
        assert health_report()["available"]
        ocr_plugin.uninstall()
        assert not health_report()["available"]
        assert (root/"scan.pdf").exists()
        ocr_plugin.install(str(Path(args.package).resolve()))
        assert health_report()["available"]
    results["lifecycle"]="disable-enable-uninstall-reinstall passed"
    (root/"results.json").write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding="utf-8")
    print(json.dumps(results,ensure_ascii=False,indent=2))

if __name__=="__main__":main()
