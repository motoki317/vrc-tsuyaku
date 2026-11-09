# Speech models

Create one `Engine` for the process with `NewEngine(libraryPath)`.
The path names the ONNX Runtime shared library: `onnxruntime.dll` on Windows or `libonnxruntime.so.1` on Linux.
`catalog.ORTLibrary(directory)` returns its path in the artifact directory.

For Japanese, `files := catalog.HaasBundle(directory, int8Encoder)` names the [Haas files](../../tools/haas-export/README.md#files-and-runtime-contract) in `directory` and picks the encoder.
Load them with `Engine.LoadHaas(files)`.
For English, `files := catalog.ParakeetBundle(directory)` names the [Parakeet TDT 0.6B v2 int8 files](../catalog/pins.go).
Load them with `Engine.LoadParakeet(files)`.

Call `Transcribe(ctx, audio)` for each phrase of 16 kHz mono `float32` audio produced by [vad.Segmenter](../vad/segmenter.go).

Haas uses the decoding algorithm in [the Python reference](../../tools/haas-export/haas_full_onnx.py).
Parakeet uses the same [SentencePiece detokenization rule as Haas](../../tools/haas-export/README.md#vocabulary-and-detokenization).
Its vocabulary has no byte pieces.

Call `Engine.LoadSilero(catalog.SileroModel(directory))` once, then `NewStream()` for each audio stream.
Pass each 512-sample window to `Probability(ctx, audio)` and reset the stream when capture restarts.

## Checks

The gated tests skip unless their required environment variables are set.
`nix develop -c just ort` downloads and verifies both CPU runtimes and prints their library paths.
Use the Linux path for `VRC_TSUYAKU_TEST_ORT_LIB`; copy the Windows runtime directory to Windows for native tests.

Haas and Parakeet transcript tests read nonempty JSONL reference files.
Each row has `id` (a unique clip identifier), `hyp` (the expected transcript) and `ref` (the human transcript).
The corresponding audio file is `<id>.wav`.

### Haas

Set these paths for the CPU parity and lifecycle tests:

| Variable | Contents |
| --- | --- |
| `VRC_TSUYAKU_TEST_ORT_LIB` | ONNX Runtime 1.31.1 CPU shared library |
| `VRC_TSUYAKU_TEST_HAAS_BUNDLE` | Haas bundle with fp32 and int8 encoders |
| `VRC_TSUYAKU_TEST_HAAS_WAV_DIR` | `<id>.wav` files, canonical 44-byte headers, 16 kHz mono PCM16 |
| `VRC_TSUYAKU_TEST_HAAS_REF_FP32` | Python reference fp32 transcripts |
| `VRC_TSUYAKU_TEST_HAAS_REF_INT8` | Python reference int8 transcripts |

Generate the reference files with `write_reference.py`.
Supply one or more JSONL manifests with `id` and nonempty `text` fields.
Each `id` must be unique across the supplied manifests. Each `text` becomes the output's `ref`.
The script refuses to overwrite an existing output file.
From `tools/haas-export/`, run:

```sh
uv run --locked python write_reference.py --bundle "$VRC_TSUYAKU_TEST_HAAS_BUNDLE" \
  --encoder encoder.onnx --wav-dir "$VRC_TSUYAKU_TEST_HAAS_WAV_DIR" \
  --output "$VRC_TSUYAKU_TEST_HAAS_REF_FP32" /path/to/manifest.jsonl
uv run --locked python write_reference.py --bundle "$VRC_TSUYAKU_TEST_HAAS_BUNDLE" \
  --encoder encoder.int8.onnx --wav-dir "$VRC_TSUYAKU_TEST_HAAS_WAV_DIR" \
  --output "$VRC_TSUYAKU_TEST_HAAS_REF_INT8" /path/to/manifest.jsonl
```

Generate the int8 reference on the platform under test, and share the fp32 reference between Linux and Windows.
Small differences in `features.onnx` output between Linux and Windows ONNX Runtime builds change int8 transcripts in both Python and Go.
On Windows, run the script with native Windows Python and Windows paths.
CPU parity requires identical text on every clip. Run the tests from the repository root:

```sh
nix develop -c go test ./internal/asr -run 'TestHaas(CPUParity|Lifecycle)$' -v -count=1 -timeout=45m
```

To run the CPU parity and lifecycle tests on native Windows, cross-compile them with `nix develop -c env GOOS=windows go test -c -o asr.test.exe ./internal/asr`.
Run `asr.test.exe -test.run "TestHaas(CPUParity|Lifecycle)$" -test.v -test.timeout=45m` on Windows. Set the variables to Windows paths.
With WSL interop, list their names in `WSLENV` without `/p` flags.

### Parakeet

Set these paths for the gated parity and lifecycle tests. The parity test skips on platforms other than Linux:

| Variable | Contents |
| --- | --- |
| `VRC_TSUYAKU_TEST_ORT_LIB` | ONNX Runtime 1.31.1 CPU shared library |
| `VRC_TSUYAKU_TEST_PARAKEET_BUNDLE` | Parakeet v2 int8 directory with `nemo128.onnx`, `encoder-model.int8.onnx`, `decoder_joint-model.int8.onnx` and `vocab.txt` |
| `VRC_TSUYAKU_TEST_PARAKEET_WAV_DIR` | `<id>.wav` files, canonical 44-byte headers, 16 kHz mono PCM16 |
| `VRC_TSUYAKU_TEST_PARAKEET_REF` | onnx-asr reference transcripts |

The app and the parity test use `onnx_asr/preprocessors/data/nemo128.onnx` from the onnx-asr 0.12.0 wheel as the bundle's `nemo128.onnx` (SHA-256 `5b4a84c52eeaa615dc46d781cc7e4598f9b432184831d57493c48adcd01371a9`).
[istupakov/parakeet-tdt-0.6b-v2-onnx](https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/tree/0bbb45a3365852604aef28b538a8f066f4ccaa85) supplies `encoder-model.int8.onnx`, `decoder_joint-model.int8.onnx` and `vocab.txt`.
That conversion repository's older `nemo128.onnx` produces different features.
For reference generation, `path` names the bundle directory.
The reference was produced by onnx-asr 0.12.0 `load_model("nemo-parakeet-tdt-0.6b-v2", path, quantization="int8", sess_options=so, providers=["CPUExecutionProvider"], preprocessor_config={"use_numpy_preprocessors": False, "use_conv_preprocessors": False, "sess_options": so, "providers": ["CPUExecutionProvider"]})`, with `so.intra_op_num_threads = 4` and `so.inter_op_num_threads = 1`, followed by `recognize(audio, sample_rate=16000)`.
The reference generator is not part of this repository.
The parity test exercises the recognizer through token generation and renders the tokens with onnx-asr's text rule.
It requires identical transcripts on every clip. It does not test the app's text rendering.
The recognizer keeps SentencePiece's spacing, as NeMo does, so the app's text can differ from `hyp` around punctuation.

```sh
nix develop -c go test ./internal/asr -run 'TestParakeet(CPUParity|Lifecycle)$' -v -count=1 -timeout=20m
```

### Silero VAD

Set these paths for the gated probability and lifecycle tests. The probability test skips on platforms other than Linux:

| Variable | Contents |
| --- | --- |
| `VRC_TSUYAKU_TEST_ORT_LIB` | ONNX Runtime 1.31.1 CPU shared library |
| `VRC_TSUYAKU_TEST_SILERO_MODEL` | Silero VAD v6.2.3 ONNX graph |
| `VRC_TSUYAKU_TEST_SILERO_WAV` | Several clips joined by silences, canonical 44-byte WAV header, 16 kHz mono PCM16 |
| `VRC_TSUYAKU_TEST_SILERO_REF` | JSON object with `sample_count` and a `probabilities` array, one value per 512-sample window |

The reference was produced on Linux by silero-vad 6.2.3 `OnnxWrapper(path, force_onnx_cpu=True).audio_forward(x, 16000)` with ONNX Runtime 1.31.1 and one intra-op thread, using a generator outside this repository.
The probability test pads the final window with zeros, as `audio_forward` does, and requires every probability to agree within 1e-6.

```sh
nix develop -c go test ./internal/asr -run 'TestSilero(Probabilities|Lifecycle)$' -v -count=1
```
