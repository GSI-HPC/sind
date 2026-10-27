// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the progress display. The server draws every page and fragment
// but the summary and the timeline of the run page, which gantt.js draws;
// this file does six small things that need the browser: it counts the
// clocks of runs that go on (an element with data-since, the start in
// milliseconds since 1970), it sizes the bars finer than the whole percent
// of their classes, it puts the progress of the run in the title of the
// tab, it announces when a command starts or ends, it opens and closes the
// task dock, and it closes the question of Stop. It evaluates no text and
// needs no inline script.
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
  function closeStop(d) {
    d.open = false;
    d.querySelector('summary').focus();
  }
  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element)) return;
    const keep = ev.target.closest('[data-stop-keep]');
    if (keep) closeStop(keep.closest('details.stopc'));
    else closeStops(ev.target.closest('details.stopc'));
  });
  document.addEventListener('keydown', ev => {
    const d = ev.key === 'Escape' && ev.target instanceof Element && ev.target.closest('details.stopc[open]');
    if (d) closeStop(d);
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

  // place sizes each bar with a data-fill, in hundredths of a percent.
  function place() {
    document.querySelectorAll('[data-fill]').forEach(el => set(el, 'width', Number(el.dataset.fill) / 100 + '%'));
  }

  function tick() {
    const now = Date.now();
    place();
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

  if (window.Idiomorph) {
    // The width that place gave a bar stays until its next tick, so that
    // the bar does not jump back to its class.
    Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, el) =>
      name !== 'style' || !(el instanceof HTMLElement && 'fill' in el.dataset);
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
