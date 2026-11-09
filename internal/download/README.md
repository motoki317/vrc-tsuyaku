# Artifact installation

Use `catalog.List(runtime.GOOS)` to display the available artifacts and their licenses.

`Directory` returns `<dataDir>/downloads/<artifact ID>`.
Call `CheckInstalled` at startup and cache its result for the UI and flow starts.
A nil `Download` result means the artifact is installed, so update the cached state without hashing it again.
For a manual installation, copy each file into that artifact directory under its `File.Path` name, not its archive member name.
On Linux, give each `Executable` file execute permission.
`CheckInstalled` hashes every required file, which takes seconds for large models.
Pass the artifact directory to the catalog's model path helpers or `Artifact.Path`.

Downloads follow HTTPS redirects and use environment proxy settings.
On Windows, stop `llama-server` before replacing its artifact.
ONNX Runtime stays loaded in the app's process after `Engine.Close`, so replacing it needs an app restart.
Open handles can prevent renaming or deleting files.

The gated Linux test fetches Silero, llama.cpp and ONNX Runtime, checks every installed hash, and exercises the downloaded runtimes:

```sh
VRC_TSUYAKU_TEST_DOWNLOAD_DIR=/path/to/writable/test-directory \
  nix develop -c go test ./internal/download -run '^TestPinnedLinuxDownloads$' -v -count=1 -timeout=15m
```
