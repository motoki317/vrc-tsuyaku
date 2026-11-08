# vrc-tsuyaku

VRC Tsuyaku (通訳, "interpreter") translates speech in VRChat. It runs on your PC, on Windows and Linux, with a web UI in your browser.

Status: in development. No release exists yet.

| Flow | Audio | Speech recognition | Translation | Output |
|---|---|---|---|---|
| Self | Your microphone | [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) (Japanese) | [PLaMo 3 Translate](https://huggingface.co/pfnet), ja→en | VRChat chatbox over OSC |
| World | VRChat's audio only | [Parakeet TDT 0.6B v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2) (English) | PLaMo 3 Translate, en→ja | Web UI and a VR view page |

Languages are settings: PLaMo 3 Translate translates between 53 languages, and each flow can use another recognizer. Every model runs on the CPU by default, and each can move to the GPU in the settings.

The app downloads the models on first start. Each model keeps its own license, and the app shows it before the download. PLaMo 3 Translate is under the PLaMo Community License, under which users under 18 need a guardian's consent.

Built with PLaMo.

## Development

```sh
nix develop
just ort         # pinned ONNX Runtime libraries into .local/
just lint test
```

vrc-tsuyaku is licensed under the [MIT License](LICENSE). [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) credits the projects it draws on.
