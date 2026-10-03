#!/usr/bin/env python3
# SPDX-License-Identifier: LGPL-3.0-or-later
"""Sindy's voice: local Kokoro-82M TTS with phoneme, word and viseme timings.

    sindy_voice.py setup                      download + patch the model (once)
    sindy_voice.py say "Hello!" -o out/hello  -> out/hello.wav + out/hello.json
    sindy_voice.py script lines.json -o dir   -> one wav/json pair per line
    sindy_voice.py samples -o dir             voice audition pack

The upstream ONNX export only returns audio. `setup` adds the duration
predictor's per-token frame counts as a second output named "duration", which
kokoro-onnx then turns into phoneme timings. Words are phonemized one at a
time so every phoneme maps back to exactly one word, and lexicon.json fixes
words espeak gets wrong (Slurm commands, acronyms).
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.request
import wave
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
CACHE = Path(os.environ.get("SINDY_VOICE_CACHE", Path.home() / ".cache" / "sindy-voice"))
RELEASE = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0"
MODEL = CACHE / "kokoro-v1.0-timed.onnx"
VOICES = CACHE / "voices-v1.0.bin"
SAMPLE_RATE = 24000
ENVELOPE_FPS = 100

# Phoneme -> viseme (see VISEMES in lib/sindy.js). Kokoro's vocabulary also
# uses single letters for diphthongs: A=eɪ I=aɪ O=oʊ W=aʊ Y=ɔɪ.
VISEME_OF = {}
for chars, vis in [
    ("aɑæ", "aa"), ("ɐʌəɚᵊɜ", "ah"), ("eɛ", "ee"), ("iɪɨ", "ih"), ("ɔɒo", "oh"), ("uʊɯ", "ou"),
    ("AI", "aa"), ("OWY", "oh"),
    ("pbm", "mbp"), ("fv", "fv"), ("θð", "th"), ("tdnkɡgŋhçxɾ", "cdg"), ("szʦʣ", "sz"),
    ("ʃʒʧʤ", "ch"), ("lɫ", "l"), ("ɹr", "r"), ("wʍ", "w"), ("jʲ", "ih"),
]:
    for c in chars:
        VISEME_OF[c] = vis
STRESS = "ˈˌ"
LENGTH = "ːˑ"
PAUSE = ".,!?;:—…\"()"
# Visemes whose weight is reduced when unstressed.
VOWELS = {"aa", "ah", "ee", "ih", "oh", "ou"}


def die(msg):
    sys.stderr.write(f"sindy_voice: {msg}\n")
    sys.exit(1)


# --------------------------------------------------------------- setup --
def setup(_args):
    CACHE.mkdir(parents=True, exist_ok=True)
    raw = CACHE / "kokoro-v1.0.onnx"
    for path in (raw, VOICES):
        if path == raw and MODEL.exists():
            continue
        if not path.exists():
            print(f"downloading {path.name} ...")
            urllib.request.urlretrieve(f"{RELEASE}/{path.name}", path)
    if not MODEL.exists():
        import onnx
        from onnx import TensorProto, helper

        print("exposing the duration predictor as a model output ...")
        m = onnx.load(str(raw))
        names = {o for n in m.graph.node for o in n.output}
        src = "/encoder/Gather_output_0"
        if src not in names:
            die(f"tensor {src} not found; the upstream export changed")
        m.graph.node.append(helper.make_node("Identity", [src], ["duration"], name="expose_duration"))
        m.graph.output.append(helper.make_tensor_value_info("duration", TensorProto.INT64, ["sequence_length"]))
        onnx.save(m, str(MODEL))
    raw.unlink(missing_ok=True)  # only the patched model is used
    print(f"ready: {MODEL}")


# ---------------------------------------------------------------- core --
class Voice:
    def __init__(self, preset="sindy"):
        if not MODEL.exists():
            die("model missing, run: sindy_voice.py setup")
        from kokoro_onnx import Kokoro

        self.k = Kokoro(str(MODEL), str(VOICES))
        presets = json.loads((HERE / "voices.json").read_text())
        if preset in presets:
            self.preset = presets[preset]
        else:
            self.preset = {"mix": {preset: 1.0}, "speed": 1.0, "lang": "en-us"}
        self.name = preset
        lex = json.loads((HERE / "lexicon.json").read_text())
        self.lexicon = {k.lower(): v for k, v in lex["words"].items()}
        self.style = self._blend(self.preset["mix"])

    def _blend(self, mix):
        total = sum(mix.values())
        return sum(self.k.get_voice_style(name) * (w / total) for name, w in mix.items()).astype(np.float32)

    def phonemize_word(self, word):
        key = word.lower()
        if key in self.lexicon:
            return self.lexicon[key]
        lang = self.preset.get("lang", "en-us")
        return self.k.tokenizer.phonemize(word, lang=lang)

    def tokenize(self, text):
        """Split text into words (with phonemes) and punctuation pauses."""
        items = []
        for raw in re.findall(r"[^\s]+", text):
            m = re.match(r'^([("\']*)(.*?)([.,!?;:)"\'…—]*)$', raw)
            lead, core, trail = m.groups()
            if core:
                items.append({"text": core, "display": raw, "ph": self.phonemize_word(core)})
            for p in trail:
                if p in ".,!?;:…—":
                    items.append({"pause": p})
        return items

    def synth(self, text):
        items = self.tokenize(text)
        parts = []
        for it in items:
            if "pause" in it:
                if parts:
                    parts[-1] = parts[-1].rstrip()
                parts.append(it["pause"] + " ")
            else:
                parts.append(it["ph"] + " ")
        phonemes = "".join(parts).strip()
        audio, sr, timings = self.k.create_timed(
            phonemes,
            voice=self.style,
            speed=self.preset.get("speed", 1.0),
            lang=self.preset.get("lang", "en-us"),
            is_phonemes=True,
        )
        if not timings:
            die("model returned no timings; re-run setup")
        return items, phonemes, np.asarray(audio, dtype=np.float32), sr, timings


def align_words(items, timings):
    """Walk the timed phoneme stream and cut it at the known word boundaries."""
    words = []
    stream = [t for t in timings]
    i = 0
    for it in items:
        if "pause" in it:
            continue
        target = "".join(c for c in it["ph"] if c != " ")
        # skip separators/punctuation before the word
        while i < len(stream) and (stream[i].phoneme in " " or stream[i].phoneme in PAUSE):
            i += 1
        start = stream[i].start if i < len(stream) else (words[-1]["end"] if words else 0)
        consumed = ""
        end = start
        while i < len(stream) and len(consumed) < len(target):
            p = stream[i].phoneme
            if p != " ":
                consumed += p
            end = stream[i].end
            i += 1
        words.append({"text": it["display"], "start": round(start, 3), "end": round(end, 3)})
    return words


def visemes_from(timings):
    """Merge stress/length marks into neighbours and map phonemes to visemes."""
    out = []
    pending_stress = 0.0
    pending_start = None
    for t in timings:
        p = t.phoneme
        if p in STRESS:
            pending_stress = 1.0
            pending_start = t.start if pending_start is None else pending_start
            continue
        if p in LENGTH:
            if out:
                out[-1][1] = t.end
            continue
        if p == " ":
            # Split word gaps between neighbours instead of closing the mouth.
            if out:
                out[-1][1] = (t.start + t.end) / 2
            pending_start = (t.start + t.end) / 2
            continue
        start = pending_start if pending_start is not None else t.start
        pending_start = None
        if p in PAUSE:
            out.append([start, t.end, "sil", 1.0])
            pending_stress = 0.0
            continue
        vis = VISEME_OF.get(p, "cdg")
        w = 1.0
        if vis in VOWELS:
            w = 1.0 if pending_stress else (0.6 if p in "əᵊɐ" else 0.8)
            pending_stress = 0.0
        out.append([start, t.end, vis, w])
    return [[round(a, 3), round(b, 3), v, w] for a, b, v, w in out if b > a]


def envelope(audio, sr):
    hop = sr // ENVELOPE_FPS
    n = len(audio) // hop + 1
    padded = np.pad(audio, (0, n * hop - len(audio)))
    rms = np.sqrt((padded.reshape(n, hop) ** 2).mean(axis=1))
    ref = np.percentile(rms[rms > 1e-4], 95) if np.any(rms > 1e-4) else 1.0
    return [round(float(v), 3) for v in np.clip(rms / ref, 0, 1.2)]


def write_wav(path, audio, sr):
    pcm = (np.clip(audio, -1, 1) * 32767).astype("<i2")
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sr)
        w.writeframes(pcm.tobytes())


def pitch_shift(audio, sr, semitones):
    """Formant-preserving pitch shift (ffmpeg rubberband); keeps the length, so timings hold."""
    with tempfile.TemporaryDirectory() as tmp:
        src, dst = Path(tmp) / "in.wav", Path(tmp) / "out.wav"
        write_wav(src, audio, sr)
        ratio = 2 ** (semitones / 12)
        subprocess.run(
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", str(src),
             "-af", f"rubberband=pitch={ratio:.5f}:formant=preserved:transients=smooth", str(dst)],
            check=True,
        )
        with wave.open(str(dst)) as w:
            out = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype(np.float32) / 32768
    out = out[: len(audio)]
    return np.pad(out, (0, len(audio) - len(out)))


def render(voice, text, base, lead=0.0, tail=0.15):
    """Synthesize `text` to base.wav/base.json. `lead` seconds of silence first."""
    items, phonemes, audio, sr, timings = voice.synth(text)
    if voice.preset.get("pitch"):
        audio = pitch_shift(audio, sr, voice.preset["pitch"])
    peak = float(np.max(np.abs(audio))) if len(audio) else 0
    if peak > 0:
        audio = audio * (0.89 / peak)  # -1 dBFS peak
    pad0 = np.zeros(int(lead * sr), dtype=np.float32)
    pad1 = np.zeros(int(tail * sr), dtype=np.float32)
    audio = np.concatenate([pad0, audio, pad1])

    class Shifted:
        def __init__(self, t):
            self.phoneme, self.start, self.end = t.phoneme, t.start + lead, t.end + lead

    timings = [Shifted(t) for t in timings]
    base = Path(base)
    base.parent.mkdir(parents=True, exist_ok=True)
    write_wav(base.with_suffix(".wav"), audio, sr)
    meta = {
        "text": text,
        "voice": voice.name,
        "duration": round(len(audio) / sr, 3),
        "sampleRate": sr,
        "phonemes": phonemes,
        "words": align_words(items, timings),
        "visemes": visemes_from(timings),
        "envelope": {"fps": ENVELOPE_FPS, "values": envelope(audio, sr)},
    }
    base.with_suffix(".json").write_text(json.dumps(meta, ensure_ascii=False, separators=(",", ":")))
    return meta


# ----------------------------------------------------------------- cli --
def cmd_say(args):
    v = Voice(args.voice)
    meta = render(v, args.text, args.out, lead=args.lead)
    print(f"{args.out}.wav  {meta['duration']}s  {len(meta['words'])} words")


def cmd_script(args):
    """lines.json: {"voice": "sindy", "lines": [{"id": "intro-1", "text": "..."}]}"""
    spec = json.loads(Path(args.file).read_text())
    v = Voice(args.voice or spec.get("voice", "sindy"))
    out = Path(args.out)
    index = []
    for line in spec["lines"]:
        digest = hashlib.sha256(json.dumps([line["text"], v.name, v.preset, v.lexicon], sort_keys=True).encode()).hexdigest()[:12]
        base = out / line["id"]
        meta_path = base.with_suffix(".json")
        if meta_path.exists() and json.loads(meta_path.read_text()).get("hash") == digest and not args.force:
            meta = json.loads(meta_path.read_text())
        else:
            meta = render(v, line["text"], base, lead=line.get("lead", 0.0))
            meta["hash"] = digest
            meta_path.write_text(json.dumps(meta, ensure_ascii=False, separators=(",", ":")))
        index.append({"id": line["id"], "duration": meta["duration"], "text": line["text"]})
        print(f"{line['id']:<24} {meta['duration']:6.2f}s  {line['text'][:60]}")
    (out / "index.json").write_text(json.dumps(index, indent=1, ensure_ascii=False))
    # Compositions load timings synchronously from a script tag (no fetch at render time).
    bundle = {line["id"]: json.loads((out / f"{line['id']}.json").read_text()) for line in spec["lines"]}
    for meta in bundle.values():
        meta.pop("phonemes", None)
    (out / "lines.js").write_text("window.SINDY_LINES = " + json.dumps(bundle, ensure_ascii=False, separators=(",", ":")) + ";\n")


def cmd_samples(args):
    text = args.text or "Hi, I'm Sindy! Today we'll spin up a Slurm cluster in Docker with sind, and run our first job with srun."
    out = Path(args.out)
    presets = json.loads((HERE / "voices.json").read_text())
    names = list(presets) + ["af_heart", "af_bella", "af_nicole", "af_sky", "af_aoede", "af_kore", "af_nova", "bf_emma"]
    for name in names:
        v = Voice(name)
        meta = render(v, text, out / name)
        print(f"{name:<14} {meta['duration']:5.2f}s")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("setup").set_defaults(fn=setup)
    p = sub.add_parser("say")
    p.add_argument("text")
    p.add_argument("-o", "--out", required=True, help="output base path (no extension)")
    p.add_argument("-v", "--voice", default="sindy")
    p.add_argument("--lead", type=float, default=0.0)
    p.set_defaults(fn=cmd_say)
    p = sub.add_parser("script")
    p.add_argument("file")
    p.add_argument("-o", "--out", required=True)
    p.add_argument("-v", "--voice")
    p.add_argument("--force", action="store_true")
    p.set_defaults(fn=cmd_script)
    p = sub.add_parser("samples")
    p.add_argument("-o", "--out", required=True)
    p.add_argument("--text")
    p.set_defaults(fn=cmd_samples)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
