# Offline OCR model resource layout

The worker does not download OCR models at runtime. Install the optional worker
extra and import a verified RapidOCR ONNX resource package into a local model
directory. Set `KAH_OCR_MODEL_DIR` to that directory before starting the API.

The default layout is:

```text
<model-dir>/
  manifest.json
  det.onnx
  rec.onnx
  cls.onnx
  dict.txt
```

`manifest.json` must use schema `kah-ocr-resource/v1`, declare every model file
with its lowercase SHA-256 in a `files` array, and may map official filenames
through a `models` object. The Worker verifies the manifest and hashes before
loading RapidOCR; an invalid package is reported by the `ocr_health` RPC and is
never used for a document.

Official RapidOCR model filenames may be retained instead. In that case set
`KAH_OCR_DET_MODEL`, `KAH_OCR_REC_MODEL`, `KAH_OCR_CLS_MODEL`, and
`KAH_OCR_REC_DICT` to file paths (absolute paths or paths relative to
`KAH_OCR_MODEL_DIR`). The application should only enable a directory after
checking the resource manifest and SHA-256 values supplied with the offline
package.

The directory is deliberately not populated in source control because model
files are large and have separate redistribution terms. A release package must
ship the exact model files, their hashes, the RapidOCR/ONNX Runtime versions,
and third-party license notices together.
