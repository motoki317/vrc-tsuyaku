# App and web UI

[Usage](../../docs/usage.md) covers using the app.

`main` serves the web page in `ui/` on `127.0.0.1` and opens it in the browser. It also prints the page's URL to standard output.
The page is embedded in the executable, so rebuild after you change `ui/`.
Ctrl+C stops the flows, cancels downloads and stops `llama-server`, and then the app exits. `POST /api/exit`, which the page's Exit button sends, does the same.

Turning a flow's switch on saves the flow as enabled, and `main` starts every enabled flow at launch. Turning the switch off saves the flow as disabled.
A flow saved as enabled that cannot start because its downloads are missing, for example after a language change, starts when the last of them finishes.
The web page saves each field change at once through `PUT /api/settings`.
`llama-server` keeps running while flows are stopped.

The app reads `settings.yaml` again within 250 ms after another program, such as a text editor, changes it.
If the file is invalid, `GET /api/state` returns the error in `settingsError`, and the app keeps its last valid settings.
Until a valid edit or `PUT /api/settings` clears the error, the app accepts a stop but refuses to start a flow or a download.
The world flow captures `VRChat.exe`. [The audio guide](../../internal/audio/README.md#application-capture) describes how capture finds VRChat and when it fails.
The app writes no transcripts to disk.

At launch, the app sends one `GET` to the latest-release API of `motoki317/vrc-tsuyaku`, the repository that `version.go` names, so a fork's build still checks the upstream releases.
Until GitHub answers, `update.state` in `GET /api/state` is `checking`.
It then becomes `available`, with the release's `version` and `url`, or `current`, or `failed`.
A 404, which GitHub returns while the repository has no release, counts as `current`.
Any status other than 200 and 404, a redirect, a timeout after 10 s, or a release tag that is not valid semver counts as `failed`.
The check compares the release tag with the module version that Go stamped into the build, pseudo-versions included.

`app` in `GET /api/state` comes from Go's build information.
`version` is that module version when it comes from a tag, such as `v0.1.0`, and is empty for a pseudo-version or a build without version information.
`revision`, `time` and `modified` come from the `vcs.*` settings that `go build` records only in a git checkout.

`GET /notices.txt`, which Settings → About links to, returns the license texts in `notices/` as plain text: those of Go, of each Go module in `go.mod`, and of the other code that the executable contains or reimplements.
After you change `go.mod`, run `just notices`, which copies each module's license texts from the module cache. `TestModuleNoticesMatchGoMod` fails until `notices/modules/` matches `go.mod`.
The texts in `notices/other/` are copied by hand, and each one's `SOURCE` file names where it came from.

## API

The server returns 403 for a request whose `Host` is neither its own address, such as `127.0.0.1:54321`, nor `localhost` with its port, which stops DNS rebinding.
It also returns 403 for a request whose `Sec-Fetch-Site` header is `cross-site` or `same-site`, so another web page in the browser can neither open the app's page nor call the API. The same goes for a request other than GET or HEAD whose `Origin` header is not the server's own origin, which covers browsers that send no `Sec-Fetch-Site`. `Origin: null` counts as another origin.
A request with neither `Sec-Fetch-Site` nor `Origin`, such as one from `curl`, passes, so every program and user on the PC can use the API.

| Method and path | Purpose |
| --- | --- |
| `GET /api/state` | `launch` (a value that differs between launches), `app` (build information), `update` (the update check: `state`, `version` and `url`), `settings`, `settingsError`, `flows` (each flow's state), `required` (each flow's artifact IDs), `artifacts` (state and licenses), `languages` (the language catalog and the translators) and `defaultTemplate` |
| `PUT /api/settings` | Save a complete `config.Settings` JSON object. Invalid input returns 400. A failed write returns 500. If another program changed `settings.yaml` after the app last read it, the app reads the file again, writes nothing and returns 409 with code `settings_changed`. The page then fetches the state and repeats the player's change once |
| `GET /api/microphones` | Microphone IDs and names |
| `POST /api/chatbox/preview` | Render a template with a sample message, such as `{"template": "{{.Translation}}", "original": "こんにちは", "translation": "Hello", "sourceLang": "ja", "targetLang": "en"}`. Returns `{"pages": [...]}`, the chatbox pages that VRChat receives, or 400 for an invalid template |
| `POST /api/flows/{flow}/{action}` | Save the flow's enabled state and start or stop it. A start on a starting or running flow leaves the run as it is. A start refused for missing downloads returns 409 without changing settings |
| `POST /api/downloads/{id}` | Start a missing artifact's download with a required JSON body, such as `{"acceptLicense": false}` |
| `DELETE /api/downloads/{id}` | Delete an installed artifact's files. Returns 409 while the artifact downloads or a starting or running flow needs it. On Windows, ONNX Runtime stays loaded after a flow first uses it, so its deletion returns 500 until the app restarts |
| `POST /api/open/{target}` | Open the data folder (`data`) or `settings.yaml` (`settings`) in the OS's file manager or default app. If `settings.yaml` is missing, the app first writes the current settings to it. Returns 204 |
| `POST /api/exit` | Exit the app as Ctrl+C does. Returns 202 before the app shuts down |
| `GET /api/events` | Newline-delimited JSON events: the latest 256 events, then new ones as they happen. While idle, the server sends an empty line every 5 s, which readers skip |

`{flow}` is `self` or `world`, `{action}` is `start` or `stop`, and `{id}` is a catalog artifact ID.
The app checks artifact files once at startup and updates their state after each download or deletion.
Apart from 403 and 404, which have plain-text bodies, an error response is a JSON object with the diagnostic text in `error` and an [`errcode`](../../internal/errcode/errcode.go) value in `code`.
Each code names the user's next step, so the page shows its own message for a known code and the diagnostic text for `unknown`.
Artifact and flow states and `error` events carry the same `code` field.

A settings save restarts a starting or running flow when the change needs another recognizer, for example from Japanese to English.
A changed microphone or chatbox address also restarts the self flow.
A changed `Translator` restarts both flows when they are starting or running. An enabled flow that waits for the old translator's download starts with the new one if it is installed.
Other changes, such as the target language or the chatbox template, apply from the flow's next phrase.

Each event has an `id`, which orders all events, a `time` and a `kind`. Events about one flow also name it in `flow`:

| Kind | `text` |
| --- | --- |
| `state` | The flow's new state: `starting`, `running` or `stopped`. A flow that fails sends an `error` event instead, and `GET /api/state` shows it as `error` |
| `speech` | `start` when the flow hears speech begin, and `end` when it ends |
| `recognized` | The recognized phrase |
| `translated` | The translation, with the recognized phrase in `original` |
| `page` | One chatbox page written to VRChat. When the phrase joined an earlier message, as described below, the text also holds that message's earlier phrases |
| `merged` | The phrase joined an earlier message, as described below. `text` holds the message's translations, joined, and `original` its recognized phrases, joined. `into` holds the `phraseId` of the message's first phrase |
| `sent` | Empty. Follows once every chatbox page of the phrase is written |
| `error` | The diagnostic text |
| `settings` | `saved` after the app saves settings, `reloaded` after an external edit, and `changed` after a stop that the app could not save because `settingsError` is set |
| `download` | The artifact ID whose state or progress changed. During a download, at most one event every 250 ms |
| `update` | The update check's result: `current`, `available` or `failed` |

The events of one phrase share a `phraseId`, unique within one app process: `recognized`, `translated`, `page`, `merged`, `sent`, and an `error` from sending that phrase.
They also carry the flow's spoken language in `sourceLang`, which is `auto` while the world flow detects the language, and its target language in `targetLang`.
The app fixes both languages and the template before it recognizes the phrase, so a later settings change does not alter the phrase.
A start that the flow's checks refuse, for example for missing downloads, emits an `error` event with `operation: "start"`.

The app groups phrases into messages and times its chatbox writes with fixed values, not settings. A message is one or more phrases that the app shows together: as one chatbox text, and as one bubble in the web UI.

- Two chatbox writes are at least 1.5 s apart, which keeps the app under VRChat's spam timeout.
- A chatbox page stays on screen in VRChat for its reading time before another page replaces it. The reading time ends 120 ms per character of the page after the message first appeared, and at least 1.5 s after the page itself appeared.
- A phrase joins the flow's latest message if that message has one chatbox page and is still in its reading time, both have the same template and languages, and the two fit in one chatbox page together. The app renders the template with the originals joined by a space and the translations joined by a space. The joined page belongs to the same message, so by the rule above a join adds 120 ms of reading time for each added character, and the joined page stays at least 1.5 s.
- In the self flow, the app decides the join when the phrase's turn to send comes. A joined page does not wait for the reading time. The app writes it 1.5 s after the previous write at the earliest, then emits `page`, `merged` and `sent`.
- The world flow writes nothing, so its phrases wait for nothing. A message first appears when the translation of its first phrase finishes. The app measures the message as a chatbox page of the default template, whatever `Chatbox.Template` holds, and emits `merged` right after the joining phrase's `translated`.
- `into` stays the same while further phrases join a message, so the web UI finds the bubble that shows the message even if it never saw the phrases that joined in between.
- When phrases back up in the self flow, a waiting chatbox page stops waiting for the reading time of the page before it. Page *n* of a phrase stops waiting *n* × 17.28 s after the phrase's translation finished. 17.28 s is the reading time of a full page.
- After a flow restarts, its first phrase joins no earlier message, and its first chatbox page waits for no reading time. The 1.5 s between writes holds across a restart.

Settings JSON uses Go field names, such as `Flows.Self.Microphone` and `Chatbox.Template`, rather than YAML keys.
`Translator` takes an `id` from `languages.translators`: `plamo-3-translate-2b`, the default, or `plamo-3-translate-0.8b`.
`Flows.Self.Spoken` and `Flows.World.Spoken` take a spoken code from the language catalog, and `Flows.Self.Target` and `Flows.World.Target` take a target code. Only `Flows.World.Spoken` accepts `auto`.
`Chatbox.Template` is a Go [`text/template`](https://pkg.go.dev/text/template) over `chatbox.Message`: `{{.Original}}`, `{{.Translation}}`, `{{.SourceLang}}` and `{{.TargetLang}}`.
The app splits the rendered text into chatbox pages of at most 144 characters and 9 lines.
`UI.Language` is `en`, `ja`, or empty to follow the browser.
Omitted fields take their zero values, not their defaults.
Settings saves ignore `LicensesAccepted` and both flows' `Enabled` fields and keep their saved values.
A data directory without `settings.yaml` starts with the default settings: the system's default microphone and VRChat's OSC address, `127.0.0.1:9000`. Before a check starts the self flow, save a microphone that records no person and an address other than `127.0.0.1:9000`.

Set `acceptLicense` to true only after the user accepts that artifact's license.
The server saves that acceptance before the download starts, and refuses to download an artifact whose license needs acceptance until an acceptance is saved.

## End-to-end check

`TestEndToEnd` skips unless both variables are set:

| Variable | Value |
| --- | --- |
| `VRC_TSUYAKU_TEST_APP_DATA_DIR` | A writable test data directory, empty or containing valid artifacts to reuse |
| `VRC_TSUYAKU_TEST_APP_WAV` | A Japanese clip of about four seconds, canonical 44-byte-header PCM16 WAV, mono, 16 kHz |

The test downloads missing artifacts through the HTTP API.
When it downloads PLaMo, it accepts the PLaMo license on behalf of the person who runs it. So only a person who accepts that license runs the test on a directory without PLaMo.
It keeps downloaded files for later runs.
To run the test on both OSes, copy every directory in one data directory's `downloads/` into the other's, except the ONNX Runtime and llama.cpp directories: each OS downloads its own.
The test runs both flows on fake audio sources, so it records and plays nothing. It supplies the clip to the self flow and receives its chatbox page on a local UDP listener.
It checks the `recognized`, `translated` and `page` events and reports latency and the peak resident memory of the app and of `llama-server`.
The chatbox page must arrive within two seconds of the clip's end, and combined peak memory must stay below 6.5 GB.
The test runs the app inside the test process, so the app's figure is the test process's memory.

```sh
nix develop -c go test ./cmd/vrc-tsuyaku -run '^TestEndToEnd$' -v -count=1 -timeout=35m
```

For native Windows, cross-compile and run the binary with Windows paths in those variables:

```sh
nix develop -c env GOOS=windows go test -c -o app.test.exe ./cmd/vrc-tsuyaku
```

Run `app.test.exe -test.run "^TestEndToEnd$" -test.v -test.count=1 -test.timeout=35m` on Windows.
When you start it from WSL, list both variable names in `WSLENV` without the `/p` flag, so that Windows receives the paths unchanged.

## World capture check

`TestWorldCapture` skips unless `VRC_TSUYAKU_TEST_APP_DATA_DIR` and `VRC_TSUYAKU_TEST_WORLD_APPLICATION` are set.
The directory must contain the world flow's downloads. On Linux, set the application name to a test playback program's executable name or `application.name`, never `VRChat.exe`.
The program plays only into a PulseAudio null sink that you load under a unique name. It must exit if that sink is missing and set the stream's no-move flag. If the test ends before the program exits, stop the program before you unload the sink.
Start the program before the test. It must play silence while the models load (about 20 s), then English speech followed by silence, and then exit.
The test reads the HTTP event stream. It checks English recognition, Japanese translation, and the capture error after the program exits.

```sh
nix develop -c go test ./cmd/vrc-tsuyaku -run '^TestWorldCapture$' -v -count=1 -timeout=3m
```

The native Windows checks play audible audio, so a person runs them, never an automated agent:

1. Pick a time when test audio disturbs no one.
2. Start two test playback programs with different executable names and different audio.
3. Run `vrc-tsuyaku record -app <program>.exe -seconds 3 isolated.wav` with one program's name.
4. Check that the WAV holds only that program's audio.
5. Start VRChat, then turn on the **Others' voices** switch. Join a world where people speak English, or play an English video in a world's video player.
6. Check that English sentences and their Japanese translations appear on the Translation screen.
7. Exit VRChat and check that the world flow shows an error. Restart VRChat, then press **Try again**.
