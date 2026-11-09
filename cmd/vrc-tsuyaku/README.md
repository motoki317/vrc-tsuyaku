# App and web UI

The [README](../../README.md) covers using the app.

`main` serves the web page in `ui/` on `127.0.0.1` and opens it in the browser. It also prints the page's URL, with the API token in its fragment, to standard output.
The page is embedded in the executable, so rebuild after you change `ui/`.
Ctrl+C stops the flows, cancels downloads and stops `llama-server`, and then the app exits.

Pressing **Start** saves the flow as enabled, and `main` starts every enabled flow at launch. **Stop** saves it as disabled.
The web page saves each field change at once through `PUT /api/settings`.
`llama-server` keeps running while flows are stopped.
The world flow captures `VRChat.exe`. [The audio guide](../../internal/audio/README.md#application-capture) describes how capture finds VRChat and when it fails.
The app writes no transcripts to disk.

## API

Every `/api/` request requires `Authorization: Bearer TOKEN`, using the token from the URL fragment.
The server rejects a token supplied only in the query string.

| Method and path | Purpose |
| --- | --- |
| `GET /api/state` | Settings, flow status, each flow's required artifact IDs, catalog licenses and artifact state |
| `PUT /api/settings` | Save a complete `config.Settings` JSON object. A changed microphone or chatbox address restarts the self flow only if it is starting or running. Other flows stay unchanged. Invalid input returns 400. A failed write returns 500 |
| `GET /api/microphones` | Microphone IDs and names |
| `POST /api/flows/{flow}/{action}` | Save the flow's enabled state and start or stop it. Start keeps an active run. A start refused for missing downloads returns 409 without changing settings |
| `POST /api/downloads/{id}` | Start a missing artifact's download with a required JSON body, such as `{"acceptLicense": false}` |
| `GET /api/events` | Newline-delimited JSON events: the latest 256 events, then new ones as they happen. While idle, the server sends an empty line every 5 s, which readers skip |

`{flow}` is `self` or `world`, `{action}` is `start` or `stop`, and `{id}` is a catalog artifact ID.
The app checks artifact files once at startup and updates their state after each download.

Each event has an `id`, which orders all events, and a `kind`.
The events of one phrase, of kinds `recognized`, `translated`, `page` and `sent`, share a `phraseId`, unique within one app process.
A `page` event reports each chatbox page written to VRChat, and `sent` follows once every chatbox page of the phrase is written.
A refused start emits an `error` event with `operation: "start"` and its reason in `text`.

Settings JSON uses Go field names, such as `Flows.Self.Microphone` and `Chatbox.Address`, rather than YAML keys.
Omitted fields take their zero values, not their defaults.
Settings saves ignore `LicensesAccepted` and both flows' `Enabled` fields and keep their saved values.
A data directory without `settings.yaml` starts with the default settings: the system's default microphone and VRChat's OSC address, `127.0.0.1:9000`. Before a check starts the self flow, save a test microphone and another address.

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
5. Start VRChat, then press **Start** on the world flow. Join a world where people speak English, or play an English video in a world's video player.
6. Check that English sentences and their Japanese translations appear under "Watch the translations".
7. Exit VRChat and check that the world flow shows an error. Restart VRChat, then press **Try again**.
