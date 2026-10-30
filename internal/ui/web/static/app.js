// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's own script: the theme switch, the live indicator and the banner
// of a lost connection, the More sheet of a phone, and what live updates do
// to the focus and tell screen readers.
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
  // of the page. The sentences of one moment are told together, once each,
  // so that one does not cut off another; the same sentence later is told
  // again. run.js and pg.js hand their sentences over as the event sind:say,
  // and help.js its toasts as sind:toast.
  let sayTimer = 0;
  let sayLines = [];
  function say(text) {
    const region = document.getElementById('say');
    if (!region || !text) return;
    if (!sayLines.includes(text)) sayLines.push(text);
    region.textContent = '';
    clearTimeout(sayTimer);
    sayTimer = setTimeout(() => {
      region.textContent = sayLines.join(' ');
      sayLines = [];
    }, 60);
  }
  document.addEventListener('sind:say', e => say(e.detail));
  document.addEventListener('sind:toast', e => toast(e.detail));

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
      const first = sheet && sheet.querySelector('summary, a');
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

  // Live updates. Before a fragment is morphed, the focus and the
  // sentences of its [data-say] elements are noted; after it, a changed
  // sentence is told, and a focused control that the update removed or
  // turned into another one gives the focus to the page's heading, so that
  // the next key does not press a control the user never chose.
  const pending = new WeakMap();

  // A control's identity leaves out what live updates change, such as the
  // count of a link or the state in a name: a link stays the same link
  // while its count goes up. Only a control with nothing stable to go by
  // is known by its text.
  const keys = ['id', 'name', 'href', 'type', 'value', 'action', 'hx-get', 'hx-post', 'hx-delete', 'for'];

  function ident(e) {
    const parts = keys.map(k => e.getAttribute(k) || '');
    Array.from(e.attributes).forEach(at => { if (at.name.startsWith('data-')) parts.push(at.name); });
    const known = parts.some(p => p && p !== 'button' && p !== 'submit');
    return [e.tagName].concat(parts, known ? [] : [text(e).slice(0, 80)]).join('|');
  }

  // The text of an element, with a space between its parts, so that a link
  // made of "Activity" and a count of 7 reads "Activity 7".
  function text(e) {
    const w = document.createTreeWalker(e, NodeFilter.SHOW_TEXT);
    const out = [];
    for (let n = w.nextNode(); n; n = w.nextNode()) {
      const t = n.nodeValue.trim();
      if (t) out.push(t);
    }
    return out.join(' ').replace(/\s+/g, ' ');
  }

  // The sentences of a command, which carry data-say-state, are run.js's:
  // it tells each change of state once, across the pages of the tab.
  function sentences(target) {
    const m = new Map();
    target.querySelectorAll('[data-say]:not([data-say-state])').forEach((e, i) => m.set(e.dataset.sayKey || String(i), e.dataset.say));
    return m;
  }

  function before(target) {
    const rec = { says: sentences(target) };
    const a = document.activeElement;
    if (a && a !== document.body && target.contains(a)) {
      rec.el = a;
      rec.id = ident(a);
      rec.label = (a.getAttribute('aria-label') || text(a)).trim();
    }
    pending.set(target, rec);
  }

  function after(target) {
    const rec = pending.get(target);
    if (!rec) return;
    pending.delete(target);
    const told = [];
    sentences(target).forEach((line, key) => {
      if (rec.says.has(key) && rec.says.get(key) !== line) told.push(line);
    });
    if (rec.el) {
      const a = document.activeElement;
      if (!(a === rec.el && a.isConnected && ident(a) === rec.id)) {
        // The same control may be there twice, such as a page in the bar
        // and in the More sheet, and only the one on show takes the focus.
        const back = Array.from(target.querySelectorAll(rec.el.tagName)).some(e => {
          if (ident(e) !== rec.id) return false;
          e.focus({ preventScroll: true });
          return document.activeElement === e;
        });
        // A fragment marked data-own-focus, the list of the task dock,
        // places the focus itself (run.js).
        if (!back && !target.hasAttribute('data-own-focus')) {
          const to = document.querySelector('main h1') || document.getElementById('main');
          if (to) {
            if (!to.hasAttribute('tabindex')) to.setAttribute('tabindex', '-1');
            to.focus({ preventScroll: true });
          }
          if (rec.label) told.unshift(rec.label + ' is no longer available.');
        }
      }
    }
    if (told.length) say(told.join(' '));
  }

  document.addEventListener('DOMContentLoaded', () => {
    showTheme();
    showTabs(document);
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', showTheme);

    // The morph keeps what the page holds itself: the open More sheet, and
    // the tabindex of the element that has the focus. A page's script,
    // which runs before this, such as run.js for the widths of its bars,
    // set its rule first; it still applies.
    if (window.Idiomorph) {
      const page = Idiomorph.defaults.callbacks.beforeAttributeUpdated;
      Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, node, kind) => {
        if (name === 'hidden' && node.id === 'rail-sheet') return false;
        // A menu of a page that is open stays open, with its button.
        if (name === 'open' && node.hasAttribute('data-pg-menu')) return false;
        if (name === 'aria-expanded' && node.parentElement && node.parentElement.hasAttribute('data-pg-menu')) return false;
        if (name === 'aria-expanded' && node.hasAttribute('data-rail-more')) return false;
        if (name === 'tabindex' && kind === 'remove' && node === document.activeElement) return false;
        return typeof page !== 'function' || page(name, node, kind) !== false;
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
    const hold = ev => {
      if (!stale()) return;
      ev.preventDefault();
      toast('Not connected to sind ui. Try again when it is back.');
    };
    document.body.addEventListener('htmx:beforeRequest', hold);
    document.addEventListener('submit', hold, true);

    // The live stream closes when the page goes, before the browser cuts
    // it: a cut stream is an error, which htmx logs and the page would
    // show as a lost connection. A page that the browser brings back from
    // its cache loads again, as its stream is closed.
    const streams = new Set();
    document.body.addEventListener('htmx:sseOpen', ev => { if (ev.detail && ev.detail.source) streams.add(ev.detail.source); });
    window.addEventListener('pagehide', () => {
      for (const s of streams) s.close();
    });
    window.addEventListener('pageshow', ev => { if (ev.persisted && streams.size) location.reload(); });
    // The toasts keep above the strip of the task dock (app.css), which
    // needs its height.
    const dock = document.getElementById('tasks');
    if (dock) new ResizeObserver(() => document.documentElement.style.setProperty('--dock-h', dock.offsetHeight + 'px')).observe(dock);
    document.body.addEventListener('htmx:sseOpen', connBack);
    document.body.addEventListener('htmx:sseError', connLost);
    document.body.addEventListener('htmx:sseBeforeMessage', ev => before(ev.target));
    document.body.addEventListener('htmx:sseMessage', ev => {
      after(ev.target);
      showTabs(ev.target);
    });
  });
})();
