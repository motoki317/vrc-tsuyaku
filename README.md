# vrc-tsuyaku

VRC Tsuyaku (通訳, "interpreter") translates speech in VRChat. It runs on your PC, on Windows and Linux, and you control it from a page in your browser.

The idea comes from [VRCT](https://github.com/misyaguziya/VRCT), a VRChat translator.

Status: in development. No release exists yet.

| Flow | Hears | Output |
|---|---|---|
| Self (**My voice**) | Your microphone | VRChat chatbox over OSC |
| World (**Others' voices**) | Everything VRChat plays: voices, world music and video players | The app's page |

You can start either flow alone, or both. Each flow has its own spoken language and target language.
[PLaMo 3 Translate 2B](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF) translates into 54 targets, Easy Japanese (やさしい日本語) among them.
The spoken language picks the speech recognition model:

| Spoken language | Speech recognition |
|---|---|
| Japanese | [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) |
| English | [Parakeet TDT 0.6B v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2) |
| Bulgarian, Croatian, Dutch, Estonian, French, German, Hungarian, Italian, Latvian, Lithuanian, Polish, Portuguese, Russian, Slovak, Slovenian, Spanish, Swedish | [Parakeet TDT 0.6B v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3) |
| Detect, for the world flow only | Parakeet TDT 0.6B v3, which recognizes English and the languages in the row above without being told which one is spoken. It also transcribes Czech, Danish, Greek, Finnish, Maltese, Romanian and Ukrainian, which PLaMo may mistranslate. It does not recognize Japanese. |

Every model runs on the CPU, alongside VRChat.

The app downloads nothing until you press a download button on its page.
Its only other internet request asks GitHub for the latest release, once each time the app starts, so that Settings → About can show whether a newer version exists.
It keeps its settings (`settings.yaml`) and downloads (`downloads/`) in the folder that holds the executable, or in the folder that the `-data-dir` flag names, and writes nowhere else.
It writes no transcripts to disk.

## Built with PLaMo

PLaMo 3 Translate is under the [PLaMo Community License](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/blob/228a27cc0bb4f111219963848d50a3127c16ca73/LICENSE/en). Article 2 states:

> The USER must be 18 years of age or older, or have reached the age of legal capacity in the USER’s country of residence, unless the USER's legal guardian or legal representative has consented to this AGREEMENT on the USER’s behalf.

## Run from source

1. Install Go 1.26 or later.
2. In the repository root, build the executable into the current folder.

   On Linux:

   ```sh
   CGO_ENABLED=0 go build ./cmd/vrc-tsuyaku
   ```

   On Windows, in PowerShell:

   ```powershell
   go build ./cmd/vrc-tsuyaku
   ```

3. Start it with a data folder outside the repository. The app creates the folder if it is missing. Without `-data-dir`, `settings.yaml` and `downloads/` land in the repository, where git does not ignore them.

   On Linux:

   ```sh
   ./vrc-tsuyaku -data-dir ~/vrc-tsuyaku-data
   ```

   On Windows, in PowerShell:

   ```powershell
   .\vrc-tsuyaku.exe -data-dir $HOME\vrc-tsuyaku-data
   ```

4. The page opens in your browser. If it does not, open the complete URL that the terminal prints, including the part after `#`.
5. On the first screen, pick the languages, press **Download all**, and press **Next** when it is enabled. Then, on the Translation screen, turn on a flow's switch. Translations appear above the switches.

Keep the terminal open while you use the app. Ctrl+C quits it.
Stopping a flow keeps its models in memory. Quit the app to free that memory.

Start VRChat before you start the world flow.
The app remembers each switch: a flow whose switch you leave on starts again the next time you open the app. If you leave **Others' voices** on, start VRChat before you open the app.
On Windows, the world flow needs Windows 11, or Windows 10 version 2004 or later. It is tested only on Windows 11.
On Linux, the app connects to PulseAudio, or to PipeWire through `pipewire-pulse`. On Linux, the world flow is tested only with test playback programs, not with VRChat under Proton.

## Chatbox message

**Settings → Chatbox** picks what the self flow writes to your chatbox: one of three presets, or **Custom**. Under **Custom**, the message is a [Go template](https://pkg.go.dev/text/template) with four fields: `{{.Original}}`, `{{.Translation}}`, `{{.SourceLang}}` and `{{.TargetLang}}`.
The first preset is the default:

```text
[{{.SourceLang}}] {{.Original}}
[{{.TargetLang}}] {{.Translation}}
```

VRChat shows at most 144 characters and 9 lines at a time, so the app splits a longer message into pages and sends them in turn.

## Development

```sh
nix develop
just ort         # optional: ONNX Runtime libraries for model tests, in .local/onnxruntime/
just lint test
```

[The developer guide](cmd/vrc-tsuyaku/README.md) covers the HTTP API and the end-to-end checks.

vrc-tsuyaku is licensed under the [MIT License](LICENSE). [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) credits the projects it draws on, and lists where each download comes from and its license.
