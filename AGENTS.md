# Rules for working on vrc-tsuyaku

- vrc-tsuyaku is one Go program for Windows and Linux. Build every binary with `CGO_ENABLED=0`, so that `GOOS=windows go build` works from Linux. Load native libraries (ONNX Runtime) at run time with purego. Run llama.cpp as a `llama-server` child process.
- No Python at run time. Python code lives only under `tools/`, for offline model conversion.
- Layout: `cmd/vrc-tsuyaku/` holds `main` and the web UI server, with the static UI files in `cmd/vrc-tsuyaku/ui/`. Everything else goes in `internal/<package>/`.
- Audio between packages is always 16 kHz mono `float32`. Capture asks the OS audio server for that format, so the app has no resampler.
- `internal/catalog` is the only place that names a model's or runtime's files, URLs, SHA-256 hashes, license and languages. Other code reads the catalog.
- The web UI listens only on `127.0.0.1`, and every `/api/` request needs the session token.
- Tests that need models, audio, ONNX Runtime or llama.cpp read their paths from `VRC_TSUYAKU_TEST_*` environment variables and skip when one is unset. Never commit models or test audio.
- Before you finish a change, run `just lint test` and `GOOS=windows go build -o /dev/null ./...` inside `nix develop`.
- Write everything in the repository (code, comments, docs, commit messages) in English. Tracked files must be publishable: no personal names, internal IDs, private paths or private history. Plans and working notes go in `docs/plans/`, which git ignores.
- vrc-tsuyaku is MIT-licensed. When you adapt code, a protocol, a file format or a design from another project, credit it: a short header comment in the adapted file (source and license) and an entry in `THIRD_PARTY_NOTICES.md`. Copy third-party code only under a compatible license, with its copyright notice.
