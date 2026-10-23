# The overview video

A tour of sind of about eight and a half minutes, for the docs landing page
and the README: the CLI, the web UI and what sind sets up around a cluster.
Sindy narrates it (`narration.json`), captions name each beat, and a lo-fi
music bed with light accents carries the sound. Every terminal output and
every page of `sind ui` in it comes from a real run, recorded on a GitHub
runner.

```text
scenario/ ──► record.py ──► terminal.json          (each shot, with its timing)
              (pty, bash)   ui/capture.json + ui/f  (record_ui.mjs: sind ui and Grafana)
                                   │
narration.json ──► ../voice/sindy_voice.py ──► voice/ (a WAV and a length for each line)
                                   │
                                   ▼
              compositor/  (index.html, engine.js, acts.js, term.js)
                 render.mjs ──► video.mkv (AV1), video.json (chapters, sound and voice cues), poster
                 sound.py   ──► sound.wav (music, accents and narration)
                 build.sh   ──► overview.webm (AV1 + Opus), overview.jpg,
                                overview-thumb.jpg, overview.vtt, overview.json
```

## Recording

`record.py` runs the scenario for real: the clusters in `scenario/`, each
shot in an interactive bash in a pseudo-terminal of 100 x 26, so sind draws
its progress tree and colours as it does for a user. It keeps what the
commands print, with its timing; the compositor types the inputs. For the web
UI, it starts `sind ui` and runs `record_ui.mjs`, which drives headless Chrome
through the pages and dialogs and keeps a screencast with the beats, the
pointer moves and the clicks.

The host needs Docker, host DNS for `*.sind` (systemd-resolved and a polkit
rule), mise, jq, Chrome and node with `video/node_modules`. `setup-host.sh`
prepares an Ubuntu GitHub runner: sind from the checkout, mise, the polkit
rule for the runner's user, and the Docker Hub images from mirror.gcr.io.

## Rendering

```bash
(cd video && npm ci)
pip install numpy -r video/voice/requirements.txt   # and ffmpeg with libsvtav1 and libopus
python3 video/voice/sindy_voice.py setup             # the voice model, once
video/overview/build.sh RECORDINGS OUT
```

The compositor is a page whose frames are a function of time
(`window.renderAt(t)`). `acts.js` holds the ten acts; each act is a chapter.
A terminal act plays shots of `terminal.json` and changes its caption at
given input lines; a command whose output takes long plays faster, with a
badge. A caption is `[line id, text]`: Sindy says the line of
`narration.json` with it, and the beat lasts until the line ends. The web UI act plays the screencast beat by beat, with a drawn
pointer and a camera that follows it where a beat asks.

To try a change of the compositor without a render, take stills:

```bash
node video/overview/compositor/render.mjs --rec RECORDINGS --out /tmp/stills --stills 12,60,240
```

## Where it runs

- The **Overview video** workflow records and renders on pull requests that
  change `video/overview`, and uploads the recordings and the video as
  artifacts. Download `overview-recordings` to render locally.
- The **Deploy docs** workflow records and renders the video of each
  checkout into its `docs/static/videos` and `docs/data/videos`, where the
  landing page (`docs/layouts/_partials/overview-video.html`) finds it. The
  render is cached by the git trees of `video/overview` and `video/voice`,
  so it is made again only when they change. Never commit rendered files.

When the CLI or the web UI changes what a shot shows, such as a renamed
command or a moved button, update the shot in `record.py` or the tour in
`record_ui.mjs`, its caption in `compositor/acts.js` and its line in
`narration.json`. The `sindy-voice` skill covers how to write and check
lines (`sindy_voice.py phonemes -s video/overview/narration.json`).

`sound.py --style` also has the beds `bright`, `ambient` and `drive`; the
video uses `lofi`, its default.
