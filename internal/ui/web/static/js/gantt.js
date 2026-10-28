// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the run page's summary and timeline. The server sends the run
// as one block of data (the attribute data-gantt of the fragment
// run-summary, see runGantt), whenever the run changes; this file draws
// the summary and the timeline from it, once a frame, at the time of the
// frame: how long each span has run, the scale of the axis, which hidden
// spans show, the rates and the time left. It owns every element it
// draws, so that no live update of the page cuts their motion, and which
// steps are open. It builds the elements and sets their styles through
// the CSSOM, as the Content-Security-Policy allows no style attribute.
(() => {
  'use strict';

  // revealAfter is how long a hidden span runs before it shows, as the
  // terminal and activity.Reveal do, in seconds.
  const revealAfter = 1;
  // autoOpenMax is the most targets of a step that opens by itself while
  // it runs.
  const autoOpenMax = 8;
  // quietAfter is how long without a new event the summary says so.
  const quietAfter = 4;
  // rateWindow is how far back a rate looks from the newest sample, and
  // staleAfter how old that sample may be before the rate is no longer
  // the rate now, in seconds.
  const rateWindow = 1;
  const staleAfter = 2;
  // The horizons that the axis of a run that goes on grows through, in
  // seconds: the next one once the clock passes 88 % of it, eased over
  // easeFor milliseconds.
  const nice = [10, 15, 20, 30, 45, 60, 75, 90, 120, 150, 180, 240, 300, 360, 480, 600, 750, 900, 1200, 1500, 1800, 2400, 3000, 3600, 5400, 7200, 10800, 14400];
  const easeFor = 450;
  const tickSteps = [1, 2, 5, 10, 15, 20, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200];
  // nouns are what the counts of the summary call the targets of a step,
  // in the singular.
  const nouns = { 'pull images': 'image', nodes: 'node' };
  // verbs say what a command did to its nodes, as the Result column of
  // Activity does (runNodeVerbs).
  const verbs = {
    'create cluster': 'ready', 'create worker': 'ready', 'delete cluster': 'removed', 'delete worker': 'removed',
    'power on': 'powered on', 'power unfreeze': 'unfrozen', 'power cut': 'cut off', 'power cycle': 'power-cycled',
    'power freeze': 'frozen', 'power reboot': 'rebooted', 'power shutdown': 'shut down',
  };

  // ---- format ----

  // dur writes seconds as the server's runDur does: 3.4s below ten
  // seconds with fine, 42s, 5m07s, 2h03m.
  function dur(s, fine) {
    s = Math.max(s, 0);
    if (s < 10 && fine) return (Math.floor(s * 10) / 10).toFixed(1) + 's';
    if (s < 60) return Math.floor(s) + 's';
    const pad = n => (n < 10 ? '0' : '') + n;
    if (s < 3600) return Math.floor(s / 60) + 'm' + pad(Math.floor(s % 60)) + 's';
    return Math.floor(s / 3600) + 'h' + pad(Math.floor(s % 3600 / 60)) + 'm';
  }
  // bytes writes a size in decimal units, as the server's runBytes does.
  function bytes(n) {
    const u = ['B', 'kB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
    if (i === 0) return Math.max(Math.round(n), 0) + ' B';
    return (n < 10 ? n.toFixed(1) : Math.round(n)) + ' ' + u[i];
  }
  function plural(n, noun) { return n + ' ' + noun + (n === 1 ? '' : 's'); }
  function tickLabel(v) {
    if (v < 60) return v + 's';
    if (v < 3600) return v % 60 ? Math.floor(v / 60) + 'm' + String(v % 60).padStart(2, '0') : v / 60 + 'm';
    return v % 3600 ? Math.floor(v / 3600) + 'h' + String(Math.floor(v % 3600 / 60)).padStart(2, '0') : v / 3600 + 'h';
  }
  // short leaves the registry of the images of sind out of a name.
  function short(name) { return name.replace(/^ghcr\.io\/gsi-hpc\//, ''); }
  // phaseName is what a target does in a call or a wait: "create" for the
  // call "docker create".
  function phaseName(s) { return s.n.replace(/^docker /, ''); }
  // phaseWord says what a target does in a phase that goes on: waits for
  // munge, starts its container, runs setup.
  function phaseWord(p) {
    if (p.k === 'wait') return 'waits for ' + (p.m || p.n);
    if (/^docker (create|start)$/.test(p.n)) return 'starts its container';
    if (p.n === 'docker pull') return 'pulls its image';
    if (p.n === 'docker exec' || p.n === 'setup') return 'runs setup';
    return phaseName(p);
  }
  // said is the word of a state: a run that was canceled was stopped.
  function said(s) { return s === 'canceled' ? 'stopped' : s; }

  // ---- the run ----

  // load reads the data of the run into a tree of spans, with times in
  // seconds since the run began.
  function load(json) {
    const d = JSON.parse(json);
    const sec = v => (v == null ? null : v / 1000);
    const by = new Map();
    const spans = (d.spans || []).map(s => {
      const o = {
        i: s.i, p: s.p || '', k: s.k, n: s.n, node: s.node || '', role: s.role || '', h: !!s.h,
        q: sec(s.q), r: sec(s.r), e: sec(s.e), c: s.c, tot: s.tot || 0, u: s.u || '',
        a: s.a || 0, z: s.z || 0, m: s.m || '', err: s.err || '', ec: s.ec || '', to: sec(s.to) || 0,
        w: (s.w || []).map(x => [x[0] / 1000, x[1], x[2]]), kids: []
      };
      by.set(o.i, o);
      return o;
    });
    spans.forEach(s => { const p = by.get(s.p); if (p) p.kids.push(s); });
    return {
      origin: d.origin || 0, end: sec(d.end), last: sec(d.last) || 0, state: d.state, status: d.status || '',
      suspended: !!d.suspended, stopping: !!d.stopping, unfinished: !!d.unfinished, title: d.title || '',
      root: spans.find(s => s.k === 'command') || { kids: [], q: 0 }, spans, by,
      usual: d.usual || {}, usualRun: sec(d.usualRun) || 0, usualN: d.usualN || 0
    };
  }

  // state is how a span stands: queued, running, or how it ended.
  function state(s) {
    if (s.e != null || run.end != null) return s.c === 'ended' ? 'ok' : s.c;
    return s.r == null ? 'queued' : 'running';
  }
  function ended(s) { return s.e != null || run.end != null; }
  function endOf(s, t) { return s.e != null ? Math.min(s.e, t) : t; }
  function elapsed(s, t) { return s.r == null ? 0 : Math.max(endOf(s, t) - s.r, 0); }
  // visible reports whether a span shows: one without the hidden flag, or
  // one other than a call that failed or has run for revealAfter.
  function visible(s, t) {
    return !s.h || (s.k !== 'call' && (s.c === 'failed' || elapsed(s, t) >= revealAfter));
  }
  // targets returns the targets of a step that show, through its batches.
  function targets(st, t) {
    const out = [];
    const walk = p => p.kids.forEach(k => {
      if (k.k === 'target' && visible(k, t)) out.push(k);
      else if (k.k === 'batch') walk(k);
    });
    walk(st);
    return out;
  }
  // phases are the calls and the waits of a target, which its row draws.
  function phases(tg) { return tg.kids.filter(k => (k.k === 'call' || k.k === 'wait') && k.r != null); }
  // waits are the waits of a step that last at least revealAfter.
  function waits(st, t) { return st.kids.filter(k => k.k === 'wait' && k.r != null && elapsed(k, t) >= revealAfter); }

  // ---- work ----

  function bsearch(w, t) { // the last index with w[i][0] <= t
    let lo = 0, hi = w.length - 1, r = -1;
    while (lo <= hi) { const m = (lo + hi) >> 1; if (w[m][0] <= t) { r = m; lo = m + 1; } else hi = m - 1; }
    return r;
  }
  function amountAt(tg, t) {
    if (!tg.w.length) return tg.a;
    const i = bsearch(tg.w, t);
    return i < 0 ? 0 : tg.w[i][1];
  }
  // firstByte is when a target first did some work, null before.
  function firstByte(tg) { const x = tg.w.find(x => x[1] > 0); return x ? x[0] : (tg.a > 0 ? tg.q : null); }
  // lastChange is when the amount of a target last grew.
  function lastChange(tg) {
    for (let i = tg.w.length - 1; i > 0; i--) if (tg.w[i][1] !== tg.w[i - 1][1]) return tg.w[i][0];
    return tg.w.length ? tg.w[0][0] : null;
  }
  // newest is the time of the newest sample of a target up to t.
  function newest(tg, t) { const i = bsearch(tg.w, t); return i < 0 ? null : tg.w[i][0]; }
  // rateAt is the bytes a second over the rateWindow that ends at the
  // newest sample up to t, 0 once that sample is older than staleAfter.
  function rateAt(tg, t) {
    if (tg.u !== 'bytes' || tg.r == null || t < tg.r) return 0;
    const i = bsearch(tg.w, t);
    if (i < 1) return 0;
    const [nt, na] = tg.w[i];
    if (t - nt > staleAfter || (tg.e != null && nt > tg.e)) return 0;
    const from = nt - rateWindow;
    const j = bsearch(tg.w, from);
    let base;
    if (j < 0) base = tg.w[0][1] * 0; // nothing before the window: from 0 at the first sample
    else if (j === tg.w.length - 1) base = tg.w[j][1];
    else {
      const [t0, a0] = tg.w[j], [t1, a1] = tg.w[j + 1];
      base = a0 + (a1 - a0) * (from - t0) / Math.max(t1 - t0, 1e-6);
    }
    const span = j < 0 ? Math.max(nt - Math.max(tg.w[0][0], tg.r), 0.25) : rateWindow;
    return Math.max(na - base, 0) / span;
  }
  // share is the part of its work a target has done, held at its most so
  // that it never goes back, as a size that a guess gave can grow.
  function share(tg) {
    if (state(tg) === 'ok') return 1;
    let most = 0;
    for (const [, a, z] of tg.w) if (z > 0) most = Math.max(most, Math.min(a / z, 1));
    if (!tg.w.length && tg.z > 0) most = Math.min(tg.a / tg.z, 1);
    return most;
  }
  // unpackFrom is when docker began to extract the layers of a pull: from
  // its message, or, for one that ended, from a long last stretch after
  // its amount stopped growing near its size.
  function unpackFrom(tg, t) {
    if (!tg.u || !tg.w.length) return null;
    const lc = lastChange(tg);
    if (lc == null) return null;
    if (tg.e == null) return tg.m === 'extracting' ? lc : null;
    return share(tg) >= 0.9 && tg.e - lc > 1.5 ? lc : null;
  }

  // ---- the scale ----

  const view = { from: 0, to: 10, f0: 0, t0: 10, at: 0, zoom: null };
  function ease(p) { return p < .5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2; }
  // goal is the range the axis should show at t: the zoomed step, the
  // whole run once it ended, or else the nice horizon past the clock.
  function goal(t) {
    if (view.zoom) return view.zoom;
    if (run.end != null) return [0, Math.max(10, run.end * 1.02)];
    const need = Math.max(t, 1) / 0.88;
    return [0, nice.find(n => n >= need) || need * 1.2];
  }
  // range eases the shown range towards the goal and returns it.
  const shown = { a: 0, b: 10 };
  function range(now, t) {
    const [ga, gb] = goal(t);
    if (ga !== view.from || gb !== view.to) {
      view.f0 = shown.a; view.t0 = shown.b; view.from = ga; view.to = gb;
      view.at = matchMedia('(prefers-reduced-motion: reduce)').matches ? now - easeFor : now;
    }
    const p = ease(Math.min(1, (now - view.at) / easeFor));
    shown.a = view.f0 + (view.from - view.f0) * p;
    shown.b = view.t0 + (view.to - view.t0) * p;
    return shown;
  }

  // ---- elements ----

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function setStyle(e, prop, v) { if (e.style[prop] !== v) e.style[prop] = v; }
  function setText(e, v) { if (e.textContent !== v) e.textContent = v; }
  function setClass(e, v) { if (e.className !== v) e.className = v; }
  function setAttr(e, k, v) { if (e.getAttribute(k) !== v) e.setAttribute(k, v); }
  // fill gives a container the children that parts describe, as [class,
  // text, title] each, rebuilding them only when they changed.
  function fill(box, parts) {
    const key = JSON.stringify(parts);
    if (box._k === key) return;
    box._k = key;
    box.replaceChildren(...parts.filter(Boolean).map(p => {
      const e = el(p[3] || 'span', p[0], p[1]);
      if (p[2]) e.title = p[2];
      return e;
    }));
  }
  // segs gives a track the segments that list describes, each {c, l, w,
  // title, f (the width of its fill, in percent)}, reusing its elements.
  function segs(trk, list) {
    while (trk.children.length > list.length) trk.lastChild.remove();
    list.forEach((s, i) => {
      let e = trk.children[i];
      if (!e || e.tagName !== (s.svg ? 'svg' : 'SPAN')) {
        e = s.svg ? document.createElementNS('http://www.w3.org/2000/svg', 'svg') : el('span');
        if (trk.children[i]) trk.replaceChild(e, trk.children[i]); else trk.appendChild(e);
      }
      if (s.svg) { drawLane(e, s); } else {
        setClass(e, s.c);
        if (s.f != null) {
          let f = e.firstChild;
          if (!f) { f = el('span', 'f'); e.appendChild(f); }
          setStyle(f, 'width', s.f.toFixed(2) + '%');
        } else if (e.firstChild) e.firstChild.remove();
      }
      setStyle(e, 'left', s.l.toFixed(1) + 'px');
      setStyle(e, 'width', Math.max(s.w, s.min || 0).toFixed(1) + 'px');
      if ((e.getAttribute('data-tip') || '') !== (s.title || '')) {
        e.setAttribute('data-tip', s.title || '');
        let tt = e.querySelector('title');
        if (s.svg) {
          if (!tt) { tt = document.createElementNS('http://www.w3.org/2000/svg', 'title'); e.prepend(tt); }
          tt.textContent = s.title || '';
        } else e.title = s.title || '';
      }
    });
  }
  // drawLane draws the throughput of a step as an area under a line.
  function drawLane(svg, s) {
    setAttr(svg, 'class', 'lane' + (s.done ? ' done' : ''));
    setAttr(svg, 'viewBox', '0 0 ' + Math.max(s.w, 1).toFixed(1) + ' 16');
    setAttr(svg, 'preserveAspectRatio', 'none');
    setAttr(svg, 'aria-hidden', 'true');
    let a = svg.querySelector('path.a'), l = svg.querySelector('path.l');
    if (!a) {
      a = document.createElementNS('http://www.w3.org/2000/svg', 'path'); a.setAttribute('class', 'a');
      l = document.createElementNS('http://www.w3.org/2000/svg', 'path'); l.setAttribute('class', 'l');
      svg.append(a, l);
    }
    setAttr(a, 'd', s.area);
    setAttr(l, 'd', s.line);
  }

  // ---- state of the page ----

  let run = null;
  let version = 0;
  let root = null; // the elements of the page that this file draws
  const rows = new Map();
  const chosen = new Map(); // step id → open, as the user chose
  const seenRunning = new Set();
  const lanes = new Map(); // step id → {key, pts}
  let frozenAt = null; // the clock where the live updates stopped

  function find() {
    const g = document.querySelector('[data-g="gantt"]');
    if (!g) return null;
    const q = n => document.querySelector('[data-g="' + n + '"]');
    return {
      g, ticks: q('ticks'), over: q('over'), cur: q('cur'), body: q('body'), whole: q('whole'),
      clock: q('clock'), hl: q('hl'), counts: q('counts'), note: q('note'), eta: q('eta')
    };
  }

  // now is the time to draw at, in seconds since the run began.
  function nowT(ms) {
    if (run.end != null) return run.end;
    if (run.suspended) return run.last;
    const conn = document.getElementById('conn');
    if (conn && conn.textContent.trim()) {
      if (frozenAt == null) frozenAt = (ms - run.origin) / 1000;
      return frozenAt;
    }
    frozenAt = null;
    return run.origin ? Math.max((ms - run.origin) / 1000, run.last) : run.last;
  }

  // isOpen says whether a step shows its targets: as the user chose; a
  // failed one; one of at most autoOpenMax targets that this page saw run,
  // which stays open once it ended, so that the rows below it do not move.
  function isOpen(st, n) {
    if (chosen.has(st.i)) return chosen.get(st.i);
    if (state(st) === 'failed') return true;
    return seenRunning.has(st.i) && n > 0 && n <= autoOpenMax;
  }

  // ---- rows ----

  function rowEl(key) {
    let r = rows.get(key);
    if (!r) {
      const e = el('div', 'g-row');
      e.dataset.key = key;
      const lbl = el('div', 'lbl'), trk = el('div', 'trk'), res = el('div', 'res');
      e.append(lbl, trk, res);
      r = { e, lbl, trk, res };
      rows.set(key, r);
    }
    return r;
  }

  // X places a time on the track.
  let W = 600, X0 = 0;
  const X = t => (t - shown.a) / (shown.b - shown.a) * W;
  const stamp = t => new Date(run.origin + t * 1000).toISOString().slice(11, 23) + ' UTC';
  function tip(label, a, b) {
    return label + ': +' + dur(a, true) + ' to +' + dur(b, true) + ' (' + dur(b - a, true) + '), ' + stamp(a) + ' to ' + stamp(b);
  }

  function mark(cls) { return ['mk ' + cls, '', cls]; }

  function stepModel(st, t) {
    const tg = targets(st, t);
    const n = Math.max(tg.length, 0);
    const by = c => tg.filter(x => state(x) === c).length;
    return {
      s: state(st), tg, n, done: by('ok') + by('skipped'), failed: by('failed'), running: tg.filter(x => state(x) === 'running'),
      bytes: tg.some(x => x.u === 'bytes'), end: endOf(st, t), waits: waits(st, t)
    };
  }

  // pullSum adds up the work of the targets of a step: the bytes, the
  // rate, those without a size yet, and the bytes to go of those that know
  // their size.
  function pullSum(m, t) {
    let a = 0, rate = 0, nosize = 0, left = 0, sure = true;
    for (const x of m.tg) {
      const st = state(x);
      if (x.u === 'bytes') {
        a += st === 'ok' ? Math.max(x.a, amountAt(x, t)) : amountAt(x, t);
        rate += rateAt(x, t);
        if (st === 'running' || st === 'queued') {
          if (x.z > 0) left += Math.max(x.z - amountAt(x, t), 0);
          if (!(x.z > 0) || x.m === 'size estimated') { nosize++; sure = false; }
        }
      } else if (st === 'running' || st === 'queued') { nosize++; sure = false; }
    }
    return { a, rate, nosize, left, sure };
  }

  function stepRes(st, m, t) {
    const el = elapsed(st, t);
    const usual = run.usual[st.n];
    const usually = usual ? 'usually ' + dur(usual / 1000, true) : '';
    const failed = m.failed ? ' · ' + m.failed + ' failed' : '';
    if (m.bytes) {
      const p = pullSum(m, t);
      if (m.s === 'running') {
        const last = Math.max(...m.tg.map(x => newest(x, t) || 0));
        let small = p.rate > 0 ? bytes(p.rate) + '/s' : (last && t - last > staleAfter ? 'no bytes for ' + dur(t - last) : '');
        if (p.nosize) small += (small ? ' · ' : '') + p.nosize + ' without size';
        return [m.done + '/' + m.n + ' · ' + bytes(p.a) + failed, small];
      }
      return [m.done + '/' + m.n + failed + ' · ' + dur(el, true), bytes(p.a) + (el > 0 && p.a ? ' · ' + bytes(p.a / el) + '/s' : '')];
    }
    if (m.n) {
      if (m.s === 'running') {
        let small = '';
        if (m.running.length > 1) small = m.running.length + ' running';
        else if (m.running.length === 1) {
          const r0 = m.running[0], ph = phases(r0).filter(p => !ended(p)).pop();
          small = (r0.node || r0.n) + (ph ? ' ' + phaseWord(ph) : '');
        }
        return [m.done + '/' + m.n + failed, small];
      }
      return [m.done + '/' + m.n + failed + ' · ' + dur(el, true), m.s === 'ok' ? usually : said(m.s)];
    }
    const w = m.waits.filter(x => !ended(x)).pop();
    if (m.s === 'running') return [dur(el, true), w ? 'waits ' + dur(elapsed(w, t)) : usually];
    const waited = m.waits.reduce((s, x) => s + elapsed(x, t), 0);
    return [dur(el, true), waited >= 1 ? 'waited ' + dur(waited) : (m.s === 'ok' ? usually : said(m.s))];
  }

  // stepCaption is the second line of a step without targets: what it
  // waits for, in the column of the labels, so that it never crosses the
  // cursor of the clock.
  function stepCaption(st, m) {
    const w = m.waits.filter(x => !ended(x)).pop();
    if (w) return 'waits for ' + w.n + (w.m ? ': ' + w.m : '');
    if (m.waits.length) return 'waited for ' + m.waits.map(x => x.n).join(', ');
    if (m.s === 'running' && st.m) return st.m;
    return '';
  }

  function lanePoints(st, m, t) {
    // The throughput of the step's pulls on a grid of a quarter second,
    // up to the newest sample, past which nothing is drawn.
    const last = Math.max(...m.tg.map(x => (x.w.length ? x.w[x.w.length - 1][0] : 0)));
    const to = Math.min(endOf(st, t), last);
    const key = version + ':' + m.tg.length + ':' + to.toFixed(2);
    const c = lanes.get(st.i);
    if (c && c.key === key) return c;
    const pts = [];
    if (st.r != null) for (let g = st.r; g <= to + 1e-6; g += 0.25) {
      let v = 0;
      for (const x of m.tg) v += rateAt(x, g);
      pts.push([g, v]);
    }
    const out = { key, pts, to };
    lanes.set(st.i, out);
    return out;
  }

  function laneSeg(st, m, t) {
    if (!m.bytes || st.r == null) return null;
    const { pts, to } = lanePoints(st, m, t);
    if (pts.length < 2) return null;
    const x0 = X(st.r), w = Math.max(X(to) - x0, 1);
    let max = 0;
    for (const p of pts) max = Math.max(max, p[1]);
    if (max <= 0) return null;
    const e10 = Math.pow(10, Math.floor(Math.log10(max)));
    const top = [1, 2, 5, 10].map(k => k * e10).find(v => v >= max);
    const span = to - st.r || 1;
    let line = '', area = 'M0 16';
    pts.forEach((p, i) => {
      const x = ((p[0] - st.r) / span * w).toFixed(1), y = (16 - p[1] / top * 15).toFixed(1);
      line += (i ? 'L' : 'M') + x + ' ' + y;
      area += 'L' + x + ' ' + y;
    });
    area += 'L' + w.toFixed(1) + ' 16Z';
    return { svg: true, l: x0, w, line, area, done: m.s !== 'running', title: 'throughput of ' + st.n + ', at most ' + bytes(max) + '/s' };
  }

  function stepTrack(st, m, t) {
    const list = [];
    if (st.r == null) {
      list.push({ c: 'wt', l: X(st.q), w: X(t) - X(st.q), min: 2, title: st.n + ': queued for ' + dur(t - st.q) });
      return list;
    }
    const x0 = X(st.r), x1 = X(m.end);
    const running = m.s === 'running';
    const seg = { c: 'bar ' + (running ? (m.n ? '' : 'indet') : m.s), l: x0, w: x1 - x0, min: 3, title: tip(st.n, st.r, m.end) };
    if (running && m.n) seg.f = m.tg.reduce((s, x) => s + share(x), 0) / m.n * 100;
    list.push(seg);
    for (const w of m.waits) {
      const a = X(w.r), b = X(endOf(w, t));
      list.push({ c: 'wt', l: a, w: b - a, min: 2, title: tip('waits for ' + w.n, w.r, endOf(w, t)) });
    }
    const lane = laneSeg(st, m, t);
    if (lane) list.push(lane);
    return list;
  }

  function strip(st, m) {
    if (!m.n) return [];
    const max = 30;
    const out = m.tg.slice(0, max).map(x => {
      const s = state(x);
      let c = { queued: 'q', running: 'run', ok: 'ok', failed: 'bad', canceled: 'cx', skipped: 'q' }[s] || 'q';
      if (s === 'running' && x.u && firstByte(x) == null) c = 'w';
      return [c, '', (x.node || x.n) + ': ' + said(s), 'i'];
    });
    if (m.n > max) out.push(['more', '+' + (m.n - max)]);
    return out;
  }

  function targetRow(tg, st, t) {
    const s = state(tg), end = endOf(tg, t);
    const list = [];
    const res = [];
    const ph = phases(tg);
    const pullOnly = (tg.u || st.n === 'pull images') && ph.every(p => p.n === 'docker pull');
    if (s === 'queued') {
      list.push({ c: 'wt', l: X(tg.q), w: X(t) - X(tg.q), min: 2, title: (tg.node || tg.n) + ': queued for ' + dur(t - tg.q) });
      res.push(['st', 'queued'], ['amt', ''], ['rt', ''], ['tm', dur(t - tg.q)]);
    } else if (pullOnly) {
      const fb = firstByte(tg);
      const fbT = fb == null ? end : Math.min(Math.max(fb, tg.r), end);
      const up = unpackFrom(tg, t);
      const aEnd = up != null ? up : end;
      if (fbT > tg.r) list.push({ c: 'wt', l: X(tg.r), w: X(fbT) - X(tg.r), min: 2, title: tip('waits for its first byte', tg.r, fbT) });
      if (aEnd > fbT || s !== 'running') {
        const seg = { c: 'bar ' + (s === 'running' ? '' : s), l: X(fbT), w: X(Math.max(aEnd, fbT)) - X(fbT), min: 3, title: tip(short(tg.node || tg.n), fbT, Math.max(aEnd, fbT)) };
        if (s === 'running') seg.f = share(tg) * 100;
        list.push(seg);
      }
      if (up != null) list.push({ c: 'up' + (s === 'running' ? '' : ' done'), l: X(up), w: X(end) - X(up), min: 2, title: tip('docker extracts', up, end) });
      const isBytes = tg.u === 'bytes';
      const a = amountAt(tg, t), z = tg.z;
      const amount = isBytes ? bytes(a) + (z > 0 ? ' / ' + (tg.m === 'size estimated' ? '≈' : '') + bytes(z) : '')
        : a + (z > 0 ? ' / ' + z : '') + ' layers';
      let word = said(s), rate = '';
      if (s === 'running') {
        const nt = newest(tg, t);
        if (fb == null) word = 'waiting';
        else if (up != null) word = 'extracting';
        else word = 'downloading';
        const r = rateAt(tg, t);
        if (r > 0) rate = bytes(r) + '/s';
        else if (isBytes && fb != null && nt != null && t - nt > staleAfter) rate = 'none ' + dur(t - nt);
      } else if (s === 'ok') word = tg.m === 'up to date' ? 'up to date' : 'done';
      res.push(['st ' + s, word], ['amt', fb == null && s === 'running' ? '' : amount], ['rt', rate], ['tm', dur(end - tg.r, true)]);
    } else if (ph.length) {
      const x0 = X(tg.r);
      if (ph[0].r > tg.r + 0.05) list.push({ c: 'phs lead', l: x0, w: X(ph[0].r) - x0 - 1, min: 1, title: tip('starts', tg.r, ph[0].r) });
      ph.forEach((p, i) => {
        const pe = endOf(p, t), live = s === 'running' && !ended(p);
        list.push({ c: 'phs c' + Math.min(3, i) + (p.k === 'wait' ? ' pw' : '') + (live ? ' live' : '') + ' ' + s, l: X(p.r), w: X(pe) - X(p.r) - 1, min: 2, title: tip(phaseName(p) + (p.m ? ' (' + p.m + ')' : ''), p.r, pe) });
      });
      const cur = ph.filter(p => !ended(p)).pop() || ph[ph.length - 1];
      let text;
      if (s === 'running') {
        const quiet = cur.k === 'call' && elapsed(cur, t) > 2 && /create|pull/.test(cur.n);
        text = phaseWord(cur) + ' · ' + dur(elapsed(cur, t), true) + (quiet ? ' · no progress reported' : '');
        if (cur.k === 'wait' && cur.to) text += ' of ' + dur(cur.to);
      } else if (s === 'failed') text = tg.err || 'failed';
      else text = dur(end - tg.r, true) + (s === 'ok' ? '' : ' · ' + said(s));
      if (tg.u === 'bytes' && tg.w.length) text += ' · pulled ' + bytes(amountAt(tg, t));
      res.push(['txt' + (s === 'failed' ? ' bad' : ''), text, s === 'failed' ? text : '']);
    } else {
      const seg = { c: 'bar ' + (s === 'running' ? 'indet' : s), l: X(tg.r), w: X(end) - X(tg.r), min: 3, title: tip(tg.node || tg.n, tg.r, end) };
      list.push(seg);
      const text = s === 'failed' ? (tg.err || 'failed') : (tg.m && s === 'running' ? tg.m + ' · ' : '') + dur(end - tg.r, true);
      res.push(['txt' + (s === 'failed' ? ' bad' : ''), text, s === 'failed' ? text : '']);
    }
    let mk = s === 'skipped' ? 'queued' : s;
    if (s === 'running' && tg.u && firstByte(tg) == null) mk = 'waiting';
    const lbl = [mark(mk), ['nm', short(tg.node || tg.n), tg.node || tg.n]];
    if (tg.role && tg.role !== tg.node) lbl.push(['role', tg.role, '', 'small']);
    return { cls: 'g-row trow ' + s + (pullOnly ? ' pull' : ''), lbl, list, res };
  }

  // ---- draw ----

  function drawRows(t) {
    const out = [];
    const top = run.root.kids.filter(s => s.k !== 'call' && visible(s, t));
    for (const st of top) {
      const m = stepModel(st, t);
      if (m.s === 'running') seenRunning.add(st.i);
      const has = m.n > 0;
      const open = has && isOpen(st, m.n);
      const [r1, r2] = stepRes(st, m, t);
      const lbl = [mark(m.s === 'running' && m.waits.some(w => !ended(w)) ? 'waiting' : (m.s === 'skipped' ? 'queued' : m.s)), ['nm', st.n, st.n]];
      if (has) lbl.push(['cnt', m.done + '/' + m.n]);
      const cap = has ? '' : stepCaption(st, m);
      out.push({
        key: st.i, step: has ? st.i : '', open,
        cls: 'g-row srow ' + m.s + (has ? ' has' : '') + (st.k === 'wait' ? ' wait' : ''),
        lbl, strip: strip(st, m), cap, list: stepTrack(st, m, t), res: [['b', r1, '', 'b'], ['sm', r2, '', 'small']]
      });
      if (open) {
        const tg = m.tg.slice().sort((a, b) => (state(b) === 'failed') - (state(a) === 'failed'));
        tg.forEach((x, i) => {
          const r = targetRow(x, st, t);
          r.key = x.i;
          if (i === tg.length - 1) r.cls += ' last';
          out.push(r);
        });
      }
    }
    // Rows keep their elements, in order; a row that went is removed.
    const keep = new Set(out.map(r => r.key));
    for (const [k, r] of rows) if (!keep.has(k)) { r.e.remove(); rows.delete(k); }
    let prev = null;
    for (const r of out) {
      const x = rowEl(r.key);
      const want = prev ? prev.nextSibling : root.body.firstChild;
      if (want !== x.e) root.body.insertBefore(x.e, want);
      setClass(x.e, r.cls);
      if (r.step) {
        x.e.dataset.step = r.step;
        setAttr(x.e, 'aria-expanded', String(r.open));
      } else { delete x.e.dataset.step; x.e.removeAttribute('aria-expanded'); }
      if (r.strip) {
        if (!x.l1) {
          x.l1 = el('div', 'l1'); x.disc = el('button', 'disc'); x.disc.type = 'button';
          x.disc.innerHTML = '<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M3 1.5 6.5 5 3 8.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>';
          x.l1b = el('span', 'l1b'); x.l2 = el('div', 'strip');
          x.l1.append(x.disc, x.l1b);
          x.lbl.replaceChildren(x.l1, x.l2);
        }
        setClass(x.disc, 'disc' + (r.step ? '' : ' none'));
        setAttr(x.disc, 'aria-expanded', String(!!r.open));
        setAttr(x.disc, 'aria-label', (r.open ? 'Hide' : 'Show') + ' the targets of ' + r.lbl[1][1]);
        x.disc.tabIndex = r.step ? 0 : -1;
        fill(x.l1b, r.lbl);
        fill(x.l2, r.cap ? [['cap', r.cap, r.cap]] : r.strip);
      } else fill(x.lbl, r.lbl);
      segs(x.trk, r.list);
      fill(x.res, r.res);
      prev = x.e;
    }
    if (!out.length) {
      if (!root.body.querySelector('.g-empty')) root.body.appendChild(el('div', 'g-empty faint', 'No steps yet.'));
    } else root.body.querySelectorAll('.g-empty').forEach(e => e.remove());
  }

  function drawAxis(t) {
    const r = root.ticks.getBoundingClientRect(), g = root.g.getBoundingClientRect();
    W = r.width || 600; X0 = r.left - g.left;
    const len = shown.b - shown.a;
    const max = innerWidth < 760 ? 4 : 8;
    const step = tickSteps.find(s => len / s <= max) || 7200;
    const ticks = [];
    for (let v = Math.ceil(shown.a / step) * step; v <= shown.b + 1e-6; v += step) ticks.push(v);
    const doneRun = run.end != null;
    const nowLbl = dur(t, doneRun);
    const nowX = X(t);
    const nowW = nowLbl.length * 7 + 12;
    const lbls = ticks.map(v => {
      const lb = tickLabel(v), lw = lb.length * 7, x = X(v);
      const lx = Math.min(Math.max(0, x - lw / 2), W - lw);
      const hide = nowX >= 0 && nowX <= W && Math.abs(lx + lw / 2 - Math.min(Math.max(nowW / 2, nowX), W - nowW / 2)) < (lw + nowW) / 2 + 4;
      return { c: hide ? 'tk gone' : 'tk', l: lx, w: lw, text: lb };
    });
    const nc = doneRun ? ({ ok: ' ok', canceled: ' warn' }[run.state] || ' bad') : '';
    if (nowX >= -1 && nowX <= W + 1) lbls.push({ c: 'nowp' + nc, l: Math.min(Math.max(0, nowX - nowW / 2), W - nowW), w: nowW, text: nowLbl });
    while (root.ticks.children.length > lbls.length) root.ticks.lastChild.remove();
    lbls.forEach((s, i) => {
      let e = root.ticks.children[i];
      if (!e) { e = el('span'); root.ticks.appendChild(e); }
      setClass(e, s.c); setText(e, s.text); setStyle(e, 'left', s.l.toFixed(1) + 'px');
    });
    const lines = ticks.filter(v => v > shown.a + 1e-6).map(v => ({ c: 'gl', l: X0 + X(v), w: 1 }));
    while (root.over.children.length > lines.length) root.over.lastChild.remove();
    lines.forEach((s, i) => {
      let e = root.over.children[i];
      if (!e) { e = el('i'); root.over.appendChild(e); }
      setStyle(e, 'left', s.l.toFixed(1) + 'px');
    });
    let cur = root.cur.firstChild;
    if (!cur) { cur = el('i'); root.cur.appendChild(cur); }
    setClass(cur, 'now' + nc + (nowX < 0 || nowX > W ? ' gone' : ''));
    setStyle(cur, 'left', (X0 + nowX).toFixed(1) + 'px');
    if (root.whole) root.whole.hidden = !view.zoom;
  }

  // ---- summary ----

  function leadStep(t) {
    const top = run.root.kids.filter(s => s.k === 'step' && visible(s, t) && state(s) === 'running');
    const withT = top.filter(s => targets(s, t).length);
    return { lead: (withT.length ? withT : top).sort((a, b) => b.r - a.r)[0], n: top.length };
  }

  // counts say in units what the run got done: "3 of 3 nodes ready",
  // "5 of 5 steps ok", and what did not, "1 timed out", "1 stopped". A
  // count of none, as while the run waits for the realm lock, is left out.
  function counts(t) {
    const steps = run.root.kids.filter(s => s.k === 'step' && visible(s, t));
    const out = [];
    for (const st of steps) {
      const noun = nouns[st.n];
      if (!noun) continue;
      const tg = targets(st, t), total = Math.max(tg.length, st.tot || 0);
      if (!total) continue;
      const verb = noun === 'image' ? 'pulled' : verbs[run.root.n] || 'done';
      out.push(['ct', tg.filter(x => state(x) === 'ok').length + ' of ' + plural(total, noun) + ' ' + verb]);
    }
    if (steps.length) out.push(['ct', steps.filter(s => ['ok', 'skipped'].includes(state(s))).length + ' of ' + plural(steps.length, 'step') + ' ok']);
    let timedOut = 0, failed = 0, stopped = 0;
    for (const s of run.spans) {
      if (!visible(s, t) || !(s.k === 'target' || (s.k === 'step' && !targets(s, t).length))) continue;
      const st = state(s);
      if (st === 'failed' && s.ec === 'timeout') timedOut++;
      else if (st === 'failed') failed++;
      else if (st === 'canceled' && s.k === 'target') stopped++;
    }
    if (timedOut) out.push(['ct bad', timedOut + ' timed out']);
    if (failed) out.push(['ct bad', failed + ' failed']);
    if (stopped) out.push(['ct warn', stopped + ' stopped']);
    return out;
  }

  // eta says how long the run has left: from the earlier runs of the
  // command, and from the bytes that the pulls have to go at their rate
  // now; "more than" when either is not sure.
  function eta(t) {
    if (run.end != null || run.suspended) return '';
    let left = 0, sure = true, any = false;
    if (run.usualN && run.usualRun) {
      any = true;
      if (t > run.usualRun) return 'longer than usual (' + dur(run.usualRun) + ')';
      left = run.usualRun - t;
    } else sure = false;
    for (const st of run.root.kids) {
      if (st.k !== 'step' || state(st) !== 'running') continue;
      const m = stepModel(st, t);
      if (!m.bytes) continue;
      const p = pullSum(m, t);
      if (p.rate > 0 && p.left > 0) { any = true; left = Math.max(left, p.left / p.rate); }
      if (!p.sure) sure = false;
    }
    if (!any || left < 1) return '';
    return (sure ? 'about ' : 'more than ') + dur(left) + ' left';
  }

  function note(t, ms) {
    const conn = document.getElementById('conn');
    if (run.end == null && conn && conn.textContent.trim()) return ['bad', 'Live updates stopped, so the clock stands at the last update. The page goes on once they come back.'];
    const failedT = run.spans.filter(s => s.k === 'target' && state(s) === 'failed' && visible(s, t)).sort((a, b) => a.e - b.e)[0];
    if (run.end != null) {
      const took = run.end;
      const usual = run.usualN ? ' Earlier runs took ' + dur(run.usualRun / 1) + '.' : '';
      const steps = run.root.kids.filter(s => s.k === 'step' && visible(s, t) && s.r != null);
      const longest = steps.sort((a, b) => elapsed(b, t) - elapsed(a, t))[0];
      const lng = longest ? ' The longest step was ' + longest.n + ' (' + dur(elapsed(longest, t), true) + (run.usual[longest.n] ? ', usually ' + dur(run.usual[longest.n] / 1000, true) : '') + ').' : '';
      // The head line says how long the run took; the note adds to it.
      if (run.state === 'ok') return ['ok', (usual + lng).trim()];
      if (run.state === 'failed') {
        if (failedT) return ['bad', (failedT.node || failedT.n) + ' failed after ' + dur(failedT.e, true) + ': ' + failedT.err];
        return ['bad', lng.trim()];
      }
      if (run.unfinished && !run.status) return ['warn', 'The event log holds no end of this run; its last event came after ' + dur(run.last, true) + '.'];
      if (run.state === 'canceled') return ['warn', lng.trim()];
      return ['', 'Ended after ' + dur(took, true) + '.'];
    }
    if (run.suspended) return ['warn', 'Suspended: the command is stopped, and goes on once it is resumed.'];
    if (failedT) return ['bad', (failedT.node || failedT.n) + ' failed at ' + dur(failedT.e) + ': ' + failedT.err];
    if (run.stopping) return ['warn', 'Stopping: what runs stops, and what is queued does not start.'];
    const lock = run.root.kids.find(s => s.k === 'wait' && !ended(s));
    if (lock) return ['', 'Waiting for the ' + lock.n + (lock.m ? ' held by ' + lock.m : '') + ' for ' + dur(elapsed(lock, t)) + '.'];
    // The spans that run, the newest first, for what the run does.
    const live = run.spans.filter(s => s.r != null && !ended(s) && s.k !== 'command').sort((a, b) => b.r - a.r);
    if (t - run.last > quietAfter) {
      const call = live.find(s => s.k === 'call');
      let what = '';
      if (call) {
        const tg = run.by.get(call.p);
        what = ' ' + (tg ? (tg.node || tg.n) + ': ' : '') + call.n + ' runs, and reports no progress.';
      }
      return ['', 'No new event for ' + dur(t - run.last) + '.' + what];
    }
    let worst = null, most = 0;
    for (const w of live) {
      if (w.k !== 'wait' || !w.to || w.p === run.root.i) continue;
      const used = elapsed(w, t) / w.to;
      if (used > most) { worst = w; most = used; }
    }
    if (worst && elapsed(worst, t) > 1) {
      const tg = run.by.get(worst.p);
      return [most >= 0.8 ? 'bad' : most >= 0.5 ? 'warn' : '', (tg ? (tg.node || tg.n) + ' waits' : 'Waits') + ' for ' + (worst.m || worst.n) + ': ' + dur(elapsed(worst, t)) + ' of its ' + dur(worst.to) + ' limit.'];
    }
    for (const st of run.root.kids) {
      if (st.k !== 'step' || !visible(st, t) || ended(st)) continue;
      const w = waits(st, t).find(x => !ended(x));
      if (w) return ['', st.n + ' waits for ' + w.n + '.'];
    }
    for (const st of run.root.kids) {
      if (st.k !== 'step' || ended(st)) continue;
      const wait = targets(st, t).filter(x => state(x) === 'running' && x.u && firstByte(x) == null);
      if (wait.length) return ['', wait.map(x => short(x.node || x.n)).join(', ') + (wait.length > 1 ? ' wait' : ' waits') + ' for the first byte.'];
    }
    return ['', ''];
  }

  function headline(t) {
    if (run.end != null) {
      const steps = run.root.kids.filter(s => s.k === 'step' && visible(s, t));
      const parts = [['b', run.state === 'ok' ? 'done' : run.state === 'ended' ? 'ended' : said(run.state), '', 'b']];
      parts.push(['', (run.state === 'canceled' ? ' after ' : ' in ') + dur(run.end, true)]);
      // The counts below say the steps and the nodes; the line adds what
      // the pulls got.
      for (const st of steps) {
        const m = stepModel(st, t);
        if (!m.bytes) continue;
        const p = pullSum(m, t);
        if (p.a) parts.push(['sep', ' · '], ['', bytes(p.a) + ' pulled']);
      }
      return parts;
    }
    const { lead, n } = leadStep(t);
    if (!lead) return [['b', run.suspended ? 'suspended' : 'starting', '', 'b']];
    const m = stepModel(lead, t);
    const parts = [['b', lead.n, '', 'b']];
    if (m.n) parts.push(['sep', ' · '], ['', m.done + ' of ' + m.n + ' done']);
    if (m.bytes) {
      const p = pullSum(m, t);
      parts.push(['sep', ' · '], ['', bytes(p.a) + (p.rate > 0 ? ' at ' + bytes(p.rate) + '/s' : '')]);
    } else if (!m.n) parts.push(['sep', ' · '], ['', dur(elapsed(lead, t))]);
    if (n > 1) parts.push(['sep', ' · '], ['faint', '+' + plural(n - 1, 'step') + ' at the same time']);
    return parts;
  }

  function drawSummary(t, ms) {
    setText(root.clock, dur(t, run.end != null));
    setClass(root.clock, 'clock' + (run.end != null ? ' done ' + run.state : ''));
    fill(root.hl, headline(t));
    fill(root.counts, counts(t));
    const [c, text] = note(t, ms);
    setClass(root.note, 'note ' + c);
    setText(root.note, text || ' ');
    if (root.eta) setText(root.eta, eta(t));
  }

  // ---- loop ----

  function read() {
    const src = document.querySelector('[data-gantt]');
    if (!src) { run = null; return; }
    const json = src.getAttribute('data-gantt');
    if (src._json === json && run) return;
    src._json = json;
    try { run = load(json); version++; } catch (e) { run = null; }
  }

  function frame(ms) {
    requestAnimationFrame(frame);
    if (document.hidden) return;
    const r = find();
    if (!r) { root = null; return; }
    if (!root || root.g !== r.g) { root = r; rows.clear(); chosen.clear(); seenRunning.clear(); lanes.clear(); view.zoom = null; root.body.replaceChildren(); }
    read();
    if (!run) return;
    const t = nowT(Date.now());
    range(ms, t);
    drawAxis(t);
    drawRows(t);
    drawSummary(t, ms);
  }

  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element) || !root) return;
    if (ev.target.closest('[data-g="whole"]')) { view.zoom = null; return; }
    const row = ev.target.closest('.g-body .srow');
    if (!row || !run) return;
    // A click on the track zooms to the step, and back; one on the rest
    // of the row opens or closes its targets.
    if (ev.target.closest('.trk')) {
      const st = run.by.get(row.dataset.key);
      if (!st || st.r == null) return;
      const t = nowT(Date.now());
      const a = st.r, b = endOf(st, t), pad = Math.max((b - a) * 0.08, 0.5);
      const z = [Math.max(a - pad, 0), b + pad];
      view.zoom = view.zoom && view.zoom[0] === z[0] ? null : z;
      return;
    }
    if (!row.dataset.step) return;
    chosen.set(row.dataset.step, row.getAttribute('aria-expanded') !== 'true');
  });
  document.addEventListener('keydown', ev => {
    if (ev.key === 'Escape' && view.zoom) view.zoom = null;
  });

  requestAnimationFrame(frame);
})();
