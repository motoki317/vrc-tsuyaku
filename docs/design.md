# Design

VRC Tsuyaku recognizes and translates speech on the user's PC, on the CPU.

## Two flows

| Flow | Hears | Shows the translation |
|---|---|---|
| "My voice" (`self` in code) | The microphone | In the VRChat chatbox, and on the app's page |
| "Others' voices" (`world`) | Everything that VRChat plays | On the app's page |

## One pipeline

Both flows run the same pipeline, which keeps phrases in the order of speech.

| Step | What it does | Package |
|---|---|---|
| Capture | Reads 16 kHz mono audio from WASAPI or PulseAudio | `internal/audio` |
| Voice detection | Cuts the audio into phrases where Silero VAD hears speech | `internal/vad`, with Silero in `internal/asr` |
| Recognition | Writes each phrase as text with Haas or Parakeet, favoring the glossary's words | `internal/asr` |
| Glossary | Fixes the spelling of the glossary's words, and passes the phrase's entries to the translation | `internal/glossary` |
| Translation | Translates the text with PLaMo on a llama.cpp server, with the flow's recent phrases as context | `internal/translate` |
| Output | Shows the translation on the page. For "My voice", also holds each chatbox message long enough to read and sends it over OSC | `internal/pipeline`, `internal/chatbox` |

`internal/pipeline` joins these steps. `cmd/vrc-tsuyaku` starts and stops the flows, and serves the page and its HTTP API. `internal/catalog` names every model and runtime file with its URL and SHA-256 hash, and `internal/download` installs them.

## Constraints

- The app reaches the internet only for a download that the user starts, and for one update check per launch.
- The app writes only into its data folder.
- The page is served only on `127.0.0.1`, and the server refuses requests that come from other web pages.
- The Go code uses no cgo, so Linux can build the Windows executable. The app loads ONNX Runtime at run time and runs llama.cpp as a child process.

Start reading the code at `Run` in `internal/pipeline/pipeline.go`, and at `handler` in `cmd/vrc-tsuyaku/http.go`, which lists every route.
