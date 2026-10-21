// SPDX-License-Identifier: LGPL-3.0-or-later

// The "more" part's script: the Copy button of a command block. Nothing here
// evaluates text.
(() => {
  'use strict';

  document.addEventListener('click', ev => {
    const btn = ev.target.closest('[data-more-copy]');
    if (!btn) return;
    const code = btn.parentElement.querySelector('code');
    if (!code || !navigator.clipboard) return;
    navigator.clipboard.writeText(code.textContent).then(() => {
      const label = btn.textContent;
      btn.textContent = 'Copied';
      btn.classList.add('more-copied');
      setTimeout(() => { btn.textContent = label; btn.classList.remove('more-copied'); }, 1500);
    }, () => { /* the browser refused: nothing to tell */ });
  });
})();
