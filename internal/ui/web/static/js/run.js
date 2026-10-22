// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the progress display. The server draws every page and fragment;
// this file does three small things that need the browser: it counts the
// clocks of runs that go on (an element with data-since, the start in
// milliseconds since 1970), it keeps the details that the user opened open
// when a fragment is morphed in, and it puts the progress of the run in the
// title of the tab. It evaluates no text and needs no inline script.
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

  function tick() {
    const now = Date.now();
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

  // A fragment that /events morphs in brings its details closed, as the
  // server draws them; the user opens and closes them, so the morph leaves
  // their open attribute alone.
  if (window.Idiomorph) {
    Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, el) =>
      !(name === 'open' && el instanceof HTMLDetailsElement);
  }

  function title() {
    const el = document.querySelector('[data-doc-title]');
    if (el && document.title !== el.dataset.docTitle) document.title = el.dataset.docTitle;
  }

  function update() {
    title();
    tick();
  }

  document.addEventListener('DOMContentLoaded', () => {
    const main = document.getElementById('main');
    // A morph changes text and attributes in place, so the observers
    // watch those too: a clock the server drew is counted again before the
    // browser paints it.
    const what = { childList: true, characterData: true, attributes: true, attributeFilter: ['data-since', 'data-doc-title'], subtree: true };
    if (main) new MutationObserver(update).observe(main, what);
    const dock = document.getElementById('tasks');
    if (dock) new MutationObserver(tick).observe(dock, what);
    update();
    setInterval(tick, 250);
  });
})();
