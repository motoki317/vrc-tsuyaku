# Audio capture

Call `Microphones(ctx)` to list IDs and display names, then `Microphone(id).Capture(ctx, consume)` to capture from one ID.
`Application(executable, applicationNames...)` captures one application's playback instead.
The doc comment on `Source` in `audio.go` defines chunk ownership, cancellation and failure behavior.

On both OSes, capture buffers audio in a queue that holds one second of audio.
If the queue overflows, `Capture` returns "audio capture buffer overflow" after the consumer's current call returns.
On Linux, an overflow in the PulseAudio server's buffer also ends capture.
On Windows, glitches in device timing can leave short gaps in the audio without ending capture.

Linux connects to the local Unix socket in `PULSE_SERVER`, or to `$XDG_RUNTIME_DIR/pulse/native` when `PULSE_SERVER` is unset.
`PULSE_SERVER` takes an absolute socket path, optionally prefixed by `unix:`.
PipeWire's `pipewire-pulse` serves the same socket.

## Application capture

Names match regardless of case.
Capture never waits for the application to start and never reconnects after it ends.

Windows (build 19041 or later) captures the named process and its child processes.
Capture fails if no process or more than one process has that name, and when the process exits.

Linux records the one playback stream whose `application.process.binary` or `application.name` equals the `executable` argument, or whose `application.name` equals one of `applicationNames`.
Capture fails if no stream or more than one stream matches, and when the stream ends or moves to another output.
Wine names a stream after the program's product name if the program has one, and after its executable name otherwise.
The world flow therefore also passes `VRChat` in `applicationNames`. That VRChat's product name is `VRChat` is an assumption: VRChat under Proton is untested.

## The `record` command

`record` writes a 16 kHz mono PCM16 WAV, the format that the speech recognition tests read.
Without `-microphone` or `-app`, it records the default microphone, so only a person runs it that way.
Use `record -list` to get microphone IDs and names, then pass an ID to `-microphone`.
Pass `-app` with an executable name to record that application's playback.
`record` matches only the name you pass, without the world flow's `VRChat`. Under Proton, if `-app VRChat.exe` finds no stream, try `-app VRChat`.

```sh
vrc-tsuyaku record -list
vrc-tsuyaku record -seconds 3 out.wav
vrc-tsuyaku record -seconds 3 -microphone '<ID>' out.wav
vrc-tsuyaku record -app VRChat.exe -seconds 3 world.wav
```

Each run needs a new output path.
The command removes incomplete output when capture or writing fails, including after Ctrl+C.

## Tests

While no `VRC_TSUYAKU_TEST_*` variable is set, `nix develop -c just test` runs only the tests that need no audio server.

The microphone tests (`TestMicrophone`, `TestMicrophoneContinuous` and `TestMicrophoneConsumerStall`) record from the source that `VRC_TSUYAKU_TEST_MICROPHONE` names, and skip when it is unset.
Set it only to a test source made from a null sink, never to a default or physical microphone.
Windows has no built-in null device, so on Windows only a person runs these tests.

On Linux, create a null sink and a remap source of its monitor.
Use sink and source names that no existing device uses:

```sh
sink_module=$(pactl load-module module-null-sink sink_name=vrc_test)
source_module=$(pactl load-module module-remap-source source_name=vrc_test_mic master=vrc_test.monitor)
nix develop -c env VRC_TSUYAKU_TEST_MICROPHONE=vrc_test_mic go test -v ./internal/audio
pactl unload-module "$source_module"
pactl unload-module "$sink_module"
```

`VRC_TSUYAKU_TEST_AUDIO=1` enables `TestRecordList` in `./cmd/vrc-tsuyaku`, which lists devices and records nothing.

`TestApplicationIsolation` checks that application capture records only the selected stream. On Linux, run it against two test playback programs:

1. Load a PulseAudio null sink under a name that no other sink uses.
2. Start two playback programs with different names: one plays 440 Hz, the other 1000 Hz. Each program must connect to the null sink by name, exit if that sink is missing, and set the stream's no-move flag (`PA_STREAM_DONT_MOVE`). Without that flag, the server moves a stream to the default output, your speakers, when its sink disappears.
3. Run the test with the 440 Hz program's executable name or `application.name`:

   ```sh
   nix develop -c env VRC_TSUYAKU_TEST_APPLICATION='<name>' go test -v ./internal/audio -run '^TestApplicationIsolation$'
   ```

4. Stop both programs, then unload the sink.

`TestApplicationAbsent` checks the error for a process that is not running. It plays nothing, and on Linux it needs a running audio server:

```sh
nix develop -c env VRC_TSUYAKU_TEST_APPLICATION_ABSENT=1 go test -v ./internal/audio -run '^TestApplicationAbsent$'
```

The [developer guide](../../cmd/vrc-tsuyaku/README.md#world-capture-check) has the world flow's capture checks, including the Windows checks that a person runs because they play audible audio.
