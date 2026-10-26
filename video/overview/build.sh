#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Render the overview video from its recordings (record.py) into OUT:
#   overview.webm   AV1 and Opus
#   overview.jpg    the poster
#   overview-thumb.jpg  the poster with a play button, for the README
#   overview.vtt    the chapters, as WebVTT
#   overview.json   title, length, size and chapters, for the docs
#
#   build.sh RECORDINGS OUT [render.mjs options]
#
# Sindy narrates narration.json over the lo-fi music bed. Needs node with
# video/node_modules, Chrome (CHROME), python3 with numpy and the voice
# tool (video/voice: requirements.txt, then sindy_voice.py setup), and
# ffmpeg with libsvtav1 and libopus.
set -euo pipefail

if [ $# -lt 2 ]; then
  echo "usage: build.sh RECORDINGS OUT [render.mjs options]" >&2
  exit 2
fi
here=$(cd "$(dirname "$0")" && pwd)
rec=$1 out=$2
shift 2
work="$out/work"
mkdir -p "$work"

python3 "$here/../voice/sindy_voice.py" script "$here/narration.json" -o "$work/voice"
node "$here/compositor/render.mjs" --rec "$rec" --out "$work" --voice "$work/voice" "$@"
python3 "$here/sound.py" "$work/video.json" "$work/sound.wav" --voice "$work/voice"
ffmpeg -hide_banner -loglevel error -y -i "$work/video.mkv" -i "$work/sound.wav" \
  -map 0:v -map 1:a -c:v copy -c:a libopus -b:a 128k -shortest "$out/overview.webm"
ffmpeg -hide_banner -loglevel error -y -i "$work/poster.jpg" -vf scale=1280:-2 -q:v 3 "$out/overview.jpg"
ffmpeg -hide_banner -loglevel error -y -i "$work/thumb.jpg" -vf scale=1280:-2 -q:v 3 "$out/overview-thumb.jpg"

python3 - "$work/video.json" "$out" <<'PY'
import json, os, sys

video = json.load(open(sys.argv[1]))
out = sys.argv[2]
dur = video["duration"]


def clock(t):
    return f"{int(t // 60)}:{int(t % 60):02d}"


def stamp(t):
    ms = round(t * 1000)
    return f"{ms // 3600000:02d}:{ms // 60000 % 60:02d}:{ms // 1000 % 60:02d}.{ms % 1000:03d}"


ch = video["chapters"]
vtt = ["WEBVTT", ""]
for i, c in enumerate(ch):
    end = ch[i + 1]["start"] if i + 1 < len(ch) else dur
    vtt += [str(i + 1), f"{stamp(c['start'])} --> {stamp(end)}", c["title"], ""]
open(os.path.join(out, "overview.vtt"), "w").write("\n".join(vtt))
manifest = {
    "title": "A tour of sind",
    "duration": dur,
    "length": clock(dur),
    "bytes": os.path.getsize(os.path.join(out, "overview.webm")),
    "chapters": [{"title": c["title"], "start": c["start"], "time": clock(c["start"])} for c in ch],
}
json.dump(manifest, open(os.path.join(out, "overview.json"), "w"), indent=2)
print(f"overview: {clock(dur)}, {manifest['bytes'] / 1048576:.1f} MB, {len(ch)} chapters")
if video.get("missing"):
    print("missing recordings: " + ", ".join(video["missing"]), file=sys.stderr)
PY
