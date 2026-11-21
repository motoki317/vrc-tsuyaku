# Speech models

Create one `Engine` for the process with `NewEngine(libraryPath)`.
The path names the ONNX Runtime shared library: `onnxruntime.dll` on Windows or `libonnxruntime.so.1` on Linux.
`catalog.ORTLibrary(directory)` returns its path in the artifact directory.

For Japanese, `files := catalog.HaasBundle(directory, int8Encoder)` names the [Haas files](../../tools/haas-export/README.md#files-and-runtime-contract) in `directory` and picks the encoder.
Load them with `Engine.LoadHaas(files)`.
For English, `files := catalog.ParakeetBundle(catalog.Parakeet, directory)` names the [Parakeet TDT 0.6B v2 int8 files](../catalog/pins.go).
For the other European languages and for detection, `catalog.ParakeetBundle(catalog.ParakeetV3, directory)` names the Parakeet TDT 0.6B v3 int8 files.
Load either bundle with `Engine.LoadParakeet(files)`.
The recognizer takes no language: v3 transcribes any of its languages from the audio alone.
`catalog.Spoken(code)` names the recognizer for each spoken language.

Call `Transcribe(ctx, audio, words)` for each phrase of 16 kHz mono `float32` audio produced by [vad.Segmenter](../vad/segmenter.go).
`words` holds the [word hints](#word-hints) for the phrase, or nothing.

Haas uses the decoding algorithm in [the Python reference](../../tools/haas-export/haas_full_onnx.py).
Parakeet uses the same [SentencePiece detokenization rule as Haas](../../tools/haas-export/README.md#vocabulary-and-detokenization).
Its vocabulary has no byte pieces.

Call `Engine.LoadSilero(catalog.SileroModel(directory))` once, then `NewStream()` for each audio stream.
Pass each 512-sample window to `Probability(ctx, audio)` and reset the stream when capture restarts.

## Phrase confidence

`Transcribe` returns an empty string for a phrase whose confidence is under the recognizer's threshold: `haasMinConfidence` for Haas, and `parakeetMinConfidence` for both Parakeet versions.
The models write text for sound that is not words, such as `うん。` or `Uh` for a noise, and they write it with low confidence.

Confidence is the mean natural-log probability of the phrase's tokens.
A token's log-probability is the log-softmax of the scores from which the decoder picked it: every vocabulary token and the blank, without the bonuses of the [word hints](#word-hints).
A token that a hint picked in place of the model's own choice does not count.
With no token to count, the confidence is 0, so the phrase keeps its text.

The thresholds come from one measurement of the int8 models on the CPU, after [`amplifyPhrase`](../pipeline/level.go) raised each phrase's level as the pipeline does.
Haas heard Japanese audio and Parakeet English audio.
Each cell counts the phrases under the threshold in the column header, out of the phrases for which the model wrote text.

Speech, where each counted phrase is lost:

| Audio | Haas, −0.3 | Parakeet v2, −0.7 | Parakeet v3, −0.7 |
| --- | --- | --- | --- |
| Audiobook speech, cut at its pauses | 0 of 118 | 0 of 60 | 1 of 44. v3 also wrote no text for 16 more of the 60 phrases, with or without the cut |
| 60 synthesized chat sentences: clean, with white noise 15 dB under the speech, and after an Opus round trip | 0 of 180 | 0 of 180 | 0 of 180 |
| Synthesized sentences with avatar names (120 Japanese, 60 English), in the same three conditions | 1 of 360 | 0 of 180 | 0 of 180 |
| The same sentences with the names as word hints | 1 of 360 | 0 of 180 | 0 of 180 |
| Synthesized short answers, such as `はい`, `だめ`, `Yes.` and `No.`, clean and with the noise, counting only those that the model heard right | 3 of 119 | 0 of 149 | 0 of 150 |

Sound that is not words, where each counted text is removed:

| Audio | Haas, −0.3 | Parakeet v2, −0.7 | Parakeet v3, −0.7 |
| --- | --- | --- | --- |
| Noise, hum, tone, buzz, clicks and bursts | 108 of 117 | 41 of 72 | 9 of 43 |
| The audiobook speech low-passed at 350 Hz, a stand-in for humming | 76 of 118 | 0 of 60. v2 still transcribes the words, so this row does not test v2 | no text for any of the 60 |

- Haas drops three short answers: `ダメ!` and `本当に。` at −0.32 and `オネガイシマス` at −0.37. Its dropped name sentence is one clip, with and without hints, with the name garbled. At −0.4, Haas would drop none of the speech in the first table, and 89 instead of 108 of the 117 texts for noise.
- Parakeet scores clear speech lower than Haas does: v2 wrote `Yes.` as low as −0.45, `No.` as low as −0.66, and "Is that LaSyusha or Rurune?" at −0.47. At −0.3, v2 would drop 16 of its 149 short answers and v3 12 of its 150.
- For noise, v2 writes `Mm-hmm.` at −0.35 to −0.08, higher than its lowest `Yes.`, so a threshold that keeps `Yes.` keeps `Mm-hmm.` too. Its `Uh` and `Yeah.` score −0.79 or lower.
- For noise, v3 writes `Yeah.` and `Mm.` at −0.73 to −0.28. At −0.6, v3 would drop 23 of those 43 texts and none of the speech that it heard right, but v2 shares `parakeetMinConfidence`, and its `No.` at −0.66 would go. The v3 audiobook phrase under −0.7 is a loop, "and the wheel and the wheel", at −1.17.
- Fillers that the speaker really says, such as `うん` and `Hmm.`, are speech: the cut removes one only when the model is unsure of it. Haas dropped 26 of 48 synthesized Japanese fillers.
- Not measured: casual speech into a headset microphone, a person's murmurs, and Parakeet v3 on languages other than English.

## Word hints

Haas and Parakeet take no prompt, so `Transcribe` favours the words in `words` while it decodes.
A token that starts a word earns a bonus of 1 on its logit, and a token that continues a word that the decoder is writing earns 8.
Where the decoder picks the blank token, which writes nothing, while it is writing a word, the token that continues the word replaces the blank if its logit plus 4 exceeds the blank's logit. So a name that the model breaks apart, such as "VRC Fury" for "VRCFury", can complete.
The decoder compares spellings, not token sequences: after [`glossary.Fold`](../glossary/glossary.go), which ignores case and the difference between hiragana and katakana, a token continues a word when its text is a prefix of the rest of the word.
In Parakeet's vocabulary, a word starts only at a token with the word-start mark `▁`. Haas's vocabulary does not mark words, so a word starts at any token.

A hint changes the transcript only if its word completes.
When the bonus changes a token and every word that used the token then breaks off, the decoder returns to that token, takes the token that it picks without the bonus, and decodes on from there.
After eight such returns in one phrase, it decodes the rest of the phrase without hints.

Three kinds of word have no effect.

- A word of fewer than three characters. The decoder leaves it out, because one token that the bonus picks can complete it.
- A word with a character that no token spells, such as 桔 in Haas's vocabulary. The entry's kana reading is a hint too, and [`glossary.Rewrite`](../glossary/rewrite.go) turns it into the word.
- A word at whose first token the decoder picks a blank, or at whose place in the audio it skips ahead. The bonus never starts a word at a blank and never changes how many frames a token skips.

The three bonuses come from a sweep on synthesized speech, Japanese and English sentences with names, against a glossary of 200 entries.
At these values, the hints changed no sentence that has no name, in clean audio, with white noise 15 dB under the speech, and after an Opus round trip.
On clean sentences that the sweep did not use, the transcript after `glossary.Rewrite` held the name in 63 % of the Japanese ones (53 % with the rewrite alone) and in 73 % of the English ones (40 % with the rewrite alone).
On 92 clips of audiobook speech, the hints changed one transcript.
Parakeet v3 uses the same values. It was measured only on the English sentences.

## Checks

The gated tests skip unless their required environment variables are set.
`nix develop -c just ort` downloads and verifies both CPU runtimes and prints their library paths.
Use the Linux path for `VRC_TSUYAKU_TEST_ORT_LIB`; copy the Windows runtime directory to Windows for native tests.

Haas and Parakeet transcript tests read nonempty JSONL reference files.
Each row has `id` (a unique clip identifier), `hyp` (the expected transcript) and `ref` (the human transcript).
The corresponding audio file is `<id>.wav`.

`TestHaasConfidenceCut` and `TestParakeetConfidenceCut` need no WAV directory and no reference file: only `VRC_TSUYAKU_TEST_ORT_LIB` and the bundle variable of each model in the tables below. A Haas bundle with only the int8 encoder is enough.

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

`TestParakeetV3CPUParity` runs on Linux and reads these paths:

| Variable | Contents |
| --- | --- |
| `VRC_TSUYAKU_TEST_ORT_LIB` | ONNX Runtime 1.31.1 CPU shared library |
| `VRC_TSUYAKU_TEST_PARAKEET_V3_BUNDLE` | Parakeet v3 int8 directory with the four files that `internal/catalog/pins.go` names |
| `VRC_TSUYAKU_TEST_PARAKEET_V3_WAV_DIR` | `<id>.wav` files, canonical 44-byte headers, 16 kHz mono PCM16 |
| `VRC_TSUYAKU_TEST_PARAKEET_V3_REF` | onnx-asr reference rows with `id`, `hyp` and `ref` as above, plus `lang` and the reference token IDs in `tokens` |

v3 uses the `nemo128.onnx` that the [v3 conversion repository](https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/tree/8f23f0c03c8761650bdb5b40aaf3e40d2c15f1ce) publishes.
The reference generator, outside this repository, runs onnx-asr 0.12.0's `NemoConformerTdt` on the same four files and the conversion repository's `config.json`, with four intra-op threads and one inter-op thread.
It takes the token IDs from the class's `_decoding` and the text from its `_decode_tokens`.
On every clip, the test requires identical token IDs, and requires both onnx-asr's text rule and the app's transcript to equal `hyp`. The rows must include at least one clip each in `en`, `de`, `fr`, `es` and `ru`.

```sh
nix develop -c go test ./internal/asr -run '^TestParakeetV3CPUParity$' -v -count=1 -timeout=20m
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
