# Translation

Create one supervisor with `translate.New(serverPath, modelPath)`.
Pass the llama.cpp b12201 CPU `llama-server` path and the PLaMo 3 Translate 2B Q4_K_M GGUF path.
Call `Start(ctx)` when a flow starts, then `Translate(ctx, direction, phrase)` for each recognized phrase.
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
