// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the dialogs of the actions: the counters of the roles, the
// Copy button, and closing. The dialogs are the server's HTML, which htmx
// puts into #layer; nothing here evaluates text.
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

  document.addEventListener('click', ev => {
    const t = ev.target;
    if (!(t instanceof Element)) return;

    if (t.closest('[data-act-close]') || t.matches('[data-act-scrim]')) {
      close();
      return;
    }

    const step = t.closest('[data-act-step]');
    if (step) {
      const form = step.closest('form');
      const input = form && form.elements[step.dataset.actStep];
      if (!input) return;
      const next = Math.max(+input.min, Math.min(+input.max, +input.value + +step.dataset.d));
      input.value = String(next);
      const shown = step.parentElement.querySelector('span');
      if (shown) shown.textContent = String(next);
      // htmx previews the command on the input event; setting a value does
      // not send one.
      input.dispatchEvent(new Event('input', { bubbles: true }));
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

  document.addEventListener('keydown', ev => {
    if (ev.key === 'Escape' && document.getElementById('act-dialog')) close();
  });

  // A dialog that has just come in gets the focus on its first field; the
  // swaps of its preview, which target parts of it, leave the focus alone.
  document.addEventListener('htmx:afterSettle', ev => {
    const l = layer();
    if (!l || ev.target !== l) return;
    const first = l.querySelector('input[type=text], input[type=number], select, input[type=checkbox]');
    if (first) first.focus();
  });
})();
