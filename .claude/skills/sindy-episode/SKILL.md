---
name: sindy-episode
description: Create or update a Sindy video episode of sind (video/episodes/<id>/, made with the avatars library) — a guide clip for a sind docs page, a video course episode or a refresher. Covers sind's series, ids, titles and lengths, the intro, takeaways and outro conventions, commands copied from the docs, the {{< video >}} embed and the course pages, and the commits; points to the library's avatars-episode and avatars-voice skills for the rest. Use whenever someone asks for a video, episode, screencast or tutorial clip for a guide, wants an existing episode changed (pacing, scenes, terminal output, narration, acting), or edits a docs page that embeds {{< video "…" >}}, because its episode must change in the same PR.
---

# Make or update a Sindy episode

sind's tutorial videos are presented by Sindy and made with the avatars
library ([dennisklein/avatars](https://github.com/dennisklein/avatars)).
`video/` is an avatars project: `avatars.json`, `brand/`, `lexicon.json`,
`checks/course.mjs` and one directory per episode in `video/episodes/<id>/`.
The library does the work: the Episode builder and its scenes, voice, check,
render and publish. This skill holds what is sind's own.

## The library's skills

Run `npm ci` in `video/` first. Then follow the library's `avatars-episode`
skill for everything this skill does not cover: scope, storyboard,
composition, acting, grounding, check warnings, contact sheets, render,
review, custom scenes and errors. Its `avatars-voice` skill covers the
narration: speakable lines, pronunciation and voicing.

- Where the plugin is loaded (`.claude/settings.json` enables it in local
  sessions), use `/avatars:avatars-episode` and `/avatars:avatars-voice`.
- Cloud sessions ignore the repository's plugin marketplace, and the npm
  package of v0.1.0 does not include `skills/`. Fetch the skills of the
  installed version, from `video/`:

  ```bash
  v=$(node -p 'require("@dennisklein/avatars/package.json").version')
  curl -sSL "https://raw.githubusercontent.com/dennisklein/avatars/v$v/skills/avatars-episode/SKILL.md"
  curl -sSL "https://raw.githubusercontent.com/dennisklein/avatars/v$v/skills/avatars-voice/SKILL.md"
  ```

  The guides they build on ship with the package in
  `video/node_modules/@dennisklein/avatars/docs/` (`authoring.md`,
  `voice.md`, `pitfalls.md`), and Sindy's character in
  `avatars/sindy/CHARACTER.md` there.

Commands are `npx avatars <command>`, run in `video/`; `npx avatars --help`
lists them. Where the library's skills and this one differ, this one wins.

## Series

The video course (`docs/content/course/_index.md`) has three series:

| Series | Id | Embedded on | Length | `intro` kicker, title | `outro` next |
| --- | --- | --- | --- | --- | --- |
| Guide clip | the page's file name: `cluster-lifecycle` | its guide page, above the first heading | 45 s to 2 min | the docs section's title, the page title | the next page to read: the page's own "going further" link, else the next page in the docs navigation |
| Course episode | `course-` and the page's file name: `course-07-failure-drills` | its own page, `docs/content/course/NN-slug.md` | 3 to 5 min | `The sind course · NN`, the episode title | the next episode's page title |
| Refresher | `refresher-<topic>`: `refresher-node-states` | its section of `docs/content/course/refreshers.md` | 60 to 90 s | `Refresher`, the topic | the course episode that shows its shortened cut |

`Episode.create` takes `title: "<Page title>: <subtitle>"` and `series:
"<Page title>"` for a guide clip, `title: "NN · <Episode title>"` and
`series: "Course NN"` for a course episode, and `title: "Refresher: <topic>"`
and `series: "Refresher"` for a refresher. Only course episodes carry
numbers. The episodes in the table's Id column are good models of each
series.

## Opening and closing

Every episode opens with why. The `intro` line greets and names the topic.
The `talk` scene shows Sindy's name tag (`nameTag: { name: "Sindy", role:
"your sind guide" }`, as in every episode; `nameTag: {}` takes the same
values, the name from the cast's host and the role from the brand) and up to three topic chips. It states the hook as the viewer's
problem rather than a definition, and the use case the episode carries
through. Every episode closes with a slide whose chapter and title are
`Takeaways`: two or three bullets that recap what the episode showed, never
a new fact. Then comes the outro, whose line names what comes next.

Course episodes stand on their own: they say early what they need and create
the cluster they use, and their talk kicker names the use case. Those that
the course homepage lists with a refresher carry it as a 20 to 45 s chapter
`Refresher: <topic>`; 02 and 08 have none. Refreshers explain background
with diagrams and slides and show sind commands only where a docs page has
them. Guide clips and course episodes cover some of the same ground; they
never share lines word for word.

## Commands and output

- Every command, output line and file line comes verbatim from the docs page
  that embeds the episode; `npx avatars check` compares them (`grounding` in
  `avatars.json`). Never invent, adjust or complete output. If the page is
  wrong, fix the page in the same PR; a "Keep in sync" comment on a page
  names the test that covers it.
- sind is silent on success, so a sind command without output on its page is
  shown without output. A command that does print something (`srun
  hostname`, `sbatch`, `sind doctor`) but has no output on its page goes
  into a `code` panel as what you run, or is left out. Or ask the maintainer
  for real output and add it to the page first.
- A stated duration needs the page or the maintainer behind it, like
  output: `{ ff: "⏩ ~40 s later" }` only then, otherwise
  `{ ff: "⏩ fast-forward" }`.

## Embed and course pages

Put the shortcode under the front matter and any comments, above the first
heading, like `docs/content/usage/cluster-lifecycle.md`:

```markdown
{{< video "<id>" >}}
```

`check` compares an episode only with the pages that embed it, so embed it
before the first check. The shortcode renders nothing until the episode is
published, so docs builds never need the video toolchain. The quickstart
page stays without a video.

A course episode's page embeds it and has this body: a lead (the use case
and what the episode shows), `## In this episode` (3 to 5 bullets that link
the reference pages), `## Commands` (every command and output the episode
shows, in order), `## Refresher: <topic>` (one sentence and a link to its
section of the refreshers page) and `## Go deeper`. Refreshers are embedded
on `docs/content/course/refreshers.md`, one section each, with a line that
links the course episode with the shortened cut.

Course pages copy their commands and output from the reference pages
outside `docs/content/course/`. The project check `video/checks/course.mjs`,
which `check` runs, warns about an episode that only course pages embed
when it shows a command or line that no reference page has: `command on no
reference page outside docs/content/course` or `output line on no reference
page …`. The course page's copy went stale or never came from a reference
page; copy the current text from the reference page into the course page and
the episode.

When a docs page with `{{< video "<id>" >}}` changes, check its episode
(`npx avatars check <id>`) and update it in the same PR. When a reference
page's commands or sample output change, grep `docs/content/course/` for the
old text and update those course pages and their episodes too. Pure prose
edits usually need no episode change; `check` passing is the signal.

## Narration

"sind" is said like *sinned* and Sindy like *Cindy*. Words only sind uses go
in `video/lexicon.json`. General HPC words (Slurm commands and daemons, MPI
tools) belong in the library's `en-us/hpc` pack, through a pull request to
dennisklein/avatars; until a release has them, add them to
`video/lexicon.json` too. Sindy's voice preset belongs to the avatar, and
there is no background music: ask the maintainer before changing either,
because each re-voices and re-renders every episode.

## Rendering and the docs

The docs workflow (`.github/workflows/docs.yml`) renders and publishes the
episodes of `main` and `next` with the library's render action, so rendered
files are never committed. The `/next/` docs preview shows an episode once
its PR is merged. The maintainer renders locally while episodes are
developed; in a cloud session, send a draft render to the user. To see an
episode on its page, run `npx avatars publish <id>` in `video/`, then `hugo
server` in `docs/`; `docs/.gitignore` ignores the published files.

To upgrade the library, follow "Upgrading the library" in `video/README.md`:
move `@dennisklein/avatars` in `video/package.json` and
`video/package-lock.json` (`npm install` in `video/`), the render action's
pinned commit in `.github/workflows/docs.yml` and the plugin `ref` in
`.claude/settings.json` to the same release, and copy the shortcode and
stylesheet from the package's `integrations/hugo/` again when they changed.
`npx avatars hash --all` before and after names the episodes that re-render;
voice and check them, and look at their stills.

## Commit and PR

- Commit `script.json`, `index.html` and `hyperframes.json` (and
  `episode.json` if there is one); `git status` must not list generated
  files.
- Separate commits: `feat(video): add the <id> episode`, then
  `docs(<section>): embed the <id> episode`, where the section is the docs
  directory (`docs(usage): embed the cluster-lifecycle episode`). Lexicon changes
  (`feat(video): pronounce <word>`), changes to the rest of the project
  (`avatars.json`, `brand/`, `checks/`) and library upgrades get their own
  commits.
- PRs target `next`; follow the `steward` skill. `make lint-docs` covers the
  page. CI renders the episode after merge.
