// SPDX-License-Identifier: LGPL-3.0-or-later
// Scene helpers for Sindy videos on HyperFrames: a narration planner, seekable
// captions and terminal, avatar shots, and CSS scene transitions.
//
// Everything that changes per frame is a pure function of timeline time and is
// driven by one GSAP setter tween (Scenes.clock), so frames render correctly in
// any order and across parallel render workers.
(function (global) {
  "use strict";

  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const norm = (w) => w.toLowerCase().replace(/[^a-z0-9]/g, "");
  const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

  // ------------------------------------------------------------ planner --
  // Lays narration lines out on the timeline with a cursor:
  //   const P = Scenes.planner(window.SINDY_LINES);
  //   P.wait(1.2); const hi = P.say("intro"); P.wait(0.4);
  //   hi.word("network")  -> absolute time the word starts
  function planner(lines) {
    let cursor = 0;
    const said = [];
    const P = {
      get t() {
        return cursor;
      },
      at(t) {
        cursor = t;
        return cursor;
      },
      wait(d) {
        cursor += d;
        return cursor;
      },
      say(id, opts) {
        opts = opts || {};
        const line = lines[id];
        if (!line) throw new Error(`voice line "${id}" missing; run voice/sindy_voice.py script`);
        const start = cursor;
        const item = {
          id,
          line,
          start,
          end: start + line.duration,
          // Start time of the n-th word matching q (index or text prefix).
          word(q, n) {
            const w = findWord(line.words, q, n || 0);
            return start + w.start;
          },
          wordEnd(q, n) {
            const w = findWord(line.words, q, n || 0);
            return start + w.end;
          },
        };
        said.push(item);
        cursor = item.end + (opts.gap == null ? 0.3 : opts.gap);
        return item;
      },
      said,
      speech() {
        return said.map((s) => ({ offset: s.start, visemes: s.line.visemes, envelope: s.line.envelope }));
      },
      // Framework-owned <audio> clips, one per spoken line.
      audio(root, dir) {
        for (const s of said) {
          const a = document.createElement("audio");
          a.id = `vo-${s.id}`;
          a.src = `${dir || "assets/voice"}/${s.id}.wav`;
          a.setAttribute("data-start", s.start.toFixed(3));
          a.setAttribute("data-duration", s.line.duration.toFixed(3));
          root.appendChild(a);
        }
      },
    };
    return P;
  }

  function findWord(words, q, n) {
    if (typeof q === "number") {
      if (!words[q]) throw new Error(`word index ${q} out of range`);
      return words[q];
    }
    const key = norm(q);
    let seen = 0;
    for (const w of words) {
      if (norm(w.text).startsWith(key)) {
        if (seen === n) return w;
        seen++;
      }
    }
    throw new Error(`word "${q}" not found in: ${words.map((w) => w.text).join(" ")}`);
  }

  // -------------------------------------------------------------- clock --
  // One setter tween that renders every per-frame component at time t.
  function clock(tl, duration, renderers) {
    const c = {
      _t: 0,
      get t() {
        return this._t;
      },
      set t(v) {
        this._t = v;
        for (const r of renderers) r(v);
      },
    };
    c.t = 0;
    tl.fromTo(c, { t: 0 }, { t: duration, duration, ease: "none", immediateRender: false }, 0);
    return c;
  }

  // ----------------------------------------------------------- captions --
  // Phrase-chunked captions with the spoken words highlighted.
  // Split spoken lines into caption phrases (shared by on-screen captions and
  // the WebVTT export, so both show the same cues).
  // opts.maxWords caps a phrase; a comma only ends one that has commaMin words.
  function phrasesOf(said, opts) {
    opts = opts || {};
    const maxWords = opts.maxWords || 7;
    const commaMin = opts.commaMin || 1;
    const phrases = [];
    for (const s of said) {
      let cur = [];
      const flush = () => {
        if (!cur.length) return;
        phrases.push({ words: cur.slice(), start: cur[0].t0 - 0.08, end: cur[cur.length - 1].t1 + 0.35 });
        cur = [];
      };
      for (const w of s.line.words) {
        cur.push({ text: w.text, t0: s.start + w.start, t1: s.start + w.end });
        if (/[.!?;:]$/.test(w.text) || (/,$/.test(w.text) && cur.length >= commaMin) || cur.length >= maxWords) flush();
      }
      flush();
    }
    // A phrase never overlaps the next one.
    for (let i = 0; i + 1 < phrases.length; i++) phrases[i].end = Math.min(phrases[i].end, phrases[i + 1].start);
    return phrases;
  }

  function captions(el, said, opts) {
    opts = opts || {};
    const phrases = phrasesOf(said, opts);
    let shown = -2;
    let lit = -1;
    const box = document.createElement("div");
    box.className = "cap-box";
    el.appendChild(box);
    return function render(t) {
      let idx = -1;
      for (let i = 0; i < phrases.length; i++) {
        if (t >= phrases[i].start && t < phrases[i].end) {
          idx = i;
          break;
        }
      }
      if (idx !== shown) {
        shown = idx;
        lit = -1;
        if (idx < 0) {
          box.style.opacity = "0";
          box.innerHTML = "";
        } else {
          box.style.opacity = "1";
          box.innerHTML = phrases[idx].words.map((w) => `<span class="cap-w">${esc(w.text)}</span>`).join(" ");
        }
      }
      if (idx >= 0) {
        const ws = phrases[idx].words;
        let n = 0;
        while (n < ws.length && ws[n].t0 <= t) n++;
        if (n !== lit) {
          lit = n;
          const spans = box.children;
          for (let i = 0; i < spans.length; i++) spans[i].classList.toggle("on", i < n);
        }
      }
    };
  }

  // ----------------------------------------------------------- terminal --
  // steps: [{ at, cmd } | { at, out } | { at, prompt: true } | { at, ff, hold? } | { at, mark, until? } | { at, clear: true }]
  // cmd lines are typed (deterministic per-char timing), out lines appear at once.
  function terminal(el, steps, opts) {
    opts = opts || {};
    const prompt = opts.prompt || "$ ";
    const cps = opts.cps || 32; // typing speed, chars per second
    const rows = opts.rows || 14;
    const sorted = steps.slice().sort((a, b) => a.at - b.at);
    const body = el.querySelector(".term-body");
    const badge = el.querySelector(".term-ff");
    let lastKey = "";

    return function render(t) {
      const lines = [];
      let typing = false;
      let ff = null;
      const marks = [];
      for (const s of sorted) {
        if (s.at > t) break;
        if (s.clear) lines.length = 0;
        // A finished silent command: show a fresh, empty prompt.
        if (s.prompt) lines.push({ kind: "cmd", text: "", idle: true });
        if (s.cmd != null) {
          if (lines.length && lines[lines.length - 1].idle) lines.pop();
          const n = Math.floor((t - s.at) * cps);
          const shown = s.cmd.slice(0, clamp(n, 0, s.cmd.length));
          if (n < s.cmd.length) typing = true;
          lines.push({ kind: "cmd", text: shown });
        }
        if (s.out != null) {
          for (const l of s.out.split("\n")) lines.push({ kind: "out", text: l });
        }
        if (s.ff && t - s.at < (s.hold || 1.6)) ff = s.ff;
        if (s.mark && (s.until == null || t < s.until)) marks.push(s.mark);
      }
      const last = lines[lines.length - 1];
      // Once output follows a command, show a fresh prompt.
      if (!last || last.kind === "out") lines.push({ kind: "cmd", text: "" });
      const cursorOn = typing || Math.floor(t * 1.8) % 2 === 0;
      const view = lines.slice(-rows);
      const key = JSON.stringify([view, cursorOn, ff, marks]);
      if (key === lastKey) return;
      lastKey = key;
      body.innerHTML = view
        .map((l, i) => {
          let html = esc(l.text);
          for (const m of marks) html = html.split(esc(m)).join(`<span class="term-hl">${esc(m)}</span>`);
          const isLast = i === view.length - 1;
          const cur = isLast && cursorOn ? '<span class="term-cursor"></span>' : "";
          return l.kind === "cmd"
            ? `<div class="term-line"><span class="term-prompt">${esc(prompt)}</span>${html}${cur}</div>`
            : `<div class="term-line term-out">${html || "&nbsp;"}</div>`;
        })
        .join("");
      if (badge) {
        badge.style.opacity = ff ? "1" : "0";
        if (ff) badge.textContent = ff;
      }
    };
  }

  // -------------------------------------------------------------- shots --
  // Avatar framing. The frame is the visible window; the stage holds the
  // 600x800 avatar SVG (so stage height = 4/3 width).
  const SHOTS = {
    hidden: { frame: { left: 1080, top: 1180, width: 780, height: 960, borderRadius: 0 }, stage: { width: 780, left: 0, top: 40 }, ring: 0 },
    hero: { frame: { left: 1080, top: 120, width: 780, height: 960, borderRadius: 0 }, stage: { width: 780, left: 0, top: 40 }, ring: 0 },
    full: { frame: { left: 60, top: 0, width: 900, height: 1080, borderRadius: 0 }, stage: { width: 900, left: 0, top: 36 }, ring: 0 },
    left: { frame: { left: 120, top: 120, width: 780, height: 960, borderRadius: 0 }, stage: { width: 780, left: 0, top: 40 }, ring: 0 },
    cornerR: { frame: { left: 1500, top: 640, width: 360, height: 360, borderRadius: 180 }, stage: { width: 450, left: -45, top: -14 }, ring: 1 },
    cornerL: { frame: { left: 60, top: 640, width: 360, height: 360, borderRadius: 180 }, stage: { width: 450, left: -45, top: -14 }, ring: 1 },
    // Small bubble for the fullscreen terminal.
    mini: { frame: { left: 1700, top: 860, width: 180, height: 180, borderRadius: 90 }, stage: { width: 225, left: -22, top: -7 }, ring: 1 },
  };

  function shot(tl, frame, name, at, dur, ease) {
    const s = SHOTS[name];
    if (!s) throw new Error(`unknown shot ${name}`);
    const stage = frame.querySelector(".sindy-stage");
    const deco = frame.querySelectorAll(".sindy-ring, .sindy-bg");
    const stageVars = Object.assign({}, s.stage, { height: (s.stage.width * 4) / 3 });
    if (!dur) {
      tl.set(frame, s.frame, at);
      tl.set(stage, stageVars, at);
      tl.set(deco, { opacity: s.ring }, at);
      return;
    }
    const e = ease || "power3.inOut";
    tl.to(frame, Object.assign({ duration: dur, ease: e }, s.frame), at);
    tl.to(stage, Object.assign({ duration: dur, ease: e }, stageVars), at);
    tl.to(deco, { opacity: s.ring, duration: dur * 0.6, ease: "power1.inOut" }, at + (s.ring ? dur * 0.4 : 0));
  }

  // -------------------------------------------------------- transitions --
  // Scenes are plain (non-clip) full-frame sections; transitions own their
  // visibility. Outgoing content stays fully visible until the handoff.
  function show(tl, el, at) {
    tl.set(el, { autoAlpha: 1 }, at);
  }
  function hide(tl, el, at) {
    tl.set(el, { autoAlpha: 0 }, at);
  }

  const transitions = {
    // Push: outgoing slides left, incoming follows from the right.
    push(tl, from, to, at, dur) {
      dur = dur || 0.6;
      show(tl, to, at);
      tl.fromTo(to, { xPercent: 100 }, { xPercent: 0, duration: dur, ease: "power3.inOut" }, at);
      tl.fromTo(from, { xPercent: 0 }, { xPercent: -100, duration: dur, ease: "power3.inOut" }, at);
      hide(tl, from, at + dur);
      tl.set(from, { xPercent: 0 }, at + dur);
    },
    // Iris: incoming scene opens as a growing circle from (x%, y%).
    iris(tl, from, to, at, dur, opt) {
      dur = dur || 0.7;
      const x = (opt && opt.x) || "50%";
      const y = (opt && opt.y) || "50%";
      show(tl, to, at);
      tl.fromTo(to, { clipPath: `circle(0% at ${x} ${y})` }, { clipPath: `circle(150% at ${x} ${y})`, duration: dur, ease: "power2.inOut" }, at);
      hide(tl, from, at + dur);
      tl.set(to, { clipPath: "none" }, at + dur);
    },
    // Blur crossfade: calm handoff for wind-down and outro.
    blur(tl, from, to, at, dur) {
      dur = dur || 0.8;
      show(tl, to, at);
      tl.fromTo(to, { opacity: 0, filter: "blur(16px)" }, { opacity: 1, filter: "blur(0px)", duration: dur, ease: "sine.inOut" }, at);
      tl.fromTo(from, { filter: "blur(0px)" }, { filter: "blur(16px)", duration: dur, ease: "sine.inOut" }, at);
      hide(tl, from, at + dur);
      tl.set(from, { filter: "none" }, at + dur);
    },
  };

  function transition(tl, kind, from, to, at, dur, opt) {
    const fn = transitions[kind];
    if (!fn) throw new Error(`unknown transition ${kind}`);
    fn(tl, from, to, at, dur, opt);
  }

  // Entrance helper: elements rise and fade in, staggered.
  function enter(tl, targets, at, opt) {
    opt = opt || {};
    tl.fromTo(
      targets,
      { opacity: 0, y: opt.y == null ? 30 : opt.y },
      { opacity: 1, y: 0, duration: opt.duration || 0.5, ease: opt.ease || "power3.out", stagger: opt.stagger || 0 },
      at
    );
  }

  // Publish episode metadata on window.__episode for tools/publish.mjs:
  // chapters (for the docs player) and caption cues (for WebVTT). Narration
  // spans and terminal content are for `episode.mjs check`.
  function episode(meta) {
    const r3 = (v) => Math.round(v * 1000) / 1000;
    global.__episode = {
      id: meta.id,
      title: meta.title,
      duration: r3(meta.duration),
      chapters: meta.chapters.map((c) => ({ title: c.title, start: r3(c.start) })),
      cues: phrasesOf(meta.said, { maxWords: 12, commaMin: 5 }).map((p) => ({
        start: r3(Math.max(0, p.start)),
        end: r3(p.end),
        text: p.words.map((w) => w.text).join(" "),
      })),
      lines: meta.said.map((s) => ({ id: s.id, text: s.line.text, start: r3(s.start), end: r3(s.end) })),
      terminal: meta.terminal || [],
    };
    return global.__episode;
  }

  // Composition variables (HyperFrames --variables), with defaults outside the runtime.
  function vars(defaults) {
    const hf = global.__hyperframes;
    const got = hf && hf.getVariables ? hf.getVariables() : {};
    return Object.assign({}, defaults, got);
  }

  global.Scenes = { planner, clock, captions, phrasesOf, terminal, shot, SHOTS, transition, show, hide, enter, episode, vars };
})(typeof window !== "undefined" ? window : globalThis);
