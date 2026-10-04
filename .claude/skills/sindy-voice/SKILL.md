---
name: sindy-voice
description: Write and check Sindy's narration for sind video episodes — speakable script.json lines, pronunciation checks and fixes in video/voice/lexicon.json, voicing with Kokoro, and the voice presets. Use whenever narration for an episode is written or edited, a word is or might be mispronounced (Slurm commands, acronyms, versions, paths, flags), or the voice, speed or presets come up.
---

# Sindy's voice

Sindy speaks through Kokoro-82M, a local TTS model, driven by
`video/voice/sindy_voice.py`. Each line of an episode's `script.json` becomes
a WAV file plus exact word and mouth-shape timings, which drive captions, lip
sync and every cue in the episode. You cannot listen to the result, so the
job is to write text that is hard to mispronounce, check the phonemes you can
read, and hand a human a short list of what to listen for.

Settled decisions (ask the maintainer before changing any of them, because
each re-voices and re-renders every episode):

- Preset `sindy` in `voice/voices.json`: 60% `af_heart`, 40% `af_bella`,
  speed 1.05.
- "sind" is said like *sinned* (`sˈɪnd`), Sindy like *Cindy* (`sˈɪndi`).
- No background music.

## Writing lines

`script.json` is `{ "voice": "sindy", "lines": [{ "id": "intro", "text": "…" }] }`.
One line is one beat of the episode: the unit that scenes say, captions break
on and cues refer to.

- **One or two short sentences per line**, about 25 words at most. Over
  roughly 50 words the model refuses the line (510 phonemes).
- **Spoken English, not docs English.** Contractions (we'll, it's, you're),
  "you" for the viewer, "we" for doing things together. Sindy is cheerful,
  curious and precise, with light humor and no hype or sarcasm (see
  `video/sindy/CHARACTER.md`). Say what is about to appear before it
  appears: "Now, sind get clusters shows that it's up and running."
- **Say commands the way a person would.** The screen shows the exact
  command; the narration names it. Plain command words read well (`sind
  create cluster`, `sind get nodes`), and Slurm commands are in the lexicon.
  Leave out flags' dashes and symbols: "with the count flag" or "add three
  workers" instead of `--count 3`, "the data directory" instead of `/data`,
  "worker zero" instead of `worker-0`, "twenty-six oh five" instead of
  `26.05`. Two dashes (`--`) are read as nothing at all.
- **Punctuation is prosody.** A period is a full stop, a comma a short pause,
  an exclamation mark adds energy, a question mark rises, an em dash (—)
  pauses. Avoid parentheses and semicolons. Captions also break at commas,
  so commas in long sentences help readability twice.
- **Cue words.** Scenes time events to words: `"what:network"` means the
  first word in line `what` that starts with "network" (case and punctuation
  ignored). Make sure each word you will cue on appears in the line, ideally
  once; otherwise the episode uses `word#1` for the second match.
- **Length budget.** Sindy speaks about 2.8 words per second. Read each line
  against what is on screen meanwhile: a command plus its output needs the
  line to last at least as long as typing and reading take.

## Checking pronunciation

Before voicing, list how every word will be pronounced, from `video/`:

```bash
python3 voice/sindy_voice.py phonemes -s episodes/<id>/script.json --flagged
python3 voice/sindy_voice.py phonemes -s episodes/<id>/script.json   # every word
python3 voice/sindy_voice.py phonemes "sacctmgr shows the accounts"  # any text
```

Each row shows the word, its phonemes and their source (`lexicon` or
`espeak`). `--flagged` shows only the words worth a look: acronyms, digits,
symbols, acronym plurals that lose their s (`CPUs` sounds like `CPU`),
consonant clusters that suggest jargon (`sacctmgr`), and words that come out
silent. Read the IPA of every flagged word and of every sind, Slurm or tool
name, even unflagged ones: espeak guesses unknown words from their spelling,
so `sacctmgr` comes out as one word, *sackt-mger*. Also read the full list
for words that are both noun and verb (interrupt, record, present, object,
increase): espeak picks one stress for both, often the wrong one.

Reading espeak IPA: `ˈ` marks primary stress before the stressed syllable,
`ˌ` secondary stress, `ː` a long vowel. `ɪ` as in *sit*, `i` as in *see*, `ɛ`
*bed*, `æ` *cat*, `ʌ` *cup*, `ɑ` *father*, `ɔ` *law*, `ʊ` *put*, `u` *food*,
`ə` *about*, `ɚ` *butter*, `ɜː` *bird*, `eɪ` *day*, `aɪ` *my*, `oʊ` *go*, `aʊ`
*now*, `ɹ` r, `ʃ` *sh*, `ʒ` *measure*, `tʃ` *ch*, `dʒ` *j*, `θ` *thin*, `ð`
*this*, `ŋ` *sing*, `ɾ` the flapped t in *data*.

## Fixing a mispronunciation

Prefer the cheapest fix that lasts:

1. **Rephrase** when the word is a one-off or should not be spoken at all:
   "worker zero", "the count flag", "version twenty-six oh five", "a CPU
   limit of two" instead of "two CPUs", "cancel" instead of "interrupt".
2. **Add a lexicon entry** in `voice/lexicon.json` when the term recurs
   (Slurm commands and daemons, tools, acronyms). Keys are whole words, matched
   case-insensitively with surrounding punctuation stripped; values are
   phonemes, with spaces between spoken parts. Build the value from pieces the
   tool prints for words that sound right:

   ```bash
   python3 voice/sindy_voice.py phonemes "S account manager"
   # S → ˈɛs, account → ɐkˈaʊnt, manager → mˈænɪdʒɚ
   ```

   then add `"sacctmgr": "ˈɛs ɐkˈaʊnt mˈænɪdʒɚ"` and re-run `phonemes` to see
   the entry used. Use only symbols that the tool prints; Kokoro silently drops
   anything outside its vocabulary. Follow the existing entries: Slurm's
   `s` commands are spelled "ess" plus the word (`squeue` → "ess queue"),
   daemons end in a spoken "dee" (`slurmd`).

Every voiced line's cache key includes the whole lexicon, so a lexicon change
re-voices every line of every episode and re-renders them all in CI. Batch
lexicon edits, and give them their own commit
(`feat(video): pronounce sacctmgr`).

## Voicing

```bash
npm run episode -- voice <id>     # writes episodes/<id>/assets/voice/, git-ignored
```

It prints each line's duration and caches lines by text, preset and lexicon;
`python3 voice/sindy_voice.py script episodes/<id>/script.json -o
episodes/<id>/assets/voice --force` re-voices everything. For quick
experiments outside an episode:

```bash
python3 voice/sindy_voice.py say "Run sind create cluster." -o renders/try/a
```

writes `renders/try/a.wav` and `.json` (words, visemes) for a human to
compare alternatives.

## What to hand a human

The agent cannot hear tone, pacing or a wrong stress. When an episode is ready
for review, list for the listener:

- every word `--flagged` showed and every lexicon entry added or changed,
  with the line it is in;
- lines where the meaning depends on emphasis or a question intonation;
- names of people, projects or sites.

## Presets and the model

`voice/voices.json` maps preset names to a voice blend (`mix`), `speed`,
optional `pitch` in semitones (needs FFmpeg's `rubberband` filter, formants
preserved) and `lang`. `npm run voice:samples` writes an audition pack of
all presets and stock voices to `renders/voice-samples/`. `npm run
voice:setup` downloads the model from the kokoro-onnx GitHub release and
patches it to expose phoneme durations; `SINDY_VOICE_CACHE` overrides its
location (default `~/.cache/sindy-voice`). kokoro-onnx needs Python 3.10
to 3.13; README "Pitfalls › Voice" lists the known failures.
