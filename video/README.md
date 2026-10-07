# sind videos

sind's tutorial videos are short screencast and slideshow hybrids presented
by Sindy, an AI-voiced virtual presenter. They play on the docs pages they
show. This directory is a project of the [avatars][avatars] library: it keeps
sind's brand, pronunciations, one check and the episodes. The library does
the rest: the avatar, the scenes, the voice, the checks, rendering and
publishing.

![Sindy in the hoodie look, in her eight moods](https://raw.githubusercontent.com/dennisklein/avatars/v0.1.0/docs/images/sindy-hoodie.png)

The library's sheet shows its demo emblem; in sind's episodes the hoodie and
the hair clip carry the sind emblem from `brand/`.

## The series

Every episode belongs to one of three series. The docs' Video Course page
(`docs/content/course/_index.md`) lists all three:

| Series | Id | Embedded on | Length | Episodes |
| --- | --- | --- | --- | --- |
| Guide clip | the page's file name: `cluster-lifecycle` | the top of its guide page | 45 s to 2 min | 19 |
| Course episode | `course-` and the page's file name: `course-07-failure-drills` | its own page, `docs/content/course/NN-slug.md` | 3 to 5 min | 10 |
| Refresher | `refresher-<topic>`: `refresher-node-states` | its section of `docs/content/course/refreshers.md` | 60 to 90 s | 8 |

The course is numbered, but each episode stands on its own. Refreshers
explain background, such as Slurm, Docker networks or MPI, and most course
episodes carry a short cut of one.

`episodes/quickstart/` was the pilot. No page embeds it: the release prep in
[#101](https://github.com/GSI-HPC/sind/pull/101) took it off the quickstart
page. `check` warns about that, and the docs workflow still renders it.

The `sindy-episode` skill (`.claude/skills/sindy-episode/SKILL.md`) has the
rules for each series: ids, titles, how an episode opens and closes, and the
course pages.

## Layout

```text
video/
  package.json        the avatars library, the only dependency
  package-lock.json   pins the library to a commit
  avatars.json        cast, theme, brand, lexicons, grounding, checks, publish
  brand/              brand.json and sind's marks: logo.svg (the intro),
                      emblem.svg (the hoodie patch), emblem-badge.svg (the hair clip)
  lexicon.json        words only sind uses: "sind"
  checks/course.mjs   the course check
  episodes/<id>/      script.json (narration), index.html (scene calls), hyperframes.json
  README.md           this file
```

`avatars.json` casts Sindy in her `hoodie` look on the `midnight` theme. It
adds `lexicon.json` to the library's lexicon packs `en-us/core` and
`en-us/hpc`, compares every episode with the docs pages that embed it
(`grounding`), runs `checks/course.mjs`, and publishes into
`docs/static/videos/` and `docs/data/videos/`.

Generated files are not committed: `node_modules/`, `renders/`, and in each
episode `vendor/`, `assets/voice/`, `snapshots/` and `.hyperframes/`.

## The library

`npm ci` installs the library at the version `package-lock.json` pins,
v0.1.0, into `node_modules/@dennisklein/avatars/`. Its docs cover everything
that is not sind's own. The same files are on GitHub in
[dennisklein/avatars][avatars], tag `v0.1.0`:

| Topic | File |
| --- | --- |
| Overview and quick start | [`README.md`][a-readme] |
| The contract: file formats, the project file, the CLI | [`DESIGN.md`][a-design] |
| Writing an episode: the Episode builder, scenes, narration and acting, time references | [`docs/authoring.md`][a-authoring] |
| Voice: setup, presets, lexicons, speakable lines | [`docs/voice.md`][a-voice] |
| Commands, checks and grounding, publishing, CI, the Hugo shortcode | [`docs/pipeline.md`][a-pipeline] |
| Known problems and their fixes: setup, cloud sessions, authoring, voice | [`docs/pitfalls.md`][a-pitfalls] |
| The feasibility study the library came from: measurements, avatar options, sources | [`docs/background.md`][a-background] |
| Sindy: how she talks and emotes | [`avatars/sindy/CHARACTER.md`][a-character] |

The library's Claude Code skills (`avatars-episode` and `avatars-voice` for
episodes, `avatars-design` for avatars, looks, themes and scenes) are not in
the npm package. They are in [`skills/`][a-skills] on GitHub, and
`.claude/settings.json` loads them as the `avatars` plugin in local sessions.
The `sindy-episode` skill adds sind's conventions.

## Run it

While you work on an episode, voice, check and render it locally. The docs
workflow renders the published version.

You need Node.js 22 or later, Python 3.10 to 3.13, FFmpeg with libx264,
libsvtav1 and libopus, and Hugo for the docs preview (the docs workflow uses
0.167.0). On Fedora, take FFmpeg from RPM Fusion; the library's
`docs/pitfalls.md` has the commands.

Once per machine, in `video/`:

```bash
npm ci
python3.12 -m venv ~/.venvs/avatars     # outside the repository: git does not ignore a venv here
. ~/.venvs/avatars/bin/activate         # in every new shell
pip install -r node_modules/@dennisklein/avatars/voice/requirements.txt
npx avatars voice-setup                 # the speech model, about 350 MB
npx hyperframes browser ensure          # the browser HyperFrames renders with
```

A model that the old pipeline set up in `~/.cache/sindy-voice` works as it
is: `export AVATARS_VOICE_CACHE=~/.cache/sindy-voice` instead of
`voice-setup`.

Then, for one episode:

```bash
npx avatars phonemes cluster-lifecycle --flagged   # words the voice may get wrong
npx avatars voice cluster-lifecycle                # narration into episodes/cluster-lifecycle/assets/voice/
npx avatars check cluster-lifecycle                # timeline, warnings, course check, stills in snapshots/
npx avatars render cluster-lifecycle --draft       # renders/cluster-lifecycle.mp4
```

`--all` instead of an id runs a command for every episode; `npm run voice`,
`npm run check` and `npm run render` do that. `npx avatars help` lists every
command and option.

To see an episode on its docs page, publish it and start Hugo:

```bash
npx avatars publish cluster-lifecycle   # into docs/static/videos/ and docs/data/videos/, git-ignored
cd ../docs
mkdir -p themes/hugo-geekdoc
curl -fsSL https://github.com/thegeeklab/hugo-geekdoc/releases/download/v4.1.3/hugo-geekdoc.tar.gz | tar -xz -C themes/hugo-geekdoc
hugo server                             # http://localhost:1313/sind/usage/cluster-lifecycle/
```

In a Claude Code cloud session the egress proxy blocks `gsi-hpc.github.io`:
check a deploy in the log of the **Deploy docs** run, not on the live site.

## Videos in the docs

A page shows an episode with one shortcode under its front matter:

```markdown
{{< video "cluster-lifecycle" >}}
```

The shortcode (`docs/layouts/shortcodes/video.html`) and its stylesheet
(`docs/assets/avatars/video.css`) are copies of the library's
`integrations/hugo/`. `docs/static/custom.css` gives the player sind's
colours (`.av-video`). The player has a poster, an English caption track
made from Sindy's word timings, and a button per chapter that jumps to it.
Under the title it shows the length and the line "Sindy is an AI-voiced
virtual presenter". The docs get no burned-in captions, so viewers can turn
them off. Players load no video until they start (`preload="none"`); only
the poster loads with the page. Until an episode is published, the shortcode renders nothing, so writing docs never
needs the video toolchain.

**Deployment.** `.github/workflows/docs.yml` builds `main` (at `/`) and
`next` (at `/next/`) together on every push to either branch that touches
`docs/`, `video/` or the workflow, and deploys one Pages artifact. There is
no `gh-pages` branch and no deploy history, so pages removed from the docs
disappear from the site. This needs the Pages source "GitHub Actions" and
`next` allowed in the `github-pages` environment. Until the next release,
`main` still carries the old peaceiris workflow: a docs push to `main`
recreates a `gh-pages` branch that Pages ignores; delete it and run
**Deploy docs** on `next`.

**Rendering in CI.** Before Hugo runs, the workflow runs `npm ci` in each
checkout's `video/`, then the library's render action
(`dennisklein/avatars/integrations/github/render`, pinned to the commit of
v0.1.0), with a store and a cache key prefix per branch. `main` has no
`video/` before the next release, so its steps are skipped until then. Each
episode has a render hash over its own files, the vendored library, the
voice cache keys of its narration, the HyperFrames version and the library's
publish code;
`npx avatars hash <id>` prints the same hash as CI for the same checkout.
Renders live in a store in the Actions cache, one entry per hash:

- A docs-only push to `next` renders nothing.
- A changed episode renders only itself, at about 3 times real time
  (render and AV1 encode) in a 4 vCPU cloud container: a 1:13 episode took
  3 min 22 s.
- A library upgrade renders the episodes whose bundle or narration it
  changes, and every episode when it changes HyperFrames or the publish
  code; `npx avatars hash --all` before and after names them.
- After the cache was evicted (a week unused), a run renders every episode
  again. The job's timeout is 180 minutes, less than rendering all of the
  course's 77 minutes of video takes, and a job that times out saves no
  renders; such a run has to be split up.

Caches belong to the branch whose push started the run: a run restores its
own branch's caches and those of `main`, the default branch, and saves both
stores under its own branch. A run on `main` therefore never sees what runs
on `next` saved, and starts with both stores empty. `main`'s store is first
saved after a release brings `video/` to `main`, so the runs after that
release render every episode of `main`, and a run on `main` those of both
checkouts: more than the 180-minute timeout allows.

The action installs FFmpeg, the voice tool's packages, the voice model and
the browser only when something must be rendered, and drops renders that no
episode uses. If an episode
fails to render, the build fails and the previous site stays online.
Rendered files are never committed.

**Format.** AV1 video and Opus audio in WebM: free codecs that Chrome, Edge
and Firefox decode without extra packages, also on distributions that ship
no H.264 decoder, such as Fedora. Safari plays AV1 only on Apple devices with
an AV1 hardware decoder; elsewhere the shortcode shows a note with a download
link under the player. On the quickstart, SVT-AV1 (preset 10, CRF 40,
visual tuning) came out smaller than the H.264 encode it replaced (CRF 28,
`-tune animation`) and closer to the master (SSIM 0.9958 against 0.9951).
VP9 needed 50% more bytes for the same quality and encoded three times
slower.

**Size.** A GitHub Pages site may take 1 GB, and `main` and `next` each
carry a copy of the videos. The web encode takes 3 to 4 MB per minute, so
the 77 minutes of the video course take up to about 300 MB per docs version.
Two copies fit, with room for the rest of the docs. The 100 GB per month
soft bandwidth limit allows roughly 300 viewings of the whole course a month.

**Fallback.** If Pages turns out too small, upload release episodes to
YouTube and point the shortcode at a privacy-enhanced embed. Every re-render
gets a new YouTube video ID.

## The course check

The course pages copy their commands and output from the reference pages
outside `docs/content/course/`. The library's grounding check compares an
episode only with the pages that embed it, so for a course episode or a
refresher it would only compare the copy. `checks/course.mjs` runs after it.
For an episode that only pages under `docs/content/course/` embed, every
command must occur on a page outside it, and every output and code line must
equal a line of such a page. Otherwise `check` warns:

```text
command on no reference page outside docs/content/course at <t> s: <command>
output line on no reference page outside docs/content/course at <t> s: <line>
line of <file> on no reference page outside docs/content/course at <t> s: <line>
```

Then the course page's copy went stale, or never came from a reference page.
Copy the current text from the reference page into the course page and the
episode.

## Upgrading the library

Move `@dennisklein/avatars` in `package.json` and `package-lock.json`
(`npm install` here), the render action's pinned commit in `.github/workflows/docs.yml` and the plugin
`ref` in `.claude/settings.json` to the same release, together.
`npx avatars hash --all` before and after the upgrade names the episodes it
re-renders; voice and check those, and look at their stills. Compare
`docs/layouts/shortcodes/video.html` and `docs/assets/avatars/video.css` with
the package's `integrations/hugo/` and copy them over when they changed.

## Results

The library grew out of a feasibility study that used
`episodes/quickstart/` as its pilot, in a 4 vCPU cloud container without a
GPU: text to speech at about 3 times real time, 45 s of 1080p30 rendered in
about 1.5 min, speech in the render within 10 ms of the plan, no glitch
frames at worker boundaries, and `hyperframes lint` clean. The library's
[`docs/background.md`][a-background] keeps the measurements and the avatar
options. Moving to the library changed no pixel and no sample: with the same
fixture narration all 37 episodes render the same frames as before, and the
voice tool writes the same WAVs.

## Decisions

- Voice: Sindy's default preset, `sindy` (Kokoro `af_heart` 60% and
  `af_bella` 40%).
- "sind" is said `/sɪnd/`, like *sinned*, rhyming with Sindy.
- Look: Sindy in the `hoodie` look, with the sind emblem on the hoodie and in
  her hair clip, on the `midnight` theme.
- No background music.
- The project stays in `video/`, next to the docs it follows.
- The docs workflow renders the episodes and the docs site serves them; no
  rendered files are committed. Move to YouTube only if GitHub Pages turns
  out too small.

[avatars]: https://github.com/dennisklein/avatars
[a-readme]: https://github.com/dennisklein/avatars/blob/v0.1.0/README.md
[a-design]: https://github.com/dennisklein/avatars/blob/v0.1.0/DESIGN.md
[a-authoring]: https://github.com/dennisklein/avatars/blob/v0.1.0/docs/authoring.md
[a-voice]: https://github.com/dennisklein/avatars/blob/v0.1.0/docs/voice.md
[a-pipeline]: https://github.com/dennisklein/avatars/blob/v0.1.0/docs/pipeline.md
[a-pitfalls]: https://github.com/dennisklein/avatars/blob/v0.1.0/docs/pitfalls.md
[a-background]: https://github.com/dennisklein/avatars/blob/v0.1.0/docs/background.md
[a-character]: https://github.com/dennisklein/avatars/blob/v0.1.0/avatars/sindy/CHARACTER.md
[a-skills]: https://github.com/dennisklein/avatars/tree/v0.1.0/skills
