# Translation

Create one supervisor with `translate.New(serverPath, modelPath)`.
Pass the path of llama.cpp's `llama-server` and the path of a PLaMo 3 Translate GGUF.
Call `Start(ctx)` when a flow starts, then `Translate(ctx, source, target, phrase, terms)` for each recognized phrase.
`terms` are the glossary terms that the caller found in the phrase. `Translate` sends them as the `glossary` option of PLaMo's chat template, as far as they fit the request.
It goes through the terms in order and keeps each one whose cost still fits a total of 240. A term costs the Unicode code points of its source word and its target word, plus 1.
The comment at `termBudget` in [supervisor.go](supervisor.go) derives the 240 from the context size.
Keep the server running while the flows are stopped.
Call `Close()` only at app shutdown. It waits for any pending `Start` to finish before it stops the server.

## Checks

The unit tests use the test executable as a fake server. They need no models:

```sh
nix develop -c go test ./internal/translate -v -count=1
```

To run them on native Windows, cross-compile from Linux:

```sh
nix develop -c env GOOS=windows go test -c -o translate.test.exe ./internal/translate
```

Run `translate.test.exe -test.v -test.timeout=2m` on Windows.

The real-model test skips only when either variable is unset:

| Variable | Path |
| --- | --- |
| `VRC_TSUYAKU_TEST_LLAMA_SERVER` | llama.cpp b12201 CPU server executable, with its shared libraries beside it |
| `VRC_TSUYAKU_TEST_PLAMO_GGUF` | PLaMo 3 Translate 2B Q4_K_M GGUF with its embedded chat template |

```sh
nix develop -c go test ./internal/translate -run '^TestPLaMoTranslation$' -v -count=1 -timeout=5m
```

The test checks only the output scripts. English must contain Latin letters and no kana or kanji. Japanese must contain kana or kanji.
It logs both outputs for inspection.
