---
name: sindy-episode
description: Create or update a Sindy video episode (video/episodes/<id>/) — a guide clip for a sind docs page, a video course episode or a refresher — storyboard, narration script.json, index.html built with the Episode builder, voice, check, render, and the {{< video >}} shortcode on the page. Use whenever someone asks for a video, episode, screencast or tutorial clip for a guide, wants an existing episode changed (pacing, scenes, terminal output, narration, acting), or edits a docs page that embeds {{< video "…" >}}, because its episode must change in the same PR.
---

# Make or update a Sindy episode

An episode is a short screencast and slideshow hybrid presented by Sindy, the
AI anime avatar. It lives in `video/episodes/<id>/` and belongs to one of
three series (see "Series" below). Only two files in it are written by hand:

- `script.json`: the narration, one entry per line: `{ "id", "text" }`.
- `index.html`: the composition, a chain of scene calls on the Episode builder.

Everything else is generated and git-ignored: `vendor/` (shared runtime),
`assets/voice/` (narration audio and timings), `snapshots/`, `../renders/`. The
docs workflow renders and publishes episodes on every push to `main`/`next`,
so rendered files are never committed.

Read before writing:

- `video/README.md`, sections "Writing an episode" (builder API) and
  "Pitfalls › Authoring episodes".
- `video/episodes/quickstart/` (the reference episode) and
  `video/sindy/CHARACTER.md` (how Sindy talks and emotes).
- The `sindy-voice` skill, for writing and checking the narration.

## Series

The video course (`docs/content/course/_index.md`) has three series:

| Series | Id | Embedded on | Length | `intro` kicker, title | `outro` next |
| --- | --- | --- | --- | --- | --- |
| Guide clip | the page's file name: `quickstart` | its guide page, above the first heading | 45 s to 2 min | the docs section's title, the page title | the next page to read |
| Course episode | `course-` and the page's file name: `course-07-failure-drills` | its own page, `docs/content/course/NN-slug.md` | 3 to 5 min | `The sind course · NN`, the episode title | the next episode's page title |
| Refresher | `refresher-<topic>`: `refresher-node-states` | its section of `docs/content/course/refreshers.md` | 60 to 90 s | `Refresher`, the topic | the course episode that shows its shortened cut |

`Episode.create` takes `title: "<Page title>: <subtitle>"` and `series:
"<Page title>"` for a guide clip, `title: "NN · <Episode title>"` and
`series: "Course NN"` for a course episode, and `title: "Refresher: <topic>"`
and `series: "Refresher"` for a refresher.

Every episode opens with why: the `intro` line greets and names the topic,
and the `talk` scene (Sindy's name tag, up to three topic chips) states the
hook as the viewer's problem rather than a definition, and the use case the
episode carries through. It closes with a slide titled `Takeaways` (two or
three bullets that recap what the episode showed, never a new fact), then the
outro, whose line names what comes next. Course episodes stand on their own:
they say early what they need and create the cluster they use, their talk
kicker names the use case, and those that the course homepage lists with a
refresher carry it as a 20 to 45 s chapter `Refresher: <topic>`. Refreshers
explain background with diagrams and slides and show sind commands only where
a docs page has them. Guide clips and course episodes cover some of the same
ground; they never share lines word for word.

## Setup

Once per machine or cloud session, from `video/`:

```bash
npm ci
python3 --version          # kokoro-onnx needs 3.10 to 3.13; else make a venv from 3.12
pip install -r voice/requirements.txt
npm run voice:setup        # Kokoro model, about 350 MB, into ~/.cache/sindy-voice
npx hyperframes browser ensure
ffmpeg -hide_banner -encoders | grep -E 'libx264|libsvtav1|libopus'
export HYPERFRAMES_NO_TELEMETRY=1
```

`SINDY_VOICE_CACHE` points the voice tool at a model that is already set up
elsewhere. If a step fails, "Pitfalls › Local setup" and "› Claude Code cloud
sessions" in the README cover the known causes.

## New episode

### 1. Read the page and decide the scope

The video complements the page; it does not replace it. Pick the happy path
and the 3 to 6 moments a newcomer should see (a concept, the main commands,
the result), and leave flag tables, edge cases and alternatives to the text.
Aim for the series' length (see "Series"). Sindy speaks about 2.8 words per second, and
transitions and pauses add up, so the length is roughly the narration's word
count divided by 2.4, plus 4 seconds.

Collect every command and output you want to show **verbatim from the page**.
Never invent, adjust or "complete" terminal output: the page's samples are
what maintainers keep in sync with real runs and review, and an episode must
not be the one place where sind looks different. If a sind command
has no output on the page, show it without output (sind is silent on
success), or ask the maintainer for real output and add it to the page first.
A command that does print something (`srun hostname`, `sbatch`, `sind
doctor`) but has no output on the page must not end a terminal as if it were
silent or hung: show it in a `code` panel as what you run, or leave it out.
Showing a subset of output lines is fine; every shown line stays whole.

Commands in one terminal must form one believable session. A page's examples
are often independent of each other (deleting `worker-2` and then
`worker-[2-4]` would fail), so pick and order them so that each works after
the previous one, and read the code when unsure.

### 2. Storyboard

Write the storyboard as a table before any code, and show it to the user when
they are around: it is the cheapest point to change the plan.

| Chapter | Scene | Line ids | On screen | Cue words |
| --- | --- | --- | --- | --- |

Pick scenes by what the viewer needs to see:

| Scene | Use it for | Rules of thumb |
| --- | --- | --- |
| `intro` | always first | kicker and title per series (see "Series"); only course episodes carry numbers |
| `talk` | framing right after the intro: what you'll build, why it matters | once per episode; up to 3 chips of about 40 characters together, or they wrap |
| `slide` | a concept or a list of steps | at most 4 bullets, each landing on a cue word said in the narration |
| `diagram` | how parts relate: networks, components, what talks to what | up to about 8 nodes on a grid; build it up on cue words, nodes before the edges between them |
| `code` | a config or source file from the page, built up or highlighted on cue | copy the file verbatim (`check` compares it like output); up to about 14 lines per panel stay readable, `wide: true` for long lines |
| `terminal` | commands and their output | lines up to about 62 columns keep the full-size font; `running: true` on a server's step leaves out the next prompt |
| `terminal` with `wide: true` | wider output, e.g. tables like `sind get nodes` | up to 96 columns at full size |
| `outro` | always last | `next` per series; for a guide clip, the title of the page to read next: the page's own "going further" link, else the next page in the docs navigation |
| `custom(kind, o)` | anything else (a table, a chat, a comparison) | see "Custom scenes" below |

Keep scenes between about 5 and 20 seconds; split a long explanation over
several scenes rather than letting one slide hang. A terminal that only shows
silent commands is mostly empty, so keep it short or give it several
commands. Fast-forward long waits with a badge (`{ ff: "⏩ ~40 s later" }`)
instead of showing them; like output, a stated duration needs the page or the
maintainer behind it, otherwise write `⏩ fast-forward`.

### 3. Narration

Write `script.json` (`{ "voice": "sindy", "lines": [...] }`) following the
`sindy-voice` skill: one line per beat, short spoken sentences, commands said
the way a person says them, and cue words that occur once in their line.
Check the pronunciation before voicing, then voice:

```bash
python3 voice/sindy_voice.py phonemes -s episodes/<id>/script.json --flagged
npm run episode -- voice <id>     # cached per line; prints each line's duration
```

Voicing needs only `script.json`, so the line durations are known before the
composition is written.

### 4. Composition

Copy `episodes/quickstart/index.html` and `hyperframes.json` into the new
directory. The head (scripts, stylesheet, fonts) and the
`data-composition-variables` attribute stay as they are. Replace the comment
at the top of the script with the docs page path, and the chain with your
storyboard:

```js
const tl = gsap.timeline({ paused: true });
Episode.create({ tl, id: "<id>", title: "<page title>: <subtitle>", series: "<short title>" })
  .intro({ chapter: "…", kicker: "Usage", title: "<page title>", say: "intro" })
  .talk({ chapter: "…", title: "…", sub: "…", chips: ["…"], say: "why" })
  .slide({ chapter: "…", title: "…", say: "steps", bullets: [{ icon: "nodes", title: "…", text: "…", at: "steps:container" }] })
  .terminal({ chapter: "…", say: ["add", "check"], steps: [{ cmd: "sind create worker --count 3", at: "add:Run" }] })
  .outro({ chapter: "Wrap-up", next: "<next guide>", say: "outro" })
  .done();
window.__timelines["main"] = tl;
```

Keep `const tl = …`, `Episode.create({ tl, … })` and the
`window.__timelines["main"] = tl` line inline in the page: HyperFrames' lint
only reads inline scripts. The builder's options (`lead`, `gap`, `shot`,
`transition`, `avatar: "none"`, terminal steps, time references) are in the
README; bullet icons are the keys of `ICON_PATHS` in `lib/episode.js`.

Acting is part of the job, not decoration:

- `mood` per line and `cues: { word: mood }` at the payoff word. Neutral while
  explaining, happy when a command succeeds, joy for big wins, thinking for a
  choice or caveat, concerned for warnings, smug for a neat trick. Expressions
  are listed in `video/sindy/CHARACTER.md`; `wink` belongs to the outro.
- Slides and terminals make her glance at their content on their own; add
  `look: { word: x }` only where a glance tells something.
- Time on-screen events to the words that name them: a bullet at its
  keyword, a command at "Run", output at "shows", a highlight
  (`{ mark: "running" }`) on the word itself. Viewers read what she says.
- Leave reading time after dense output: a larger `gap` on the line, or a
  `lead` on the next scene.

### 5. Embed

Put the shortcode on the docs page now, under the front matter and any
comments, above the first heading, like
`docs/content/getting-started/quickstart.md`:

```markdown
{{< video "<id>" >}}
```

`check` compares the episode's terminal content with the page that embeds it,
so the comparison only runs once the shortcode is there. The shortcode renders
nothing until the episode is published, so docs builds never need the video
toolchain.

A course episode's page embeds it already and has this body: a lead (the use
case and what the episode shows), `## In this episode` (3 to 5 bullets that
link the reference pages), `## Commands` (every command and output the
episode shows, in order, copied verbatim from the reference pages: this is
what `check` compares against, and what viewers copy), `## Refresher: <topic>`
(one sentence and a link to its section of the refreshers page), and `## Go
deeper`. Refreshers are embedded on `docs/content/course/refreshers.md`, one
section each.

### 6. Check

```bash
npm run episode -- check <id>
```

It loads the composition and prints the timeline (chapters, and the start of
every line), then lint, then two stills per chapter (middle and end) as
contact sheets in `episodes/<id>/snapshots/`. Errors (exit code 1) cover page
errors, missing files, network fetches and narration that changed since it
was voiced. Fix all of them, and resolve every warning or say why it stays:

- `console: terminal: N columns do not fit` → `wide: true`, or show fewer
  columns by choosing another command from the page.
- `console: cue "line:word" matches N words` → the cue picked the first
  match; write `line:word#0` if that is right, or a longer prefix.
- `console: diagram …: nodes … overlap` or `… reaches outside the diagram
  area` → change `grid`, `pos` or `width`; with many columns, `shot: "mini"`
  gives the diagram the full width.
- `text cut off in a diagram node` → widen the node (`width`) or shorten the
  title; 260 px fit about 11 characters.
- `command/output line not on the docs page` (or `line of <file>` for a
  `code` scene) → copy it from the page; the
  episode is wrong, not the page. If the page is wrong, fix it in the same PR
  (a "Keep in sync" comment on the page names the test that covers it).
- `N s without narration` → dead air; shorten the wait or add a line.
- `line … is in script.json but never said` → use it or delete it.
- `no docs page embeds` → step 5.
- `command/output line on no reference page outside docs/content/course` →
  a course page's copy went stale or never came from a reference page; copy
  the current text from the reference page into the course page and the
  episode.

Then **look at the contact sheets** (open the JPGs) like a viewer would:
text clipped or overflowing its box, captions covering terminal output or a
bullet, the avatar bubble covering content, chips wrapping, a scene that is
still empty at its end, bullets that never appeared, a highlight on the wrong
text, a font too small to read. For extra stills around a moment the
timeline names, run inside the episode directory:

```bash
npx hyperframes snapshot --no-end --describe false -o snapshots/extra --at 41.5,42,42.5
```

`check` deletes `snapshots/` on every run. `--quick` skips lint and the
stills while iterating on timing.

### 7. Render and review

```bash
npm run episode -- render <id> --draft    # renders/<id>.mp4, fast
npm run episode -- render <id>            # standard quality with burned-in captions
```

A render takes about 3 times the video's length on 4 CPUs. In a cloud
session, send the MP4 to the user (`SendUserFile`, if you have it) or give
its path, instead of describing it.

You cannot hear the result. Before calling an episode done, ask a human to
watch it and listen for mispronounced words (list the ones `phonemes
--flagged` showed and the lexicon entries you added), pacing, and lines that
sound flat. The maintainer renders locally while episodes are developed.

To see the episode on its docs page (README, "Run it"): `npm run episode --
publish <id>`, then `hugo server` in `docs/`.

### 8. Commit and PR

- Commit `script.json`, `index.html` and `hyperframes.json` only;
  `git status` must not list generated files.
- Separate commits: `feat(video): add the <id> episode`, then
  `docs(<id>): embed the <id> episode`. Lexicon or library changes get
  their own commits.
- PRs target `next`; follow the `steward` skill. `make lint-docs` covers the
  page. CI renders the episode after merge; the `/next/` docs preview shows it.

## Update an episode

When a docs page with `{{< video "<id>" >}}` changes, check its episode and
update it in the same PR:

1. `npm run episode -- voice <id>` (narration is not committed, so a fresh
   checkout needs it; it is cached afterwards), then `npm run episode --
   check <id> --quick`, which lists terminal content that no longer matches
   the page. Copy the new commands and output into `index.html`.
2. If the change affects what Sindy says (a renamed flag, a new step), edit
   those lines in `script.json` and re-run `voice`; unchanged lines are
   cached. Time references to changed lines may need new cue words.
3. Run the full `check`, look at the stills of the changed chapters, and
   render a draft.

Pure prose edits on the page usually need no episode change; `check` passing
is the signal.

Course pages copy commands and output from the reference pages. When a
reference page's commands or sample output change, grep
`docs/content/course/` for the old text and update those course pages and
their episodes as well; `check` on a course episode warns about copies that
no reference page has any more.

## Custom scenes

`custom(kind, o)` begins a scene (transition, shot, chapter) and returns
`{ el, t }` instead of the chain. Fill `el` with DOM, animate on the episode's
timeline, then continue with the API object:

```js
const ep = Episode.create({ tl, id: "networking", title: "…", series: "Networking" })
  .intro({ … })
  .talk({ … });
const { el, t } = ep.custom("compare", { chapter: "Two ways", shot: "cornerR" });
el.innerHTML = `<div class="bg-glow"></div><table class="compare">…</table>`;
Scenes.enter(ep.tl, el.querySelector(".compare"), t + 0.5);
ep.P.wait(0.6);
ep.say({ id: "compare", mood: "neutral" });
ep.outro({ … }).done();
```

Style it in the page's `<style>` with the palette variables from
`lib/scenes.css`. Everything must be a pure function of the timeline: no
`Math.random`, `Date.now`, `setTimeout`, CSS animations or network requests;
HyperFrames renders frames in any order on parallel workers.

Move a scene into `lib/episode.js` only when a second episode needs it. A
new slide layout (a table, say) builds on `slideFrame(kind, o)` there, as
`slide`, `diagram` and `code` do: it gets the chapter label, title, corner shot,
narration and cue timing for free.
Changing `lib/`, the voice presets or the lexicon re-renders **every**
episode in CI and can change all of them, so run `npm run episode -- voice
--all` (a fresh checkout has no narration) and `npm run episode -- check
--all` afterwards, and look at every episode's stills.

## Errors

| Error | Cause and fix |
| --- | --- |
| `voice line "x" missing` | run `npm run episode -- voice <id>` |
| `line "x" has not been said yet` | time references can only point at lines said in this or an earlier scene |
| `word "x" not found in: …` | the cue must be the start of a word in that line (case and punctuation ignored); use `x#1` for the second match |
| `bad time reference` | write `"line:word"`, `"line:word#n"`, `"line:word+0.5"` or seconds |
| `diagram …: group/edge/pulse names no node` | ids in `around`, `from`, `to` and `pulse` must be node ids (edges also take group ids) |
| `diagram …: reveal "x" matches nothing` | the selector must match an element inside the diagram's `svg` |
| lint: missing timeline or duration source | the page must create `tl`, pass it to `Episode.create` and register it on `window.__timelines` inline |
| lint: `font_family_without_font_face` | use only Inter and JetBrains Mono, which the copied head declares |
| `fetches from the network` | load everything from `vendor/` or the episode directory |
| a frame flashes at a worker boundary | `npx hyperframes render --workers 1`, then find the non-deterministic code |
