#!/usr/bin/env python3
# SPDX-License-Identifier: LGPL-3.0-or-later
"""Synthesize the sound track of the overview video: a music bed whose
energy follows the acts, light accents on events (key clicks, clicks,
chimes for a success, a low tone for a failure), and the narration, with
the music ducked under it.

    sound.py video.json out.wav [--style STYLE] [--voice DIR]

--style picks the music bed: lofi (the default), bright, ambient or drive.
--voice names the lines that sindy_voice.py made from narration.json.
video.json comes from compositor/render.mjs: its duration, its energy
points [[time, level 0..3], ...], its sounds [[time, kind], ...] and its
narration cues [[time, line id], ...]. The music is made here from sine
waves and noise, with a fixed seed, so the same input gives the same file.
"""
import argparse
import json
import wave

import numpy as np

SR = 48000


def midi(m):
    return 440.0 * 2 ** ((m - 69) / 12)


class Track:
    """The timeline of the video and the building blocks of the beds."""

    def __init__(self, d, bpm, progs, bars_per_chord=2):
        self.dur = d["duration"]
        self.n = int(self.dur * SR) + SR
        self.rng = np.random.default_rng(7)
        self.t = np.arange(self.n) / SR
        self.beat = 60 / bpm
        self.bar = 4 * self.beat
        self.progs = progs
        self.seg = bars_per_chord * self.bar
        # Energy per sample, eased between acts; each act takes the next
        # progression, so the bed changes with the chapters.
        pts = d["energy"]
        self.starts = [p[0] for p in pts]
        xs, ys = [], []
        for t, e in pts:
            xs += [t, t + 2.0]
            ys += [ys[-1] if ys else e, e]
        xs.append(self.dur)
        ys.append(ys[-1])
        self.xs, self.ys = xs, ys
        self.energy = np.interp(self.t, xs, ys)

    def e(self, t):
        return float(np.interp(t, self.xs, self.ys))

    def chord(self, t):
        act = max(0, int(np.searchsorted(self.starts, t, side="right")) - 1)
        prog = self.progs[act % len(self.progs)]
        return prog[int(t // self.seg) % len(prog)]

    def buf(self):
        return np.zeros(self.n)

    def add(self, buf, start, sig, gain=1.0):
        i = int(start * SR)
        if i >= len(buf) or i + len(sig) <= 0:
            return
        if i < 0:
            sig, i = sig[-i:], 0
        j = min(len(buf), i + len(sig))
        buf[i:j] += sig[: j - i] * gain

    def noise_hp(self, k):
        x = self.rng.standard_normal(k)
        return np.diff(np.diff(x, prepend=0), prepend=0) / 4

    def reverb(self, x, sec, mix):
        m = int(sec * SR)
        ir = self.rng.standard_normal(m) * np.exp(-np.arange(m) / SR * (6.9 / sec))
        ir = np.convolve(ir, np.ones(8) / 8, mode="same")
        ir /= np.sqrt(np.sum(ir ** 2))
        size = 1 << int(np.ceil(np.log2(len(x) + m)))
        y = np.fft.irfft(np.fft.rfft(x, size) * np.fft.rfft(ir, size), size)[: len(x)]
        return x * (1 - mix) + y * mix * 1.6


def env(k, a, r):
    e = np.ones(k)
    na, nr = int(a * SR), int(r * SR)
    if na:
        e[:na] = np.linspace(0, 1, na)
    if nr:
        e[-nr:] *= np.linspace(1, 0, nr)
    return e


def soft_saw(f, k, harm=5, detune=0.0):
    tt = np.arange(k) / SR
    out = np.zeros(k)
    for h in range(1, harm + 1):
        out += np.sin(2 * np.pi * f * h * (1 + detune) * tt + h * 0.7) / (h ** 1.3)
    return out


def pluck(f, length=0.5, bright=1.0, decay=7.0):
    k = int(length * SR)
    tt = np.arange(k) / SR
    s = (np.sin(2 * np.pi * f * tt) + 0.5 * bright * np.sin(4 * np.pi * f * tt) * np.exp(-tt * 9)
         + 0.25 * bright * np.sin(6 * np.pi * f * tt) * np.exp(-tt * 14))
    return s * np.exp(-tt * decay) * env(k, 0.002, 0.05)


def lowpass(x, hz):
    """A gentle low-pass in the frequency domain, for a whole buffer."""
    size = 1 << int(np.ceil(np.log2(len(x))))
    spec = np.fft.rfft(x, size)
    f = np.fft.rfftfreq(size, 1 / SR)
    spec /= np.sqrt(1 + (f / hz) ** 4)
    return np.fft.irfft(spec, size)[: len(x)]


def delay(x, s):
    m = int(s * SR)
    return np.concatenate([np.zeros(m), x[:-m]])


def kick_sound(length=0.4, top=80, low=45, sweep=28, decay=9):
    kt = np.arange(int(length * SR)) / SR
    return np.sin(2 * np.pi * np.cumsum(low + top * np.exp(-kt * sweep)) / SR) * np.exp(-kt * decay), np.exp(-kt * 6)


def pad_blocks(tr, out, harm=5, attack=0.8, release=1.0, gain=1 / 12, skip_root=True):
    """A pad block for each chord, cross-faded, always on."""
    k = 0
    while k * tr.seg < tr.dur + tr.seg:
        m = int((tr.seg + 1.2) * SR)
        sig = np.zeros(m)
        notes = tr.chord(max(0.0, k * tr.seg + 0.1))
        for note in notes[1:] if skip_root else notes:
            for dt in (-0.004, 0.0, 0.0045):
                sig += soft_saw(midi(note), m, harm=harm, detune=dt)
        tr.add(out, k * tr.seg - 0.4, sig * env(m, attack, release) * gain)
        k += 1


# ----------------------------------------------------------------- the beds
# Each bed returns (left, right) without the accents.

def bed_bright(d):
    """Pop in C at 96 BPM: pad, eighth bass, sixteenth plucks, light drums."""
    tr = Track(d, 96, [
        [[48, 55, 60, 64, 67, 71], [43, 55, 59, 62, 67, 69], [45, 57, 60, 64, 67, 72], [41, 57, 60, 65, 69, 72]],
        [[45, 57, 60, 64, 67, 72], [41, 57, 60, 65, 69, 72], [48, 55, 60, 64, 67, 71], [43, 55, 59, 62, 67, 74]],
    ])
    beat, dur = tr.beat, tr.dur
    pad, bass, arp, drums, kick_env = (tr.buf() for _ in range(5))
    pad_blocks(tr, pad)
    pad *= (0.55 + 0.1 * np.sin(2 * np.pi * 0.11 * tr.t)) * (0.75 + 0.1 * tr.energy)

    # bass on eighths from energy 1, arp on sixteenths from energy 2,
    # drums from energy 2, claps at 3
    step = beat / 2
    t = 0.0
    while t < dur:
        e = tr.e(t)
        if e >= 0.8:
            m = int(step * SR * 0.95)
            tt = np.arange(m) / SR
            f = midi(tr.chord(t)[0])
            s = np.sin(2 * np.pi * f * tt) + 0.25 * np.sin(4 * np.pi * f * tt) + 0.08 * np.sin(6 * np.pi * f * tt)
            accent = 1.0 if int(round(t / step)) % 2 == 0 else 0.7
            tr.add(bass, t, s * np.exp(-tt * 5.5) * env(m, 0.005, 0.03) * accent * 0.3 * min(1, e))
        t += step
    pattern = [0, 2, 1, 3, 2, 4, 3, 1]
    t, i = 0.0, 0
    while t < dur:
        e = tr.e(t)
        if e >= 1.6:
            ch = tr.chord(t)
            notes = sorted(ch[2:]) + [ch[2] + 12]
            note = notes[pattern[i % 8] % len(notes)] + 12
            vel = 0.9 if i % 4 == 0 else 0.55
            tr.add(arp, t, pluck(midi(note), 0.45) * vel * 0.11 * min(1, e - 1.2))
        t += beat / 4
        i += 1

    kick, kenv = kick_sound()
    t = 0.0
    while t < dur - 0.01:
        e = tr.e(t)
        if e >= 1.8:
            g = min(1, e - 1.5)
            tr.add(drums, t, kick, 0.5 * g)
            tr.add(kick_env, t, kenv * g)
            m = int(0.06 * SR)
            h = tr.noise_hp(m) * np.exp(-np.arange(m) / SR * 70)
            tr.add(drums, t + beat / 2, h, 0.09 * g)
            tr.add(drums, t + beat / 4, h, 0.035 * g)
            tr.add(drums, t + 3 * beat / 4, h, 0.035 * g)
            if e >= 2.6 and int(round(t / beat)) % 2 == 1:
                m = int(0.2 * SR)
                c = tr.rng.standard_normal(m) * np.exp(-np.arange(m) / SR * 22)
                c = np.convolve(c, np.ones(6) / 6, mode="same") - np.convolve(c, np.ones(40) / 40, mode="same")
                tr.add(drums, t, c, 0.09)
        t += beat
    duck = 1 - 0.4 * np.clip(kick_env, 0, 1)
    pad *= duck
    bass *= 0.6 + 0.4 * duck

    wet = tr.reverb(pad * 0.9 + arp, 2.4, 0.35)
    left = wet + bass + drums + delay(arp, 0.011) * 0.25
    right = delay(wet, 0.013) + bass + drums + arp * 0.25
    return tr, left, right


def bed_ambient(d):
    """Calm and wide in D at 70 BPM: slow pads, a sub bass, sparse bells;
    no drums, only a soft shaker in the busiest acts."""
    tr = Track(d, 70, [
        [[38, 57, 61, 64, 66, 69], [35, 54, 57, 61, 62, 66], [43, 54, 57, 59, 62, 66], [45, 52, 57, 59, 61, 64]],
        [[35, 54, 57, 61, 62, 66], [43, 54, 57, 59, 62, 66], [38, 57, 61, 64, 66, 69], [45, 55, 57, 61, 64, 69]],
    ], bars_per_chord=2)
    beat, dur = tr.beat, tr.dur
    pad, sub, bells, shaker = (tr.buf() for _ in range(4))
    pad_blocks(tr, pad, harm=3, attack=2.2, release=2.0, gain=1 / 11)
    pad *= (0.6 + 0.12 * np.sin(2 * np.pi * 0.07 * tr.t)) * (0.8 + 0.08 * tr.energy)
    pad = lowpass(pad, 2200)

    # a sub bass that holds the root of each chord, from energy 1
    k = 0
    while k * tr.seg < dur:
        t0 = k * tr.seg
        e = tr.e(t0 + 0.1)
        if e >= 0.8:
            m = int((tr.seg + 0.6) * SR)
            tt = np.arange(m) / SR
            f = midi(tr.chord(t0 + 0.1)[0] + 12)
            s = np.sin(2 * np.pi * f * tt) + 0.15 * np.sin(4 * np.pi * f * tt)
            tr.add(sub, t0, s * env(m, 0.6, 0.8) * 0.16 * min(1, e))
        k += 1

    # bells on a pentatonic of the chord, sparse, denser with energy
    t = 0.0
    while t < dur:
        e = tr.e(t)
        if tr.rng.random() < 0.06 + 0.07 * e:
            ch = tr.chord(t)
            notes = sorted(ch[2:]) + [n + 12 for n in sorted(ch[2:])]
            note = notes[int(tr.rng.integers(len(notes)))] + 12
            tr.add(bells, t, pluck(midi(note), 2.4, bright=0.35, decay=2.2) * (0.05 + 0.02 * tr.rng.random()))
        t += beat / 2

    # a soft shaker on eighths at energy 2 and more
    t = 0.0
    while t < dur:
        e = tr.e(t)
        if e >= 1.8:
            m = int(0.09 * SR)
            tt = np.arange(m) / SR
            s = tr.noise_hp(m) * np.sin(np.pi * np.clip(tt / 0.09, 0, 1)) ** 2
            tr.add(shaker, t, s, (0.05 if int(round(t / (beat / 2))) % 2 else 0.03) * min(1, e - 1.5))
        t += beat / 2

    wet = tr.reverb(pad + bells, 4.5, 0.5)
    left = wet + sub + delay(shaker, 0.004) + delay(bells, 0.19) * 0.25
    right = delay(wet, 0.017) + sub + shaker + delay(bells, 0.29) * 0.25
    return tr, left, right


def bed_lofi(d):
    """Warm lo-fi at 82 BPM: electric piano sevenths, a round bass, swung
    soft drums and a little vinyl noise."""
    tr = Track(d, 82, [
        [[41, 57, 60, 64, 69], [40, 55, 59, 62, 67], [38, 57, 60, 64, 65], [36, 55, 59, 64, 67]],
        [[38, 57, 60, 64, 65], [43, 53, 57, 59, 64], [36, 55, 59, 64, 67], [33, 55, 60, 64, 67]],
    ], bars_per_chord=1)
    beat, dur = tr.beat, tr.dur
    swing = 0.6  # the second eighth of a beat lands at 60 %
    keys, bass, drums, kick_env = (tr.buf() for _ in range(4))

    def ep(f, length):
        k = int(length * SR)
        tt = np.arange(k) / SR
        s = (np.sin(2 * np.pi * f * tt) + 0.35 * np.sin(4 * np.pi * f * tt) * np.exp(-tt * 3)
             + 0.12 * np.sin(2 * np.pi * f * 7.01 * tt) * np.exp(-tt * 18))
        return s * np.exp(-tt * 1.6) * (1 + 0.15 * np.sin(2 * np.pi * 4.6 * tt)) * env(k, 0.004, 0.2)

    # chords: on beat one and on the swung "and" of two
    bar = 0
    while bar * tr.bar < dur:
        t0 = bar * tr.bar
        ch = tr.chord(t0 + 0.05)
        for hit, vel in ((0.0, 1.0), (1 + swing, 0.7)):
            th = t0 + hit * beat
            for j, note in enumerate(ch[1:]):
                tr.add(keys, th + j * 0.012, ep(midi(note), 2.2), 0.05 * vel)
        bar += 1
    keys = lowpass(keys, 3200)

    # bass on one and three, with a pickup into the next bar, from energy 1
    bar = 0
    while bar * tr.bar < dur:
        t0 = bar * tr.bar
        e = tr.e(t0)
        if e >= 0.8:
            root = tr.chord(t0 + 0.05)[0]
            for hit, note, ln in ((0, root, 1.6), (2, root, 1.0), (3 + swing, root + 7, 0.35)):
                m = int(ln * beat * SR)
                tt = np.arange(m) / SR
                f = midi(note)
                s = np.sin(2 * np.pi * f * tt) + 0.2 * np.sin(4 * np.pi * f * tt)
                tr.add(bass, t0 + hit * beat, s * env(m, 0.01, 0.08) * np.exp(-tt * 1.8) * 0.28 * min(1, e))
        bar += 1

    # drums from energy 1.5: a muffled kick, a soft snare, swung hats
    kick, kenv = kick_sound(0.35, top=60, low=48, sweep=30, decay=11)
    kick = lowpass(kick, 900)
    bar = 0
    while bar * tr.bar < dur:
        t0 = bar * tr.bar
        e = tr.e(t0)
        if e >= 1.4:
            g = min(1, e - 1.1)
            for hit in (0, 2 + swing):
                tr.add(drums, t0 + hit * beat, kick, 0.45 * g)
                tr.add(kick_env, t0 + hit * beat, kenv * g)
            for hit in (1, 3):
                m = int(0.18 * SR)
                tt = np.arange(m) / SR
                s = np.convolve(tr.rng.standard_normal(m), np.ones(5) / 5, mode="same") * np.exp(-tt * 20)
                s += 0.4 * np.sin(2 * np.pi * 190 * tt) * np.exp(-tt * 30)
                tr.add(drums, t0 + hit * beat, s, 0.07 * g)
            for b in range(4):
                for half, vel in ((0, 0.035), (swing, 0.022)):
                    if half and e < 2.5 and b % 2:
                        continue
                    m = int(0.05 * SR)
                    h = tr.noise_hp(m) * np.exp(-np.arange(m) / SR * 90)
                    tr.add(drums, t0 + (b + half) * beat, h, vel * g)
        bar += 1
    duck = 1 - 0.3 * np.clip(kick_env, 0, 1)
    keys *= duck

    # vinyl: a quiet hiss and sparse crackle, always on
    vinyl = lowpass(tr.rng.standard_normal(tr.n), 5000) * 0.004
    clicks = np.zeros(tr.n)
    idx = tr.rng.choice(tr.n, size=int(tr.dur * 6), replace=False)
    clicks[idx] = tr.rng.standard_normal(len(idx)) * 0.06
    vinyl += np.convolve(clicks, np.ones(3) / 3, mode="same")

    wet = tr.reverb(keys, 1.8, 0.28)
    mixed = lowpass(bass + drums, 6000)
    left = wet + mixed + vinyl
    right = delay(wet, 0.009) + mixed + delay(vinyl, 0.003)
    return tr, left, right


def bed_drive(d):
    """Synthwave in A minor at 112 BPM: a pulsing bass, a ducked saw pad,
    a bright arpeggio and four on the floor in the busy acts."""
    tr = Track(d, 112, [
        [[45, 57, 60, 64, 69], [41, 57, 60, 65, 69], [48, 55, 60, 64, 67], [43, 55, 59, 62, 67]],
        [[41, 57, 60, 65, 69], [43, 55, 59, 62, 67], [45, 57, 60, 64, 69], [40, 56, 59, 64, 68]],
    ], bars_per_chord=2)
    beat, dur = tr.beat, tr.dur
    pad, bass, arp, drums, kick_env = (tr.buf() for _ in range(5))
    pad_blocks(tr, pad, harm=7, attack=0.4, release=0.8, gain=1 / 13)
    pad *= 0.6 + 0.1 * tr.energy
    pad = lowpass(pad, 3800)

    # bass: eighths from energy 1, sixteenths from 2, root and octave
    t, i = 0.0, 0
    while t < dur:
        e = tr.e(t)
        step = beat / 4 if e >= 1.9 else beat / 2
        if e >= 0.8:
            root = tr.chord(t)[0]
            note = root + (12 if i % 4 == 2 else 0)
            m = int(step * SR * 0.9)
            tt = np.arange(m) / SR
            s = soft_saw(midi(note), m, harm=6) * np.exp(-tt * 9) * env(m, 0.003, 0.02)
            tr.add(bass, t, s * 0.16 * min(1, e))
        t += step
        i += 1
    bass = lowpass(bass, 1400)

    pattern = [0, 1, 2, 3, 4, 3, 2, 1]
    t, i = 0.0, 0
    while t < dur:
        e = tr.e(t)
        if e >= 1.4:
            ch = tr.chord(t)
            notes = sorted(ch[1:]) + [ch[1] + 12]
            note = notes[pattern[i % 8] % len(notes)] + 12
            vel = 0.9 if i % 4 == 0 else 0.6
            tr.add(arp, t, pluck(midi(note), 0.3, bright=1.2, decay=10) * vel * 0.09 * min(1, e - 1.0))
        t += beat / 4
        i += 1

    kick, kenv = kick_sound(0.4, top=110, low=48, sweep=32, decay=8)
    t = 0.0
    while t < dur - 0.01:
        e = tr.e(t)
        if e >= 1.8:
            g = min(1, e - 1.5)
            tr.add(drums, t, kick, 0.55 * g)
            tr.add(kick_env, t, kenv * g)
            m = int(0.12 * SR)
            oh = tr.noise_hp(m) * np.exp(-np.arange(m) / SR * 25)
            tr.add(drums, t + beat / 2, oh, 0.06 * g)
            if e >= 2.4 and int(round(t / beat)) % 2 == 1:
                m = int(0.25 * SR)
                tt = np.arange(m) / SR
                c = tr.rng.standard_normal(m) * np.exp(-tt * 16)
                c = np.convolve(c, np.ones(4) / 4, mode="same") - np.convolve(c, np.ones(30) / 30, mode="same")
                c += 0.5 * np.sin(2 * np.pi * 200 * tt) * np.exp(-tt * 25)
                tr.add(drums, t, c, 0.12)
        t += beat
    duck = 1 - 0.55 * np.clip(kick_env, 0, 1)
    pad *= duck
    bass *= 0.5 + 0.5 * duck

    wet = tr.reverb(pad + arp * 0.8, 2.0, 0.3)
    left = wet + bass + drums + delay(arp, beat * 0.75) * 0.18
    right = delay(wet, 0.012) + bass + drums + delay(arp, beat * 0.5) * 0.18
    return tr, left, right


BEDS = {"bright": bed_bright, "ambient": bed_ambient, "lofi": bed_lofi, "drive": bed_drive}


def accents(tr, d):
    fx = tr.buf()
    penta = [72, 74, 76, 79, 81, 84, 86, 88, 91]
    tick_i = 0
    last_key = -1.0
    for t, kind in d["sfx"]:
        if kind == "key":
            if t - last_key < 0.035:
                continue
            last_key = t
            m = int(0.025 * SR)
            tr.add(fx, t, tr.noise_hp(m) * np.exp(-np.arange(m) / SR * 260) * (0.08 + 0.03 * tr.rng.random()))
        elif kind == "enter":
            m = int(0.05 * SR)
            tr.add(fx, t, tr.noise_hp(m) * np.exp(-np.arange(m) / SR * 120) * 0.15)
        elif kind == "click":
            m = int(0.04 * SR)
            tt = np.arange(m) / SR
            s = np.sin(2 * np.pi * 1700 * tt) * np.exp(-tt * 180) * 0.5 + tr.noise_hp(m) * np.exp(-tt * 300) * 0.5
            tr.add(fx, t, s * 0.2)
        elif kind == "tick":
            tr.add(fx, t, pluck(midi(penta[tick_i % 9]), 0.7, bright=0.6) * 0.09)
            tick_i += 1
        elif kind == "whoosh":
            m = int(0.9 * SR)
            tt = np.arange(m) / SR
            s = tr.rng.standard_normal(m)
            s = np.convolve(s, np.ones(12) / 12, mode="same") - np.convolve(s, np.ones(120) / 120, mode="same")
            tr.add(fx, t - 0.45, s * np.sin(np.pi * np.clip(tt / 0.9, 0, 1)) ** 2 * 0.12)
        elif kind == "done":
            for j, note in enumerate([79, 84]):
                tr.add(fx, t + j * 0.09, pluck(midi(note), 0.9, bright=0.4) * 0.11)
        elif kind == "fail":
            for j, note in enumerate([52, 51]):
                m = int(0.5 * SR)
                tt = np.arange(m) / SR
                s = (np.sin(2 * np.pi * midi(note) * tt) + 0.3 * np.sin(4 * np.pi * midi(note) * tt)) * np.exp(-tt * 5)
                tr.add(fx, t + j * 0.16, s * env(m, 0.005, 0.1) * 0.12)
    return tr.reverb(fx, 1.2, 0.15)


def read_wav(path):
    with wave.open(str(path), "rb") as w:
        sr, ch = w.getframerate(), w.getnchannels()
        x = np.frombuffer(w.readframes(w.getnframes()), "<i2").astype(np.float64) / 32768
    x = x.reshape(-1, ch).mean(1)
    if sr != SR:  # resample in the frequency domain
        n = int(round(len(x) * SR / sr))
        x = np.fft.irfft(np.fft.rfft(x), n) * (n / len(x))
    return x


def narration(tr, d, folder):
    """The narration at its cues, and how far to duck the music (0..1)."""
    voice = tr.buf()
    for t, line in d.get("voice", []):
        tr.add(voice, t, read_wav(f"{folder}/{line}.wav"))
    k = int(0.25 * SR)
    kern = np.ones(k) / k
    talking = (np.abs(voice) > 0.01).astype(float)
    duck = np.clip(np.convolve(np.convolve(talking, kern, "same"), kern, "same") * 1.5, 0, 1)
    return voice, duck


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("video")
    ap.add_argument("out")
    ap.add_argument("--style", choices=list(BEDS), default="lofi")
    ap.add_argument("--voice", help="folder of the narration lines")
    args = ap.parse_args()
    d = json.load(open(args.video))
    tr, left, right = BEDS[args.style](d)
    fx = accents(tr, d)
    bed = np.stack([left + fx * 0.8, right + fx * 0.8], 1)
    bed *= 10 ** (-18 / 20) / np.sqrt(np.mean(bed ** 2))
    if args.voice:
        # the music sits 8 dB lower under the voice, and 4 dB lower overall
        voice, duck = narration(tr, d, args.voice)
        bed *= (10 ** (-4 / 20) * 10 ** (-8 * duck / 20))[:, None]
        rms = np.sqrt(np.mean(voice[voice != 0] ** 2)) if np.any(voice) else 1
        mix = bed + (voice * 10 ** (-17 / 20) / rms)[:, None]
    else:
        mix = bed
    dur = tr.dur
    fade = np.interp(tr.t, [0, 0.4, dur - 2.5, dur - 0.1, dur + 1], [0, 1, 1, 0, 0])
    mix = (mix * fade[:, None])[: int(dur * SR)]
    mix = np.tanh(mix * 1.1) / 1.1
    peak = np.max(np.abs(mix))
    if peak > 0.89:
        mix *= 0.89 / peak
    with wave.open(args.out, "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes((mix * 32767).astype("<i2").tobytes())


if __name__ == "__main__":
    main()
