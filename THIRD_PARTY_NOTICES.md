# Third-party notices

vrc-tsuyaku uses the [MIT License](LICENSE). The following projects informed its implementation. Each section names the vrc-tsuyaku files that use the project and says whether code was copied or only reimplemented.

## traPtitech/NeoShowcase

Source: [traPtitech/NeoShowcase](https://github.com/traPtitech/NeoShowcase/tree/06fa2e6c52e74f760f09a84ac36cab12b3f4e947), commit `06fa2e6c52e74f760f09a84ac36cab12b3f4e947`. License: MIT.

`.golangci.yml` copies the upstream file verbatim, apart from the attribution header.

The full upstream notice follows, with its copyright holder's name unchanged:

```text
MIT License

Copyright (c) 2020 東京工業大学デジタル創作同好会traP

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Haas-ASR and Haas-ja-v1.0

Source: [third-intelligence/Haas-ASR](https://github.com/third-intelligence/Haas-ASR/tree/acc614f68769dcbfae9dba96b48439c4a66295af), commit `acc614f68769dcbfae9dba96b48439c4a66295af`. License: Apache-2.0.
Model: [third-intelligence/Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0). License: Apache-2.0.

`tools/haas-export/export_onnx.py` calls Haas-ASR's model loader and extracts the checkpoint's tokenizer.
`tools/haas-export/export_onnx_decoder.py` reimplements the prediction network of `src/haas_asr/prednet.py`: its layer order and the convolution and recurrent states that it passes from one step to the next.
`tools/haas-export/haas_full_onnx.py` passes the same states to the exported network.
`internal/asr/haas.go` uses the same prediction states and exported networks.
These files copy no Haas-ASR source code. The generated ONNX files contain converted model weights.
The app downloads the Haas bundle from [motoki317/Haas-ja-v1.0-onnx](https://huggingface.co/motoki317/Haas-ja-v1.0-onnx).

Neither the pinned Haas-ASR checkout nor the Haas-ja-v1.0 model repository contains a NOTICE file.
[The Apache-2.0 license text](tools/haas-export/licenses/Apache-2.0.txt) is Haas-ASR's LICENSE file. The export steps in `tools/haas-export/README.md` copy it into the Haas ONNX bundle as `LICENSE`.

## NVIDIA NeMo

Source: [NVIDIA/NeMo](https://github.com/NVIDIA/NeMo), nemo-toolkit 2.7.3. License: Apache-2.0.

`tools/haas-export/export_onnx.py` calls NeMo's encoder exporter.
`tools/haas-export/export_onnx_decoder.py` reimplements `FilterbankFeatures` from `nemo/collections/asr/parts/preprocessing/features.py`.
`tools/haas-export/haas_full_onnx.py` reimplements greedy TDT decoding from `nemo/collections/asr/parts/submodules/rnnt_greedy_decoding.py`.
`internal/asr/tdt.go` implements the same decoding loop in Go.
`tools/haas-export/haas_full_onnx.py` and `internal/asr/haas.go` reimplement `TranscriptionTensorDataset._pad_audio` from `nemo/collections/asr/parts/mixins/transcription.py`.
They zero-pad clips under one second to one second, splitting padding across both sides with any extra sample on the right.
No NeMo source code is copied.

## flash-linear-attention

Source: [fla-org/flash-linear-attention](https://github.com/fla-org/flash-linear-attention), version 0.5.2. License: MIT.

`tools/haas-export/export_onnx_decoder.py` reimplements the single-step KDA recurrence from `fla/ops/kda/fused_recurrent.py`, `ShortConvolution.step` from `fla/modules/convolution.py` and `FusedRMSNormGated` from `fla/modules/layernorm_gated.py`.
It copies no upstream source code or Triton kernels.

## ONNX Runtime

Source: [microsoft/onnxruntime](https://github.com/microsoft/onnxruntime), version 1.31.1. License: MIT.

The app downloads the CPU build of ONNX Runtime.

`tools/haas-export/quantize_onnx.py` calls the int8 quantizer, and `tools/haas-export/haas_full_onnx.py` calls the inference API. Neither script copies ONNX Runtime source code.
`internal/asr` calls the runtime through the Go binding [getcharzp/onnxruntime_purego](https://github.com/getcharzp/onnxruntime_purego), version v1.28.0 (MIT).
The binding depends on [ebitengine/purego](https://github.com/ebitengine/purego), version v0.9.0 (Apache-2.0). Both Go modules are compiled into the executable.

## onnx-asr

Source: [istupakov/onnx-asr](https://github.com/istupakov/onnx-asr), version 0.12.0. License: MIT.

`tools/haas-export/export_vocab.py` writes onnx-asr's `<piece> <id>` vocabulary format, and `internal/asr/vocab.go` reads it.
`internal/catalog/pins.go` uses onnx-asr's Parakeet file names.
`internal/asr/parakeet.go` reimplements the combined prediction/joint step from `models/nemo.py`.
The app runs the `onnx_asr/preprocessors/data/nemo128.onnx` feature extractor from the onnx-asr 0.12.0 wheel.
The app downloads the wheel from [PyPI](https://pypi.org/project/onnx-asr/0.12.0/) and extracts this feature extractor.
`internal/asr/parakeet_test.go` reimplements onnx-asr's text rule from `asr.py` only for parity comparisons.
It joins vocabulary pieces and replaces `▁` with spaces.
It removes the first leading whitespace character and whitespace not followed by a Unicode word character.
No onnx-asr source code is copied.

## NVIDIA Parakeet

Model: [nvidia/parakeet-tdt-0.6b-v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2), by NVIDIA. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).
ONNX conversion: [istupakov/parakeet-tdt-0.6b-v2-onnx](https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/tree/0bbb45a3365852604aef28b538a8f066f4ccaa85), revision `0bbb45a3365852604aef28b538a8f066f4ccaa85`. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).

`internal/asr/parakeet.go` runs the converted int8 encoder and decoder/joint weights.
The app downloads these weights from the ONNX conversion repository.

## SentencePiece

Source: [google/sentencepiece](https://github.com/google/sentencepiece), version 0.2.3. License: Apache-2.0.

`internal/asr/vocab.go` reimplements SentencePiece decoding.
No SentencePiece source code is copied.

## Silero VAD

Source and model: [snakers4/silero-vad](https://github.com/snakers4/silero-vad/tree/5cd7945676eb32225748052e2e6a0580e4686a08), v6.2.3 at commit `5cd7945676eb32225748052e2e6a0580e4686a08`. License: MIT.

`internal/asr/silero.go` reimplements the state and audio-history protocol of `OnnxWrapper` in `src/silero_vad/utils_vad.py`.
`internal/vad/segmenter.go` uses `VADIterator`'s probability hysteresis and silence confirmation, with its default thresholds of 0.5 and 0.35.
The app sets 300 ms pre-roll, 500 ms end silence, a 250 ms minimum and a 15 s cut.
Neither file copies upstream source code.
`internal/asr/silero.go` runs `silero_vad.onnx`, which the app downloads from silero-vad at the commit linked above.

## Go dependencies

Go module dependencies retain their own licenses and notices, which are not vendored here.
