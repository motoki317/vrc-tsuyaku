# Third-party notices

These projects supply code, algorithms, file formats, ideas, models, ONNX graphs or programs that vrc-tsuyaku uses.
Neither the repository nor the executable includes the models, ONNX graphs or programs listed here.
The app downloads the Haas and Parakeet conversions, the onnx-asr feature extractor, Silero VAD, PLaMo, ONNX Runtime and llama.cpp.
The Go module entries say which modules ship in each OS's executable.

## VRCT

Source: [misyaguziya/VRCT](https://github.com/misyaguziya/VRCT/tree/9427520c0ae39406a17ba0f280b1ad2735395d51), commit `9427520c0ae39406a17ba0f280b1ad2735395d51`. License: MIT.

vrc-tsuyaku takes its idea from VRCT. Both apps translate the user's speech into the VRChat chatbox and translate the voices that the user hears in VRChat. vrc-tsuyaku copies no VRCT code.

`internal/chatbox/pager.go` reimplements VRCT's original-before-translation join with a newline separator.

## traPtitech/NeoShowcase

Source: [traPtitech/NeoShowcase](https://github.com/traPtitech/NeoShowcase/tree/06fa2e6c52e74f760f09a84ac36cab12b3f4e947), commit `06fa2e6c52e74f760f09a84ac36cab12b3f4e947`. License: MIT.

`.golangci.yml` copies the upstream file verbatim, apart from the attribution header. The upstream notice follows:

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
For Parakeet v2, the app downloads the onnx-asr 0.12.0 wheel from [PyPI](https://pypi.org/project/onnx-asr/0.12.0/) and extracts `onnx_asr/preprocessors/data/nemo128.onnx`.
For v3, it uses the feature extractor published with the pinned ONNX conversion.
`internal/asr/parakeet_test.go` reimplements onnx-asr's text rule from `asr.py` only for parity comparisons.
It joins vocabulary pieces and replaces `▁` with spaces.
It removes the first leading whitespace character and whitespace not followed by a Unicode word character.
No onnx-asr source code is copied.

## NVIDIA Parakeet

Model: [nvidia/parakeet-tdt-0.6b-v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2), by NVIDIA. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).
ONNX conversion: [istupakov/parakeet-tdt-0.6b-v2-onnx](https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/tree/0bbb45a3365852604aef28b538a8f066f4ccaa85), revision `0bbb45a3365852604aef28b538a8f066f4ccaa85`. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).

Model: [nvidia/parakeet-tdt-0.6b-v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3), by NVIDIA. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).
ONNX conversion: [istupakov/parakeet-tdt-0.6b-v3-onnx](https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/tree/8f23f0c03c8761650bdb5b40aaf3e40d2c15f1ce), revision `8f23f0c03c8761650bdb5b40aaf3e40d2c15f1ce`. License: [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/).

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

## llama.cpp

Source: [ggml-org/llama.cpp](https://github.com/ggml-org/llama.cpp/tree/b12201), build b12201. License: MIT.

`internal/translate` starts `llama-server` and calls its HTTP API.
The app downloads the Windows and Linux x64 CPU builds from the [b12201 release](https://github.com/ggml-org/llama.cpp/releases/tag/b12201).
The Windows build includes the OpenMP runtime `libomp.dll` under [Apache-2.0 WITH LLVM-exception](https://github.com/llvm/llvm-project/blob/baf637dcde8ccc24fd6ffbc82d8321ba18ec857f/openmp/LICENSE.TXT).
That revision's `openmp/LICENSE.TXT` matches the bundled `LICENSE-LLVM-OpenMP` byte for byte.

## jfreymuth/pulse

Source: [jfreymuth/pulse](https://github.com/jfreymuth/pulse/tree/v0.1.1), version v0.1.1. License: MIT.

`internal/audio/microphone_linux.go` calls the module's `proto` package. Its `connectPulse` function reimplements Unix socket setup from `proto/connect.go`; upstream also accepts TCP servers.
`microphone_linux.go` copies no upstream source code. This Go module is compiled into the Linux executable.

## go-wca and go-ole

Sources: [moutend/go-wca](https://github.com/moutend/go-wca/tree/v0.3.0), version v0.3.0, and [go-ole/go-ole](https://github.com/go-ole/go-ole/tree/v1.2.6), version v1.2.6. Both are MIT-licensed.

`internal/audio/microphone_windows.go` calls WASAPI and COM through these Go modules, which are compiled into the Windows executable.

## golang.org/x/sys

Source: [golang.org/x/sys](https://go.googlesource.com/sys), version v0.49.0. License: BSD-3-Clause.

`internal/translate` calls the Windows process and job-object APIs, and `internal/audio/application_windows.go` calls process and COM activation APIs through this Go module, which is compiled into the Windows executable.

## PLaMo 3 Translate

Model: [pfnet/plamo-3-translate-2b-GGUF](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF), Q4_K_M. License: [PLaMo Community License](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/blob/228a27cc0bb4f111219963848d50a3127c16ca73/LICENSE/en).

The app downloads the Q4_K_M GGUF at revision `228a27cc0bb4f111219963848d50a3127c16ca73` from this model repository.
`internal/catalog/licenses/plamo-community.txt` contains the complete English license from that revision for display before downloading.
Article 2 states:

> The USER must be 18 years of age or older, or have reached the age of legal capacity in the USER’s country of residence, unless the USER's legal guardian or legal representative has consented to this AGREEMENT on the USER’s behalf.

Article 4(1) states: "When providing the MODEL MATERIALS to third parties, the USER must meet the following requirements:" Its clause (ii) states:

> Explicitly indicate "Built with PLaMo" in related websites, user interfaces, blog posts, product information pages, or product documentation.

The app shows "Built with PLaMo" in its UI and documentation.

## go.yaml.in/yaml/v3

Source: [yaml/go-yaml](https://github.com/yaml/go-yaml/tree/v3.0.6), version v3.0.6. Licenses: MIT and Apache-2.0.

`internal/config` reads and writes YAML through this Go module, which is compiled into the executable.
The module's `NOTICE` follows verbatim:

```text
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

## whispering-ui

Source: [Sharrnah/whispering-ui](https://github.com/Sharrnah/whispering-ui/tree/00b7533b148d0feec959e8ddf8c7b72b6ca9c329), commit `00b7533b148d0feec959e8ddf8c7b72b6ca9c329`. License: [MIT](https://github.com/Sharrnah/whispering-ui/blob/00b7533b148d0feec959e8ddf8c7b72b6ca9c329/LICENSE).

`internal/audio/application_windows.go` adapts the process-loopback activation code in `Utilities/ProcessCapture_windows.go`: the activation parameters and the COM completion handler with a shared callback table.

```text
MIT License

Copyright (c) 2022 Sharrnah

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
