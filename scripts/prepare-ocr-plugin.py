"""Download the official GitHub resource bundle and build a local OCR plugin ZIP."""
import argparse
import hashlib
import json
import urllib.request
import zipfile
from pathlib import Path
URL="https://github.com/RapidAI/RapidOCR/releases/download/v1.1.0/required_for_whl_v3.0.0.zip"
SHA256="e050aa8cf29cdbea04550204336859069dcbf7bb86761112d2008ae93db13296"
MODELS={"det.onnx":"ch_PP-OCRv4_det_infer.onnx","rec.onnx":"ch_PP-OCRv4_rec_infer.onnx","cls.onnx":"ch_ppocr_mobile_v2.0_cls_infer.onnx","dict.txt":"ppocr_keys_v1.txt"}

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--output",required=True)
    args=parser.parse_args()
    root=Path(args.output).resolve();root.mkdir(parents=True,exist_ok=True)
    archive=root/"rapidocr-resources.zip"
    if not archive.exists():
        temporary=archive.with_suffix(".download")
        with urllib.request.urlopen(URL,timeout=60) as response,temporary.open("wb") as target:
            total=0
            while block:=response.read(1024*1024):
                total+=len(block)
                if total>100*1024*1024:raise ValueError("Resource archive exceeds size limit")
                target.write(block)
        temporary.replace(archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA256:
        raise ValueError("GitHub archive changed; review the new artifact before updating its pinned hash")
    files={}
    with zipfile.ZipFile(archive) as z:
        for name,source in MODELS.items():files[name]=z.read("required_for_whl_v3.0.0/resources/models/"+source)
    manifest={"schema":"kah-ocr-resource/v1","version":"rapidocr-github-ppocrv4-3.0.0","source":URL,"ocrVersion":"PP-OCRv4",
              "files":[{"name":name,"sha256":hashlib.sha256(data).hexdigest()} for name,data in files.items()]}
    # Keep the upstream license alongside the resource bundle.
    with urllib.request.urlopen("https://raw.githubusercontent.com/RapidAI/RapidOCR/main/LICENSE",timeout=60) as response:
        files["LICENSE"]=response.read(1024*1024)
    files["NOTICE"]=("Source: "+URL+"\nModels: PaddleOCR PP-OCRv4, distributed by RapidAI/RapidOCR.\nArchive SHA256: "+SHA256+"\n").encode()
    target=root/"kah-ocr-ppocrv4.zip"
    with zipfile.ZipFile(target,"w",zipfile.ZIP_DEFLATED) as z:
        z.writestr("manifest.json",json.dumps(manifest,indent=2))
        for name,data in files.items():z.writestr(name,data)
    print(target)

if __name__=="__main__":main()
