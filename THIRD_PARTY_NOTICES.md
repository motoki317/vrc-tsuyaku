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

## Go dependencies

Go module dependencies retain their own licenses and notices, which are not vendored here.
