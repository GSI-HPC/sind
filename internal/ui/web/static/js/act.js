// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the dialogs of the actions: the steppers, the directory that
// follows the name, the links to a field with a problem, the Copy button,
// and closing. The dialogs are the server's HTML, which htmx puts into
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

    const jump = t.closest('[data-act-jump]');
    if (jump) {
      ev.preventDefault();
      const el = field(jump.closest('form'), jump.dataset.actJump);
      if (!el) return;
      // A field in a folded section: open the section first.
      for (let d = el.closest('details'); d; d = d.parentElement.closest('details')) d.open = true;
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
  });
})();
