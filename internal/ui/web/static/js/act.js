// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the dialogs of the actions: the steppers, the editors of the
// nodes, the worker groups and the rows of users, groups and accounts of a
// new cluster, the directory that follows the name, the pull that follows
// the image, Edit YAML, the links to a field with a problem, the Copy
// button, and the frame of every dialog: the focus, which stays in the
// dialog and goes back to the button that opened it, the status, and the
// notice of a command that started. The dialogs are the server's HTML,
// which htmx puts into #layer; nothing here evaluates text.
(() => {
  'use strict';

  const layer = () => document.getElementById('layer');
  const dialog = () => document.getElementById('act-dialog');
  const formOf = () => { const d = dialog(); return d && d.querySelector('form.modal'); };
  const inPage = d => !d.closest('#layer');

  // A touch screen gets no focus in a field when a dialog opens, as its
  // keyboard would cover the dialog.
  const touch = () => matchMedia('(pointer: coarse)').matches;

  // What the open dialog needs: the element that opened it and how to find
  // it again after a swap, the element in it that had the focus last, the
  // elements made inert, and the status said last. The opener stays after
  // a close, for the notice that the dialog leaves.
  const S = { dialog: null, opener: null, find: null, last: null, inert: [], said: null };

  // isolate makes everything but the dialog, the layer, the notices and
  // the page's live region inert, as a modal dialog needs: Tab and the
  // screen reader stay in the dialog, the help pane and the jump box in the
  // layer still work, and what app.js says is still heard.
  function isolate(root) {
    release();
    const walk = parent => {
      for (const c of parent.children) {
        if (c === root || c.id === 'layer' || c.id === 'act-notes' || c.id === 'say' || c.inert || /^(SCRIPT|TEMPLATE|STYLE|LINK)$/.test(c.tagName)) continue;
        if (c.contains(root)) { walk(c); continue; }
        c.inert = true;
        S.inert.push(c);
      }
    };
    walk(document.body);
  }

  function release() {
    for (const el of S.inert) el.inert = false;
    S.inert = [];
  }

  // finder says how to find the opener again, should a live update put a
  // new element in its place.
  function finder(el) {
    if (el.id) return '#' + CSS.escape(el.id);
    for (const a of ['hx-get', 'href']) {
      const v = el.getAttribute(a);
      if (v) return el.tagName.toLowerCase() + '[' + a + '="' + CSS.escape(v) + '"]';
    }
    return null;
  }

  // focusBack gives the focus to the element that opened the dialog, or,
  // when it went, to the heading of the page, which does not scroll the
  // page. With still, the page does not scroll for the opener either.
  function focusBack(still) {
    const usable = el => el && el.isConnected && !el.closest('[inert]');
    let el = S.opener;
    if (!usable(el)) el = S.find && document.querySelector(S.find);
    if (!usable(el)) {
      still = true;
      el = document.querySelector('#main h1') || document.getElementById('main');
      if (el && !el.hasAttribute('tabindex')) el.setAttribute('tabindex', '-1');
    }
    if (el) el.focus({ preventScroll: !!still });
  }

  // focusIn gives the focus to the element of the open dialog that had it
  // last, or to its first field, or on a touch screen to the dialog itself.
  function focusIn(d) {
    const form = d.querySelector('form.modal');
    if (!form) return;
    if (S.last && d.contains(S.last) && S.last.getClientRects().length) { S.last.focus(); return; }
    const first = touch() ? null : [...form.querySelectorAll('.mb :is(input:not([type=hidden]), select, textarea)')].find(el => !el.disabled && el.getClientRects().length);
    (first || form).focus();
  }

  // closeNote closes a notice. The focus that was on it goes into the open
  // dialog, to the next notice, or back to where the dialog was opened,
  // without a scroll of the page.
  function closeNote(n) {
    const had = n.contains(document.activeElement);
    n.remove();
    if (!had) return;
    const d = dialog();
    if (d) { focusIn(d); return; }
    const next = document.querySelector('#act-notes [data-act-note-close]');
    if (next) next.focus({ preventScroll: true }); else focusBack(true);
  }

  // opened sets up a dialog that has just come in: the page behind it goes
  // inert, and its first field takes the focus, or on a touch screen the
  // dialog itself.
  function opened(d) {
    S.dialog = d;
    S.last = null;
    S.said = null;
    isolate(d);
    footer(d);
    focusIn(d);
  }

  // closed forgets the dialog and gives the focus back.
  function closed() {
    S.dialog = S.last = null;
    release();
    focusBack(false);
  }

  // close closes the dialog.
  function close() {
    const d = dialog();
    if (!d) return;
    if (inPage(d)) {
      // A dialog opened as a page of its own: leave it.
      if (history.length > 1) history.back(); else location.assign('/');
      return;
    }
    d.remove();
    closed();
  }

  // footer keeps the scroll padding of a dialog at the height of its
  // footer, which sticks to the bottom on a small screen, so that a field
  // with the focus never hides below it. The height goes on the root, as
  // htmx would copy a style of the dialog to the next one of its id.
  function footer(d) {
    if (!window.ResizeObserver) return;
    const ro = new ResizeObserver(() => {
      if (!d.isConnected) { ro.disconnect(); return; }
      const f = [...d.querySelectorAll('.mf')].find(x => !x.hidden);
      document.documentElement.style.setProperty('--act-mf-h', (f ? f.offsetHeight : 0) + 'px');
    });
    for (const f of d.querySelectorAll('.mf')) ro.observe(f);
  }

  // field returns the control of the form named name: the first one of a
  // group of radio buttons.
  function field(form, name) {
    const el = form && form.elements[name];
    return el instanceof RadioNodeList ? el[0] : el;
  }

  // goTo opens what hides the field name and gives it the focus; without a
  // name, the list of problems of the plan takes it.
  function goTo(form, name) {
    const el = name ? field(form, name) : document.getElementById('act-probs');
    if (!(el instanceof HTMLElement)) return;
    // A field in a folded section or a closed editor: open it first.
    for (let d = el.closest('details'); d; d = d.parentElement.closest('details')) d.open = true;
    const panel = el.closest('.act-ned');
    if (panel && panel.hidden) {
      panel.hidden = false;
      const button = document.querySelector('[aria-controls="' + panel.id + '"]');
      if (button) button.setAttribute('aria-expanded', 'true');
    }
    el.focus({ preventScroll: true });
    el.scrollIntoView({ block: 'center' });
  }

  // A click on the dim edge closes the dialog only where the press began
  // there too, not at the end of a drag that selects text in a field.
  let pressedScrim = false;
  document.addEventListener('pointerdown', ev => {
    pressedScrim = ev.target instanceof Element && ev.target.matches('[data-act-scrim]');
  });

  document.addEventListener('click', ev => {
    const t = ev.target;
    if (!(t instanceof Element)) return;

    if (t.closest('[data-act-close]') || (t.matches('[data-act-scrim]') && pressedScrim)) {
      close();
      return;
    }
    const go = t.closest('[data-act-go]');
    if (go) { goTo(go.closest('form'), go.dataset.actGo); return; }
    const note = t.closest('[data-act-note-close]');
    if (note) { closeNote(note.closest('[data-act-note]')); return; }

    const step = t.closest('[data-act-step]');
    if (step) {
      const input = field(step.closest('form'), step.dataset.actStep);
      if (!input) return;
      const now = parseInt(input.value, 10);
      // An absent min or max is no bound; +'' would read as 0.
      const lo = input.min === '' ? -Infinity : +input.min;
      const hi = input.max === '' ? Infinity : +input.max;
      const next = Math.max(lo, Math.min(hi, (Number.isNaN(now) ? (lo === -Infinity ? 0 : lo) : now) + +step.dataset.d));
      input.value = String(next);
      // htmx previews the command on the input event; setting a value does
      // not send one.
      input.dispatchEvent(new Event('input', { bubbles: true }));
      return;
    }

    // The size button of a node opens and closes its editor.
    const chip = t.closest('[data-act-ed]');
    if (chip) {
      const panel = document.getElementById('act-ed-' + chip.dataset.actEd);
      if (!panel) return;
      panel.hidden = !panel.hidden;
      chip.setAttribute('aria-expanded', String(!panel.hidden));
      if (!panel.hidden) panel.scrollIntoView({ block: 'nearest' });
      return;
    }

    // Use the defaults: empty the fields of a node.
    const reset = t.closest('[data-act-reset]');
    if (reset) {
      const panel = document.getElementById('act-ed-' + reset.dataset.actReset);
      if (!panel) return;
      panel.querySelectorAll('input').forEach(i => {
        if (i.type === 'checkbox') i.checked = false; else i.value = '';
      });
      reset.closest('form').dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }

    // A new worker group: a copy of the template with a key of its own.
    if (t.closest('[data-act-wgadd]')) {
      const tpl = document.getElementById('act-wg-tpl');
      const groups = document.getElementById('act-groups');
      if (!tpl || !groups) return;
      const key = 'w' + Math.random().toString(36).slice(2, 8).padEnd(6, '0');
      groups.insertAdjacentHTML('beforeend', tpl.innerHTML.replaceAll('__WG__', key));
      const count = document.getElementById('act-' + key + '-count');
      count.value = '1';
      count.focus();
      count.dispatchEvent(new Event('input', { bubbles: true }));
      return;
    }

    // A new row of a user, group, account or limit: a copy of its
    // template, with the key of the row, or a new key of its own.
    const rowAdd = t.closest('[data-act-rowadd]');
    if (rowAdd) {
      const tpl = document.getElementById('act-tpl-' + rowAdd.dataset.actRowadd);
      const box = document.getElementById(rowAdd.dataset.actInto);
      if (!tpl || !box) return;
      const key = rowAdd.dataset.actKey || rowAdd.dataset.actPrefix + Math.random().toString(36).slice(2, 8).padEnd(6, '0');
      box.insertAdjacentHTML('beforeend', tpl.innerHTML.replaceAll('__ROW__', key));
      const first = box.lastElementChild && box.lastElementChild.querySelector('input:not([type=hidden])');
      if (first) first.focus();
      rowAdd.closest('form').dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }

    const rowDel = t.closest('[data-act-rowdel]');
    if (rowDel) {
      const row = rowDel.closest('[data-act-row]');
      const form = rowDel.closest('form');
      if (!row) return;
      const box = row.parentElement;
      row.remove();
      // The button that had the focus is gone.
      const add = form.querySelector('[data-act-into="' + box.id + '"]');
      if (add) add.focus();
      form.dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }

    const del = t.closest('[data-act-wgdel]');
    if (del) {
      const form = del.closest('form');
      for (const prefix of ['act-wg-', 'act-ed-']) {
        const el = document.getElementById(prefix + del.dataset.actWgdel);
        if (el) el.remove();
      }
      // The button that had the focus is gone.
      const add = form.querySelector('[data-act-wgadd]');
      if (add) add.focus();
      form.dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }

    // Edit YAML: the config of the plan goes into the editor, which takes
    // the place of the form until the user discards the edits.
    if (t.closest('[data-act-yaml-edit]')) {
      yamlMode(t.closest('form'), true);
      return;
    }
    if (t.closest('[data-act-yaml-discard]')) {
      yamlMode(t.closest('form'), false);
      return;
    }

    const jump = t.closest('[data-act-jump]');
    if (jump) {
      ev.preventDefault();
      goTo(jump.closest('form'), jump.dataset.actJump);
      return;
    }

    const copy = t.closest('.scrim [data-copy]');
    if (copy && navigator.clipboard) {
      navigator.clipboard.writeText(copy.dataset.copy).then(() => {
        const was = copy.textContent;
        copy.textContent = 'Copied';
        setTimeout(() => { copy.textContent = was; }, 1200);
      }, () => { /* no permission: the command stays on the screen */ });
    }
  });

  // The dialog keeps the element that had the focus last.
  document.addEventListener('focusin', ev => {
    const t = ev.target;
    if (t instanceof Element && t.closest('#act-dialog form')) S.last = t;
  });

  // The directory of the data follows the name until the user types one;
  // a Docker volume needs none.
  document.addEventListener('input', ev => {
    const t = ev.target;
    if (!(t instanceof HTMLInputElement) || !t.form) return;
    const dir = t.form.elements.dir;
    if (!(dir instanceof HTMLInputElement)) return;
    if (t === dir) {
      delete dir.dataset.actFollow;
    } else if (t.name === 'name' && 'actFollow' in dir.dataset) {
      dir.value = '~/sind/' + t.value.trim();
    } else if (t.name === 'dtype') {
      dir.readOnly = t.value === 'volume';
    }
  });

  // Pull follows the image until the user ticks or clears the box.
  document.addEventListener('change', ev => {
    const auto = document.getElementById('act-pull-auto');
    if (auto && ev.target instanceof Element && ev.target.id === 'act-pull') auto.remove();
  });

  // yamlMode turns the editor of the YAML on, with the config of the plan,
  // or off. Its fields are disabled while it is off, so the form does not
  // post them.
  function yamlMode(form, on) {
    const ed = document.getElementById('act-yamled');
    const text = document.getElementById('act-yaml');
    const flag = document.getElementById('act-yaml-on');
    const plan = document.querySelector('.scrim .act-yaml');
    if (!form || !ed || !text || !flag) return;
    if (on) text.value = plan ? plan.textContent : '';
    ed.hidden = !on;
    text.disabled = flag.disabled = !on;
    form.querySelector('.act-form').classList.toggle('act-yamlmode', on);
    const first = on ? text : form.querySelector('.act-form input[type=text]');
    if (first) first.focus();
    form.dispatchEvent(new Event('change', { bubbles: true }));
  }

  // followPull ticks the box as the preview says, while it follows the
  // image.
  function followPull() {
    const auto = document.getElementById('act-pull-auto');
    const hint = document.getElementById('act-pull-hint');
    const pull = document.getElementById('act-pull');
    if (auto && hint && pull && hint.dataset.actPull) pull.checked = hint.dataset.actPull === 'on';
  }

  // The fields of a node show the node defaults that they take when empty.
  function placeholders() {
    const eff = document.getElementById('act-eff');
    if (!eff || !eff.dataset.cpus) return;
    document.querySelectorAll('.scrim [data-act-ph]').forEach(i => {
      i.placeholder = eff.dataset[i.dataset.actPh] || '';
    });
  }

  // Esc closes the dialog. In the help pane over a dialog, it closes the
  // pane, and the focus goes back into the dialog; the jump box takes its
  // own.
  document.addEventListener('keydown', ev => {
    const form = formOf();
    const t = ev.target;
    if (!form || ev.defaultPrevented || !(t instanceof Element) || t.closest('#jump')) return;
    const pane = t.closest('#helppane');
    if (pane) {
      const x = pane.querySelector('[data-help-close]');
      if (ev.key !== 'Escape' || !x) return;
      ev.preventDefault();
      x.click();
      focusIn(dialog());
      return;
    }
    if (ev.key === 'Escape') {
      ev.preventDefault();
      close();
    }
  });

  // A field with a problem says so to assistive technology too.
  function markProblems(root) {
    root.querySelectorAll('.scrim .err[id^="act-err-"]').forEach(err => {
      const el = field(err.closest('form'), err.id.slice('act-err-'.length));
      if (!(el instanceof Element)) return;
      if (err.textContent) el.setAttribute('aria-invalid', 'true');
      else el.removeAttribute('aria-invalid');
    });
  }

  // say tells assistive technology the status of a dialog with a plan when
  // it changes: the first problem, or that the command can run. The first
  // status of a dialog it says only when the command cannot run.
  function say() {
    const live = document.getElementById('act-say');
    const src = document.querySelector('#act-status [data-act-key]');
    if (!live || !src || src.dataset.actKey === S.said) return;
    const first = S.said === null;
    S.said = src.dataset.actKey;
    if (first && S.said === 'ready') return;
    live.textContent = src.dataset.actSay;
  }

  // The element that opens a dialog.
  document.addEventListener('htmx:configRequest', ev => {
    const d = ev.detail;
    if (!d || d.target !== layer() || d.verb !== 'get' || !/^\/dialogs\/[a-z-]+(\?|$)/.test(d.path)) return;
    const el = d.elt instanceof HTMLElement && d.elt !== document.body ? d.elt : document.activeElement;
    S.opener = S.find = null;
    if (el instanceof HTMLElement && el !== document.body && !el.closest('#layer')) {
      S.opener = el;
      S.find = finder(el);
    }
  });

  // A command started: a dialog that does not follow its run closes, while
  // its notice stays for a while. One opened as a page of its own goes to
  // the run.
  document.addEventListener('act-started', ev => {
    const det = ev.detail || {};
    for (const n of document.querySelectorAll('#act-notes [data-act-note]:not([data-act-timed])')) {
      n.dataset.actTimed = '';
      const tick = () => { if (n.matches(':hover, :focus-within')) setTimeout(tick, 2000); else n.remove(); };
      setTimeout(tick, 10000);
    }
    if (det.follow) return;
    const d = dialog();
    if (d && inPage(d)) { location.assign(det.url); return; }
    close();
  });

  // A dialog that has just come in is set up; the swaps of its preview,
  // which target parts of it, leave the focus alone.
  document.addEventListener('htmx:afterSettle', () => {
    const d = dialog();
    if (d && d !== S.dialog) opened(d);
    else if (!d && S.dialog) closed();
    markProblems(document);
    followPull();
    placeholders();
    say();
  });

  // A dialog opened as a page of its own, as the jump box does, is set up
  // now.
  const d0 = dialog();
  if (d0) opened(d0);
})();
