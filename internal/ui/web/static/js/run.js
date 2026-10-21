// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the progress display. The server draws every page and fragment;
// this file does three small things that need the browser: it counts the
// clocks of runs that go on (an element with data-since, the start in
// milliseconds since 1970), it keeps the rows of the timeline that the user
// opened open when a fragment is swapped in, and it puts the progress of the
// run in the title of the tab. It evaluates no text and needs no inline script.
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

  // The rows of the timeline that are open, by span id. A fragment that
  // /events swaps in brings them back closed.
  const open = new Set();

  document.addEventListener('toggle', ev => {
    const el = ev.target;
    if (!(el instanceof HTMLDetailsElement) || !el.dataset.openId) return;
    if (el.open) open.add(el.dataset.openId);
    else open.delete(el.dataset.openId);
  }, true);

  function restore() {
    document.querySelectorAll('details[data-open-id]').forEach(el => {
      if (open.has(el.dataset.openId) && !el.open) el.open = true;
    });
  }

  function title() {
    const el = document.querySelector('[data-doc-title]');
    if (el && document.title !== el.dataset.docTitle) document.title = el.dataset.docTitle;
  }

  function update() {
    restore();
    title();
    tick();
  }

  document.addEventListener('DOMContentLoaded', () => {
    const main = document.getElementById('main');
    if (main) new MutationObserver(update).observe(main, { childList: true, subtree: true });
    const dock = document.getElementById('tasks');
    if (dock) new MutationObserver(tick).observe(dock, { childList: true, subtree: true });
    update();
    setInterval(tick, 250);
  });
})();
