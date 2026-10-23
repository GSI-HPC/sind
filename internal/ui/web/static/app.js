// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's own script: the theme switch, the CSRF header of htmx requests,
// the live indicator, and the More sheet of a phone.
// Pages are rendered on the server; htmx swaps their parts. Nothing here
// evaluates text, and no page has inline script.
(() => {
  'use strict';

  const store = {
    get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* private window */ } },
  };

  // The theme is set before the page is drawn, so that it does not flash.
  const root = document.documentElement;
  const saved = store.get('sind-ui-theme');
  if (saved === 'light' || saved === 'dark') root.dataset.theme = saved;

  function csrfToken() {
    const meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.content : '';
  }

  function dark() {
    return root.dataset.theme
      ? root.dataset.theme === 'dark'
      : matchMedia('(prefers-color-scheme: dark)').matches;
  }

  // The theme button is a toggle of the dark theme: pressed while it is on.
  function showTheme() {
    const on = dark();
    document.querySelectorAll('[data-theme-toggle]').forEach(b => {
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.title = on ? 'Dark theme is on: switch to light' : 'Dark theme is off: switch to dark';
    });
  }

  function setLive(on) {
    const el = document.getElementById('live');
    if (!el) return;
    el.classList.toggle('off', !on);
    el.title = on ? 'Live: changes show as they happen' : 'Not connected: reconnecting';
    el.lastElementChild.textContent = on ? 'live' : 'offline';
  }

  // The More sheet of a phone's bar. Its open state is on the rail, which
  // live updates do not replace; the morph keeps the sheet's own state.
  function rail() { return document.getElementById('rail'); }

  function setSheet(open) {
    const nav = rail();
    if (!nav) return;
    nav.classList.toggle('open', open);
    const sheet = document.getElementById('rail-sheet');
    const more = nav.querySelector('[data-rail-more]');
    if (sheet) sheet.hidden = !open;
    if (more) more.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open) {
      const first = sheet && sheet.querySelector('a');
      if (first) first.focus();
    }
  }

  function sheetOpen() { const nav = rail(); return !!nav && nav.classList.contains('open'); }

  // A tab strip that scrolls sideways shows its current tab.
  function showTabs(scope) {
    scope.querySelectorAll('.tabs').forEach(strip => {
      const on = strip.querySelector('[aria-current="page"], .on');
      if (!on || strip.scrollWidth <= strip.clientWidth) return;
      const s = strip.getBoundingClientRect();
      const r = on.getBoundingClientRect();
      if (r.left >= s.left && r.right <= s.right) return;
      strip.scrollLeft += (r.left + r.width / 2) - (s.left + s.width / 2);
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    showTheme();
    showTabs(document);
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', showTheme);

    // The morph keeps what the page holds itself: the open More sheet.
    if (window.Idiomorph) {
      Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, node) => {
        if (name === 'hidden' && node.id === 'rail-sheet') return false;
        if (name === 'aria-expanded' && node.hasAttribute('data-rail-more')) return false;
        return true;
      };
    }

    document.addEventListener('click', ev => {
      const t = ev.target;
      if (t.closest('[data-theme-toggle]')) {
        root.dataset.theme = dark() ? 'light' : 'dark';
        store.set('sind-ui-theme', root.dataset.theme);
        showTheme();
        return;
      }
      if (t.closest('[data-rail-more]')) { setSheet(!sheetOpen()); return; }
      if (sheetOpen() && !t.closest('#rail-sheet')) setSheet(false);
      // A table cell that holds only a checkbox is the checkbox's target
      // too, which a finger finds more easily.
      const cell = t.closest('td, th');
      const box = cell && cell.children.length === 1 && cell.querySelector(':scope > input[type=checkbox]');
      if (box && t === cell) box.click();
    });

    document.addEventListener('keydown', ev => {
      if (ev.key !== 'Escape' || !sheetOpen()) return;
      setSheet(false);
      const more = document.querySelector('[data-rail-more]');
      if (more) more.focus();
    });

    // Every request that htmx sends carries the session's CSRF token.
    document.body.addEventListener('htmx:configRequest', ev => {
      ev.detail.headers['X-Sind-CSRF'] = csrfToken();
    });

    document.body.addEventListener('htmx:sseOpen', () => setLive(true));
    document.body.addEventListener('htmx:sseError', () => setLive(false));
    document.body.addEventListener('htmx:sseMessage', ev => showTabs(ev.target));
  });
})();
