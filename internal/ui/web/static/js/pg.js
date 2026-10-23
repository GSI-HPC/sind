// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the main pages: clickable table rows, the filter field of
// the lists, the node selection of a cluster's nodes tab, and Copy for
// commands. Nothing here evaluates text, and the page holds no inline
// script.
(() => {
  'use strict';

  // The nodes that the user ticked, by name. Live updates replace the
  // table, so the set lives here and is drawn onto the table again.
  const selected = new Set();
  let selectedFor = '';

  // fold writes node names as one node set, as sind's commands take it:
  // worker-0 worker-1 worker-3 become worker-[0-1,3].
  function fold(names) {
    const groups = new Map();
    const single = [];
    for (const n of names) {
      const m = /^(.*?-)(\d+)$/.exec(n);
      if (!m) { single.push(n); continue; }
      if (!groups.has(m[1])) groups.set(m[1], []);
      groups.get(m[1]).push(+m[2]);
    }
    const parts = [...single];
    for (const [prefix, nums] of groups) {
      nums.sort((a, b) => a - b);
      const ranges = [];
      for (let i = 0; i < nums.length; i++) {
        let j = i;
        while (j + 1 < nums.length && nums[j + 1] === nums[j] + 1) j++;
        ranges.push(i === j ? String(nums[i]) : nums[i] + '-' + nums[j]);
        i = j;
      }
      parts.push(nums.length === 1 ? prefix + nums[0] : prefix + '[' + ranges.join(',') + ']');
    }
    return parts.join(' ');
  }

  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }

  // words are the words of the filter field, in lower case.
  function words() {
    const f = document.querySelector('[data-pg-filter]');
    return f ? f.value.toLowerCase().split(/\s+/).filter(Boolean) : [];
  }

  // filter hides the rows of a list that do not hold every word of the
  // filter field. When the user typed, it tells screen readers how many
  // rows match.
  function filter(typed) {
    const list = document.querySelector('[data-pg-list]');
    if (!list) return;
    const w = words();
    let shown = 0;
    const rows = list.querySelectorAll('tr[data-pg-text]');
    for (const r of rows) {
      const on = w.every(x => r.dataset.pgText.includes(x));
      r.hidden = !on;
      if (on) shown++;
    }
    const none = list.querySelector('[data-pg-none]');
    if (none) none.hidden = shown > 0;
    if (typed) {
      const noun = list.dataset.pgList || 'row';
      const all = plural(rows.length, noun, noun + 's');
      say(w.length ? shown + ' of ' + all + ' match the filter.' : all + ' shown.');
    }
  }

  // say tells screen readers a sentence through the page's polite live
  // region, once the user stops typing.
  let sayTimer = 0;
  function say(text) {
    const region = document.getElementById('say');
    if (!region) return;
    clearTimeout(sayTimer);
    sayTimer = setTimeout(() => {
      region.textContent = '';
      setTimeout(() => { region.textContent = text; }, 60);
    }, 500);
  }

  // keepText writes the text of the filter field into the address of the
  // page and into the links that keep it: the chips, the column heads and
  // the realms.
  function keepText(address) {
    const f = document.querySelector('[data-pg-filter]');
    if (!f) return;
    const v = f.value.trim();
    const set = u => { if (v) u.searchParams.set('q', v); else u.searchParams.delete('q'); };
    if (address) {
      const u = new URL(location.href);
      set(u);
      history.replaceState(history.state, '', u.pathname + u.search + u.hash);
    }
    for (const a of document.querySelectorAll('a[data-pg-keep]')) {
      const u = new URL(a.href);
      set(u);
      a.setAttribute('href', u.pathname + u.search);
    }
  }

  // clusterKey names the cluster whose nodes are selected, so that a
  // selection does not follow the user to another page.
  function clusterKey() { return location.pathname; }

  // draw shows the selection in the table and in its bar.
  function draw() {
    const root = document.querySelector('[data-pg-nodes]');
    if (!root) return;
    if (selectedFor !== clusterKey()) { selected.clear(); selectedFor = clusterKey(); }
    const rows = [...root.querySelectorAll('tr[data-pg-node]')];
    // A node that is gone is no longer selected.
    const names = new Set(rows.map(r => r.dataset.pgNode));
    for (const n of [...selected]) if (!names.has(n)) selected.delete(n);
    for (const r of rows) {
      const on = selected.has(r.dataset.pgNode);
      r.classList.toggle('pg-sel', on);
      r.querySelector('input[data-pg-sel]').checked = on;
    }
    const all = root.querySelector('input[data-pg-all]');
    if (all) all.checked = rows.length > 0 && selected.size === rows.length;
    const bar = root.querySelector('[data-pg-selbar]');
    bar.hidden = selected.size === 0;
    const chosen = rows.filter(r => selected.has(r.dataset.pgNode));
    bar.querySelector('[data-pg-expr]').textContent = fold(chosen.map(r => r.dataset.pgNode));
    // Only workers can be drained, resumed and deleted.
    const workers = chosen.every(r => r.dataset.pgRole === 'worker');
    for (const b of bar.querySelectorAll('[data-pg-workers]')) b.hidden = !workers;
    // The delete button names what it deletes.
    const del = bar.querySelector('[data-pg-delete]');
    if (del) del.textContent = chosen.length === 1 ? 'Delete ' + chosen[0].dataset.pgNode + '…' : 'Delete ' + chosen.length + ' workers…';
  }

  // dialogURL adds the selected nodes to the address of a dialog.
  function dialogURL(a) {
    const nodes = [...selected].sort().join(',');
    return a.getAttribute('href') + '&nodes=' + encodeURIComponent(nodes);
  }

  function copy(text, btn) {
    const done = () => {
      const old = btn.textContent;
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = old; }, 1200);
    };
    if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, () => {});
  }

  // realmPicker makes the realm picker of the top bar safe for the keyboard:
  // a pick of the mouse, or of the list that the keyboard opened, applies at
  // once; the arrow keys on the closed list only browse it, and Enter, or
  // the focus leaving, applies the realm they reached. On Apple systems the
  // arrow keys open the list, as Space does elsewhere.
  function realmPicker() {
    const form = document.querySelector('form[data-pg-realm]');
    if (!form) return;
    const sel = form.querySelector('select');
    const hint = form.querySelector('[data-pg-realm-hint]');
    const start = sel.value;
    const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
    const arrowsOpen = /mac|iphone|ipad|ipod/i.test(platform);
    let browsing = false;
    let applied = false;
    const apply = () => {
      if (applied || sel.value === start) return;
      applied = true;
      form.requestSubmit();
    };
    sel.addEventListener('keydown', ev => {
      if (ev.key === 'Enter') { ev.preventDefault(); apply(); return; }
      if (ev.key === 'Escape' && browsing) {
        sel.value = start;
        hint.textContent = '';
        browsing = false;
        return;
      }
      // Space, Alt+arrow and F4 open the list, where a pick is a choice.
      const arrow = /^Arrow(Up|Down)$/.test(ev.key);
      const opens = ev.key === ' ' || ev.key === 'F4' || (arrow && (ev.altKey || arrowsOpen));
      browsing = !opens && ev.key !== 'Tab' && ev.key !== 'Shift' && ev.key !== 'Escape';
    });
    sel.addEventListener('pointerdown', () => { browsing = false; });
    sel.addEventListener('change', () => {
      if (!browsing) { apply(); return; }
      const name = sel.value ? 'realm ' + sel.value : 'all realms';
      hint.textContent = sel.value === start ? '' : 'Press Enter to show ' + name + '.';
    });
    sel.addEventListener('blur', apply);
  }

  document.addEventListener('DOMContentLoaded', () => {
    realmPicker();
    document.addEventListener('change', ev => {
      const t = ev.target;
      if (t.matches('input[data-pg-sel]')) {
        selectedFor = clusterKey();
        if (t.checked) selected.add(t.dataset.pgSel); else selected.delete(t.dataset.pgSel);
        draw();
      } else if (t.matches('input[data-pg-all]')) {
        selectedFor = clusterKey();
        for (const c of document.querySelectorAll('input[data-pg-sel]')) {
          if (t.checked) selected.add(c.dataset.pgSel); else selected.delete(c.dataset.pgSel);
        }
        draw();
      }
    });

    document.addEventListener('input', ev => {
      if (!ev.target.matches('[data-pg-filter]')) return;
      filter(true);
      keepText(true);
    });

    // With the script, the list filters as the user types; Enter has
    // nothing left to do. Without it, the form asks the server.
    document.addEventListener('submit', ev => {
      if (ev.target.matches('[data-pg-filter-form]')) ev.preventDefault();
    });

    // Escape in the filter field empties it, before the other keys of the
    // page see it.
    document.addEventListener('keydown', ev => {
      const t = ev.target;
      if (ev.key !== 'Escape' || !t.matches || !t.matches('[data-pg-filter]') || !t.value) return;
      ev.preventDefault();
      ev.stopPropagation();
      t.value = '';
      t.dispatchEvent(new Event('input', { bubbles: true }));
    }, true);

    document.addEventListener('click', ev => {
      const t = ev.target;
      const dlg = t.closest('a[data-pg-dialog]');
      if (dlg) {
        ev.preventDefault();
        if (window.htmx) window.htmx.ajax('GET', dialogURL(dlg), { target: '#layer', swap: 'innerHTML' });
        return;
      }
      if (t.closest('[data-pg-clear]')) { selected.clear(); draw(); return; }
      const cp = t.closest('[data-pg-copy]');
      if (cp) {
        const code = cp.closest('.cmd').querySelector('code');
        copy(code.textContent, cp);
        return;
      }
      const ct = t.closest('[data-pg-copy-text]');
      if (ct) { copy(ct.dataset.pgCopyText, ct); return; }
      // Copy of a long text, such as a config file: the text of the
      // element it names.
      const cf = t.closest('[data-pg-copy-from]');
      if (cf) {
        const src = document.getElementById(cf.dataset.pgCopyFrom);
        if (src) copy(src.textContent, cf);
        return;
      }
      // A row of a list is a link, for the mouse; its first link is for
      // the keyboard.
      const row = t.closest('tr[data-href]');
      if (row && !t.closest('a, button, input, select, label')) {
        const a = row.querySelector('a[href]');
        if (a) a.click();
      }
    });

    filter();
    draw();
  });

  // After a live update replaced a table, the filter, the links and the
  // selection are drawn again.
  document.addEventListener('htmx:afterSwap', () => {
    filter();
    keepText(false);
    draw();
  });
})();
