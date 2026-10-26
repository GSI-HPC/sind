// SPDX-License-Identifier: LGPL-3.0-or-later

// The engine of the overview video: a timeline that acts.js fills in order,
// with captions, chapters, terminal windows that play the recorded shots,
// a browser window that plays the sind ui screencast, cards, diagrams and
// the logo. Every frame is a function of time only: window.renderAt(t).
//
// The recordings come from rec/data.js (build.sh writes it):
//   REC.term     terminal.json of record.py
//   REC.ui       capture.json of record_ui.mjs; its frames are in rec/ui/
//   REC.files    the scenario files, by name

(function () {
  "use strict";

  const stage = document.getElementById("stage");
  const clamp = (x, a = 0, b = 1) => Math.min(b, Math.max(a, x));
  const lerp = (a, b, x) => a + (b - a) * x;
  const P = (t, a, b) => (b === a ? (t >= b ? 1 : 0) : clamp((t - a) / (b - a)));
  const eo = x => 1 - Math.pow(1 - x, 3);
  const eio = x => (x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2);
  const eback = x => { const c1 = 1.4, c3 = c1 + 1; return 1 + c3 * Math.pow(x - 1, 3) + c1 * Math.pow(x - 1, 2); };
  const hash = i => { const x = Math.sin(i * 127.1 + 311.7) * 43758.5453; return x - Math.floor(x); };
  const esc = s => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const plain = s => s.replace(/\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07/g, "").replace(/\r/g, "");
  function el(tag, cls, parent, html) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (html != null) e.innerHTML = html;
    (parent || stage).appendChild(e);
    return e;
  }
  const css = (e, o) => { for (const k in o) e.style[k] = o[k]; return e; };
  // Fade in at a, out at b: 0 outside, 1 inside, eased edges.
  const span = (t, a, b, fi = 0.5, fo = 0.4) => Math.min(eo(P(t, a, a + fi)), 1 - eio(P(t, b - fo, b)));
  const show = (e, v) => { e.style.display = v > 0.001 ? "" : "none"; return v > 0.001; };

  const REC = window.REC || { term: { shots: [] }, ui: { frames: [], events: [] }, files: {} };
  const V = {
    t: 0, draws: [], browsers: [], sfx: [], chapters: [], caps: [], energy: [], missing: [], voice: [], voiceFree: 0,
    clamp, lerp, P, eo, eio, eback, hash, esc, el, css, span, show, stage, REC,
  };
  V.draw = fn => V.draws.push(fn);
  V.sound = (t, kind) => V.sfx.push([+t.toFixed(3), kind]);
  // A hold also waits for the narration to end.
  V.hold = s => { V.t = Math.max(V.t + s, V.voiceFree + 0.4); return V.t; };

  // Narration: Sindy says line id (REC.voice: id -> seconds, from
  // narration.json) at t, or as soon as the line before has ended.
  const VOICE = REC.voice || {};
  V.say = (t, id) => {
    if (!id) return t;
    if (VOICE[id] == null) { V.missing.push("voice:" + id); return t; }
    const at = Math.max(t, V.voiceFree + 0.3);
    V.voice.push([+at.toFixed(3), id]);
    V.voiceFree = at + VOICE[id];
    return at;
  };
  // The time to wait for, before a beat that starts a new line.
  V.free = t => Math.max(t, V.voiceFree + 0.5);

  // ------------------------------------------------------- captions, chapters
  const head = el("div", "head");
  const kick = el("div", "kicker", head), titleA = el("div", "title", head), titleB = el("div", "title", head);
  const bar = el("div", "chapbar");
  const chapLabel = el("div", "chaplabel");

  // A chapter is one act; energy (0..3) drives the music.
  V.chapter = (title, energy = 1) => {
    V.chapters.push({ title, start: +V.t.toFixed(2) });
    V.energy.push([+V.t.toFixed(2), energy]);
  };
  // A caption: the beat's sentence under the act's kicker; null hides it.
  // A caption is [id, text] with a narration line, or only text.
  V.cap = (t, c) => {
    const [id, text] = Array.isArray(c) ? c : [null, c];
    V.caps.push({ t, text, chapter: V.chapters.length - 1 });
    V.say(t + 0.2, id);
  };

  V.draw(t => {
    let i = -1;
    for (let j = 0; j < V.caps.length; j++) if (V.caps[j].t <= t) i = j;
    const cur = V.caps[i], prev = V.caps[i - 1];
    const ch = V.chapters[cur ? cur.chapter : 0];
    const fin = cur ? eo(P(t, cur.t, cur.t + 0.6)) : 0;
    const visible = cur && cur.text;
    // the kicker fades with a change of chapter or a hidden caption
    const sameCh = prev && prev.chapter === cur.chapter && prev.text;
    const kv = visible ? (sameCh ? 1 : fin) : (prev && prev.text ? 1 - eo(P(t, cur.t, cur.t + 0.3)) : 0);
    if (show(head, Math.max(kv, visible ? 1 : 0, prev && prev.text ? 1 - P(t, cur.t, cur.t + 0.3) : 0))) {
      const kc = visible ? ch : V.chapters[prev ? prev.chapter : 0];
      kick.textContent = kc ? kc.title : "";
      kick.style.opacity = kv;
      titleA.innerHTML = visible ? cur.text : "";
      css(titleA, { opacity: visible ? fin : 0, transform: `translateY(${(1 - fin) * 16}px)` });
      const pv = prev && prev.text ? 1 - eio(P(t, cur.t, cur.t + 0.3)) : 0;
      titleB.innerHTML = prev && prev.text ? prev.text : "";
      css(titleB, { opacity: pv, transform: `translateY(${-(1 - pv) * 10}px)` });
    }
    // the chapter bar: one segment per act, the current one fills
    let c = -1;
    for (let j = 0; j < V.chapters.length; j++) if (V.chapters[j].start <= t) c = j;
    const on = c > 0 && c < V.chapters.length - 1 ? span(t, V.chapters[1].start, V.chapters[V.chapters.length - 1].start, 0.8, 0.6) : 0;
    if (show(bar, on) && show(chapLabel, on)) {
      if (bar.childElementCount !== V.chapters.length - 2) {
        bar.innerHTML = V.chapters.slice(1, -1).map(() => "<i><b></b></i>").join("");
      }
      [...bar.children].forEach((seg, j) => {
        const a = V.chapters[j + 1].start, b = V.chapters[j + 2].start;
        seg.firstChild.style.width = (P(t, a, b) * 100).toFixed(2) + "%";
      });
      bar.style.opacity = on * 0.9;
      chapLabel.textContent = `${c} / ${V.chapters.length - 2} · ${V.chapters[c].title}`;
      chapLabel.style.opacity = on * 0.9;
    }
  });

  // ----------------------------------------------------------------- windows
  // A macOS-like window frame; kind "term" or "browser". o.at/o.until set
  // when it shows, as [[a, b], ...] so one window can come back.
  V.window = o => {
    const w = el("div", "win " + (o.cls || ""));
    css(w, { left: o.x + "px", top: o.y + "px", width: o.w + "px", height: o.h + "px" });
    const top = el("div", "bar", w, "<b></b><b></b><b></b>");
    const title = el(o.url ? "div" : "span", o.url ? "url" : "", top, esc(o.title || ""));
    const badge = el("div", "badge", w);
    const body = el("div", "body", w);
    const win = { node: w, body, title, badge, spans: [], badges: [], from: o.from || "up" };
    // Spans that touch merge, so the window stays put between two shots.
    win.showFor = (a, b) => {
      const last = win.spans[win.spans.length - 1];
      if (last && a <= last[1] + 0.8) last[1] = Math.max(last[1], b); else win.spans.push([a, b]);
    };
    win.fast = (a, b, speed) => win.badges.push([a, b, speed]);
    V.draw(t => {
      let v = 0, vin = 1;
      for (const [a, b] of win.spans) {
        const s = span(t, a, b, 0.6, 0.45);
        if (s > v) { v = s; vin = eo(P(t, a, a + 0.7)); }
      }
      if (!show(w, v)) return;
      const dy = win.from === "up" ? (1 - vin) * 40 : 0, dx = win.from === "left" ? (1 - vin) * -60 : win.from === "right" ? (1 - vin) * 60 : 0;
      css(w, { opacity: v, transform: `translate(${dx}px,${dy}px) scale(${lerp(0.97, 1, vin)})` });
      let bv = 0, label = "";
      for (const [a, b, s] of win.badges) {
        const x = span(t, a, b, 0.25, 0.25);
        if (x > bv) { bv = x; label = `▸▸ ${s >= 10 ? Math.round(s) : s.toFixed(1).replace(/\.0$/, "")}× faster`; }
      }
      badge.style.opacity = bv;
      if (bv > 0) badge.textContent = label;
    });
    return win;
  };

  // A terminal window: 100 x 26 cells, as record.py records them.
  V.terminal = o => {
    const cols = REC.term.cols || 100, rows = REC.term.rows || 26;
    const fs = o.font || 18.5, lh = Math.round(fs * 1.46);
    const w = V.window({ ...o, w: Math.ceil(cols * fs * 0.6 + 56), h: rows * lh + 44 + 36 });
    w.cols = cols; w.rows = rows; w.fs = fs; w.lh = lh;
    w.shots = [];
    return w;
  };

  const CHECK = /: done in /, FAIL = /: failed in |^✗ |\n✗ /;

  // Play a recorded shot in a terminal window from V.t. Each input line is
  // typed at about 14 characters per second; the output follows with its
  // recorded timing. A command whose output takes long plays faster (with a
  // badge). After each command the screen holds, longer for more lines.
  // o.caps maps an input index to a caption; o.onInput(i, info) runs at each.
  // o.marks maps an input index to patterns: once its output is in, the
  // lines on screen that match one get a highlight band.
  V.shot = (win, id, o = {}) => {
    const shot = REC.term.shots.find(s => s.id === id);
    const node = el("div", "term", win.body);
    css(node, { font: `${win.fs}px/${win.lh}px var(--mono)` });
    const term = new window.Term(node, win.cols, win.rows);
    const t0 = V.t;
    const info = { id, start: t0, inputs: [] };
    if (!shot) {
      V.missing.push(id);
      term.add(t0, `\x1b[38;5;244m(no recording of ${id})\x1b[0m`);
      V.t += 4;
      info.end = V.t;
    } else {
      const ev = shot.events;
      let i = 0, vt = t0;
      for (; i < ev.length && ev[i].k !== "i"; i++) if (ev[i].k === "o") term.add(vt, ev[i].d);
      const prior = win.shots[win.shots.length - 1];
      vt += o.lead ?? (prior && prior.end >= t0 - 0.01 ? 1.1 : 0.7);
      let n = 0;
      while (i < ev.length) {
        const e = ev[i];
        if (e.k !== "i") { i++; continue; }
        const inp = { text: e.d, start: vt };
        if (o.caps && o.caps[n] !== undefined) {
          if (Array.isArray(o.caps[n])) vt = V.free(vt);
          inp.start = vt;
          V.cap(vt - 0.3, o.caps[n]);
        }
        const cps = Math.max(14, e.d.length / 3.4);
        let tt = vt + 0.25;
        for (let j = 0; j < e.d.length; j++) {
          tt += (1 / cps) * (0.55 + hash(j * 3.1 + vt * 7) * 0.9);
          term.add(tt, e.d[j]);
          if (j % 2 === 0) V.sound(tt, "key");
        }
        tt += 0.4;
        term.add(tt, "\r\n");
        V.sound(tt, "enter");
        inp.enter = tt;
        const outs = [];
        let j = i + 1;
        for (; j < ev.length && ev[j].k !== "i"; j++) if (ev[j].k === "o" || ev[j].k === "key") outs.push(ev[j]);
        const last = outs.length ? Math.max(0, outs[outs.length - 1].t - e.t) : 0;
        const room = o.maxReal ?? 6;
        const speed = last > room ? last / (room + 2.2 * Math.log2(last / room)) : 1;
        let done = false, fail = false, lines = 0;
        for (const x of outs) {
          const vx = tt + Math.max(0, x.t - e.t) / speed;
          if (x.k === "key") { term.add(vx, x.d); V.sound(vx, "key"); continue; }
          term.add(vx, x.d);
          const p = plain(x.d);
          lines += (p.match(/\n/g) || []).length;
          if (!done && CHECK.test(p)) { done = true; V.sound(vx, "done"); }
          if (!fail && FAIL.test(p)) { fail = true; V.sound(vx, "fail"); }
          if (o.ticks) (p.match(/✓/g) || []).forEach((_, k) => V.sound(vx + k * 0.09, "tick"));
        }
        inp.speed = speed;
        inp.out = tt + last / speed;
        if (speed >= 1.5) win.fast(tt, inp.out, speed);
        const hold = (o.holds && o.holds[n]) ?? clamp(1.2 + 0.12 * lines, 1.8, o.maxHold ?? 4.5);
        vt = inp.out + hold;
        info.inputs.push(inp);
        if (o.onInput) o.onInput(n, inp);
        n++;
        i = j;
      }
      // the last line ends before the next beat
      vt = Math.max(vt, V.voiceFree + 0.3);
      V.t = vt;
      info.end = vt;
    }
    // A shot that follows another in the same window waits until the
    // screen before it has faded out, as after a clear.
    const prev = win.shots[win.shots.length - 1];
    const follows = prev && prev.end >= t0 - 0.01;
    if (follows) prev.cut = t0 + 0.3;
    const a = follows ? t0 + 0.55 : t0, b = info.end;
    const marks = Object.entries(o.marks || {}).flatMap(([k, res]) => {
      const inp = info.inputs[k];
      return inp ? [{ at: inp.out + 0.3, res }] : [];
    });
    const bands = [];
    const band = k => bands[k] || (bands[k] = el("div", "mark", win.body));
    V.draw(t => {
      const z = info.cut ?? b + (o.linger ?? 0.6);
      const v = t >= a - 0.3 && t < z;
      node.style.display = v ? "" : "none";
      if (!v) { bands.forEach(d => { d.style.display = "none"; }); return; }
      node.style.opacity = Math.min(eo(P(t, a - 0.3, a)), 1 - eio(P(t, z - 0.3, z)));
      const idle = info.inputs.every(x => t < x.start || t > x.out + 0.15) || t < a + 0.9;
      term.at(t, idle ? Math.floor(t * 1.8) % 2 === 0 : true);
      const sc = term.scr, top = Math.max(0, Math.max(sc.lines.length, sc.r + 1) - sc.rows);
      let nb = 0;
      for (const m of marks) {
        if (t < m.at) continue;
        for (let r = top; r < top + sc.rows; r++) {
          const text = (sc.lines[r] || []).map(c => c[0]).join("");
          if (!m.res.some(re => re.test(text))) continue;
          const d = band(nb++), v = eo(P(t, m.at, m.at + 0.35));
          css(d, { display: "block", top: (18 + (r - top) * win.lh) + "px", height: win.lh + "px", opacity: v * node.style.opacity, transform: `scaleX(${lerp(0.6, 1, v)})` });
        }
      }
      for (let k = nb; k < bands.length; k++) bands[k].style.display = "none";
    });
    win.shots.push(info);
    return info;
  };

  // ------------------------------------------------------------------ cards
  // A card: a title tab and HTML; o.at and o.until in video time.
  V.card = o => {
    const c = el("div", "card " + (o.cls || ""));
    css(c, { left: o.x + "px", top: o.y + "px", width: o.w + "px" });
    c.innerHTML = (o.title ? `<div class="tab">${o.title}</div>` : "") + `<div class="cbody">${o.html}</div>`;
    V.draw(t => {
      const v = span(t, o.at, o.until, 0.6, 0.4);
      if (!show(c, v)) return;
      const vin = eo(P(t, o.at, o.at + 0.7));
      css(c, { opacity: v, transform: `translate(${(1 - vin) * (o.dx ?? -40)}px,${(1 - vin) * (o.dy ?? 0)}px)` });
    });
    return c;
  };

  // YAML with colour, for config cards.
  V.yaml = src => esc(src).split("\n").map(l => {
    const m = /^(\s*)(- )?([A-Za-z_][\w-]*)(:)(.*)$/.exec(l);
    if (l.trim().startsWith("#")) return `<span class="c">${l}</span>`;
    if (!m) return l.replace(/^(\s*- )(.*)$/, '$1<span class="v">$2</span>');
    return `${m[1]}${m[2] || ""}<span class="k">${m[3]}</span>${m[4]}<span class="v">${m[5]}</span>`;
  }).join("\n");

  // ---------------------------------------------------------------- diagram
  // An SVG layer for boxes and edges that draw themselves from o.at on.
  V.diagram = (o) => {
    const NS = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(NS, "svg");
    svg.setAttribute("width", 1920); svg.setAttribute("height", 1080);
    css(svg, { position: "absolute", left: 0, top: 0 });
    stage.appendChild(svg);
    const items = [];
    const d = {
      box(x, y, w, h, title, sub, at, opt = {}) {
        const g = document.createElementNS(NS, "g");
        g.innerHTML = `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="12" fill="${opt.fill || "#0d1628"}" stroke="${opt.stroke || "#2b3b58"}" stroke-width="1.6"/>` +
          `<text x="${x + 18}" y="${y + (sub ? 33 : h / 2 + 8)}" font-size="${opt.size || 22}" fill="#e2e8f0">${title}</text>` +
          (sub ? `<text x="${x + 18}" y="${y + 60}" font-size="17" fill="#8b98ad" class="sans">${sub}</text>` : "");
        svg.appendChild(g); items.push({ g, at, kind: "box" });
        return { x, y, w, h, cx: x + w / 2, cy: y + h / 2, l: [x, y + h / 2], r: [x + w, y + h / 2] };
      },
      area(x, y, w, h, label, at, color) {
        const g = document.createElementNS(NS, "g");
        g.innerHTML = `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="18" fill="${color}" fill-opacity=".09" stroke="${color}" stroke-opacity=".4" stroke-dasharray="6 5"/>` +
          `<text x="${x + 18}" y="${y - 14}" font-size="16" fill="#8b98ad" letter-spacing="3" class="sans" font-weight="600">${label}</text>`;
        svg.appendChild(g); items.push({ g, at, kind: "box" });
      },
      edge(a, b, at, color = "#2dd4bf", dur = 0.6, opt = {}) {
        const p = document.createElementNS(NS, "path");
        const mx = (a[0] + b[0]) / 2;
        p.setAttribute("d", opt.straight ? `M${a[0]},${a[1]} L${b[0]},${b[1]}` : `M${a[0]},${a[1]} C${mx},${a[1]} ${mx},${b[1]} ${b[0]},${b[1]}`);
        p.setAttribute("fill", "none"); p.setAttribute("stroke", color); p.setAttribute("stroke-width", opt.width || 2.6);
        p.setAttribute("stroke-linecap", "round");
        svg.appendChild(p);
        const len = p.getTotalLength();
        p.style.strokeDasharray = len;
        items.push({ g: p, at, kind: "edge", len, dur });
        return p;
      },
      text(x, y, html, at, opt = {}) {
        const t = document.createElementNS(NS, "text");
        t.setAttribute("x", x); t.setAttribute("y", y); t.setAttribute("font-size", opt.size || 18);
        t.setAttribute("fill", opt.color || "#94a3b8"); if (opt.anchor) t.setAttribute("text-anchor", opt.anchor);
        if (opt.sans) t.setAttribute("class", "sans");
        t.innerHTML = html; svg.appendChild(t); items.push({ g: t, at, kind: "box" });
      },
      // a dot that runs along an edge, from at for dur seconds
      pulse(path, at, dur, color = "#5eead4") {
        const c = document.createElementNS(NS, "circle");
        c.setAttribute("r", 7); c.setAttribute("fill", color);
        svg.appendChild(c); items.push({ g: c, at, kind: "pulse", path, dur });
      },
    };
    V.draw(t => {
      const v = span(t, o.at, o.until, 0.4, 0.5);
      if (!show(svg, v)) return;
      svg.style.opacity = v;
      for (const it of items) {
        if (it.kind === "box") { const x = eo(P(t, it.at, it.at + 0.45)); it.g.style.opacity = x; it.g.style.transform = `translateY(${(1 - x) * 12}px)`; }
        else if (it.kind === "edge") it.g.style.strokeDashoffset = it.len * (1 - eio(P(t, it.at, it.at + it.dur)));
        else {
          const x = P(t, it.at, it.at + it.dur), len = it.path.getTotalLength();
          const pt = it.path.getPointAtLength(len * eio(x));
          it.g.setAttribute("cx", pt.x); it.g.setAttribute("cy", pt.y);
          it.g.style.opacity = x > 0 && x < 1 ? 1 : 0;
        }
      }
    });
    return d;
  };

  // ------------------------------------------------------------------- logo
  // The nine tiles of the sind icon: [col, row, colour, opacity, lines].
  const ICON = [[0, 0, "#0EA5E9", 0.92], [1, 0, "#0EA5E9", 0.70], [2, 0, "#0EA5E9", 0.48], [0, 1, "#14B8A6", 0.82], [1, 1, "#0D9488", 1, true],
    [2, 1, "#14B8A6", 0.58], [0, 2, "#0EA5E9", 0.48], [1, 2, "#0EA5E9", 0.34], [2, 2, "#14B8A6", 0.40]];
  V.logo = opt => {
    const k = opt.k, size = 52 * k, gap = 10 * k;
    const g = el("div", "abs"); css(g, { left: 0, top: 0, width: "1920px", height: "1080px" });
    const tiles = ICON.map(([c, r, col, op, lines]) => {
      const d = el("div", "tile", g); css(d, { width: size + "px", height: size + "px", background: col });
      if (lines) for (const y of [20, 27, 34]) { const i = el("i", "", d); i.style.top = (y / 52 * 100) + "%"; i.style.borderRadius = (2 * k) + "px"; }
      return { d, c, r, col, op };
    });
    const word = el("div", "abs", g, "sind"); css(word, { font: `600 ${100 * k}px/1 var(--sans)`, letterSpacing: (-2 * k) + "px", color: "#e2e8f0" });
    const tag = el("div", "abs", g, "SLURM · IN · DOCKER"); css(tag, { font: `500 ${12 * k}px/1 var(--sans)`, color: "#94a3b8", whiteSpace: "nowrap" });
    const iconW = 176 * k, wordW = 205 * k, total = iconW + 40 * k + wordW;
    const x0 = opt.cx - total / 2, y0 = opt.cy - iconW / 2;
    tiles.forEach(o => { o.x = x0 + o.c * (size + gap); o.y = y0 + o.r * (size + gap); });
    return { g, tiles, word, tag, k, size, x0, y0, iconW, wordW, total, wx: x0 + iconW + 40 * k };
  };

  // -------------------------------------------------------------- the sind ui
  const UIF = REC.ui.frames || [], UIE = REC.ui.events || [];
  const UIW = (REC.ui.viewport || [1600, 900])[0], UIH = (REC.ui.viewport || [1600, 900])[1];
  const MOVES = UIE.filter(e => e.name === "move"), CLICKS = UIE.filter(e => e.name === "click");
  const GAPS = UIE.filter(e => e.name === "gap");
  V.uiMarks = UIE.filter(e => !["move", "click", "type", "gap"].includes(e.name));
  const frameAt = f => {
    let lo = 0, hi = UIF.length - 1;
    if (hi < 0) return null;
    while (lo < hi) { const m = (lo + hi + 1) >> 1; if (UIF[m][0] <= f) lo = m; else hi = m - 1; }
    return UIF[lo][1];
  };
  // The pointer: eased moves from one target to the next, in footage time.
  const cursorAt = f => {
    let x = UIW * 0.62, y = UIH * 0.6;
    for (const m of MOVES) {
      if (f < m.t) break;
      const k = eio(P(f, m.t, m.t + (m.dur ?? 0.55)));
      x = lerp(x, m.x, k); y = lerp(y, m.y, k);
    }
    return [x, y];
  };

  // A browser window that plays the screencast between two marks. Each
  // beat (mark) plays in real time, unless o.beats gives it a length in
  // video seconds (a long run, with a badge). o.beats[mark]: { cap, zoom,
  // follow, len }. The camera follows the pointer where follow is set.
  V.browser = o => {
    const s0 = o.scale || 0.9, vw = UIW * s0, vh = UIH * s0;
    const win = V.window({ x: o.x ?? (960 - vw / 2), y: o.y ?? 196, w: vw, h: vh + 44, url: true, title: o.url || "", cls: "browser" });
    const vp = el("div", "vp", win.body); css(vp, { width: vw + "px", height: vh + "px" });
    const inner = el("div", "inner", vp); css(inner, { width: UIW + "px", height: UIH + "px" });
    const img = el("img", "", inner); css(img, { width: UIW + "px", height: UIH + "px" });
    // the frame before a gap, fading out over the frame after it
    const img2 = el("img", "abs", inner); css(img2, { left: 0, top: 0, width: UIW + "px", height: UIH + "px", opacity: 0 });
    const cur = el("div", "abs", inner, '<svg width="30" height="30" viewBox="0 0 24 24"><path d="M4 2 L4 19 L8.6 14.8 L11.6 21.4 L14.3 20.2 L11.4 13.8 L17.6 13.6 Z" fill="#fff" stroke="#0b1222" stroke-width="1.3" stroke-linejoin="round"/></svg>');
    const ring = el("div", "ring", inner);
    V.browsers.push(win);
    win.img = img; win.img2 = img2;
    win.play = (from, to, p = {}) => {
      const i0 = V.uiMarks.findIndex(m => m.name === from);
      let i1 = V.uiMarks.findIndex(m => m.name === to);
      if (i0 < 0) { V.missing.push("ui:" + from); return null; }
      if (i1 < 0) i1 = V.uiMarks.length - 1;
      const seg = [], fades = []; // seg: [v0, v1, f0, f1, beat]; fades: [v, footage before the gap]
      for (let i = i0; i < i1; i++) {
        const m = V.uiMarks[i], f0 = m.t, f1 = V.uiMarks[i + 1].t;
        const b = (p.beats && p.beats[m.name]) || {};
        const v0 = V.t;
        if (b.cap !== undefined) V.cap(v0 + (b.capDelay || 0), b.cap);
        // more lines for the beat, without a caption of their own
        for (const id of [].concat(b.say || [])) V.say(v0 + 0.3, id);
        if (b.len && f1 - f0 > b.len * 1.4) {
          win.fast(v0 + 0.3, v0 + b.len - 0.3, (f1 - f0) / b.len);
          seg.push([v0, v0 + b.len, f0, f1, b]);
          V.t += b.len;
          continue;
        }
        // a beat in real time leaves out its gaps
        let f = f0;
        for (const g of GAPS) {
          if (g.end <= f || g.t >= f1) continue;
          // (a sliver before a gap, such as a mark just before a page load, is left out too)
          if (g.t - f > 0.15) { seg.push([V.t, V.t + g.t - f, f, g.t, b]); V.t += g.t - f; }
          const lastFade = fades[fades.length - 1];
          if (seg.length && !(lastFade && lastFade[0] === V.t)) fades.push([V.t, seg[seg.length - 1][3] - 0.001]);
          f = Math.min(g.end, f1);
        }
        if (f1 > f) { seg.push([V.t, V.t + f1 - f, f, f1, b]); V.t += f1 - f; }
        // a line longer than its beat holds the beat's last frame
        if (V.t < V.voiceFree + 0.3) { seg.push([V.t, V.voiceFree + 0.3, f1, f1, b]); V.t = V.voiceFree + 0.3; }
      }
      const vStart = seg.length ? seg[0][0] : V.t, vEnd = V.t;
      const footAt = v => { for (const s of seg) if (v < s[1]) return [lerp(s[2], s[3], P(v, s[0], s[1])), s[4]]; const s = seg[seg.length - 1]; return [s[3], s[4]]; };
      // click and type sounds at their video times
      const videoAt = f => { for (const s of seg) if (f >= s[2] && f < s[3]) return lerp(s[0], s[1], (f - s[2]) / (s[3] - s[2])); return null; };
      CLICKS.forEach(c => { const v = videoAt(c.t); if (v != null) V.sound(v, "click"); });
      UIE.filter(e => e.name === "type").forEach(e => {
        for (let k = 0; k < e.text.length; k++) { const v = videoAt(e.t + 0.04 + k * 0.13); if (v != null) V.sound(v, "key"); }
      });
      (p.sounds || []).forEach(([mark, kind, dt]) => {
        const m = V.uiMarks.find(x => x.name === mark); const v = m && videoAt(m.t + (dt || 0));
        if (v != null) V.sound(v, kind);
      });
      // The camera, worked out frame by frame ahead of time: a critically
      // damped spring towards the target, which starts and stops softly.
      // The target stays where the view does not leave the page.
      const fps = 30, cam = [], w0 = 5, dt = 1 / fps;
      const inPage = (c, size, view, zz) => clamp(c, view / (2 * s0 * zz), size - view / (2 * s0 * zz));
      let z = 1, cx = UIW / 2, cy = UIH / 2, vz = 0, vx = 0, vy = 0;
      const spring = (x, v, to) => { const acc = w0 * w0 * (to - x) - 2 * w0 * v; v += acc * dt; return [x + v * dt, v]; };
      for (let v = vStart; v <= vEnd + 1 / fps; v += 1 / fps) {
        const [f, b] = footAt(v);
        const tz = b.zoom || 1;
        let tx = UIW / 2, ty = UIH / 2;
        if (b.at) [tx, ty] = b.at;
        if (b.follow) [tx, ty] = cursorAt(f);
        tx = inPage(tx, UIW, vw, tz); ty = inPage(ty, UIH, vh, tz);
        if (!cam.length) { z = tz; cx = tx; cy = ty; } else {
          [z, vz] = spring(z, vz, tz); [cx, vx] = spring(cx, vx, tx); [cy, vy] = spring(cy, vy, ty);
        }
        cam.push([z, cx, cy]);
      }
      win.showFor(vStart - (p.lead ?? 0), vEnd + (p.linger ?? 0.4));
      V.draw(t => {
        if (t < vStart - (p.lead ?? 0) - 0.6 || t > vEnd + (p.linger ?? 0.4) + 0.1) return;
        const vt = clamp(t, vStart, vEnd);
        const [f, b] = footAt(vt);
        win.want = frameAt(f);
        win.want2 = null;
        for (const [fv, ff] of fades) if (vt >= fv && vt < fv + 0.45) { win.want2 = frameAt(ff); img2.style.opacity = 1 - eio(P(vt, fv, fv + 0.45)); }
        if (!win.want2) img2.style.opacity = 0;
        const [zz, ccx, ccy] = cam[clamp(Math.round((vt - vStart) * fps), 0, cam.length - 1)];
        const s = s0 * zz;
        const tx = clamp(vw / 2 - ccx * s, vw - UIW * s, 0), ty = clamp(vh / 2 - ccy * s, vh - UIH * s, 0);
        inner.style.transform = `translate(${tx}px,${ty}px) scale(${s})`;
        const [mx, my] = cursorAt(f);
        css(cur, { left: (mx - 5) + "px", top: (my - 3) + "px", opacity: p.cursor === false ? 0 : 1, transform: `scale(${1 / zz})`, transformOrigin: "5px 3px" });
        let rc = null, rv = 0;
        for (const c of CLICKS) if (f >= c.t && f < c.t + 0.5) { rc = c; rv = (f - c.t) / 0.5; }
        if (rc && p.cursor !== false) css(ring, { left: (rc.x - 30) + "px", top: (rc.y - 30) + "px", opacity: 1 - rv, transform: `scale(${(0.3 + rv) / zz})` });
        else ring.style.opacity = 0;
        if (p.url) win.title.textContent = p.url;
      });
      return { start: vStart, end: vEnd };
    };
    return win;
  };

  // ----------------------------------------------------------------- render
  V.finish = () => {
    V.duration = +V.t.toFixed(2);
    const vignette = el("div"); vignette.id = "vignette";
    window.VIDEO = {
      duration: V.duration,
      chapters: V.chapters.filter(c => c.title !== "end").map(c => ({ title: c.title, start: c.start })),
      energy: V.energy,
      sfx: V.sfx.sort((a, b) => a[0] - b[0]),
      voice: V.voice,
      poster: V.poster ?? 12,
      missing: V.missing,
    };
  };
  window.renderAt = async t => {
    for (const d of V.draws) d(t);
    const waits = [];
    for (const w of V.browsers || []) {
      if (!w.want || w.node.style.display === "none") continue;
      for (const [img, want] of [[w.img, w.want], [w.img2, w.want2]]) {
        if (!want) continue;
        const src = "rec/ui/" + want;
        if (img.getAttribute("src") !== src) { img.setAttribute("src", src); waits.push(img.decode().catch(() => {})); }
      }
    }
    await Promise.all(waits);
    return true;
  };
  window.ready = (async () => { await document.fonts.ready; return true; })();
  window.V = V;
})();
