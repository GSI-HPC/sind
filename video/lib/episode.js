// SPDX-License-Identifier: LGPL-3.0-or-later
// Episode builder: an episode is a sequence of scene calls. Each call builds
// its scene's DOM, lays its narration on the timeline, moves Sindy to the
// scene's shot and adds the transition from the previous scene.
//
//   const tl = gsap.timeline({ paused: true });
//   const ep = Episode.create({ tl, id: "quickstart", title: "…", series: "Quickstart" });
//   ep.intro({ kicker: "Getting started", title: "Quickstart", say: "intro" });
//   ep.talk({ chapter: "What you'll build", title: "…", say: "welcome" });
//   ep.slide({ chapter: "…", title: "…", bullets: [{ icon: "network", title: "…", text: "…", at: "what:network" }], say: "what" });
//   ep.terminal({ chapter: "…", say: ["create", "check"], steps: [{ cmd: "sind create cluster", at: "create:Run" }, …] });
//   ep.terminal({ wide: true, … });          // fullscreen terminal for long lines
//   ep.outro({ chapter: "Wrap-up", next: "…", say: "outro" });
//   ep.done();                              // builds the avatar, captions, audio, clock
//   window.__timelines["main"] = tl;         // in the page, so HyperFrames' lint sees it
//
// Narration (`say`): a line id, or { id, mood, cues: { word: mood }, gap },
// or an array of those. Times are written as "line:word" (the start of the
// first word in that line beginning with "word"; "line:word#2" for the third
// match), and every helper accepts a number of seconds instead.
(function (global) {
  "use strict";

  const S = () => global.Scenes;

  // ------------------------------------------------------------- DOM --
  function h(tag, attrs, children) {
    const el = document.createElement(tag);
    for (const k in attrs || {}) {
      if (k === "class") el.className = attrs[k];
      else if (k === "text") el.textContent = attrs[k];
      else if (k === "html") el.innerHTML = attrs[k];
      else if (k === "style") el.setAttribute("style", attrs[k]);
      else el.setAttribute(k, attrs[k]);
    }
    for (const c of [].concat(children || [])) if (c) el.appendChild(c);
    return el;
  }

  const ICON_PATHS = {
    network: '<circle cx="12" cy="5" r="2.5"/><circle cx="5" cy="19" r="2.5"/><circle cx="19" cy="19" r="2.5"/><path d="M12 7.5v4M12 11.5l-5.5 5.5M12 11.5l5.5 5.5"/>',
    key: '<circle cx="8" cy="12" r="4"/><path d="M12 12h9M18 12v3M21 12v2"/>',
    nodes: '<rect x="3" y="4" width="8" height="7" rx="1.5"/><rect x="13" y="4" width="8" height="7" rx="1.5"/><rect x="3" y="13" width="8" height="7" rx="1.5"/><rect x="13" y="13" width="8" height="7" rx="1.5"/>',
    terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M12 15h5"/>',
    check: '<circle cx="12" cy="12" r="9"/><path d="M8 12.5l2.5 2.5L16 9.5"/>',
    job: '<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/>',
    gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1"/>',
    warn: '<path d="M12 3l10 18H2z"/><path d="M12 10v5M12 18v.5"/>',
    docker: '<rect x="3" y="10" width="4" height="4"/><rect x="8" y="10" width="4" height="4"/><rect x="13" y="10" width="4" height="4"/><rect x="8" y="5" width="4" height="4"/><path d="M2 15c2 4 6 5 10 5s8-2 10-7"/>',
  };
  function icon(name) {
    const paths = ICON_PATHS[name] || ICON_PATHS.check;
    return h("div", { class: "icon", html: `<svg viewBox="0 0 24 24" fill="none" stroke="#5eead4" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${paths}</svg>` });
  }

  const LOGO_CELLS = [
    ["#0EA5E9", 0.92], ["#0EA5E9", 0.7], ["#0EA5E9", 0.48],
    ["#14B8A6", 0.82], ["#0D9488", 1], ["#14B8A6", 0.58],
    ["#0EA5E9", 0.48], ["#0EA5E9", 0.34], ["#14B8A6", 0.4],
  ];
  function logoGrid() {
    return h("div", { class: "logo-grid" }, LOGO_CELLS.map(([c, o]) => h("i", { style: `background:${c};opacity:${o}` })));
  }

  // Words match cues ignoring case and punctuation, as in Scenes.planner.
  const norm = (w) => w.toLowerCase().replace(/[^a-z0-9]/g, "");

  // JetBrains Mono advances 0.6em per character.
  const MONO_ADVANCE = 0.6;

  // ------------------------------------------------------------ builder --
  function create(opts) {
    opts = opts || {};
    const root = typeof opts.root === "string" ? document.querySelector(opts.root) : opts.root || document.querySelector("[data-composition-id]");
    const compId = root.getAttribute("data-composition-id");
    // The page creates and registers the timeline itself (HyperFrames' lint
    // reads only inline scripts): see episodes/quickstart/index.html.
    const tl = opts.tl;
    if (!tl) throw new Error("Episode.create needs { tl: gsap.timeline({ paused: true }) }");
    const P = S().planner(opts.lines || global.SINDY_LINES);
    const series = opts.series || opts.title || "";

    const expressions = [{ t: 0, name: "happy" }];
    const gaze = [{ t: 0, x: 0, y: 0 }];
    const gestures = [];
    const renderers = [];
    const chapters = [];
    const scenes = [];
    const shown = []; // terminal commands and output, for `episode.mjs check`
    let shotName = null;
    let sceneCount = 0;

    // Layers above the scenes: avatar, name tag, captions, fade.
    const stage = h("div", { class: "sindy-stage" });
    const frame = h("div", { id: "sindy-frame" }, [h("div", { class: "sindy-bg" }), stage, h("div", { class: "sindy-ring" })]);
    const captionsEl = h("div", { id: "captions" });
    const blackout = h("div", { id: "blackout" });
    root.appendChild(frame);
    root.appendChild(captionsEl);
    root.appendChild(blackout);

    // ---- time references ----
    function time(ref) {
      if (typeof ref === "number") return ref;
      const m = /^([^:]+):([^#+-]+)(?:#(\d+))?([+-][\d.]+)?$/.exec(String(ref));
      if (!m) throw new Error(`bad time reference "${ref}" (want "line:word", "line:word#n" or seconds)`);
      const said = P.said.find((s) => s.id === m[1]);
      if (!said) throw new Error(`line "${m[1]}" has not been said yet (reference "${ref}")`);
      const word = m[2].trim();
      if (m[3] == null) {
        // A prefix that matches several words picks the first; say so.
        const key = norm(word);
        const hits = said.line.words.filter((w) => norm(w.text).startsWith(key)).map((w) => w.text);
        if (hits.length > 1) console.warn(`cue "${ref}" matches ${hits.length} words (${hits.join(", ")}); write "${m[1]}:${word}#0" for the first, or a longer prefix`);
      }
      return said.word(word, m[3] ? Number(m[3]) : 0) + (m[4] ? Number(m[4]) : 0);
    }
    const lineEnd = (id) => {
      const said = P.said.find((s) => s.id === id);
      if (!said) throw new Error(`line "${id}" has not been said yet`);
      return said.end;
    };

    // ---- acting ----
    const feel = (at, mood, blend) => expressions.push({ t: time(at), name: mood, blend });
    const look = (at, x, y) => gaze.push({ t: time(at), x, y: y || 0 });
    const wave = (at, dur) => gestures.push({ t: time(at), name: "wave", dur: dur || 2.2 });
    // Glance at content (on the left of the screen) and back to the camera.
    const glance = (at, hold, x, y) => {
      const t = time(at);
      look(t, x == null ? -0.7 : x, y == null ? 0.1 : y);
      look(t + (hold || 0.8), 0, 0);
    };

    function say(items, defaultMood, gap) {
      const list = [].concat(items || []);
      const out = [];
      list.forEach((item, i) => {
        const spec = typeof item === "string" ? { id: item } : item;
        const last = i === list.length - 1;
        const s = P.say(spec.id, { gap: spec.gap != null ? spec.gap : last ? (gap != null ? gap : 0.2) : 0.25 });
        if (spec.mood || defaultMood) feel(s.start, spec.mood || defaultMood);
        for (const [word, mood] of Object.entries(spec.cues || {})) feel(`${spec.id}:${word}`, mood);
        for (const [word, x] of Object.entries(spec.look || {})) look(`${spec.id}:${word}`, x === "camera" ? 0 : x);
        out.push(s);
      });
      return out;
    }

    // ---- scenes ----
    const TRANSITION_DUR = { push: 0.7, iris: 0.8, blur: 0.9 };

    function begin(kind, o, defaults) {
      const el = h("section", { class: `scene scene-${kind}`, id: `s${++sceneCount}-${kind}` });
      root.insertBefore(el, frame);
      const t = P.t;
      const prev = scenes[scenes.length - 1];
      if (!prev) {
        S().show(tl, el, 0);
      } else {
        const kindT = o.transition || defaults.transition;
        S().transition(tl, kindT, prev, el, t, o.transitionDur || TRANSITION_DUR[kindT] || 0.7, kindT === "iris" ? { x: "70%", y: "45%" } : undefined);
      }
      const shot = o.shot !== undefined ? o.shot : defaults.shot;
      if (shot && shot !== shotName) {
        S().shot(tl, frame, shot, t, prev ? 0.9 : 0);
        shotName = shot;
      }
      if (o.chapter) chapters.push({ title: o.chapter, start: t });
      scenes.push(el);
      return { el, t };
    }

    function chapterLabel(o) {
      const label = o.label || o.chapter;
      if (!label) return null;
      return h("div", { class: "chapter" }, [h("span", { class: "dot" }), document.createTextNode(series), h("b", { text: ` · ${label}` })]);
    }

    function intro(o) {
      o = o || {};
      const { el } = begin("intro", o, {});
      el.append(
        h("div", { class: "bg-glow" }),
        h("div", { class: "bg-grid" }),
        h("div", { class: "brand" }, [
          h("div", { class: "lockup" }, [logoGrid(), h("div", {}, [h("div", { class: "wordmark", text: "sind" }), h("div", { class: "tagline", text: "SLURM · IN · DOCKER" })])]),
          h("div", { class: "ep" }, [h("div", { class: "kicker", text: o.kicker || "" }), h("div", { class: "headline", text: o.title || series })]),
        ]),
        h("div", { class: "disclosure", text: "Sindy is an AI-voiced virtual presenter" })
      );
      chapters.length = 0;
      chapters.push({ title: o.chapter || "Intro", start: 0 });
      tl.fromTo(el.querySelectorAll(".logo-grid i"), { scale: 0, opacity: 0 }, { scale: 1, opacity: 1, duration: 0.45, ease: "back.out(2)", stagger: { each: 0.05, from: "center" } }, 0.15);
      S().enter(tl, el.querySelector(".wordmark"), 0.55, { y: 40 });
      S().enter(tl, el.querySelector(".tagline"), 0.8, { y: 16 });
      S().enter(tl, el.querySelector(".ep"), 1.1);
      S().enter(tl, el.querySelector(".disclosure"), 1.3, { y: 0 });
      S().shot(tl, frame, "hidden", 0);
      S().shot(tl, frame, "hero", 0.7, 0.9, "power3.out");
      shotName = "hero";
      P.at(o.start != null ? o.start : 1.5);
      const said = say(o.say, "joy", 0.25);
      if (said.length && o.wave !== false) wave(said[0].start - 0.1, 2.2);
      if (said.length) {
        // From "joy" to "happy" once the first sentence is over.
        const w = said[0].line.words;
        const k = w.findIndex((x, i) => i > 0 && /[.!?]$/.test(w[i - 1].text));
        if (k > 0) feel(said[0].start + w[k].start, "happy");
      }
      return api;
    }

    function talk(o) {
      o = o || {};
      const prevIntro = scenes.length && scenes[scenes.length - 1].classList.contains("scene-intro");
      const { el, t } = begin("talk", o, { transition: prevIntro ? "iris" : "push", shot: "full" });
      const card = h("div", { class: "card" }, [
        o.kicker ? h("div", { class: "kicker", text: o.kicker }) : null,
        h("div", { class: "headline", text: o.title || "" }),
        o.sub ? h("div", { class: "sub", text: o.sub }) : null,
        o.chips ? h("div", { class: "chips" }, o.chips.map((c, i) => h("span", { class: i === 0 ? "chip accent" : "chip", text: c }))) : null,
      ]);
      el.append(h("div", { class: "bg-glow" }), h("div", { class: "bg-grid" }), card);
      S().enter(tl, card.children, t + 0.5, { stagger: 0.12 });
      if (o.nameTag) {
        const tag = h("div", { class: "lower-third", style: "opacity: 0" }, [h("div", { class: "name", text: o.nameTag.name || "Sindy" }), h("div", { class: "role", text: o.nameTag.role || "your sind guide" })]);
        root.insertBefore(tag, captionsEl);
        tl.fromTo(tag, { opacity: 0, x: -60 }, { opacity: 1, x: 0, duration: 0.5, ease: "power3.out" }, t + 0.9);
        tl.to(tag, { opacity: 0, x: -60, duration: 0.4, ease: "power2.in" }, t + (o.nameTag.hold || 5.2));
      }
      P.wait(o.lead != null ? o.lead : 0.6);
      say(o.say, "neutral");
      return api;
    }

    // The frame shared by slide layouts (bullets, diagram): background,
    // chapter label and title, with Sindy in the corner. The layout fills the
    // area below the title, then calls narrate() so that its items can refer
    // to the lines, and shows each item at cue(item, i): just before its cue
    // word (`at`), or one after another when it has none.
    function slideFrame(kind, o) {
      const { el, t } = begin(kind, o, { transition: "push", shot: "cornerR" });
      const title = h("div", { class: "slide-title", text: o.title || "" });
      el.append(h("div", { class: "bg-glow" }), chapterLabel(o), title);
      S().enter(tl, title, t + 0.5);
      return {
        el,
        t,
        narrate() {
          P.wait(o.lead != null ? o.lead : 0.6);
          say(o.say, o.mood || "neutral");
        },
        cue(item, i) {
          return item.at != null ? time(item.at) - 0.15 : t + 0.9 + i * 0.5;
        },
      };
    }

    function slide(o) {
      o = o || {};
      const f = slideFrame("slide", o);
      const bullets = (o.bullets || []).map((b) => h("div", { class: "bullet" }, [icon(b.icon), h("div", { class: "txt" }, [h("div", { class: "t1", text: b.title }), b.text ? h("div", { class: "t2", text: b.text }) : null])]));
      f.el.append(h("div", { class: "bullets" }, bullets));
      for (const b of bullets) tl.set(b, { opacity: 0 }, 0);
      f.narrate();
      (o.bullets || []).forEach((b, i) => {
        const at = f.cue(b, i);
        tl.fromTo(bullets[i], { opacity: 0, x: -50 }, { opacity: 1, x: 0, duration: 0.45, ease: "power3.out" }, at);
        if (b.at != null) glance(at);
      });
      return api;
    }

    function terminal(o) {
      o = o || {};
      const wide = !!o.wide;
      const shot = o.avatar === "none" ? "hidden" : wide ? "mini" : "cornerR";
      const { el, t } = begin(wide ? "terminal-wide" : "terminal", o, { transition: "push", shot });
      const body = h("div", { class: "term-body" });
      const win = h("div", { class: wide ? "term wide" : "term" }, [
        h("div", { class: "term-bar" }, [h("i", { style: "background:#f87171" }), h("i", { style: "background:#fbbf24" }), h("i", { style: "background:#34d399" }), h("span", { class: "title", text: o.title || "~/work — bash" })]),
        body,
        h("div", { class: "term-ff" }),
      ]);
      el.append(h("div", { class: "bg-glow" }), chapterLabel(o), win);
      S().enter(tl, win, t + 0.4, { y: 40 });
      P.wait(o.lead != null ? o.lead : 0.6);
      say(o.say, o.mood || "happy", 0.3);

      // Resolve step times: `at` is absolute, `after` follows the previous step.
      const prompt = o.prompt || "$ ";
      const cps = o.cps || 32;
      let prevEnd = t + 1;
      const steps = (o.steps || []).map((st) => {
        const at = st.at != null ? time(st.at) + (st.delay || 0) : prevEnd + (st.after != null ? st.after : 0.3);
        const step = Object.assign({}, st, { at });
        if (st.until != null) step.until = time(st.until);
        delete step.after;
        delete step.delay;
        prevEnd = st.cmd != null ? at + st.cmd.length / cps : at;
        if (st.cmd != null && o.glance !== false && shot !== "hidden") glance(at, Math.max(0.8, st.cmd.length / cps + 0.4), wide ? -0.5 : -0.8, 0.2);
        return step;
      });

      // Fit the font to the longest line; the wide terminal goes smaller.
      const cols = Math.max(20, ...steps.flatMap((s) => (s.cmd != null ? [prompt.length + s.cmd.length + 1] : s.out != null ? s.out.split("\n").map((l) => l.length) : [])));
      const geo = wide ? { width: 1800, height: 800, max: 30, min: 18 } : { width: 1340, height: 700, max: 34, min: 22 };
      const inner = geo.width - 68;
      const font = Math.max(geo.min, Math.min(geo.max, Math.floor(inner / (cols * MONO_ADVANCE))));
      if (inner / (cols * MONO_ADVANCE) < geo.min) console.warn(`terminal: ${cols} columns do not fit at ${geo.min}px; use wide: true or shorten the lines`);
      body.style.fontSize = `${font}px`;
      const rows = Math.floor((geo.height - 58 - 52) / (font * 1.55));
      renderers.push(S().terminal(win, steps, { prompt, cps, rows }));
      for (const st of steps) {
        if (st.cmd != null) shown.push({ cmd: st.cmd, at: st.at });
        if (st.out != null) shown.push({ out: st.out, at: st.at });
      }
      return api;
    }

    function outro(o) {
      o = o || {};
      const { el, t } = begin("outro", o, { transition: "blur", shot: "left" });
      const links = o.links || [
        ["Docs", "gsi-hpc.github.io/sind"],
        ["Code", "github.com/GSI-HPC/sind"],
      ];
      const card = h("div", { class: "card" }, [
        h("div", { class: "headline", text: o.title || "Thanks for watching!" }),
        h("div", { class: "links" }, links.map(([k, v]) => h("div", { class: "link" }, [h("span", { text: k }), document.createTextNode(v)]))),
        o.next ? h("div", { class: "next" }, [h("div", { class: "kicker", text: "Next up" }), h("div", { class: "t", text: o.next })]) : null,
      ]);
      el.append(h("div", { class: "bg-glow" }), h("div", { class: "bg-grid" }), card);
      S().enter(tl, card.children, t + 0.6, { stagger: 0.15 });
      P.wait(o.lead != null ? o.lead : 0.7);
      const said = say(o.say, "joy", 0);
      const last = said[said.length - 1];
      if (last) {
        feel(last.start + Math.min(1.2, last.line.duration / 3), "happy");
        if (o.wave !== false) wave(o.wave != null ? o.wave : Math.max(last.start, last.end - 2.6), 2.4);
        feel(last.end + 0.2, "wink");
      }
      return api;
    }

    // Anything else (a custom scene): returns { el, t } to fill in by hand.
    function custom(kind, o) {
      return begin(kind, o || {}, { transition: "push", shot: shotName || "cornerR" });
    }

    function done(o) {
      o = o || {};
      const END = P.t + (o.tail != null ? o.tail : 1.8);
      tl.to(blackout, { opacity: 1, duration: 0.8, ease: "power1.in" }, END - 0.8);
      tl.fromTo(root.querySelectorAll(".bg-grid"), { backgroundPosition: "0px 0px" }, { backgroundPosition: `${Math.round(END * 6)}px ${Math.round(END * 10)}px`, duration: END, ease: "none" }, 0);
      P.audio(root);
      const sindy = Sindy.create(stage, { speech: P.speech(), expressions, gaze, gestures, seed: opts.seed || 11, maxDuration: END + 5 });
      const list = [sindy.render, ...renderers];
      // Docs renders turn burned-in captions off and ship a WebVTT track instead.
      if (S().vars({ captions: true }).captions) list.push(S().captions(captionsEl, P.said));
      S().clock(tl, END, list);
      S().episode({ id: opts.id || compId, title: opts.title || series, duration: END, said: P.said, chapters, terminal: shown });
      return tl;
    }

    const api = { tl, P, root, time, lineEnd, feel, look, wave, glance, say, intro, talk, slide, terminal, outro, custom, done };
    return api;
  }

  global.Episode = { create, icon, ICON_PATHS };
})(typeof window !== "undefined" ? window : globalThis);
