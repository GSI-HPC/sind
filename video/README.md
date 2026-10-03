# sind videos with Sindy (feasibility study)

Can coding agents produce tutorial videos for each sind guide, as a screencast
and slideshow hybrid presented by an AI anime avatar, cheaply enough to
re-render whenever the docs change? This directory is the answer so far: **yes,
on a CPU**. It contains a working pipeline and a 45-second pilot episode.

![Sindy expression sheet](sindy/sheet.png)

## Pipeline

```text
script.json ──► voice/sindy_voice.py ──► line.wav + line.json (words, visemes, loudness)
                 (Kokoro-82M, CPU)                       │
                                                         ▼
pilot/index.html  ──  lib/scenes.js (planner, captions, terminal, shots, transitions)
                  ──  lib/sindy.js  (avatar: lip sync, blinks, expressions, gaze, wave)
                                                         │
                                                         ▼
                                   HyperFrames (headless Chrome + FFmpeg) ──► MP4
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
- **Pilot** (`pilot/`): intro with a logo animation and wave, fullscreen talk
  with name tag, slide with corner avatar, terminal screencast of the
  quickstart, outro with links and wink, and a fade to black.

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
- No background music or sound effects yet. HyperFrames can mix and duck them.
- Nobody has judged the voices by ear yet; the audition pack is for that.

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
npm run voice:setup        # download Kokoro (about 350 MB) and patch it, once
npm run pilot:voice        # synthesize pilot narration into pilot/assets/voice/
npm run pilot:render       # renders/pilot.mp4
npm run voice:samples      # audition pack in renders/voice-samples/
npm run sheet              # sindy/sheet.png expression sheet
```

`npx hyperframes preview` inside `pilot/` opens the HyperFrames Studio, and
`npx hyperframes snapshot --at 5,10` takes stills.

## Layout

```text
video/
  lib/sindy.js          avatar rig
  lib/scenes.js, .css   planner, captions, terminal, shots, transitions, look
  sindy/                character bible, expression sheet page
  voice/                TTS tool, voice presets, pronunciation lexicon
  pilot/                HyperFrames project for the pilot episode
  tools/                vendoring, screenshots, lip-sync statistics
```

Generated files (`vendor/`, `assets/voice/`, `renders/`) are not committed.
Renders need no network: GSAP and the fonts come from npm, and the voice
metadata loads from a local script.

## Decisions for the maintainer

1. **Voice**: which audition sample (`npm run voice:samples`) fits Sindy, or
   should a hosted voice (ElevenLabs voice design, with timestamps) be used?
2. **Pronunciation of "sind"**: `/sɪnd/`, rhyming with Sindy (the current
   lexicon), or `/saɪnd/` by analogy with *kind*?
3. **Art direction**: keep refining the code-drawn Sindy, or commission
   layered (Live2D-ready) art for the same rig?
4. **Music**: license a royalty-free bed, or none.
5. **Home**: keep `video/` in this repo, close to the docs it follows, or move
   it to its own repository?

## Next: skills

Once the look and voice are settled, bake the workflow into
`.claude/skills/`:

- `sindy-episode`: turn a docs guide into a storyboard (intro, talk, slide,
  terminal, outro), a narration `script.json` and an `index.html` built on
  `lib/scenes.js`; then voice, lint, snapshot, render and QA (glitch scan,
  audio sync, mouth statistics, caption overlap).
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
