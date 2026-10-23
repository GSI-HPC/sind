// SPDX-License-Identifier: LGPL-3.0-or-later

// The "more" part's script: the Copy buttons, and the filter of the DNS
// records. Nothing here evaluates text.
(() => {
  'use strict';

  // filters holds what was typed into each filter, by the id of its
  // table, so that a live update, which draws the table and the field
  // again, keeps it.
  const filters = new Map();

  function flash(btn, text) {
    const label = btn.dataset.moreLabel || btn.textContent;
    btn.dataset.moreLabel = label;
    btn.textContent = text;
    btn.classList.add('more-copied');
    clearTimeout(btn.moreTimer);
    btn.moreTimer = setTimeout(() => { btn.textContent = label; btn.classList.remove('more-copied'); }, 1500);
  }

  // select selects what el holds, for the reader to copy with Ctrl+C.
  function select(el) {
    if (el instanceof HTMLTextAreaElement) { el.focus(); el.select(); el.scrollTop = 0; return; }
    const range = document.createRange();
    range.selectNodeContents(el);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  // oldCopy copies text the way browsers did before the clipboard API,
  // which they still allow on a page that is not served from this
  // machine, and says whether it worked.
  function oldCopy(text, btn) {
    const area = document.createElement('textarea');
    area.className = 'more-clip';
    area.value = text;
    area.readOnly = true;
    area.tabIndex = -1;
    area.setAttribute('aria-hidden', 'true');
    document.body.appendChild(area);
    area.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    area.remove();
    btn.focus();
    return ok;
  }

  // copy puts text on the clipboard. When the browser has no clipboard
  // API or refuses it, as on a page that is not served from this machine,
  // it copies the old way; when that fails too, it selects what target()
  // returns, if anything, and asks the reader to press Ctrl+C.
  function copy(text, btn, target) {
    const refused = () => {
      if (oldCopy(text, btn)) { flash(btn, 'Copied'); return; }
      const el = target();
      if (el) select(el);
      flash(btn, 'Press Ctrl+C');
    };
    if (!navigator.clipboard) { refused(); return; }
    navigator.clipboard.writeText(text).then(() => flash(btn, 'Copied'), refused);
  }

  // shownText returns the element beside btn that shows text: the name in
  // its row, or the address beside it.
  function shownText(btn, text) {
    const box = btn.closest('tr, .more-addr');
    if (!box) return null;
    return [...box.querySelectorAll('.mono')].find(el => el.textContent.trim() === text) || null;
  }

  // copyBox shows lines in a box under the header of btn's panel, for the
  // reader to copy with Ctrl+C, and returns it.
  function copyBox(btn, id, lines) {
    let box = byData('data-more-copybox', id);
    if (!box) {
      box = document.createElement('textarea');
      box.className = 'more-copybox';
      box.readOnly = true;
      box.spellcheck = false;
      box.dataset.moreCopybox = id;
      box.setAttribute('aria-label', 'The records as lines of /etc/hosts. Press Ctrl+C to copy them.');
      (btn.closest('.ph') || btn).after(box);
    }
    box.value = lines;
    box.rows = Math.min(lines.split('\n').length - 1, 8) || 1;
    return box;
  }

  function byData(name, id) {
    return document.querySelector('[' + name + '="' + CSS.escape(id) + '"]');
  }

  // filter shows the rows of the table id that hold what was typed, and
  // says how many.
  function filter(id) {
    const table = document.getElementById(id);
    if (!table) return;
    const q = (filters.get(id) || '').trim().toLowerCase();
    const rows = [...table.tBodies[0].rows];
    let shown = 0;
    for (const r of rows) {
      const hit = !q || r.dataset.moreRow.toLowerCase().includes(q);
      r.hidden = !hit;
      if (hit) shown++;
    }
    const count = byData('data-more-count', id);
    const records = rows.length === 1 ? '1 record' : rows.length + ' records';
    if (count) count.textContent = q ? shown + ' of ' + records : records;
    const none = byData('data-more-none', id);
    if (none) none.hidden = shown > 0;
  }

  // refilter puts back what was typed after a live update.
  function refilter() {
    for (const input of document.querySelectorAll('[data-more-filter]')) {
      const id = input.dataset.moreFilter;
      if (!filters.has(id)) continue;
      if (input.value !== filters.get(id)) input.value = filters.get(id);
      filter(id);
    }
  }

  document.addEventListener('input', ev => {
    const input = ev.target.closest('[data-more-filter]');
    if (!input) return;
    filters.set(input.dataset.moreFilter, input.value);
    filter(input.dataset.moreFilter);
  });
  document.addEventListener('htmx:afterSettle', refilter);

  document.addEventListener('click', ev => {
    const t = ev.target;
    const btn = t.closest('[data-more-copy]');
    if (btn) {
      const code = btn.parentElement.querySelector('code');
      if (code) copy(code.textContent, btn, () => code);
      return;
    }
    const text = t.closest('[data-more-copy-text]');
    if (text) {
      const value = text.dataset.moreCopyText;
      copy(value, text, () => shownText(text, value));
      return;
    }
    const rows = t.closest('[data-more-copy-rows]');
    if (rows) {
      const id = rows.dataset.moreCopyRows;
      const table = document.getElementById(id);
      if (!table) return;
      const lines = [...table.tBodies[0].rows].filter(r => !r.hidden).map(r => r.dataset.moreRow).join('\n') + '\n';
      copy(lines, rows, () => copyBox(rows, id, lines));
    }
  });
})();
