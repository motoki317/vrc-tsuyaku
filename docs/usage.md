# Using VRC Tsuyaku

## Two translations

| On the page | Hears | Shows the translation |
|---|---|---|
| My voice | your microphone | in your VRChat chatbox, over OSC, and on the page |
| Others' voices | everything that VRChat plays: voices, world music and video players | on the page |

Each has its own spoken language and target language, which Settings → Languages and models changes.

The app remembers each switch: a switch that you leave on starts again the next time you open the app. "Others' voices" needs VRChat to be running, so if you leave it on, start VRChat before you open the app.

On Linux, "Others' voices" is tested only with test playback programs, not with VRChat under Proton.

## Audio level

Settings → Audio shows two level meters: one under "My voice" for your microphone, and one under "Others' voices" for the sound from VRChat. A meter moves while its switch on the Translation screen is on. Each of the two has its own "Automatic" checkbox and slider.

Leave "Automatic" on unless one of the two problems below happens. With "Automatic" on, the app amplifies quiet speech by itself.

- The app translates a sound that you did not mean: people who talk near your microphone under "My voice", or a video or a song with vocals under "Others' voices". Watch how far that sound moves the meter. Then turn "Automatic" off there and drag the slider a little to the right of that point. The app ignores sound that does not reach the slider. Typing and steady noise are normally not translated, so they need no slider.
- The app misses a quiet talker while someone louder also talks, because "Automatic" adjusts to the louder voice. Turn "Automatic" off and drag the slider far to the left, to about −70 dB. The slider also sets how much the app amplifies quiet sound: the further left, the more. At −70 dB, the app amplifies as much as it can and ignores almost nothing.

If speech hardly moves the meter at all, no setting helps. Raise the microphone's level in Windows, or the voice volume in VRChat.

## Translation model

My voice and Others' voices share the translator that Settings → Languages and models picks: PLaMo 3 Translate 2B, the default, or 0.8B. The 2B model needs about 3.2 GB of RAM. The 0.8B model needs about 1.0 GB and translates a sentence about three times as fast, but less accurately. Try 0.8B if VRChat lags or translations fall behind.

## Chatbox message

Settings → Chatbox picks what "My voice" writes to your chatbox. The default, "Both, with codes", writes the original and the translation, each after its language code:

```text
[ja] こんにちは
[en] Hello
```

"Original and translation" writes the same two lines without the codes, and "Translation only" writes only the translation. Under "Custom", you write the message yourself. The app replaces `{{.Original}}`, `{{.Translation}}`, `{{.SourceLang}}` and `{{.TargetLang}}` with the phrase, its translation and the two language codes. The message follows Go's [template syntax](https://pkg.go.dev/text/template).

VRChat shows at most 144 characters and 9 lines at a time, so the app splits a longer message into pages and sends them in turn.

A new message replaces the one in your chatbox. So the app leaves each message, or each page of a long one, up long enough to read: about one second for every 8 characters, and at least 1.5 seconds. If you say something more during that time, and it fits in the chatbox together with the message on screen, the app adds it to that message, and shows the two as one bubble in its own window. For example, "うん。" right after "今日は寒いですね。" gives:

```text
[ja] 今日は寒いですね。 うん。
[en] It's cold today. Yeah.
```

What does not fit waits for its turn. When you keep talking, a message waits about 17 seconds at most after the app translated it. After that the app sends it to the chatbox, even if the message there had less than its reading time.

"Others' voices" joins phrases in the same way. If a phrase arrives during the reading time of the bubble before it, and the two are short enough for one chatbox message, the app adds the phrase to that bubble. Nothing waits there.

## Memory

Every model runs on the CPU, alongside VRChat. Settings → Languages and models shows how much RAM each model needs, and an estimate for both switches on. When you turn a switch off or change its spoken language, the app frees the speech recognition model that no switch uses any more. The translation model stays in memory until you exit the app.
