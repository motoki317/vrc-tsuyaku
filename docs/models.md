# Models and data

## Speech recognition

The spoken language picks the speech recognition model:

| Spoken language | Speech recognition |
|---|---|
| Japanese | [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) |
| English | [Parakeet TDT 0.6B v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2) |
| Bulgarian, Croatian, Dutch, Estonian, French, German, Hungarian, Italian, Latvian, Lithuanian, Polish, Portuguese, Russian, Slovak, Slovenian, Spanish, Swedish | [Parakeet TDT 0.6B v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3) |
| "Detect", which only "Others' voices" offers | Parakeet TDT 0.6B v3, which recognizes English and the languages in the row above without being told which one is spoken. It also transcribes Czech, Danish, Greek, Finnish, Maltese, Romanian and Ukrainian, which are not among PLaMo's languages, so their translations may be wrong. It does not recognize Japanese. |

## Translation

[PLaMo 3 Translate 2B](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF) or [0.8B](https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF) translates into 54 targets, Easy Japanese (やさしい日本語) among them. "Download all" fetches only the one that Settings → Languages and models selects, and [Usage](usage.md#translation-model) compares the two.

Both models are under the same [PLaMo Community License](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/blob/4f672106b847200bfdc0f6af6c321cd94eba1aed/LICENSE/en). Article 2 states:

> The USER must be 18 years of age or older, or have reached the age of legal capacity in the USER's country of residence, unless the USER's legal guardian or legal representative has consented to this AGREEMENT on the USER's behalf.

Article 5(4) applies to you when other players read your translations in the chatbox, and the app does not add the notice that it asks for:

> When providing the OUTPUT (including any dataset containing the OUTPUT) to a third party or making it publicly available, the USER must indicate, in a manner recognizable to the recipients, that the terms of this AGREEMENT apply to any model trained using such OUTPUT.

## Downloads, storage and network

The app downloads nothing until you press a download button on its page. Besides the models, every language choice needs ONNX Runtime, Silero VAD and llama.cpp. [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) lists where each download comes from.

Settings → Downloads lists every file that the app can download, in three groups: what your current languages and translator need, what else is downloaded, and what is not. There you can download or delete each file, and see how much disk space the downloaded files take.

Apart from downloads, the app's only internet request asks GitHub for the latest release, once each time the app starts, so that Settings → About can show whether a newer version exists. On your PC, the app also sends chatbox messages over OSC to the address in Settings → Chatbox, `127.0.0.1:9000` by default, and serves its page and runs `llama-server` on `127.0.0.1`.

The app keeps its settings (`settings.yaml`) and downloads (`downloads/`) in the folder that holds the executable, or in the folder that the `-data-dir` flag names, and writes nowhere else. It writes no transcripts to disk.
