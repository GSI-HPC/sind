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

While developing an episode, render it locally (a laptop or a Claude Code
cloud session); the docs workflow renders the published version. Requires
Node.js 22+, Python 3.10+ and FFmpeg; HyperFrames downloads its own Chrome, or
uses `HYPERFRAMES_BROWSER_PATH`.

```bash
cd video
npm install
pip install -r voice/requirements.txt
npm run voice:setup                        # download Kokoro (about 350 MB) and patch it, once
npm run episode -- voice quickstart        # narration into episodes/quickstart/assets/voice/
npm run episode -- render quickstart       # renders/quickstart.mp4 (add --draft for speed)
npm run episode -- publish quickstart      # docs/static/videos/, docs/data/videos/ for a local Hugo preview
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
- The docs workflow renders the episodes and the docs site serves them; no
  rendered files are committed. Move to YouTube only if GitHub Pages turns out
  too small.

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

**Deployment.** `.github/workflows/docs.yml` builds `main` (at `/`) and `next`
(at `/next/`) together on every push to either branch that touches `docs/` or
`video/`, and deploys one Pages artifact. No `gh-pages` branch and no deploy
history; pages removed from the docs disappear from the site.

**Rendering in CI.** Before Hugo runs, the workflow calls `episode.mjs ci` for
each branch. Every episode has a hash over its own files and everything shared
that changes its pixels or sound (`lib/`, voice presets, lexicon, tools,
lockfile). Renders live in a store kept in the Actions cache under that hash,
so a docs-only push renders nothing, and changing one episode renders only that
one (about 2-3× real time on a 4 vCPU runner). Changing `lib/` re-renders every
episode, and so does a run after the cache was evicted (7 days unused).
Rendered files are never committed; locally they are git-ignored.

**Size.** The web encode (H.264 CRF 28, `-tune animation`) takes about 4 MB
per minute; the 45-second pilot is 2.7 MB. Ten 3-minute guides are about
120 MB per docs version, well under the 1 GB GitHub Pages site limit. The
100 GB/month soft bandwidth limit allows roughly 8,000 full views a month.

**Fallback.** If Pages turns out too small, upload release episodes to
YouTube and point the shortcode at a privacy-enhanced embed. Note that every
re-render gets a new YouTube video ID.

### One-time switch from the gh-pages branch

Do this in one sitting, right after merging the workflow into `next`:

1. Merge the PR into `next` while Pages still serves `gh-pages`. Its first
   **Deploy docs** run builds both versions; its deploy job most likely fails
   because Pages does not accept Actions deployments yet. The live site is
   unaffected.
2. **Settings → Pages → Build and deployment → Source: GitHub Actions.**
3. **Settings → Environments → `github-pages`**: if deployments are limited to
   selected branches, allow `next` as well as `main`.
4. Re-run the failed deploy job; the build artifact is reused.
5. Check `/sind/` and `/sind/next/`, then delete the `gh-pages` branch.
6. Until the next release, `main` still carries the old peaceiris workflow. If
   docs change on `main` before then, it pushes a new `gh-pages` branch that
   Pages ignores; delete it again, and run **Deploy docs** on `next` to update
   the release docs.

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
- Real terminal output: record asciinema casts of the documented commands
  against a real cluster and play them back in the terminal scene.

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
