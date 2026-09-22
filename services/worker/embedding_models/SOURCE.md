# Local embedding model sources

The first catalog entry is `BAAI/bge-small-zh-v1.5` at upstream commit
`7999e1d3359715c523056ef9478215996d62a620`.

The catalog records the model card dimension (512), 512-token limit, CLS
pooling and the Chinese query prefix. The fixed upstream
`1_Pooling/config.json` at that commit has `pooling_mode_cls_token: true`,
`pooling_mode_mean_tokens: false`, and SHA-256
`AAA8861589F80C961A03CC86C7EEAEF7605C1676B9AB55329D33A304738769C6`.
That single pooling file is evidence for the configuration only; it is not a
complete installable model package. A reproducible ONNX conversion and
tokenizer resource manifest has not been published in this repository yet, so
online installation is disabled. Use a separately signed offline package with
`kah-embedding-model/v1` and complete SHA-256 entries when installing a model.

The worker never downloads this source during startup and never executes files
from a model package.
