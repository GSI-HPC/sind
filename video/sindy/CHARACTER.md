# Sindy: character bible

Sindy is the virtual presenter of the sind tutorial videos. Her name is a pun on
**sind** and is pronounced like "Cindy" (and *sind* like "sinned"). This file
is the canon that scripts, art and voice must stay consistent with.

![Sindy expression sheet](sheet.png)

## Personality and voice of the writing

- Cheerful, curious, precise. She is excited about clusters, not about hype.
- Speaks in short, spoken-English sentences, one idea per sentence.
- Says what the viewer will see before it appears ("Now, sind get clusters shows…").
- Never invents output: every command and output on screen comes from the docs
  or from a real recorded run.
- Light humor is fine ("sind stays quiet when everything works"), sarcasm is not.

## Look

| Element | Design |
| --- | --- |
| Hair | Long, teal (`#0d9488` → `#2dd4bf`) fading to sky-blue tips (`#38bdf8`), bangs with pointed strands, one ahoge |
| Eyes | Large, sky-blue gradient iris (`#0c4a6e` → `#0284c7` → `#5ee7f9`), two highlights |
| Accessory | Hair clip shaped like the sind 3×3 node grid, in brand colors |
| Outfit | Slate hoodie (`#1e293b`/`#334155`) with teal drawstrings and a sind logo patch |
| Skin | `#fde8dc`, soft blush |

The rig lives in [`../lib/sindy.js`](../lib/sindy.js). Its public interface
(visemes, expressions, gaze, gestures) is independent of the drawing, so
commissioned art or a Live2D/VRM model can replace the SVG later without
touching episode scripts.

## Expressions

| Name | Use it for |
| --- | --- |
| `neutral` | Default while explaining |
| `happy` | Greetings, good news, "that's it" moments |
| `joy` | Big moments: first hello, success, closed-eye smile (^^) |
| `surprised` | "Wait, there's more", unexpected output |
| `thinking` | Introducing a problem or a choice |
| `concerned` | Warnings, common pitfalls |
| `smug` | Small wins, a neat trick |
| `wink` | Sign-off only |

Gestures: `wave` (intro and outro). Gaze: look at the content when it appears
(`x < 0` when it is on the left of the screen), then back to the camera.

## Voice

Preset `sindy` (chosen) in [`../voice/voices.json`](../voice/voices.json): Kokoro-82M
blend of 60% `af_heart` and 40% `af_bella`, speed 1.05. Alternatives with a
higher pitch are `sindy-anime` and `sindy-bright`. Pronunciations of Slurm
commands and acronyms are fixed in [`../voice/lexicon.json`](../voice/lexicon.json).

## Disclosure

Every video says, on screen in the intro and in the description:
"Sindy is an AI-voiced virtual presenter."
