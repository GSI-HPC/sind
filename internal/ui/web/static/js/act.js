// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the dialogs of the actions: the steppers, the editors of the
// nodes, the worker groups and the rows of users, groups and accounts of a
// new cluster, the directory that follows the name, the pull that follows
// the image, Edit YAML and Start from YAML with the name and realm that
// follow the YAML, the links to a field with a problem, the Copy button,
// and the frame of every dialog: the focus, which stays in the dialog and
// goes back to the button that opened it, the question before a close
// loses changes, the draft of a new cluster, Enter and Ctrl+Enter, the
// status, and the notice of a command that started. The dialogs are the
// server's HTML, which htmx puts into #layer; nothing here evaluates text,
// and no storage is needed to work: every access is guarded.
(() => {
  'use strict';

  const layer = () => document.getElementById('layer');
  const dialog = () => document.getElementById('act-dialog');
  const formOf = () => { const d = dialog(); return d && d.querySelector('form.modal'); };
  const inPage = d => !d.closest('#layer');

  const local = {
    get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* private window */ } },
    del(k) { try { localStorage.removeItem(k); } catch (e) { /* private window */ } },
  };
  const draftKey = id => 'sind-ui-draft-' + id;

  // A touch screen gets no focus in a field when a dialog opens, as its
  // keyboard would cover the dialog. macOS shows Cmd in place of Ctrl.
  const touch = () => matchMedia('(pointer: coarse)').matches;
  if (/Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)) document.documentElement.classList.add('act-mac');

  // What the open dialog needs: the element that opened it and how to find
  // it again after a swap, the link it came from, the element in it that
  // had the focus last, whether the user changed a field, the question the
  // footer asks, the elements made inert, and the status said last. The
  // opener stays after a close, for the notice that the dialog leaves.
  const S = { dialog: null, opener: null, find: null, path: '', last: null, dirty: false, asking: null, back: null, inert: [], said: null, saveTimer: 0 };

  // isolate makes everything but the dialog, the layer, the notices and
  // the page's live region inert, marked data-act-inert, as a modal dialog needs: Tab and the
  // screen reader stay in the dialog, the help pane and the jump box in the
  // layer still work, and what app.js says is still heard.
  function isolate(root) {
    release();
    const walk = parent => {
      for (const c of parent.children) {
        if (c === root || c.id === 'layer' || c.id === 'act-notes' || c.id === 'say' || (c.inert && !c.hasAttribute('data-help-inert')) || /^(SCRIPT|TEMPLATE|STYLE|LINK)$/.test(c.tagName)) continue;
        if (c.contains(root)) { walk(c); continue; }
        c.inert = true;
        c.setAttribute('data-act-inert', '');
        S.inert.push(c);
      }
    };
    walk(document.body);
  }

  function release() {
    // The help pane may hold an element inert too (help.js).
    for (const el of S.inert) {
      el.removeAttribute('data-act-inert');
      if (!el.hasAttribute('data-help-inert')) el.inert = false;
    }
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

  // quiet drops a request of a dialog that closes, and the answer to one
  // that comes after it closed: its parts would find no place to go, as
  // htmx reports. The dialog hears both, as they bubble up to it, also
  // once it is out of the page.
  function quiet(ev) {
    const d = ev.currentTarget;
    if (!d.isConnected || d.hasAttribute('data-act-closing')) ev.preventDefault();
  }

  // opened sets up a dialog that has just come in: the page behind it goes
  // inert, and its first field takes the focus, or on a touch screen the
  // dialog itself.
  function opened(d) {
    const form = d.querySelector('form.modal');
    d.addEventListener('htmx:beforeRequest', quiet);
    d.addEventListener('htmx:beforeOnLoad', quiet);
    S.dialog = d;
    S.last = null;
    S.dirty = !!form && form.hasAttribute('data-act-restored');
    S.asking = null;
    S.said = null;
    keptOpen.clear();
    kept.clear();
    isolate(d);
    footer(d);
    focusIn(d);
  }

  // closed forgets the dialog and gives the focus back.
  function closed() {
    clearTimeout(S.saveTimer);
    S.dialog = S.last = null;
    S.dirty = false;
    S.asking = null;
    release();
    focusBack(false);
  }

  // close closes the dialog. One with changes asks first, unless force.
  function close(force) {
    const d = dialog();
    if (!d) return;
    if (!force && S.dirty) { ask('close'); return; }
    if (inPage(d)) {
      // A dialog opened as a page of its own: leave it.
      S.dirty = false;
      if (history.length > 1) history.back(); else location.assign('/');
      return;
    }
    // The field with the focus asks for a look at the command when it
    // loses the focus, as it changed: let it lose it while the dialog
    // still drops such requests (quiet).
    d.setAttribute('data-act-closing', '');
    if (d.contains(document.activeElement)) document.activeElement.blur();
    d.remove();
    closed();
  }

  // ask shows a question in place of the footer: close and lose the
  // changes, or start over and lose them.
  function ask(mode) {
    const form = formOf();
    const bar = form && form.querySelector('#act-ask');
    const mf = form && form.querySelector('#act-mf');
    if (!bar || !mf) return;
    for (const q of bar.querySelectorAll('[data-act-ask]')) q.hidden = q.dataset.actAsk !== mode;
    // The question names the new cluster, the cluster of the dialog, or
    // its realm.
    const name = ['name', 'cluster', 'realm'].map(k => form.elements.namedItem(k)).find(el => el instanceof HTMLInputElement);
    const v = name ? name.value.trim() : '';
    const of = bar.querySelector('[data-act-ask-of]');
    of.hidden = !v;
    of.querySelector('[data-act-ask-name]').textContent = v;
    if (!S.asking) S.back = document.activeElement;
    S.asking = mode;
    mf.hidden = true;
    bar.hidden = false;
    bar.querySelector('[data-act-keep]').focus();
  }

  // unask takes the question away; with refocus, the focus goes back to
  // where it was before.
  function unask(refocus) {
    const form = formOf();
    if (!form || !S.asking) return;
    S.asking = null;
    form.querySelector('#act-ask').hidden = true;
    form.querySelector('#act-mf').hidden = false;
    if (refocus) (S.back && S.back.isConnected && form.contains(S.back) ? S.back : form.querySelector('[data-act-close]')).focus();
  }

  // discard drops the changes and the draft, then closes the dialog or
  // opens it again from the start.
  function discard() {
    const form = formOf();
    const mode = S.asking;
    if (form && form.dataset.actDraft) local.del(draftKey(form.dataset.actDraft));
    S.dirty = false;
    unask(false);
    if (mode === 'restart') restart(); else close(true);
  }

  function restart() {
    const d = dialog();
    if (!d) return;
    if (inPage(d) || !window.htmx || !S.path) { location.reload(); return; }
    window.htmx.ajax('GET', S.path, { target: '#layer', swap: 'innerHTML' });
  }

  // changed marks the dialog as changed by the user, and keeps its draft.
  function changed() {
    S.dirty = true;
    const form = formOf();
    if (!form || !form.dataset.actDraft) return;
    clearTimeout(S.saveTimer);
    S.saveTimer = setTimeout(save, 300);
  }

  function save() {
    clearTimeout(S.saveTimer);
    const form = formOf();
    if (!form || !form.dataset.actDraft || !S.dirty) return;
    const data = new URLSearchParams();
    for (const [k, v] of new FormData(form)) {
      if (typeof v === 'string' && !['csrf', 'shown', 'confirm'].includes(k)) data.append(k, v);
    }
    local.set(draftKey(form.dataset.actDraft), data.toString());
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

  // fields counts the fields of a form, a group of radio buttons or
  // checkboxes of one name as one.
  function fields(form) {
    const names = new Set();
    for (const el of form.elements) {
      if (el.disabled || !el.name || el.tagName === 'BUTTON' || /^(hidden|button|submit|reset|image)$/.test(el.type)) continue;
      names.add(el.name);
    }
    return names.size;
  }

  // submit presses the button of the dialog, or, while the command cannot
  // run, goes to the first problem, or to the name to type.
  function submit(form) {
    const ok = form.querySelector('#act-ok');
    if (ok && !ok.disabled) { form.requestSubmit(ok); return; }
    const go = form.querySelector('[data-act-go]');
    if (go) { go.click(); return; }
    const bad = form.querySelector('[aria-invalid=true], #act-confirm');
    if (bad) bad.focus();
  }

  // numberGroups names the worker groups as the server does: workers, or
  // workers 1, workers 2 and so on when there is more than one, in their
  // rows and in the names of their buttons.
  function numberGroups() {
    const rows = document.querySelectorAll('#act-groups .act-wgrow');
    rows.forEach((row, i) => {
      const label = rows.length > 1 ? 'workers ' + (i + 1) : 'workers';
      const b = row.querySelector('.act-lab b');
      if (b) b.textContent = label;
      // The size button takes its name from a hidden span; the others
      // name themselves, before the workers of the group.
      const size = row.querySelector('.act-cz .vh');
      if (size) size.textContent = 'Size of ' + label;
      for (const [sel, name] of [['[data-d="-1"]', 'Fewer in '], ['[data-d="1"]', 'More in '], ['[data-act-wgdel]', 'Remove ']]) {
        const button = row.querySelector(sel);
        if (button) button.setAttribute('aria-label', name + label);
      }
    });
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
      close(false);
      return;
    }
    if (t.closest('[data-act-keep]')) { unask(true); return; }
    if (t.closest('[data-act-discard]')) { discard(); return; }
    if (t.closest('[data-act-restart]')) { ask('restart'); return; }
    const go = t.closest('[data-act-go]');
    if (go) { goTo(go.closest('form'), go.dataset.actGo); return; }
    const note = t.closest('[data-act-note-close]');
    if (note) { closeNote(note.closest('[data-act-note]')); return; }
    // The buttons that change the form of a new cluster.
    if (ev.isTrusted && t.closest('.scrim :is([data-act-step], [data-act-rowadd], [data-act-rowdel], [data-act-wgadd], [data-act-wgdel], [data-act-reset])')) changed();

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
      numberGroups();
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
      numberGroups();
      // The button that had the focus is gone.
      const add = form.querySelector('[data-act-wgadd]');
      if (add) add.focus();
      form.dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }

    // Edit YAML: the config of the plan goes into the editor, which takes
    // the place of the form until the user discards the edits. Start from
    // YAML opens it empty, for a config to paste or a file to open.
    if (t.closest('[data-act-yaml-edit]')) {
      yamlMode(t.closest('form'), true);
      return;
    }
    if (t.closest('[data-act-yaml-start]')) {
      yamlMode(t.closest('form'), true, '');
      return;
    }
    if (t.closest('[data-act-yaml-open]')) {
      const file = document.getElementById('act-yaml-file');
      if (file) file.click();
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

  // A change that the user makes to a field, but to the typed name of a
  // delete, which is quick to type again.
  for (const type of ['input', 'change']) {
    document.addEventListener(type, ev => {
      const t = ev.target;
      if (ev.isTrusted && t instanceof Element && t.closest('#act-dialog form') && t.name && t.name !== 'confirm') changed();
    });
  }

  // The dialog keeps the element that had the focus last. A click into the
  // form while the footer asks means: keep editing.
  document.addEventListener('focusin', ev => {
    const t = ev.target;
    if (!(t instanceof Element) || !t.closest('#act-dialog form')) return;
    S.last = t;
    if (S.asking && !t.closest('#act-ask')) unask(false);
    // A default name is selected the first time it takes the focus, also
    // after a tap, so that typing replaces it. The selection waits for
    // the caret that the tap or click puts in.
    if (t instanceof HTMLInputElement && t.hasAttribute('data-act-select')) {
      t.removeAttribute('data-act-select');
      setTimeout(() => { if (document.activeElement === t) t.setSelectionRange(0, t.value.length); });
    }
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

  // yamlMode turns the editor of the YAML on, with start or else the
  // config of the plan, or off. Its fields are disabled while it is off, so
  // the form does not post them.
  // Discard YAML edits puts back the name and the realm that the form had.
  const formIDs = new Map();

  function yamlMode(form, on, start) {
    const ed = document.getElementById('act-yamled');
    const text = document.getElementById('act-yaml');
    const flag = document.getElementById('act-yaml-on');
    const plan = document.querySelector('.scrim .act-yaml');
    if (!form || !ed || !text || !flag) return;
    if (on && text.disabled) {
      formIDs.clear();
      for (const key of ['name', 'realm']) {
        const input = form.elements[key];
        if (input instanceof HTMLInputElement) formIDs.set(key, input.value);
      }
    }
    if (on) text.value = start !== undefined ? start : (plan ? plan.textContent : '');
    ed.hidden = !on;
    text.disabled = flag.disabled = !on;
    if (!on) {
      for (const [key, value] of formIDs) {
        const input = form.elements[key];
        if (!(input instanceof HTMLInputElement) || input.value === value) continue;
        input.value = value;
        // The directory follows the name again.
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
      formIDs.clear();
    }
    form.querySelector('.act-form').classList.toggle('act-yamlmode', on);
    const first = on ? text : form.querySelector('.act-form input[type=text]');
    if (first) first.focus();
    form.dispatchEvent(new Event('change', { bubbles: true }));
  }

  // In YAML mode, Name and Realm stay beside the editor: they show the
  // name and the realm of the YAML, and a change of either goes into it.
  // A YAML without a name makes the cluster default, as sind does; one
  // without a realm takes the realm of the field.
  const yamlKey = key => new RegExp('^' + key + ':[ \\t]*(.*?)[ \\t]*(#.*)?$', 'm');
  const unquote = v => v.replace(/^(["'])(.*)\1$/, '$2');

  function inYAMLMode() {
    const text = document.getElementById('act-yaml');
    return text instanceof HTMLTextAreaElement && !text.disabled ? text : null;
  }

  // fromYAML puts the name and the realm of the YAML into their fields.
  function fromYAML(text) {
    const form = text.form;
    for (const key of ['name', 'realm']) {
      const input = form && form.elements[key];
      if (!(input instanceof HTMLInputElement)) continue;
      const m = text.value.match(yamlKey(key));
      if (m) input.value = unquote(m[1]);
      else if (key === 'name' && text.value.trim() !== '') input.value = 'default';
    }
  }

  // toYAML puts the value of the field Name or Realm into the YAML: in
  // place of its line, or as a new line after kind, or after name for a
  // realm. An empty realm adds no line.
  function toYAML(text, key, value) {
    const re = yamlKey(key);
    if (re.test(text.value)) {
      text.value = text.value.replace(re, (all, v, comment) => key + ': ' + value + (comment ? ' ' + comment : ''));
      return;
    }
    if (key === 'realm' && value === '') return;
    const after = key === 'realm' && yamlKey('name').test(text.value) ? /^name:.*$/m : /^kind:.*$/m;
    text.value = after.test(text.value) ? text.value.replace(after, k => k + '\n' + key + ': ' + value) : key + ': ' + value + '\n' + text.value;
  }

  document.addEventListener('input', ev => {
    const t = ev.target;
    const text = inYAMLMode();
    if (!text) return;
    if (t === text) fromYAML(text);
    else if (t instanceof HTMLInputElement && t.form === text.form && (t.name === 'name' || t.name === 'realm')) toYAML(text, t.name, t.value.trim());
  });

  // loadYAML puts the text of a file into the editor, as a paste would.
  function loadYAML(file) {
    const text = inYAMLMode();
    const err = document.getElementById('act-err-yaml');
    if (!text || !file) return;
    const refuse = message => {
      if (err) {
        err.textContent = message;
        err.scrollIntoView({ block: 'center' });
      }
      text.setAttribute('aria-invalid', 'true');
    };
    if (file.size > 64 * 1024) {
      refuse(file.name + ' is larger than 64 KiB.');
      return;
    }
    file.text().then(v => {
      text.value = v;
      fromYAML(text);
      text.focus();
      // A loaded file is a change of the user's, which the draft keeps.
      changed();
      text.dispatchEvent(new Event('input', { bubbles: true }));
    }, () => refuse('sind ui cannot read ' + file.name + '.'));
  }

  // The input and change events of the file field are not the form's:
  // they reach no listener of htmx, whose preview would replace a message
  // of loadYAML at once. A file that loads starts a preview as typing does.
  const isFile = t => t instanceof HTMLInputElement && t.id === 'act-yaml-file';
  document.addEventListener('input', ev => {
    if (isFile(ev.target)) ev.stopPropagation();
  }, true);
  document.addEventListener('change', ev => {
    const t = ev.target;
    if (!isFile(t)) return;
    ev.stopPropagation();
    loadYAML(t.files && t.files[0]);
    t.value = '';
  }, true);

  // A file dropped on the editor opens as Open a file does.
  for (const type of ['dragenter', 'dragover', 'dragleave', 'drop']) {
    document.addEventListener(type, ev => {
      const text = inYAMLMode();
      if (!text || ev.target !== text || !ev.dataTransfer || !Array.from(ev.dataTransfer.types || []).includes('Files')) return;
      ev.preventDefault();
      text.classList.toggle('act-drop', type === 'dragenter' || type === 'dragover');
      if (type === 'drop') loadYAML(ev.dataTransfer.files[0]);
    });
  }

  // A part of the preview with data-act-hold, such as Preview slurm.conf,
  // stays open when a preview takes its place.
  const keptOpen = new Set();
  document.addEventListener('toggle', ev => {
    const d = ev.target;
    if (!(d instanceof HTMLDetailsElement) || !d.hasAttribute('data-act-hold') || !d.id) return;
    if (d.open) keptOpen.add(d.id); else keptOpen.delete(d.id);
  }, true);
  // A checkbox of the preview with data-act-hold, such as Create anyway,
  // keeps what the user chose when a preview takes its place, also one
  // that was on its way when the user ticked it, and the next preview
  // posts it so.
  const kept = new Map();
  document.addEventListener('change', ev => {
    const t = ev.target;
    if (t instanceof HTMLInputElement && t.type === 'checkbox' && t.hasAttribute('data-act-hold') && t.id) kept.set(t.id, t.checked);
  });
  document.addEventListener('htmx:configRequest', ev => {
    const params = ev.detail && ev.detail.parameters;
    if (!params) return;
    for (const [id, on] of kept) {
      const box = document.getElementById(id);
      if (!(box instanceof HTMLInputElement) || !box.name) continue;
      if (on) params[box.name] = box.value; else delete params[box.name];
    }
  });

  document.addEventListener('htmx:afterSwap', () => {
    for (const id of keptOpen) {
      const d = document.getElementById(id);
      if (d instanceof HTMLDetailsElement) d.open = true;
    }
    for (const [id, on] of kept) {
      const box = document.getElementById(id);
      if (box instanceof HTMLInputElement) box.checked = on;
      else kept.delete(id);
    }
  });

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

  // Esc closes the dialog, or answers the question with Keep editing. In
  // the help pane over a dialog, it closes the pane, and the focus goes
  // back into the dialog; the jump box takes its own. Enter does not run
  // the command of a dialog with more than one field, as it would without
  // a look at it; Ctrl+Enter, or Cmd+Enter, does in every dialog.
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
      if (S.asking) unask(true); else close(false);
      return;
    }
    if (ev.key !== 'Enter' || ev.isComposing || !form.contains(t)) return;
    if (ev.ctrlKey || ev.metaKey) {
      ev.preventDefault();
      submit(form);
      return;
    }
    if (t instanceof HTMLInputElement && !/^(button|submit|reset|image)$/.test(t.type) && fields(form) > 1) ev.preventDefault();
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

  // The element that opens a dialog, and the link of it. A dialog that
  // keeps drafts opens with the user's draft: htmx posts it with the link,
  // and the server fills the fields in with it where it fits the link.
  document.addEventListener('htmx:configRequest', ev => {
    const d = ev.detail;
    if (!d || d.target !== layer() || d.verb !== 'get' || !/^\/dialogs\/[a-z-]+(\?|$)/.test(d.path)) return;
    S.path = d.path;
    const el = d.elt instanceof HTMLElement && d.elt !== document.body ? d.elt : document.activeElement;
    S.opener = S.find = null;
    if (el instanceof HTMLElement && el !== document.body && !el.closest('#layer')) {
      S.opener = el;
      S.find = finder(el);
    }
    const draft = local.get(draftKey(d.path.slice('/dialogs/'.length).split('?')[0]));
    if (!draft) return;
    // htmx set the headers for a GET, which has no body.
    d.verb = 'post';
    d.useUrlParams = false;
    d.headers['Content-Type'] = 'application/x-www-form-urlencoded';
    d.parameters.draft = draft;
  });

  // A command started: the draft goes, and a dialog that does not follow
  // its run closes, while its notice stays for a while. One opened as a
  // page of its own goes to the run.
  document.addEventListener('act-started', ev => {
    const det = ev.detail || {};
    const form = formOf();
    if (form && form.dataset.actDraft) local.del(draftKey(form.dataset.actDraft));
    S.dirty = false;
    for (const n of document.querySelectorAll('#act-notes [data-act-note]:not([data-act-timed])')) {
      n.dataset.actTimed = '';
      const tick = () => { if (n.matches(':hover, :focus-within')) setTimeout(tick, 2000); else n.remove(); };
      setTimeout(tick, 10000);
    }
    if (det.follow) return;
    const d = dialog();
    if (d && inPage(d)) { location.assign(det.url); return; }
    close(true);
  });

  // A draft is saved before the page goes, too. A dialog with changes
  // that keeps no draft asks the browser to ask first, as the jump box, a
  // g key or a link would lose them.
  window.addEventListener('pagehide', save);
  window.addEventListener('beforeunload', ev => {
    const form = formOf();
    if (!S.dirty || !form || form.dataset.actDraft) return;
    ev.preventDefault();
    ev.returnValue = '';
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
  // now. With a draft, it opens again with it, in its place.
  const d0 = dialog();
  if (d0) {
    opened(d0);
    const form = formOf();
    const draft = form && form.dataset.actDraft && !form.hasAttribute('data-act-restored') && local.get(draftKey(form.dataset.actDraft));
    if (draft) {
      document.addEventListener('DOMContentLoaded', () => {
        if (window.htmx) window.htmx.ajax('POST', location.pathname + location.search, { target: '#act-dialog', swap: 'outerHTML', values: { draft } });
      });
    }
  }
})();
