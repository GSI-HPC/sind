# sind videos with Sindy (feasibility study)

Can coding agents produce tutorial videos for each sind guide, as a screencast
and slideshow hybrid presented by an AI anime avatar, cheaply enough to
re-render whenever the docs change? This directory is the answer so far: **yes,
on a CPU**. It contains a working pipeline, a 45-second pilot episode for the
quickstart, and its embedding in the docs.

![Sindy expression sheet](sindy/sheet.png)

## Pipeline

```text
script.json ──► voice/sindy_voice.py ──► line.wav + line.json (words, visemes, loudness)
                 (Kokoro-82M, CPU)                       │
                                                         ▼
episodes/<id>/     ──  lib/scenes.js (planner, captions, terminal, shots, transitions)
                   ──  lib/sindy.js  (avatar: lip sync, blinks, expressions, gaze, wave)
                                                         │
                                                         ▼
                                   HyperFrames (headless Chrome + FFmpeg) ──► MP4
                                                         │
                                         tools/publish.mjs ──► docs: web MP4, poster,
                                                               WebVTT captions, chapters
```

- **Avatar** (`lib/sindy.js`): a vector anime rig drawn in SVG. Every frame is
  a pure function of time: lip sync from phoneme timings with coarticulation
  smoothing and loudness, seeded blinks and micro-saccades, breathing, head
  sway, hair that lags behind the head, 8 expressions, gaze targets and a wave
  gesture. No WebGL, no clocks, no randomness, so HyperFrames can render frames
  in any order on parallel workers.
- **Voice** (`voice/`): Kokoro-82M (Apache-2.0) through `kokoro-onnx`. The
  upstream ONNX export only returns audio; `setup` exposes the model's
  duration predictor as a second output, which yields exact per-phoneme timings
  for free. Words are phonemized one at a time, so captions get exact word
  times, and `lexicon.json` fixes Slurm terms espeak gets wrong (`squeue`,
  `slurmctld`, `CLI`, …). Presets blend voices and can pitch-shift with
  formants preserved.
- **Scenes** (`lib/scenes.js`, `lib/scenes.css`): a cursor-based planner that
  lays narration lines on the timeline and resolves cue words ("show bullet 2
  when she says *munge*"), karaoke captions, a seekable terminal with typed
  commands and a fast-forward badge, avatar shots (`hero`, `full`, `left`,
  `cornerR`, `cornerL`) that Sindy glides between, and iris, push and blur
  transitions.
- **Episodes** (`episodes/<id>/`, one per docs page): the pilot
  `episodes/quickstart/` has an intro with a logo animation and wave,
  fullscreen talk with name tag, slide with corner avatar, terminal screencast
  of the quickstart, outro with links and wink, and a fade to black.

## Results

| What | Measured in a 4 vCPU cloud container, no GPU |
| --- | --- |
| TTS | about 3× faster than real time (6.8 s line: 3.4 s including model load) |
| Render | 45 s of 1080p30 in about 1.5 min (software GL, 2 workers) |
| Audio sync | speech onset in the MP4 matches the plan to 10 ms |
| Determinism | frame-difference scan found no glitch frames at worker boundaries |
| Lint | `hyperframes lint`: 0 errors, 0 warnings |

The renders also showed what doesn't work yet:

- Sindy faces front. Head turns are faked with parallax, and the only arm
  motion is the wave.
- Kokoro has no emotion control, and the voice can only be chosen from blends
  of its stock voices.

## Avatar options

| Approach | Anime quality | CPU-only | Seekable | Cost | Notes |
| --- | --- | --- | --- | --- | --- |
| **Code-drawn SVG rig (this)** | medium | yes | pure f(t) | $0 | agent-editable, versioned in git, no art pipeline |
| Layered art (PNGTuber style) | medium–high | yes | pure f(t) | commission | same rig interface, swap the drawing layer for PSD layers |
| Live2D | high | yes (slow) | physics must be re-simulated or pre-rendered | rig $200–3,500 + art | Cubism Core is proprietary; check license for GSI |
| VRM 3D (VRoid + three-vrm) | medium–high | yes (slow) | spring bones must be re-simulated | $0 | free VRoid Studio, generic look unless customized |
| Generative video (HeyGen, Hedra, Kling) | high but drifts | no | no | about $0.03–0.12/s per render | every script edit re-generates; good for a one-off intro |

The rig's interface (visemes `aa/ih/ou/ee/oh/…`, expressions, gaze, gestures)
deliberately matches VRM and Live2D concepts, so the drawing can be upgraded
without changing episode scripts.

## Run it

Requires Node.js 22+, Python 3.10+, FFmpeg, and Chrome. In a cloud
container, point HyperFrames at the preinstalled browser with
`HYPERFRAMES_BROWSER_PATH`.

```bash
cd video
npm install
pip install -r voice/requirements.txt
npm run voice:setup                        # download Kokoro (about 350 MB) and patch it, once
npm run episode -- voice quickstart        # narration into episodes/quickstart/assets/voice/
npm run episode -- render quickstart       # renders/quickstart.mp4 (add --draft for speed)
npm run episode -- publish quickstart      # docs/static/videos/ and docs/data/videos/
npm run voice:samples                      # audition pack in renders/voice-samples/
npm run sheet                              # sindy/sheet.png expression sheet
```

`--all` instead of an episode id processes every episode. `npx hyperframes
preview` inside an episode opens the HyperFrames Studio, and `npx hyperframes
snapshot --at 5,10` takes stills.

## Layout

```text
video/
  lib/sindy.js          avatar rig
  lib/scenes.js, .css   planner, captions, terminal, shots, transitions, look
  sindy/                character bible, expression sheet page
  voice/                TTS tool, voice presets, pronunciation lexicon
  episodes/<id>/        one HyperFrames project per docs page (script.json + index.html)
  tools/                episode CLI, publishing, vendoring, screenshots, lip-sync stats
```

Generated files (`vendor/`, `assets/voice/`, `renders/`) are not committed.
Renders need no network: GSAP and the fonts come from npm, and the voice
metadata loads from a local script.

## Decisions

- Voice: the `sindy` preset (Kokoro `af_heart` 60% + `af_bella` 40%).
- "sind" is pronounced `/sɪnd/`, like *sinned*, rhyming with Sindy.
- Art: the code-drawn Sindy.
- No background music.
- The pipeline stays in `video/`, next to the docs it follows.

## Videos in the docs

A guide shows its episode with one shortcode under the page title:

```markdown
{{< video "quickstart" >}}
```

It renders a 16:9 player with a poster, an English WebVTT caption track
generated from Sindy's word timings, and a row of chapter buttons that jump to
each scene. The docs render has no burned-in captions (`--variables
'{"captions":false}'`), so captions stay toggleable, accessible and
translatable. If an episode was not published into `docs/static/videos/` (for
example a local `hugo server`), the shortcode renders nothing, so writing docs
never needs the video toolchain.

**Size.** The web encode (H.264 CRF 28, `-tune animation`) takes about 4 MB
per minute; the 45-second pilot is 2.7 MB. Ten 3-minute guides are about
120 MB per docs version, well under the 1 GB GitHub Pages site limit. The
100 GB/month soft bandwidth limit allows roughly 8,000 full views a month.

**Hosting (proposal, needs a decision).** Render in CI and serve the MP4s from
the docs site itself:

- Self-hosted, so no third-party embed, cookies or tracking, and each docs
  version (`main` at `/`, `next` at `/next/`) carries videos made from the
  same commit as its text.
- Nothing binary is committed to `main`/`next`: the rendered episodes are
  cached in Actions, keyed on a hash of the episode, `lib/`, the voice preset
  and the lexicon, so a docs push only re-renders changed episodes (about 2×
  real time on a 4 vCPU runner).
- The catch: today `peaceiris/actions-gh-pages` commits every build into the
  `gh-pages` branch with `keep_files`, so every re-render would add megabytes
  to a branch that each clone downloads. Switch both docs workflows to one
  artifact-based Pages deployment (`actions/upload-pages-artifact` and
  `actions/deploy-pages`) that builds `main` and `next` together. That keeps no
  history and also drops pages deleted from the docs, which `keep_files`
  currently leaves online. It needs **Settings → Pages → Source: GitHub
  Actions**.

Alternatives: a separate repository whose Pages site only hosts the video
files (leaves the current workflows alone, but needs a deploy token and cross-site
URLs); YouTube (discovery and adaptive streaming, but uploads need OAuth or
manual work, every re-render gets a new video ID, embeds contact Google, and
versions drift; better as an extra channel for release episodes); or release
assets (no home for the `next` preview).

## Next: skills

Bake the workflow into `.claude/skills/`:

- `sindy-episode`: turn a docs guide into a storyboard (intro, talk, slide,
  terminal, outro), a narration `script.json` and an `index.html` built on
  `lib/scenes.js`; then voice, lint, snapshot, render and QA (glitch scan,
  audio sync, mouth statistics, caption overlap), and add the shortcode to the
  page. When a guide with an episode changes, its script changes in the same
  PR.
- `sindy-voice`: rules for speakable scripts, lexicon upkeep, voice presets.
- The HyperFrames skills (`npx hyperframes skills update`) as a dependency for
  composition rules.
- Real terminal output: record asciinema casts of the documented commands in
  CI, where Docker is available, and play them back in the terminal scene.

## Sources

- HyperFrames: [repository](https://github.com/heygen-com/hyperframes),
  [skills](https://github.com/heygen-com/hyperframes/tree/main/skills)
- Kokoro: [Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M),
  [kokoro-onnx](https://github.com/thewh1teagle/kokoro-onnx)
- Lip sync: [Rhubarb Lip Sync](https://github.com/DanielSWolf/rhubarb-lip-sync),
  [VRM 1.0 expressions](https://github.com/vrm-c/vrm-specification/blob/master/specification/VRMC_vrm-1.0/expressions.md),
  [Live2D lip sync](https://docs.live2d.com/en/cubism-sdk-manual/lipsync/)
- Avatars: [three-vrm](https://github.com/pixiv/three-vrm),
  [TalkingHead](https://github.com/met4citizen/TalkingHead),
  [HeadTTS](https://github.com/met4citizen/HeadTTS),
  [Live2D SDK license](https://www.live2d.com/en/sdk/license/),
  [VRoid commercial use](https://vroid.pixiv.help/hc/en-us/articles/4405813333657-Can-I-use-the-models-created-with-VRoid-Studio-Stable-Ver-for-commercial-purposes)
- Generative: [HeyGen Avatar IV](https://www.heygen.com/avatars/avatar-iv),
  [Hedra Character-3](https://www.hedra.com/models/video/hedra/character-3),
  [LivePortrait](https://liveportrait.github.io/)
- Hosted TTS: [ElevenLabs with timestamps](https://elevenlabs.io/docs/api-reference/text-to-speech/convert-with-timestamps),
  [Azure visemes](https://learn.microsoft.com/en-us/azure/ai-services/speech-service/how-to-speech-synthesis-viseme)
- Disclosure: [EU AI Act Art. 50 FAQ](https://digital-strategy.ec.europa.eu/en/faqs/transparency-obligations-under-article-50-ai-act)
