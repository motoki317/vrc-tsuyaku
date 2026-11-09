# Install VRC Tsuyaku

[日本語](https://github.com/motoki317/vrc-tsuyaku/blob/main/docs/install.ja.md)

VRC Tsuyaku runs on 64-bit Windows 11, or on 64-bit Windows 10 version 2004 (May 2020 Update) or later. It needs about 4 GB of free disk space for its models.

## Install and start

1. Download [vrc-tsuyaku-windows.zip](https://github.com/motoki317/vrc-tsuyaku/releases/latest/download/vrc-tsuyaku-windows.zip).
2. Right-click the zip, choose "Extract All", and then press "Extract". You get a folder named `vrc-tsuyaku-windows` with `vrc-tsuyaku.exe` inside.
3. Move that folder to a place where it can stay, such as your Desktop. The app saves its settings and models in this folder. So do not put it in `Program Files`, and if OneDrive backs up your Desktop, choose a folder outside OneDrive, such as `C:\vrc-tsuyaku-windows`.
4. Double-click `vrc-tsuyaku.exe` in that folder.

The first time you start it, Windows shows "Windows protected your PC", because the app is not signed. Click "More info", then "Run anyway".

A black console window opens, and the app opens its page in your browser. Keep the console window open while you use the app. If the page does not open, or after you close its tab, open the address that the console window prints in your browser.

## Set up translation

On the first start, the page shows two rows:

- "My voice" translates what you say into your microphone and writes the translation into your VRChat chatbox.
- "Others' voices" translates what you hear in VRChat and shows it on the page.

1. Check each row's languages: the spoken language, then the language to translate into. By default, "My voice" translates Japanese into English, and "Others' voices" translates English into Japanese. Change them if you need to, then press "Download all".
2. A dialog shows the license of PLaMo, the translation model. Press "Accept and download" only if you are 18 or older, or if your guardian agrees.
3. When the downloads finish, press "Next". The page switches to the Translation screen.
4. Start VRChat. Then turn on "My voice", "Others' voices", or both.

If your chatbox stays empty, turn on OSC in VRChat: Action Menu → Options → OSC → Enabled.

## Exit

Press "Exit" at the top right of the page, or close the console window.

## Update

When a new version exists, Settings → About says so. Exit the app, download the new zip from the link in step 1, and replace `vrc-tsuyaku.exe` in your folder with the one from the new zip. Your settings and models stay in the folder, so you do not download them again.

## Uninstall

Exit the app and delete its folder. The app writes nothing outside that folder.

## Linux

Download [vrc-tsuyaku-linux.tar.gz](https://github.com/motoki317/vrc-tsuyaku/releases/latest/download/vrc-tsuyaku-linux.tar.gz), then run:

```sh
tar -xzf vrc-tsuyaku-linux.tar.gz
./vrc-tsuyaku/vrc-tsuyaku
```

The app needs PulseAudio, or PipeWire with `pipewire-pulse`.
