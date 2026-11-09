# Haas ONNX export

This offline tool converts [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) into ONNX networks for the Go recognizer, `internal/asr`.

## Environment

Use Linux and Python 3.12 for export. The model loads on the CPU.
The export dependencies include CUDA and Triton packages because Haas-ASR imports flash-linear-attention.

`pyproject.toml` and `uv.lock` hold the dependency pins.
The Python reference (`haas_full_onnx.py`) and quantizer need only the base dependency group.

## Export

Run these commands from `tools/haas-export/` in the vrc-tsuyaku checkout.
Set `HAAS_NEMO` to your local `.nemo` checkpoint from the model repository.
Set `HAAS_CHECKPOINT_REVISION` to the full Hugging Face commit used to download that checkpoint.
Set `HAAS_BUNDLE` to a new empty output directory outside the checkout.
Export and quantization load large models and take several minutes.

```sh
set -eu
export HAAS_NEMO=/path/to/Haas-ja-v1.0.nemo
export HAAS_BUNDLE=/path/to/haas-onnx
export HAAS_CHECKPOINT_REVISION=replace-with-the-model-revision
uv run --locked --group export python export_onnx.py "$HAAS_NEMO" "$HAAS_BUNDLE"
uv run --locked --group export python export_onnx_decoder.py "$HAAS_NEMO" "$HAAS_BUNDLE"
uv run --locked python quantize_onnx.py "$HAAS_BUNDLE"
uv run --locked python export_vocab.py "$HAAS_BUNDLE/tokenizer.model" "$HAAS_BUNDLE/vocab.txt"
cp licenses/Apache-2.0.txt "$HAAS_BUNDLE/LICENSE"
HAAS_CHECKPOINT_SHA256=$(sha256sum < "$HAAS_NEMO")
HAAS_CHECKPOINT_SHA256=${HAAS_CHECKPOINT_SHA256%% *}
HAAS_EXPORT_COMMIT=$(git rev-parse HEAD)
cat > "$HAAS_BUNDLE/README.md" <<EOF
# Haas-ja-v1.0 ONNX bundle

Converted weights from [third-intelligence/Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0/tree/$HAAS_CHECKPOINT_REVISION).

- Checkpoint revision: $HAAS_CHECKPOINT_REVISION
- Checkpoint SHA-256: $HAAS_CHECKPOINT_SHA256
- Export commit: $HAAS_EXPORT_COMMIT
- Encoder files included: encoder.onnx and encoder.onnx.data (fp32), encoder.int8.onnx (int8).

License: [Apache-2.0](LICENSE).

[Export tool and runtime contract](https://github.com/motoki317/vrc-tsuyaku/tree/$HAAS_EXPORT_COMMIT/tools/haas-export)
EOF
```

If a step fails, repeat all steps with a new empty `HAAS_BUNDLE` directory because the scripts refuse to overwrite files.

## Files and runtime contract

| File | Purpose |
| --- | --- |
| `features.onnx` | 16 kHz mono float32 audio to 128-bin log-mel features |
| `encoder.onnx` and `encoder.onnx.data` | fp32 FastConformer encoder |
| `encoder.int8.onnx` | Alternative CPU encoder with int8 weights |
| `decoder_step.onnx` | One prediction step with explicit convolution and recurrent states |
| `joint.onnx` | One encoder frame and prediction output to token and duration logits |
| `tokenizer.model` | SentencePiece tokenizer |
| `vocab.txt` | Token pieces for the Go recognizer, with blank as `<blk> 3072` |
| `LICENSE` | Apache-2.0 license for the converted model |
| `README.md` | Model provenance, included encoders, license and link to this tool |

The Go recognizer needs `features.onnx`, `decoder_step.onnx`, `joint.onnx`, `vocab.txt` and one encoder variant.
Keep `encoder.onnx` and `encoder.onnx.data` together for fp32. A bundle that uses only int8 can omit `encoder.onnx` and `encoder.onnx.data`.
Only the Python reference needs `tokenizer.model`.

The Go recognizer must pad audio shorter than 16,000 samples to 16,000 samples.
It must put `floor((16000 - samples)/2)` zeros on the left and the rest on the right.
`N` is the sample count after this padding.
`features.onnx` accepts float32 `audio[1,N]` and returns `mel[1,128,N//160+1]`, with frames at or beyond `N//160` zeroed.
The Go recognizer must pad the mel frames with zeros to a multiple of 16.
The parity measurements in [Python reference and measured parity](#python-reference-and-measured-parity) use 16, so the Go recognizer must use 16 to reproduce those transcripts.

The encoder accepts float32 `audio_signal[1,128,F]`, where `F` is the padded mel frame count, and int64 `length[1]` containing `N//160`.
It returns float32 `outputs[1,1024,U]` and int64 `encoded_lengths[1]`, where `U` is the output frame count before trimming.
The Go recognizer must trim `outputs[0,:,:]` to its first `T = encoded_lengths[0]` frames, then transpose it to `[T,1024]` for decoding.

`decoder_step.onnx` accepts an int64 `label[B]`, float32 `conv_state[2,B,960,4]` and float32 `rec_state[2,B,5,64,64]`.
`B` is the batch size. The Python reference uses 1.
The Go recognizer must initialize both states to zero and use blank ID 3072 as the first label.
Its outputs are `dec_out[B,640]`, `conv_state_out` and `rec_state_out`.
`joint.onnx` accepts float32 `enc_frame[B,1024]` and `dec_out[B,640]`.
Its `logits[B,3078]` contain 3073 token scores, including blank, followed by five duration scores for durations 0–4.
The Go recognizer must use the greedy TDT loop, which emits at most 10 symbols per frame.
After the tenth symbol on a frame, it must advance one frame if the predicted duration is 0.

## Vocabulary and detokenization

The output has 3073 UTF-8 lines in `<piece> <id>` format, with IDs 0–3072 in order.
The first 3072 pieces come directly from SentencePiece.
Split each line at its last space.

The greedy loop excludes blank IDs before detokenization.
The tokenizer has 2815 normal pieces, 256 byte pieces (IDs 1–256) and `<unk>` (ID 0). It has no control pieces.
Classify each piece by exact match before joining, so text such as `<0x41>` spelled across several pieces stays literal.
The piece `<unk>` becomes ` ⁇ ` (U+2047 with a space on each side), and `▁` becomes a space.
Decode each consecutive run of `<0xNN>` byte pieces as UTF-8, with one U+FFFD replacement character for each invalid byte.
For example, `<0xE3><0x81>` gives two U+FFFD characters, and `<0xE3><0x81><0x81><0x82>` gives `ぁ` followed by one U+FFFD.
Join the decoded pieces and trim the characters that Python's `str.strip()` removes: Go's `unicode.IsSpace` set plus U+001C–U+001F.

## Python reference and measured parity

`haas_full_onnx.py` is the Python reference for the Go recognizer.
Call `HaasFullOnnx(model_dir, threads=4).transcribe(audio)` with a one-dimensional, 16 kHz float32 NumPy array.
The result is `(text, token_ids, token_logprobs)`.
The log-softmax for `token_logprobs` runs over the 3073 token scores, without the duration scores.
Select int8 with `encoder_file="encoder.int8.onnx"`.
When `providers` includes DirectML, the Python reference disables memory patterns and sets sequential execution for all four sessions.

The measurements use ONNX Runtime 1.31.1 with four threads on the CPU on Linux.
With the fp32 encoder, the Python reference transcripts match NeMo 2.7.3 on 600 Japanese clips from FLEURS, ReazonSpeech and SpeechBSD.
Native Windows CPU and DirectML runs also give the same transcripts as NeMo 2.7.3.
With `encoder.int8.onnx`, the Python reference changes 196 of the 600 transcripts compared with fp32.

Strict character error rate (CER) uses jacer 1.2.0's strict form, micro-averaged over each dataset.
It compares characters without the spelling variants its flex form accepts.

| Dataset | fp32 CER | int8 CER |
| --- | --- | --- |
| FLEURS | 3.77% | 4.02% |
| ReazonSpeech | 4.64% | 4.36% |
| SpeechBSD | 6.65% | 6.59% |

## Provenance

`licenses/Apache-2.0.txt` carries the license text from the pinned Haas-ASR checkout, which contains no NOTICE file.
If you omit an encoder variant, remove its files from the bundle README's list too.

## Checks

```sh
uv run --locked python -m unittest discover -v
```

The gated vocabulary test skips unless both variables below are set.
The manifest can be any JSONL file with a `text` field on each line.
The gated vocabulary test compares this detokenization rule with `SentencePieceProcessor.decode(ids).strip()` on the first 20 encoded manifest texts, using SentencePiece 0.2.3 from `uv.lock`.
It needs `tokenizer.model`, which `export_onnx.py` extracts, but no audio or ONNX files.
It writes `vocab.txt` into a temporary directory.

```sh
export VRC_TSUYAKU_TEST_HAAS_TOKENIZER=/path/to/tokenizer.model
export VRC_TSUYAKU_TEST_HAAS_MANIFEST=/path/to/manifest.jsonl
uv run --locked python -m unittest discover -v
```
