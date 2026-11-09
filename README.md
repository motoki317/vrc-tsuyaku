# vrc-tsuyaku

VRC Tsuyaku (通訳, "interpreter") translates speech in VRChat. It runs on your PC, on Windows and Linux, and you control it from a page in your browser.

The idea comes from [VRCT](https://github.com/misyaguziya/VRCT), a VRChat translator.

Status: in development. No release exists yet.

| Flow | Hears | Speech recognition | Translation | Output |
|---|---|---|---|---|
| Self | Your microphone | [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) (Japanese) | [PLaMo 3 Translate 2B](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF), ja→en | VRChat chatbox over OSC |
| World | Everything VRChat plays: voices, world music and video players | [Parakeet TDT 0.6B v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2) (English) | PLaMo 3 Translate 2B, en→ja | The app's page |

You can start either flow alone, or both. The languages and models are fixed, and every model runs on the CPU, alongside VRChat.

The app downloads nothing until you press a download button on its page, and it sends no other internet requests.
It keeps its settings (`settings.yaml`) and downloads (`downloads/`) in the folder that holds the executable, or in the folder that `-data-dir` names, and writes nowhere else.
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
5. Get the downloads on the page's Setup screen. Then, on the Translation screen, turn on a flow's switch: **My voice** runs the self flow, and **Others' voices** runs the world flow. Translations appear below the switches.

Keep the terminal open while you use the app. Ctrl+C quits it.
Stopping a flow keeps its models in memory. Quit the app to free that memory.

Start VRChat before you start the world flow.
A started flow also starts each time the app opens, until you turn its switch off. If you leave the world flow started, start VRChat before you open the app.
On Windows, the world flow needs Windows 11, or Windows 10 version 2004 or later. Windows 10 is untested.
On Linux, the app connects to PulseAudio, or to PipeWire through `pipewire-pulse`. The world flow with VRChat under Proton is untested.

## Development

```sh
nix develop
just ort         # optional: ONNX Runtime libraries for model tests, in .local/onnxruntime/
just lint test
```

[The developer guide](cmd/vrc-tsuyaku/README.md) covers the HTTP API and the end-to-end checks.

vrc-tsuyaku is licensed under the [MIT License](LICENSE). [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) credits the projects it draws on, and lists where each download comes from and its license.
