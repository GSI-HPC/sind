// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's own script: the theme switch, the CSRF header of htmx requests
// and the live indicator. Pages are rendered on the server; htmx swaps their
// parts. Nothing here evaluates text, and no page has inline script.
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

  function setLive(on) {
    const el = document.getElementById('live');
    if (!el) return;
    el.classList.toggle('off', !on);
    el.title = on ? 'Live: changes show as they happen' : 'Not connected: reconnecting';
    el.lastElementChild.textContent = on ? 'live' : 'offline';
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.addEventListener('click', ev => {
      if (!ev.target.closest('[data-theme-toggle]')) return;
      const dark = root.dataset.theme
        ? root.dataset.theme === 'dark'
        : matchMedia('(prefers-color-scheme: dark)').matches;
      root.dataset.theme = dark ? 'light' : 'dark';
      store.set('sind-ui-theme', root.dataset.theme);
    });

    // Every request that htmx sends carries the session's CSRF token.
    document.body.addEventListener('htmx:configRequest', ev => {
      ev.detail.headers['X-Sind-CSRF'] = csrfToken();
    });

    document.body.addEventListener('htmx:sseOpen', () => setLive(true));
    document.body.addEventListener('htmx:sseError', () => setLive(false));
  });
})();
