# Models and data

## Speech recognition

| Spoken language | Model |
|---|---|
| Japanese | [Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0) |
| English | [Parakeet TDT 0.6B v2](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2) |
| Bulgarian, Croatian, Dutch, Estonian, French, German, Hungarian, Italian, Latvian, Lithuanian, Polish, Portuguese, Russian, Slovak, Slovenian, Spanish, Swedish | [Parakeet TDT 0.6B v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3) |
| "Detect" | Parakeet TDT 0.6B v3 |

"Detect" recognizes English, the languages of the v3 row, and Czech, Danish, Greek, Finnish, Maltese, Romanian and Ukrainian, but not Japanese.

PLaMo's languages do not include Portuguese, Swedish, or the seven languages that only "Detect" recognizes, so their translations may be wrong.

## Translation

[PLaMo 3 Translate 2B](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF) or [0.8B](https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF) translates into 54 target languages, Easy Japanese (やさしい日本語) among them.

Both models are under the [PLaMo Community License](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/blob/4f672106b847200bfdc0f6af6c321cd94eba1aed/LICENSE/en). Article 2 states:

> The USER must be 18 years of age or older, or have reached the age of legal capacity in the USER's country of residence, unless the USER's legal guardian or legal representative has consented to this AGREEMENT on the USER's behalf.

Article 5(4) applies when other players read your translations in the chatbox. The app does not add the notice that it asks for, so you must give it yourself:

> When providing the OUTPUT (including any dataset containing the OUTPUT) to a third party or making it publicly available, the USER must indicate, in a manner recognizable to the recipients, that the terms of this AGREEMENT apply to any model trained using such OUTPUT.

## Downloads, storage and network

- The app downloads nothing until you press a download button. [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) lists where each download comes from.
- Apart from downloads, the app's only internet request checks GitHub for a new release when the app starts.
- The app sends chatbox messages over OSC to the address in Settings → Chatbox, `127.0.0.1:9000` by default.
- The page and `llama-server` listen only on `127.0.0.1`, so other computers cannot reach them.
- The app keeps its settings and downloads in the executable's folder or the one that `-data-dir` names, and writes nowhere else. It writes no transcripts to disk.
