# Haas ONNX export

This offline tool converts [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0), a Japanese speech recognition model, into the ONNX files that VRC Tsuyaku's recognizer loads. The app never runs this tool.

## Export

Use Linux with [uv](https://docs.astral.sh/uv/).

```sh
export HAAS_NEMO=/path/to/Haas-ja-v1.0.nemo
export HAAS_CHECKPOINT_REVISION=the-hugging-face-commit-of-that-checkpoint
export HAAS_BUNDLE=/path/to/a-new-empty-folder
./export.sh
```

## Runtime contract

The code states what each file of the bundle holds, the tensor shapes and the decoding rules:

- [internal/asr/haas.go](../../internal/asr/haas.go) and [internal/asr/vocab.go](../../internal/asr/vocab.go): the Go recognizer
- [haas_full_onnx.py](haas_full_onnx.py): its Python reference. `TestHaasCPUParity` in `internal/asr` requires the Go recognizer to give the reference's text on every clip.

## Measured parity

The app downloads only the int8 encoder. With the fp32 encoder, the Python reference gives the same transcripts as the original model in NeMo 2.7.3, on 600 Japanese clips from FLEURS, ReazonSpeech and SpeechBSD, on Linux and on native Windows. With the int8 encoder, 196 of the 600 transcripts differ from those of fp32, and the error rates stay close:

| Dataset | fp32 CER | int8 CER |
| --- | --- | --- |
| FLEURS | 3.77% | 4.02% |
| ReazonSpeech | 4.64% | 4.36% |
| SpeechBSD | 6.65% | 6.59% |

CER is the strict character error rate of jacer 1.2.0, micro-averaged over each dataset. The measurements ran ONNX Runtime 1.31.1 with four threads on a Linux CPU.

## Checks

```sh
uv run --locked python -m unittest discover -v
```
