// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the dialogs of the actions: the steppers, the editors of the
// nodes and the worker groups of a new cluster, the directory that follows
// the name, the pull that follows the image, the links to a field with a
// problem, the Copy button, and closing. The dialogs are the server's HTML, which htmx puts into
// #layer; nothing here evaluates text.
(() => {
  'use strict';

  const layer = () => document.getElementById('layer');

  function close() {
    const d = document.getElementById('act-dialog');
    if (d && !d.closest('#layer')) {
      // A dialog opened as a page of its own: leave it.
      if (history.length > 1) history.back(); else location.assign('/');
      return;
    }
    const l = layer();
    if (l) l.replaceChildren();
  }

  // field returns the control of the form named name: the first one of a
  // group of radio buttons.
  function field(form, name) {
    const el = form && form.elements[name];
    return el instanceof RadioNodeList ? el[0] : el;
  }

  document.addEventListener('click', ev => {
    const t = ev.target;
    if (!(t instanceof Element)) return;

    if (t.closest('[data-act-close]') || t.matches('[data-act-scrim]')) {
      close();
      return;
    }

    const step = t.closest('[data-act-step]');
    if (step) {
      const input = field(step.closest('form'), step.dataset.actStep);
      if (!input) return;
      const now = parseInt(input.value, 10);
      const next = Math.max(+input.min, Math.min(+input.max, (Number.isNaN(now) ? +input.min : now) + +step.dataset.d));
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

    const jump = t.closest('[data-act-jump]');
    if (jump) {
      ev.preventDefault();
      const el = field(jump.closest('form'), jump.dataset.actJump);
      if (!el) return;
      // A field in a folded section or a closed editor: open it first.
      for (let d = el.closest('details'); d; d = d.parentElement.closest('details')) d.open = true;
      const panel = el.closest('.act-ned');
      if (panel && panel.hidden) {
        panel.hidden = false;
        const button = document.querySelector('[aria-controls="' + panel.id + '"]');
        if (button) button.setAttribute('aria-expanded', 'true');
      }
      el.focus();
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

  document.addEventListener('keydown', ev => {
    if (ev.key === 'Escape' && document.getElementById('act-dialog')) close();
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

  // A dialog that has just come in gets the focus on its first field; the
  // swaps of its preview, which target parts of it, leave the focus alone.
  document.addEventListener('htmx:afterSettle', ev => {
    const l = layer();
    if (l && ev.target === l) {
      const first = l.querySelector('input[type=text], input[type=number], select, input[type=checkbox]');
      if (first) first.focus();
    }
    markProblems(document);
    followPull();
    placeholders();
  });
})();
