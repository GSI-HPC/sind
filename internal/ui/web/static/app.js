// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's own script: the theme switch, the CSRF header of htmx requests,
// the live indicator and the banner of a lost connection, and the More
// sheet of a phone.
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

  // say tells screen readers a sentence, through the one polite live region
  // of the page. The same sentence twice is told twice.
  let sayTimer = 0;
  function say(text) {
    const region = document.getElementById('say');
    if (!region || !text) return;
    region.textContent = '';
    clearTimeout(sayTimer);
    sayTimer = setTimeout(() => { region.textContent = text; }, 60);
  }

  function toast(text) {
    const old = document.querySelector('.toast');
    if (old) old.remove();
    const t = el('div', 'toast', text);
    t.setAttribute('aria-hidden', 'true');
    document.body.appendChild(t);
    setTimeout(() => t.remove(), 2600);
    say(text);
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text) e.textContent = text;
    return e;
  }

  // The connection. A stream that drops for a moment and comes back shows
  // only in the indicator; one that stays away for longer dims the page,
  // holds its actions and fills the banner #conn, which says how old the
  // page is. A request for the page tells whether sind ui is down or was
  // started again, which ends every session.
  const conn = { since: 0, timer: 0, shown: false, restarted: false };
  const lostAfter = 1500;

  function app() { return document.querySelector('.app'); }
  function stale() { const a = app(); return !!a && a.classList.contains('stale'); }

  function clock(t) {
    return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  function setLive(on) {
    const live = document.getElementById('live');
    if (!live) return;
    live.classList.toggle('off', !on);
    live.title = on ? 'Live: changes show as they happen' : 'Not connected: reconnecting';
    live.lastElementChild.textContent = on ? 'live' : 'offline';
  }

  function banner(title, text, action) {
    const box = document.getElementById('conn');
    if (!box) return;
    const msg = el('span', 'msg');
    msg.append(el('b', '', title), ' ' + text);
    box.replaceChildren(msg, action);
  }

  function showLost() {
    const a = app();
    if (!a) return;
    a.classList.add('stale');
    conn.shown = true;
    const retry = el('button', 'btn sm', 'Retry');
    retry.type = 'button';
    retry.dataset.connRetry = '';
    banner('Connection lost.', 'What you see is from ' + clock(conn.since) + '. sind ui tries again by itself.', retry);
    probe(false);
  }

  function showRestarted() {
    if (conn.restarted) return;
    conn.restarted = true;
    const signIn = el('a', 'btn sm', 'Sign in again');
    signIn.href = location.href;
    banner('sind ui restarted.', 'This browser must sign in again. What you see is from ' + clock(conn.since) + '.', signIn);
  }

  // probe asks for the page: 403 is a new sind ui, which does not know this
  // browser; an answer is a server that is back, which the stream finds by
  // itself. A press of Retry loads the page again then.
  function probe(retry) {
    fetch(location.href, { method: 'HEAD', cache: 'no-store', credentials: 'same-origin' }).then(r => {
      if (r.status === 403) showRestarted();
      else if (retry) location.reload();
    }, () => { if (retry) toast('sind ui does not answer yet.'); });
  }

  function connLost() {
    setLive(false);
    // Each new try of the stream that fails asks again whether sind ui
    // was started again.
    if (conn.shown) probe(false);
    if (conn.since) return;
    conn.since = Date.now();
    conn.timer = setTimeout(showLost, lostAfter);
  }

  function connBack() {
    setLive(true);
    clearTimeout(conn.timer);
    conn.since = 0;
    if (!conn.shown) return;
    conn.shown = false;
    const a = app();
    if (a) a.classList.remove('stale');
    const box = document.getElementById('conn');
    if (box) box.replaceChildren();
    say('Connected again. The page is up to date.');
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
      if (t.closest('[data-conn-retry]')) { probe(true); return; }
      const copy = t.closest('[data-shell-copy]');
      if (copy) {
        const done = navigator.clipboard ? navigator.clipboard.writeText(copy.dataset.shellCopy) : Promise.reject(new Error('no clipboard'));
        done.then(() => toast('Copied'), () => toast('Select the text and press Ctrl+C to copy'));
        return;
      }
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

    // While the page is not live, its actions wait: they would act on what
    // the page showed then, or fail without a word.
    document.body.addEventListener('htmx:beforeRequest', ev => {
      if (!stale()) return;
      ev.preventDefault();
      toast('Not connected to sind ui. Try again when it is back.');
    });
    document.addEventListener('submit', ev => {
      if (!stale()) return;
      ev.preventDefault();
      toast('Not connected to sind ui. Try again when it is back.');
    }, true);

    // Every request that htmx sends carries the session's CSRF token.
    document.body.addEventListener('htmx:configRequest', ev => {
      ev.detail.headers['X-Sind-CSRF'] = csrfToken();
    });

    document.body.addEventListener('htmx:sseOpen', connBack);
    document.body.addEventListener('htmx:sseError', connLost);
    document.body.addEventListener('htmx:sseMessage', ev => showTabs(ev.target));
  });
})();
