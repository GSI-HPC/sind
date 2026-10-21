// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's help, in layers that open only when asked: a hint card when the
// pointer rests on a dotted word (data-word) or on an action's button
// (data-help), the help pane that ? opens, and the jump box that / opens.
// The texts come from the server as HTML, from GET /help and GET /jump.
// Nothing here evaluates text, and no storage is needed to work: every access
// is guarded.
(() => {
  'use strict';

  const local = {
    get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* private window */ } },
  };
  const session = {
    get(k) { try { return JSON.parse(sessionStorage.getItem(k) || 'null'); } catch (e) { return null; } },
    set(k, v) { try { sessionStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* private window */ } },
  };

  const SEEN_KEY = 'sind-ui-seen';
  const PANE_KEY = 'sind-ui-help-pane';
  const delay = { word: 300, action: 700 };

  // PAGE stands for the help of the page that shows; the server finds it by
  // the path.
  const PAGE = 'page';

  const H = {
    seen: new Set(),
    cache: new Map(),
    stack: [],
    hint: null, timer: 0, over: null,
    jumpTimer: 0, jumpSeq: 0, searchTimer: 0, searchSeq: 0,
    g: 0,
  };
  try {
    const seen = JSON.parse(local.get(SEEN_KEY) || '[]');
    if (Array.isArray(seen)) for (const id of seen) if (typeof id === 'string') H.seen.add(id);
  } catch (e) { /* a broken value: nobody has seen anything */ }

  const $ = id => document.getElementById(id);
  const layer = () => $('layer') || document.body;
  const pane = () => $('helppane');
  const jumpBox = () => $('jump');
  const typing = () => /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement && document.activeElement.tagName);

  function topicURL(topic, extra) {
    const q = new URLSearchParams({ topic: topic === PAGE ? location.pathname : topic });
    if (extra) for (const k of Object.keys(extra)) q.set(k, extra[k]);
    return '/help?' + q;
  }

  async function fetchHTML(url) {
    const r = await fetch(url, { headers: { Accept: 'text/html' } });
    if (!r.ok) throw new Error(r.status + ' ' + url);
    return r.text();
  }

  function toast(text) {
    const old = document.querySelector('.toast');
    if (old) old.remove();
    const el = document.createElement('div');
    el.className = 'toast';
    el.textContent = text;
    document.body.appendChild(el);
    setTimeout(() => el.remove(), 2600);
  }

  // Words you have seen lose their dots, so pages get quieter as you learn.
  function see(id) {
    if (H.seen.has(id)) return;
    H.seen.add(id);
    local.set(SEEN_KEY, JSON.stringify([...H.seen]));
    mark();
  }

  function mark() {
    for (const el of document.querySelectorAll('[data-word]')) {
      el.classList.add('term-word');
      if (!el.hasAttribute('tabindex')) el.setAttribute('tabindex', '0');
      el.classList.toggle('seen', H.seen.has(el.dataset.word));
    }
  }

  // Hint cards.

  const target = t => t && t.closest ? t.closest('[data-word], [data-help]') : null;
  function topicOf(el) {
    if (!el) return null;
    if (el.dataset.word) return 'word/' + el.dataset.word;
    if (el.dataset.help) return 'action/' + el.dataset.help;
    return null;
  }

  async function showHint(el) {
    const topic = topicOf(el);
    if (!topic) return;
    let html = H.cache.get(topic);
    if (html === undefined) {
      try { html = await fetchHTML(topicURL(topic, { card: '1' })); } catch (e) { html = null; }
      H.cache.set(topic, html);
    }
    if (!html || !el.isConnected || (H.over !== el && document.activeElement !== el)) return;
    hideHint();
    const card = document.createElement('div');
    card.className = 'hintcard';
    card.id = 'help-hint';
    card.setAttribute('role', 'tooltip');
    card.innerHTML = html;
    document.body.appendChild(card);
    el.setAttribute('aria-describedby', 'help-hint');
    const r = el.getBoundingClientRect();
    const w = card.offsetWidth, h = card.offsetHeight;
    let y = r.bottom + 8;
    if (y + h > innerHeight - 8) y = Math.max(8, r.top - h - 8);
    card.style.left = Math.max(8, Math.min(r.left, innerWidth - w - 8)) + 'px';
    card.style.top = y + 'px';
    H.hint = { card, anchor: el, topic };
    if (el.dataset.word) see(el.dataset.word);
  }

  function hideHint() {
    clearTimeout(H.timer);
    if (!H.hint) return;
    H.hint.anchor.removeAttribute('aria-describedby');
    H.hint.card.remove();
    H.hint = null;
  }

  // The help pane.

  function syncButton() {
    const on = !!pane();
    document.body.classList.toggle('help-open', on);
    for (const b of document.querySelectorAll('[data-help-toggle]')) {
      b.classList.toggle('on', on);
      b.setAttribute('aria-expanded', String(on));
    }
  }

  function save() {
    session.set(PANE_KEY, { open: !!pane(), stack: H.stack });
  }

  function ensurePane() {
    if (pane()) return true;
    const shell = $('help-shell');
    if (!shell) return false;
    layer().appendChild(shell.content.cloneNode(true));
    syncButton();
    return true;
  }

  // open shows topic in the pane: after the current entry with push, else
  // after the help of this page, so that Back leads there.
  function open(topic, push) {
    if (!ensurePane()) return;
    if (push && H.stack.length) H.stack.push(topic);
    else H.stack = topic === PAGE ? [PAGE] : [PAGE, topic];
    load();
  }

  function close() {
    const p = pane();
    if (!p) return;
    p.remove();
    syncButton();
    save();
  }

  async function load() {
    const topic = H.stack[H.stack.length - 1];
    const body = $('help-body');
    const q = $('help-q');
    if (q) q.value = '';
    try {
      body.innerHTML = await fetchHTML(topicURL(topic));
    } catch (e) {
      body.innerHTML = '<p class="muted">There is no help for this. Reload the page if sind ui was upgraded.</p>';
    }
    if (topic.startsWith('word/')) see(topic.slice(5));
    afterLoad();
    body.scrollTop = 0;
    save();
  }

  function afterLoad() {
    const body = $('help-body');
    const crumb = body.querySelector('[data-crumb]');
    $('help-crumb').textContent = crumb ? 'Help · ' + crumb.dataset.crumb : 'Help';
    pane().querySelector('[data-help-back]').hidden = H.stack.length < 2;
  }

  function search(q) {
    clearTimeout(H.searchTimer);
    H.searchTimer = setTimeout(async () => {
      if (!q.trim()) { load(); return; }
      const seq = ++H.searchSeq;
      try {
        const html = await fetchHTML('/help?' + new URLSearchParams({ q }));
        if (seq !== H.searchSeq || !pane()) return;
        $('help-body').innerHTML = html;
        afterLoad();
      } catch (e) { /* keep what shows */ }
    }, 120);
  }

  // ? explains what the pointer or the focus is on, else the open dialog,
  // else the page; a second ? closes the help of the page.
  function help() {
    if (H.hint) { const t = H.hint.topic; hideHint(); open(t); return; }
    const t = topicOf(target(document.activeElement)) || topicOf(H.over);
    const el = target(document.activeElement) || H.over;
    if (t && !(el && el.closest('#helppane'))) { open(t); return; }
    const dlg = document.querySelector('#layer [data-help-dialog]');
    if (dlg) { open('action/' + dlg.dataset.helpDialog); return; }
    if (pane() && H.stack.length === 1) { close(); return; }
    open(PAGE);
  }

  // The jump box.

  function openJump(q) {
    if (jumpBox()) { $('jump-q').focus(); return; }
    const shell = $('help-jump-shell');
    if (!shell) return;
    hideHint();
    layer().appendChild(shell.content.cloneNode(true));
    const input = $('jump-q');
    input.value = q || '';
    input.focus();
    refreshJump();
  }

  function closeJump() {
    const j = jumpBox();
    if (j) j.remove();
  }

  async function refreshJump() {
    const seq = ++H.jumpSeq;
    const input = $('jump-q');
    if (!input) return;
    try {
      const html = await fetchHTML('/jump?' + new URLSearchParams({ q: input.value }));
      if (seq !== H.jumpSeq || !jumpBox()) return;
      $('jump-list').innerHTML = html;
      const first = $('jump-list').querySelector('.pi');
      if (first) first.classList.add('on');
    } catch (e) {
      if (seq === H.jumpSeq && jumpBox()) $('jump-list').innerHTML = '<div class="empty">The list is not available. Try again.</div>';
    }
  }

  function moveJump(d) {
    const items = [...$('jump-list').querySelectorAll('.pi')];
    if (!items.length) return;
    let i = items.findIndex(x => x.classList.contains('on'));
    items.forEach(x => x.classList.remove('on'));
    i = Math.max(0, Math.min(items.length - 1, i + d));
    items[i].classList.add('on');
    items[i].scrollIntoView({ block: 'nearest' });
  }

  function pickJump() {
    const on = $('jump-list').querySelector('.pi.on') || $('jump-list').querySelector('.pi');
    if (on) on.click();
  }

  // Events. They are added in the capture phase, so that ? and Esc reach
  // the help before anything else on the page.

  document.addEventListener('mouseover', ev => {
    const el = target(ev.target);
    H.over = el;
    if (!el || el.closest('#helppane, #jump')) { hideHint(); return; }
    if (H.hint && H.hint.anchor === el) return;
    hideHint();
    H.timer = setTimeout(() => { if (H.over === el) showHint(el); }, el.dataset.word ? delay.word : delay.action);
  });
  document.addEventListener('mouseout', ev => {
    const el = target(ev.target);
    if (!el || el.contains(ev.relatedTarget)) return;
    if (H.over === el) H.over = null;
    hideHint();
  });
  document.addEventListener('focusin', ev => {
    const el = ev.target.closest && ev.target.closest('[data-word]');
    if (el && !el.closest('#helppane')) showHint(el);
    else if (H.hint && H.hint.anchor !== ev.target) hideHint();
  });

  document.addEventListener('click', ev => {
    const t = ev.target;
    if (H.hint) hideHint();
    if (t.closest('[data-help-toggle]')) { if (pane()) close(); else open(PAGE); return; }
    if (t.closest('[data-help-close]')) { close(); return; }
    if (t.closest('[data-help-back]')) { H.stack.pop(); load(); return; }
    if (t.closest('[data-palette-open]')) { openJump(''); return; }
    const copy = t.closest('[data-help-copy]');
    if (copy) {
      const text = copy.dataset.helpCopy;
      const done = navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject(new Error('no clipboard'));
      done.then(() => toast('Copied'), () => toast('Select the text and press Ctrl+C to copy'));
      return;
    }
    const topic = t.closest('[data-help-topic]');
    if (topic) {
      ev.preventDefault();
      const fromJump = !!topic.closest('#jump');
      if (fromJump) closeJump();
      open(topic.dataset.helpTopic, !fromJump && !!pane());
      return;
    }
    const jumpBg = t.closest('#jump');
    if (jumpBg && !t.closest('.box')) { closeJump(); return; }
    const word = t.closest('[data-word]');
    if (word && !word.closest('#helppane, a, button')) { open('word/' + word.dataset.word); return; }
  }, true);

  document.addEventListener('input', ev => {
    if (ev.target.id === 'help-q') search(ev.target.value);
    if (ev.target.id === 'jump-q') { clearTimeout(H.jumpTimer); H.jumpTimer = setTimeout(refreshJump, 60); }
  });

  // The pages that g then a key goes to.
  const goto = { a: '/activity', c: '/clusters', o: '/', d: '/doctor', n: '/nodes' };

  window.addEventListener('keydown', ev => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'k') {
      ev.preventDefault();
      openJump('');
      return;
    }
    if (jumpBox()) {
      if (ev.key === 'Escape') { closeJump(); ev.preventDefault(); ev.stopImmediatePropagation(); }
      else if (ev.key === 'ArrowDown') { moveJump(1); ev.preventDefault(); }
      else if (ev.key === 'ArrowUp') { moveJump(-1); ev.preventDefault(); }
      else if (ev.key === 'Enter') { ev.preventDefault(); pickJump(); }
      return;
    }
    if (ev.key === 'Escape') {
      if (H.hint) { hideHint(); ev.preventDefault(); ev.stopImmediatePropagation(); return; }
      // An open dialog takes Esc first.
      if (pane() && !document.querySelector('#layer [role="dialog"]')) { close(); ev.preventDefault(); }
      return;
    }
    if (typing() || ev.ctrlKey || ev.metaKey || ev.altKey) return;
    if (ev.key === '/') { ev.preventDefault(); openJump(''); return; }
    if (ev.key === '?') { ev.preventDefault(); help(); return; }
    if (ev.key === 'g') { H.g = Date.now(); return; }
    if (Date.now() - H.g < 1200 && goto[ev.key]) { H.g = 0; location.href = goto[ev.key]; return; }
    H.g = 0;
  }, true);

  document.addEventListener('DOMContentLoaded', () => {
    mark();
    const st = session.get(PANE_KEY);
    if (st && st.open && Array.isArray(st.stack) && st.stack.length && ensurePane()) {
      H.stack = st.stack.filter(t => typeof t === 'string' && (t === PAGE || /^(word|action|howto)\/[a-z0-9-]+$/.test(t)));
      if (!H.stack.length) H.stack = [PAGE];
      load();
    }
    const changed = () => { mark(); if (H.hint && !H.hint.anchor.isConnected) hideHint(); };
    document.body.addEventListener('htmx:afterSwap', changed);
    document.body.addEventListener('htmx:sseMessage', changed);
  });
})();
