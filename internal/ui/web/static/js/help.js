// SPDX-License-Identifier: LGPL-3.0-or-later

// sind ui's help, in layers that open only when asked: a hint card when the
// pointer rests on a dotted word (data-word), the help pane that ? opens, and
// the jump box that / opens.
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
  // KEYS_KEY is "off" when the user turned the single-key shortcuts off.
  const KEYS_KEY = 'sind-ui-keys';

  // PAGE stands for the help of the page that shows; the server finds it by
  // the path.
  const PAGE = 'page';
  // The topics that the pane may restore: those of help.go.
  const TOPIC_RE = /^(page|index|words|keys|(word|action|howto)\/[a-z0-9-]+)$/;

  // The pages that g then a key goes to; help.go lists the same keys.
  const goto = { o: '/', c: '/clusters', n: '/nodes', m: '/mesh', p: '/monitoring', t: '/topology', d: '/doctor', a: '/activity' };

  const H = {
    seen: new Set(),
    cache: new Map(),
    stack: [],
    hint: null, timer: 0, over: null,
    jumpTimer: 0, jumpSeq: 0, searchTimer: 0, searchSeq: 0,
    g: 0, gTimer: 0,
    // The pane element, kept while the pane is open, so that a swap of the
    // layer does not lose it; where the focus goes back to on close.
    paneEl: null, paneBack: null,
    // The dialog of an action that the pane covers, while it is lifted.
    lifted: null,
    // The element that had the focus before the jump box opened, and the
    // action whose cluster or realm the box asks for.
    jumpBack: null, step: null,
  };
  try {
    const seen = JSON.parse(local.get(SEEN_KEY) || '[]');
    if (Array.isArray(seen)) for (const id of seen) if (typeof id === 'string') H.seen.add(id);
  } catch (e) { /* a broken value: nobody has seen anything */ }

  const $ = id => document.getElementById(id);
  const layer = () => $('layer') || document.body;
  const pane = () => $('helppane');
  const jumpBox = () => $('jump');
  const touch = () => matchMedia('(hover: none) and (pointer: coarse)').matches;
  const typing = () => {
    const el = document.activeElement;
    return !!el && (/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName) || el.isContentEditable || !!(el.closest && el.closest('[role="textbox"]')));
  };

  // Single-key shortcuts: /, ? and the g keys; / filters a list page. The root has the class
  // keys-off while they are off, which the help texts show and which hides
  // the key caps of class k1-on; the buttons of the top bar stop naming
  // the keys too.
  const keysOn = () => local.get(KEYS_KEY) !== 'off';
  function setKeys(on) {
    local.set(KEYS_KEY, on ? 'on' : 'off');
    syncKeys();
  }
  function syncKeys() {
    const on = keysOn();
    document.documentElement.classList.toggle('keys-off', !on);
    for (const b of document.querySelectorAll('[data-help-keys]')) b.checked = on;
    for (const b of document.querySelectorAll('[data-help-toggle]')) {
      b.title = on ? 'Help (?)' : 'Help';
      if (on) b.setAttribute('aria-keyshortcuts', '?'); else b.removeAttribute('aria-keyshortcuts');
    }
    // On a list page, / goes to its filter instead (listFilter).
    const filter = listFilter();
    for (const b of document.querySelectorAll('[data-palette-open]')) {
      b.setAttribute('aria-keyshortcuts', on && !filter ? '/ Control+K' : 'Control+K');
      for (const k of b.querySelectorAll('kbd')) k.hidden = !!filter;
    }
    if (filter) {
      if (on) filter.setAttribute('aria-keyshortcuts', '/'); else filter.removeAttribute('aria-keyshortcuts');
    }
  }

  // listFilter returns the filter field of a list page, such as Clusters,
  // when it shows: / goes there, as the jump box has Ctrl+K too.
  function listFilter() {
    const f = document.querySelector('[data-pg-filter]');
    return f && f.getClientRects().length && !f.closest('[inert]') ? f : null;
  }
  syncKeys();

  // focusable reports whether el can take the focus back.
  const focusable = el => !!el && el !== document.body && el.isConnected && !el.closest('[inert]') && typeof el.focus === 'function';

  function topicURL(topic, extra) {
    const q = new URLSearchParams({ topic: topic === PAGE ? location.pathname : topic });
    if (extra) for (const k of Object.keys(extra)) q.set(k, extra[k]);
    return '/help?' + q;
  }

  // fetchHTML asks for a part of a page, as htmx does: GET /help opened in
  // the browser is a page of its own.
  async function fetchHTML(url) {
    const r = await fetch(url, { headers: { Accept: 'text/html', 'HX-Request': 'true' } });
    if (!r.ok) throw new Error(r.status + ' ' + url);
    return r.text();
  }

  // Words you have seen lose their dots, so pages get quieter as you learn.
  function see(id) {
    if (H.seen.has(id)) return;
    H.seen.add(id);
    local.set(SEEN_KEY, JSON.stringify([...H.seen]));
    mark();
  }

  // mark gives the dotted words their look. Only the first mark of a word
  // takes the focus, so that a table of roles adds one stop, not one a row.
  function mark() {
    const first = new Set();
    for (const el of document.querySelectorAll('[data-word]')) {
      const id = el.dataset.word;
      el.classList.add('term-word');
      el.classList.toggle('seen', H.seen.has(id));
      if (el.closest('a, button, label, summary')) continue;
      if (!first.has(id)) {
        first.add(id);
        if (!el.hasAttribute('tabindex') || el.dataset.wordTab) { el.setAttribute('tabindex', '0'); el.dataset.wordTab = '1'; }
      } else if (el.dataset.wordTab) {
        el.removeAttribute('tabindex');
        delete el.dataset.wordTab;
      }
    }
  }

  // Hint cards.

  const target = t => t && t.closest ? t.closest('[data-word]') : null;
  const topicOf = el => el && el.dataset.word ? 'word/' + el.dataset.word : null;

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
    see(el.dataset.word);
  }

  function hideHint() {
    clearTimeout(H.timer);
    if (!H.hint) return;
    H.hint.anchor.removeAttribute('aria-describedby');
    H.hint.card.remove();
    H.hint = null;
  }

  // The help pane: a dialog beside the page, or over it on a narrow screen.
  // It takes the focus when it opens and gives it back when it closes. It stays below the dialog of an
  // action, so that a pane left open does not cover a dialog that opens
  // later, until it explains that dialog: then it lifts above it (the class
  // over) and is the modal dialog, as the dialog that it covers cannot be
  // used. It drops back when that dialog goes.

  // dialogUnder returns the open dialog of an action, if any.
  const dialogUnder = () => document.querySelector('#layer .scrim [role="dialog"]');

  // explain opens topic above the open dialog, for something in it; Esc
  // gives the focus back to the dialog. from is what asked, when it is not
  // the element with the focus.
  function explain(topic, from) {
    const back = from || document.activeElement;
    open(topic);
    lift(back);
  }

  function lift(back) {
    const p = pane();
    const dlg = dialogUnder();
    if (!p || !dlg) return;
    if (focusable(back) && dlg.contains(back)) H.paneBack = back;
    dlg.setAttribute('aria-modal', 'false');
    p.classList.add('over');
    H.lifted = dlg;
    modal();
  }

  function drop() {
    const dlg = H.lifted;
    H.lifted = null;
    if (dlg && dlg.isConnected) dlg.setAttribute('aria-modal', 'true');
    const p = H.paneEl;
    if (!p) return;
    p.classList.remove('over');
    modal();
  }

  // On a narrow screen the pane covers the page (help.css), so it is modal
  // there too: what is behind it goes inert, as act.js does for a dialog,
  // so that Tab and a screen reader stay in the pane. An element that both
  // make inert carries the marks of both, data-help-inert and
  // data-act-inert, and wakes up only when neither holds it.
  const narrow = matchMedia('(max-width: 600px)');
  H.inert = [];
  function modal() {
    unmodal();
    const p = H.paneEl;
    if (!p || !p.isConnected) return;
    const covers = narrow.matches;
    p.setAttribute('aria-modal', String(covers || !!H.lifted));
    if (!covers) return;
    const walk = parent => {
      for (const c of parent.children) {
        if (c === p || c.id === 'say' || (c.inert && !c.hasAttribute('data-act-inert')) || /^(SCRIPT|TEMPLATE|STYLE|LINK)$/.test(c.tagName)) continue;
        if (c.contains(p)) { walk(c); continue; }
        c.inert = true;
        c.setAttribute('data-help-inert', '');
        H.inert.push(c);
      }
    };
    walk(document.body);
  }

  function unmodal() {
    for (const el of H.inert) {
      el.removeAttribute('data-help-inert');
      if (!el.hasAttribute('data-act-inert')) el.inert = false;
    }
    H.inert = [];
  }
  narrow.addEventListener('change', modal);

  function syncButton() {
    const on = !!pane();
    document.body.classList.toggle('help-open', on);
    for (const b of document.querySelectorAll('[data-help-toggle]')) b.setAttribute('aria-expanded', String(on));
  }

  function save() {
    session.set(PANE_KEY, { open: !!pane(), stack: H.stack });
  }

  function ensurePane() {
    if (pane()) return true;
    const shell = $('help-shell');
    if (!shell) return false;
    const back = document.activeElement;
    if (focusable(back) && !back.closest('#jump')) H.paneBack = back;
    layer().appendChild(shell.content.cloneNode(true));
    H.paneEl = pane();
    syncButton();
    modal();
    return true;
  }

  const current = () => H.stack[H.stack.length - 1];

  // open shows topic in the pane: after the current entry with push, else
  // after the help of this page, so that Back leads there. The focus goes
  // into the pane: to its search field, or on a touch screen, whose
  // keyboard would cover the pane, to its body.
  function open(topic, push) {
    if (!ensurePane()) return;
    if (push && H.stack.length) H.stack.push(topic);
    else H.stack = topic === PAGE ? [PAGE] : [PAGE, topic];
    load();
    const p = pane();
    if (!p.contains(document.activeElement)) (touch() ? $('help-body') : $('help-q')).focus();
  }

  function close() {
    const p = pane();
    if (!p) return;
    const inside = p.contains(document.activeElement);
    drop();
    unmodal();
    p.remove();
    H.paneEl = null;
    syncButton();
    save();
    if (inside) {
      const back = focusable(H.paneBack) ? H.paneBack : document.querySelector('[data-help-toggle]');
      if (back) back.focus();
    }
    H.paneBack = null;
  }

  // say tells a screen reader what the pane shows now, in one short line.
  function say(text) {
    const st = $('help-status');
    if (st) st.textContent = text;
  }

  async function load() {
    const topic = current();
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
    const h2 = body.querySelector('h2');
    say('Help: ' + (h2 ? h2.textContent : 'not found'));
    body.scrollTop = 0;
    save();
  }

  function afterLoad() {
    const body = $('help-body');
    const crumb = body.querySelector('[data-crumb]');
    $('help-crumb').textContent = crumb ? 'Help · ' + crumb.dataset.crumb : 'Help';
    pane().querySelector('[data-help-back]').hidden = H.stack.length < 2;
    syncKeys();
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
        const n = $('help-body').querySelectorAll('.hits li').length;
        say(n ? n + (n === 1 ? ' result' : ' results') : 'Nothing in help matches.');
      } catch (e) { /* keep what shows */ }
    }, 120);
  }

  // ? explains what the pointer or the focus is on, else the open dialog,
  // else the page; ? closes the pane when it has nothing else to explain.
  function help() {
    // Over a dialog, the pane has to come above it to be seen.
    const show = dialogUnder() ? explain : open;
    if (H.hint) { const t = H.hint.topic; hideHint(); show(t); return; }
    const el = target(document.activeElement) || H.over;
    const t = topicOf(el);
    if (t && !el.closest('#helppane')) { show(t); return; }
    const dlg = document.querySelector('#layer [data-help-open]');
    if (dlg) {
      const dt = 'action/' + dlg.dataset.helpOpen;
      if (H.lifted && current() === dt) close(); else explain(dt);
      return;
    }
    if (pane()) { close(); return; }
    open(PAGE);
  }

  // The jump box: a modal dialog with a combobox. The focus stays in its
  // field; the arrow keys move the option that the field names as active.

  function openJump(q) {
    if (jumpBox()) { $('jump-q').focus(); return; }
    const shell = $('help-jump-shell');
    if (!shell) return;
    hideHint();
    const back = document.activeElement;
    H.jumpBack = focusable(back) ? back : null;
    H.step = null;
    layer().appendChild(shell.content.cloneNode(true));
    const input = $('jump-q');
    input.value = q || '';
    input.focus();
    refreshJump();
  }

  // closeJump closes the box and gives the focus back to where it was.
  function closeJump() {
    const j = jumpBox();
    if (!j) return;
    j.remove();
    H.step = null;
    if (focusable(H.jumpBack)) H.jumpBack.focus();
    H.jumpBack = null;
  }

  // step asks for the cluster or the realm of an action, or with null
  // goes back to the first list.
  function step(opt) {
    const input = $('jump-q');
    const bar = $('jump-step');
    H.step = opt ? opt.dataset.jumpStep : null;
    input.value = '';
    bar.hidden = !opt;
    if (opt) {
      $('jump-step-name').textContent = opt.firstChild.textContent.replace(/…$/, '');
    }
    input.placeholder = opt ? '' : 'Jump to… (start with ? for help only)';
    input.setAttribute('aria-label', 'Jump to');
    input.focus();
    refreshJump();
  }

  async function refreshJump() {
    const seq = ++H.jumpSeq;
    const input = $('jump-q');
    if (!input) return;
    const q = new URLSearchParams({ q: input.value, from: location.pathname + location.search });
    if (H.step) q.set('step', H.step);
    try {
      const html = await fetchHTML('/jump?' + q);
      if (seq !== H.jumpSeq || !jumpBox()) return;
      $('jump-res').innerHTML = html;
      const list = $('jump-list');
      const items = list ? [...list.querySelectorAll('.pi')] : [];
      input.setAttribute('aria-expanded', String(items.length > 0));
      // In a step, the field names the question, which the status says too.
      if (H.step && list) {
        input.placeholder = list.getAttribute('aria-label');
        input.setAttribute('aria-label', input.placeholder);
      }
      setActive(items, items.length ? 0 : -1);
      const st = $('jump-status');
      if (st) st.textContent = list ? list.dataset.jumpSay : '';
    } catch (e) {
      if (seq === H.jumpSeq && jumpBox()) {
        $('jump-res').innerHTML = '<div class="empty">The list is not available. Try again.</div>';
        input.setAttribute('aria-expanded', 'false');
        input.removeAttribute('aria-activedescendant');
      }
    }
  }

  // setActive makes option i the active one, or none for -1.
  function setActive(items, i) {
    const input = $('jump-q');
    items.forEach((x, k) => {
      x.classList.toggle('on', k === i);
      x.setAttribute('aria-selected', String(k === i));
    });
    if (i < 0) { input.removeAttribute('aria-activedescendant'); return; }
    input.setAttribute('aria-activedescendant', items[i].id);
    items[i].scrollIntoView({ block: 'nearest' });
  }

  function moveJump(d) {
    const list = $('jump-list');
    const items = list ? [...list.querySelectorAll('.pi')] : [];
    if (!items.length) return;
    const i = items.findIndex(x => x.classList.contains('on'));
    setActive(items, Math.max(0, Math.min(items.length - 1, i + d)));
  }

  function pickJump() {
    const list = $('jump-list');
    const on = list && (list.querySelector('.pi.on') || list.querySelector('.pi'));
    if (on) on.click();
  }

  // openDialog opens the dialog of a link over the page, as its buttons do.
  function openDialog(href) {
    closeJump();
    htmx.ajax('GET', href, { target: '#layer', swap: 'innerHTML' });
  }

  // runCmd runs a command of the jump box.
  function runCmd(cmd) {
    closeJump();
    if (cmd === 'help') { open(PAGE); return; }
    if (cmd === 'theme') { const b = document.querySelector('[data-theme-toggle]'); if (b) b.click(); return; }
    if (cmd === 'keys') {
      setKeys(!keysOn());
      // app.js shows the toast.
      document.dispatchEvent(new CustomEvent('sind:toast', { detail: keysOn() ? 'Single-key shortcuts are on.' : 'Single-key shortcuts are off. Ctrl+K still opens the jump box.' }));
    }
  }

  // The hint of a g chord: the keys that may follow, for a moment.
  function chordHint(on) {
    clearTimeout(H.gTimer);
    const old = document.querySelector('.chordhint');
    if (old) old.remove();
    if (!on) return;
    const el = document.createElement('div');
    el.className = 'chordhint';
    el.setAttribute('aria-hidden', 'true');
    const names = { o: 'Overview', c: 'Clusters', n: 'Nodes', m: 'Mesh', p: 'Monitoring', t: 'Topology', d: 'Doctor', a: 'Activity' };
    const b = document.createElement('b');
    b.textContent = 'g then';
    el.appendChild(b);
    for (const k of Object.keys(goto)) {
      const s = document.createElement('span');
      const kbd = document.createElement('kbd');
      kbd.textContent = k;
      s.append(kbd, ' ' + names[k]);
      el.appendChild(s);
    }
    document.body.appendChild(el);
    H.gTimer = setTimeout(() => chordHint(false), 1200);
  }

  // Events. They are added in the capture phase, so that ? and Esc reach
  // the help before anything else on the page.

  document.addEventListener('mouseover', ev => {
    const el = target(ev.target);
    H.over = el;
    if (!el || el.closest('#helppane, #jump')) { hideHint(); return; }
    if (H.hint && H.hint.anchor === el) return;
    hideHint();
    H.timer = setTimeout(() => { if (H.over === el) showHint(el); }, 300);
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
    if (!(t instanceof Element)) return;
    if (H.hint) hideHint();
    if (t.closest('[data-help-toggle]')) { if (pane()) close(); else open(PAGE); return; }
    if (t.closest('[data-help-close]')) { close(); return; }
    if (t.closest('[data-help-back]')) { H.stack.pop(); load(); return; }
    const helpOpen = t.closest('[data-help-open]');
    if (helpOpen) { explain('action/' + helpOpen.dataset.helpOpen); return; }
    if (t.closest('[data-palette-open]')) { openJump(''); return; }
    if (t.closest('[data-jump-close]')) { closeJump(); return; }
    if (t.closest('[data-jump-back]')) { step(null); return; }
    const topic = t.closest('[data-help-topic]');
    // On the help page, a topic is a link that the browser follows.
    if (topic && !topic.closest('.helpdoc')) {
      ev.preventDefault();
      const fromJump = !!topic.closest('#jump');
      if (fromJump) closeJump();
      open(topic.dataset.helpTopic, !fromJump && !!pane());
      return;
    }
    const stepOpt = t.closest('#jump [data-jump-step]');
    if (stepOpt) { step(stepOpt); return; }
    const cmd = t.closest('#jump [data-jump-cmd]');
    if (cmd) { runCmd(cmd.dataset.jumpCmd); return; }
    const dlg = t.closest('#jump [data-jump-dialog]');
    if (dlg) { ev.preventDefault(); openDialog(dlg.getAttribute('href')); return; }
    const jumpBg = t.closest('#jump');
    if (jumpBg && !t.closest('.box')) { closeJump(); return; }
    const word = t.closest('[data-word]');
    if (word && !word.closest('#helppane, a, button, label, summary')) {
      // The word explains itself; a row that it sits in does not open.
      ev.preventDefault();
      ev.stopPropagation();
      const dlg = dialogUnder();
      if (dlg && dlg.contains(word)) explain('word/' + word.dataset.word, word);
      else open('word/' + word.dataset.word);
    }
  }, true);

  document.addEventListener('input', ev => {
    if (ev.target.id === 'help-q') search(ev.target.value);
    if (ev.target.id === 'jump-q') { clearTimeout(H.jumpTimer); H.jumpTimer = setTimeout(refreshJump, 60); }
  });

  document.addEventListener('change', ev => {
    const t = ev.target;
    if (t instanceof HTMLInputElement && t.matches('[data-help-keys]')) setKeys(t.checked);
  });

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
      else if (ev.key === 'Tab') { ev.preventDefault(); $('jump-q').focus(); }
      else if (ev.key === 'Backspace' && H.step && !$('jump-q').value) { ev.preventDefault(); step(null); }
      return;
    }
    if (ev.key === 'Escape') {
      if (H.hint) { hideHint(); ev.preventDefault(); ev.stopImmediatePropagation(); return; }
      const p = pane();
      // Esc in the pane closes it, also over a dialog; elsewhere an open
      // dialog takes Esc first.
      if (p && (p.contains(document.activeElement) || !document.querySelector('#layer [role="dialog"]:not(#helppane)'))) {
        close();
        ev.preventDefault();
        ev.stopImmediatePropagation();
      }
      return;
    }
    if (typing() || ev.ctrlKey || ev.metaKey || ev.altKey || !keysOn()) return;
    if (ev.key === '/') {
      ev.preventDefault();
      const filter = listFilter();
      if (filter) { filter.focus(); filter.select(); } else openJump('');
      return;
    }
    if (ev.key === '?') { ev.preventDefault(); help(); return; }
    const pending = Date.now() - H.g < 1200;
    if (ev.key === 'g' && !pending) { H.g = Date.now(); chordHint(true); return; }
    const chord = pending && goto[ev.key];
    H.g = 0;
    chordHint(false);
    if (chord) location.href = chord;
  }, true);

  document.addEventListener('DOMContentLoaded', () => {
    // A change of the layer that takes the open pane with it, as a dialog
    // that htmx swaps in, puts it back, below the new dialog; a pane lifted
    // above a dialog that went drops back.
    const l = $('layer');
    if (l) {
      new MutationObserver(() => {
        if (H.lifted && !H.lifted.isConnected) drop();
        if (H.paneEl && !H.paneEl.isConnected) { l.appendChild(H.paneEl); modal(); }
      }).observe(l, { childList: true });
    }
    mark();
    const st = session.get(PANE_KEY);
    if (st && st.open && Array.isArray(st.stack) && st.stack.length && ensurePane()) {
      H.stack = st.stack.filter(t => typeof t === 'string' && TOPIC_RE.test(t));
      if (!H.stack.length) H.stack = [PAGE];
      load();
    }
    const changed = () => { mark(); syncKeys(); if (H.hint && !H.hint.anchor.isConnected) hideHint(); };
    document.body.addEventListener('htmx:afterSwap', changed);
    document.body.addEventListener('htmx:sseMessage', changed);
  });
})();
