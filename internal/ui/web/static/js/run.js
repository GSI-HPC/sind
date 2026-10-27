// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the progress display. The server draws every page and fragment;
// this file does seven small things that need the browser: it counts the
// clocks of runs that go on (an element with data-since, the start in
// milliseconds since 1970), it places the bars and the timeline finer than
// the whole percent of their classes, it keeps the details as the user left them
// when a fragment is morphed in, it puts the progress of the run in the
// title of the tab, it announces when a command starts or ends, it opens
// and closes the task dock, and it closes the question of Stop. It evaluates no text and needs no inline script.
(() => {
  'use strict';

  // dur writes a duration as the server's runDur does: 3.4s, 42s, 5m07s, 2h03m.
  function dur(ms) {
    const d = Math.max(ms, 0);
    const pad = n => (n < 10 ? '0' : '') + n;
    if (d < 10000) {
      const t = Math.floor(d / 100);
      return Math.floor(t / 10) + '.' + (t % 10) + 's';
    }
    if (d < 60000) return Math.floor(d / 1000) + 's';
    if (d < 3600000) return Math.floor(d / 60000) + 'm' + pad(Math.floor(d % 60000 / 1000)) + 's';
    return Math.floor(d / 3600000) + 'h' + pad(Math.floor(d % 3600000 / 60000)) + 'm';
  }

  // The dock shows its first card, or every card once the user opens it
  // with Show all; the choice stays for the viewer. A card of a command
  // that ended ok hides at its data-hide-at.
  const dockKey = 'sind-ui-dock-open';
  function dockOpen() {
    try { return localStorage.getItem(dockKey) === '1'; } catch (e) { return false; }
  }
  function drawDock() {
    const dock = document.getElementById('tasks');
    if (!dock) return;
    const open = dockOpen();
    dock.classList.toggle('open', open);
    const btn = dock.querySelector('[data-dock-toggle]');
    if (!btn) return;
    const label = open ? 'Show one' : 'Show all';
    if (btn.textContent !== label) btn.textContent = label;
    if (btn.getAttribute('aria-expanded') !== String(open)) btn.setAttribute('aria-expanded', String(open));
  }
  // Stop asks in the page (template run-stop): Keep running and Escape
  // close the question, and so does a click elsewhere.
  function closeStops(except) {
    document.querySelectorAll('details.stopc[open]').forEach(d => { if (d !== except) d.open = false; });
  }
  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element)) return;
    const keep = ev.target.closest('[data-stop-keep]');
    if (keep) {
      const d = keep.closest('details.stopc');
      d.open = false;
      d.querySelector('summary').focus();
      return;
    }
    closeStops(ev.target.closest('details.stopc'));
  });
  document.addEventListener('keydown', ev => {
    if (ev.key !== 'Escape' || !(ev.target instanceof Element)) return;
    const d = ev.target.closest('details.stopc[open]');
    if (!d) return;
    d.open = false;
    d.querySelector('summary').focus();
  });

  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element) || !ev.target.closest('[data-dock-toggle]')) return;
    try { localStorage.setItem(dockKey, dockOpen() ? '0' : '1'); } catch (e) { /* private window */ }
    drawDock();
  });

  // set gives a property of the style of el a value, if it has another.
  function set(el, prop, value) {
    if (el.style[prop] !== value) el.style[prop] = value;
  }

  // place sizes each bar with a data-fill, in hundredths of a percent, and
  // places the timeline of a run whose clock goes on (data-origin on the
  // gantt, its start; not data-since, which is a clock that tick writes) from its times, in milliseconds since the start of the run: the
  // labels and the gridlines at their data-at, the bars from their data-from
  // to their data-to, or to the clock for a span that goes on. The scale
  // grows with the clock as the server's does, and is at least eight
  // seconds, so that all of them move together and smoothly between two
  // updates. Once the clock stands, the classes of the server place them.
  function place(now) {
    document.querySelectorAll('[data-fill]').forEach(el => set(el, 'width', Number(el.dataset.fill) / 100 + '%'));
    document.querySelectorAll('.gantt').forEach(g => {
      const since = Number(g.dataset.origin);
      const live = since > 0;
      const scale = Math.max(now - since, 8000);
      const pct = ms => Math.min(Math.max(ms / scale * 100, 0), 100) + '%';
      g.querySelectorAll('[data-at]').forEach(el => set(el, 'left', live ? pct(Number(el.dataset.at)) : ''));
      g.querySelectorAll('[data-from]').forEach(el => {
        const from = Number(el.dataset.from);
        const to = 'to' in el.dataset ? Number(el.dataset.to) : now - since;
        set(el, 'left', live ? pct(from) : '');
        set(el, 'width', live ? pct(Math.min(Math.max(to, from), scale) - from) : '');
      });
    });
  }

  function tick() {
    const now = Date.now();
    place(now);
    document.querySelectorAll('[data-hide-at]').forEach(el => {
      if (now >= Number(el.dataset.hideAt) && !el.classList.contains('gone')) el.classList.add('gone');
    });
    document.querySelectorAll('[data-since]').forEach(el => {
      const since = Number(el.dataset.since);
      if (!(since > 0)) return;
      const text = dur(now - since);
      if (el.textContent === text) return;
      // Changing the text node leaves the observers below alone.
      if (el.firstChild && el.firstChild.nodeType === Node.TEXT_NODE) el.firstChild.nodeValue = text;
      else el.textContent = text;
    });
  }

  // A fragment that /events morphs in brings its details as the server
  // draws them: a step's open while it runs and closed once it ended, every
  // other one closed. The morph follows the server for a step until the
  // user opens or closes it; from then on, and for every other details, it
  // leaves the open attribute alone. A step that ended stays open a moment,
  // so that the user sees how it ended, and then folds away (fold).
  // app.js calls this rule from its own.
  const chosen = new Set();
  const folding = new Map();
  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element)) return;
    const sum = ev.target.closest('details.grow-details > summary');
    if (!sum || !sum.parentElement.id) return;
    chosen.add(sum.parentElement.id);
    unfold(sum.parentElement);
  });
  const foldAfter = 1200;
  const foldFor = 350;

  // fold closes a step that ended: after foldAfter, its height shrinks to
  // that of its summary over foldFor, unless the user chose its state in
  // between or asks for less motion.
  function fold(el) {
    if (folding.has(el.id)) return;
    folding.set(el.id, setTimeout(() => {
      if (chosen.has(el.id) || !el.isConnected) { folding.delete(el.id); return; }
      const sum = el.querySelector(':scope > summary');
      if (!sum || matchMedia('(prefers-reduced-motion: reduce)').matches) { done(); return; }
      el.style.overflow = 'hidden';
      el.style.height = el.offsetHeight + 'px';
      el.style.transition = 'height ' + foldFor + 'ms ease';
      requestAnimationFrame(() => { el.style.height = sum.offsetHeight + 'px'; });
      folding.set(el.id, setTimeout(done, foldFor));
      function done() {
        folding.delete(el.id);
        el.style.overflow = el.style.height = el.style.transition = '';
        if (!chosen.has(el.id)) el.open = false;
      }
    }, foldAfter));
  }

  // unfold stops the fold of a step whose state the user chose.
  function unfold(el) {
    clearTimeout(folding.get(el.id));
    folding.delete(el.id);
    el.style.overflow = el.style.height = el.style.transition = '';
  }

  if (window.Idiomorph) {
    Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, el, kind) => {
      // The style that place gave stays until its next tick, so that a bar
      // does not jump back to its class.
      if (name === 'style') return !(el instanceof HTMLElement && ('fill' in el.dataset || 'at' in el.dataset || 'from' in el.dataset));
      if (name !== 'open' || !(el instanceof HTMLDetailsElement)) return true;
      if (!el.classList.contains('grow-details') || chosen.has(el.id)) return false;
      if (kind === 'remove' && el.open) { fold(el); return false; }
      unfold(el);
      return true;
    };
  }

  function title() {
    const el = document.querySelector('[data-doc-title]');
    if (el && document.title !== el.dataset.docTitle) document.title = el.dataset.docTitle;
  }

  // The commands that were announced, by their data-say-key, with the
  // state that was: kept for the tab, so that the next page announces a
  // command that ended meanwhile, and only once.
  const saidKey = 'sind-ui-said';
  let said = {};
  try { said = JSON.parse(sessionStorage.getItem(saidKey) || '{}') || {}; } catch (e) { said = {}; }
  let firstScan = true;

  // announce puts one sentence in the live region #say for each command
  // whose state changed: started, stopping, done, failed or stopped. The
  // clocks and the progress lines are outside any live region, so they
  // change in silence. A command that the page shows when it opens is
  // announced only if it started a moment ago, as after a dialog's Run.
  function announce() {
    const region = document.getElementById('say');
    if (!region) return;
    const out = [];
    const now = Date.now();
    document.querySelectorAll('[data-say-key]').forEach(el => {
      const key = el.dataset.sayKey;
      const state = el.dataset.sayState;
      const prev = said[key];
      if (prev === state) return;
      said[key] = state;
      if (prev === undefined && firstScan) {
        const since = Number(el.dataset.saySince);
        if (!(state === 'running' && since > 0 && now - since < 10000)) return;
      }
      if (el.dataset.say && !out.includes(el.dataset.say)) out.push(el.dataset.say);
    });
    firstScan = false;
    const keys = Object.keys(said);
    if (keys.length > 200) {
      // Forget the oldest.
      keys.slice(0, keys.length - 100).forEach(k => { delete said[k]; });
    }
    try { sessionStorage.setItem(saidKey, JSON.stringify(said)); } catch (e) { /* private window */ }
    // app.js tells them, with what the same live update changed.
    if (out.length) document.dispatchEvent(new CustomEvent('sind:say', { detail: out.join(' ') }));
  }

  function update() {
    title();
    tick();
    announce();
  }

  document.addEventListener('DOMContentLoaded', () => {
    const main = document.getElementById('main');
    // A morph changes text and attributes in place, so the observers
    // watch those too: a clock the server drew is counted again before the
    // browser paints it.
    const what = { childList: true, characterData: true, attributes: true, attributeFilter: ['data-since', 'data-doc-title', 'data-say-state'], subtree: true };
    if (main) new MutationObserver(update).observe(main, what);
    const dock = document.getElementById('tasks');
    if (dock) new MutationObserver(() => { tick(); announce(); drawDock(); }).observe(dock, what);
    drawDock();
    update();
    // Tenths of a second, as the terminal draws them.
    setInterval(tick, 100);
  });
})();
