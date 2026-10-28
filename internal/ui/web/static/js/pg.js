// SPDX-License-Identifier: LGPL-3.0-or-later

// The script of the main pages: clickable table rows, the filter field of
// the lists, the selections of the Clusters list and of a cluster's Nodes
// tab, and Copy for commands. Nothing here evaluates text, and the page
// holds no inline script.
(() => {
  'use strict';

  // The rows that the user ticked, by their data-pg-item. Live updates
  // replace the table, so the set lives here and is drawn onto the table
  // again; the tab keeps it in sessionStorage, so a reload keeps it too.
  const sel = { key: '', items: new Set() };

  function storeKey() { return 'sind-ui-sel:' + location.pathname; }

  function loadSel() {
    try {
      const v = JSON.parse(sessionStorage.getItem(storeKey()) || '[]');
      return Array.isArray(v) ? v.filter(x => typeof x === 'string') : [];
    } catch (e) { return []; }
  }

  function saveSel() {
    try {
      if (sel.items.size) sessionStorage.setItem(storeKey(), JSON.stringify([...sel.items]));
      else sessionStorage.removeItem(storeKey());
    } catch (e) { /* private window */ }
  }

  // ensure loads the selection of this page, so that a selection does not
  // follow the user to another page.
  function ensure() {
    if (sel.key === storeKey()) return;
    sel.key = storeKey();
    sel.items = new Set(loadSel());
  }

  // fold writes node names as one node set, as sind's commands take it:
  // controller,worker-[0-1,3] (nodeset.js).
  function fold(names) { return window.sindNodeSet.fold(names); }

  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }

  // words are the words of the filter field, in lower case.
  function words() {
    const f = document.querySelector('[data-pg-filter]');
    return f ? f.value.toLowerCase().split(/\s+/).filter(Boolean) : [];
  }

  // filter hides the rows of a list that do not hold every word of the
  // filter field, and drops them from the selection: an action acts only
  // on rows that the user sees. When the user typed, it tells screen
  // readers how many rows match.
  function filter(typed) {
    const list = document.querySelector('[data-pg-list]');
    if (!list) return;
    ensure();
    const w = words();
    let shown = 0;
    const rows = list.querySelectorAll('tr[data-pg-text]');
    for (const r of rows) {
      const on = w.every(x => r.dataset.pgText.includes(x));
      r.hidden = !on;
      if (on) shown++;
      else if (r.dataset.pgItem) sel.items.delete(r.dataset.pgItem);
    }
    const none = list.querySelector('[data-pg-none]');
    if (none) none.hidden = shown > 0;
    if (typed) {
      const noun = list.dataset.pgList || 'row';
      const all = plural(rows.length, noun, noun + 's');
      say(w.length ? shown + ' of ' + all + ' match the filter.' : all + ' shown.');
    }
  }

  // say tells screen readers a sentence, once the user stops typing: app.js
  // puts it into the page's polite live region.
  let sayTimer = 0;
  function say(text) {
    clearTimeout(sayTimer);
    sayTimer = setTimeout(() => document.dispatchEvent(new CustomEvent('sind:say', { detail: text })), 500);
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

  // selectable reports whether a row can be selected: its checkbox is not
  // off, as it is while a command creates or deletes what the row shows.
  function selectable(r) {
    const box = r.querySelector('input[data-pg-sel]');
    return !!box && !box.disabled;
  }

  // draw shows the selection in the table and in its bar, with only the
  // actions that fit the selected rows.
  function draw() {
    const root = document.querySelector('[data-pg-select]');
    if (!root) return;
    ensure();
    const rows = [...root.querySelectorAll('tr[data-pg-item]')];
    // A row that is gone is no longer selected, nor one that a command
    // creates or deletes, whose checkbox is off; before the first rows
    // arrive, the selection stays.
    if (rows.length) {
      const names = new Set(rows.filter(selectable).map(r => r.dataset.pgItem));
      for (const n of [...sel.items]) if (!names.has(n)) sel.items.delete(n);
    }
    for (const r of rows) {
      const on = sel.items.has(r.dataset.pgItem);
      r.classList.toggle('pg-sel', on);
      r.querySelector('input[data-pg-sel]').checked = on;
    }
    const chosen = rows.filter(r => sel.items.has(r.dataset.pgItem));
    const shown = rows.filter(r => !r.hidden && selectable(r));
    const all = root.querySelector('input[data-pg-all]');
    if (all) {
      // Off while no row shown can be selected, as during a create.
      all.disabled = shown.length === 0;
      all.checked = shown.length > 0 && chosen.length === shown.length;
      all.indeterminate = chosen.length > 0 && chosen.length < shown.length;
    }
    const bar = root.querySelector('[data-pg-selbar]');
    bar.hidden = chosen.length === 0;
    if (root.dataset.pgSelect === 'nodes') drawNodeBar(bar, chosen);
    else bar.querySelector('[data-pg-expr]').textContent = chosen.map(r => r.dataset.pgLabel).join(', ');
    const bulk = bar.querySelector('a[data-pg-bulk]');
    if (bulk) bulk.textContent = chosen.length > 1 ? 'Delete ' + chosen.length + ' clusters…' : 'Delete…';
    grid(root);
    saveSel();
  }

  // isWorker reports whether the row of the nodes tab is a worker.
  function isWorker(r) { return r.dataset.pgMatch.split(' ').includes('worker'); }

  // count says how many nodes the rows are: "3 workers" while all are
  // workers, else "3 nodes"; the name of one.
  function count(rows) {
    if (rows.length === 1) return rows[0].dataset.pgItem;
    return rows.every(isWorker) ? plural(rows.length, 'worker', 'workers') : plural(rows.length, 'node', 'nodes');
  }

  // and joins words as a sentence does: "a, b and c".
  function and(words) {
    return words.length < 2 ? words.join('') : words.slice(0, -1).join(', ') + ' and ' + words[words.length - 1];
  }

  // drawNodeBar fills the bar of the nodes tab: how many nodes are
  // selected and which; each verb that fits one of them, named with the
  // nodes it acts on, which the row's data-pg-ops say; More while one of
  // its verbs fits; and the line that names the nodes a verb skips, as
  // "Drain skips controller, db and worker-[4-5]."
  function drawNodeBar(bar, chosen) {
    bar.querySelector('[data-pg-count]').textContent = chosen.length === 1 ? '1 node' : plural(chosen.length, 'node', 'nodes');
    bar.querySelector('[data-pg-expr]').textContent = fold(chosen.map(r => r.dataset.pgItem));
    const skips = new Map();
    for (const a of bar.querySelectorAll('a[data-pg-fit]')) {
      const fits = chosen.filter(r => (r.dataset.pgOps || '').split(' ').includes(a.dataset.pgFit));
      a.hidden = fits.length === 0;
      const label = a.querySelector('.pg-ml') || a;
      label.textContent = a.dataset.pgVerb + ' ' + count(fits) + '…';
      a.dataset.pgNodes = fits.map(r => r.dataset.pgItem).sort().join(',');
      if (a.hidden || fits.length === chosen.length) continue;
      const skipped = and(window.sindNodeSet.parts(chosen.filter(r => !fits.includes(r)).map(r => r.dataset.pgItem)));
      if (!skips.has(skipped)) skips.set(skipped, []);
      skips.get(skipped).push(a.dataset.pgVerb);
    }
    const more = bar.querySelector('[data-pg-selmore]');
    if (more) {
      more.hidden = !more.querySelector('a[data-pg-fit]:not([hidden])');
      if (more.hidden) more.open = false;
    }
    const line = bar.querySelector('[data-pg-skip]');
    const parts = [...skips].map(([nodes, verbs]) => and(verbs) + (verbs.length === 1 ? ' skips ' : ' skip ') + nodes);
    line.hidden = parts.length === 0;
    line.textContent = parts.length ? parts.join('; ') + '.' : '';
  }

  // sayCount says how many nodes are selected, after the user changed it.
  function sayCount(root) {
    if (!root || root.dataset.pgSelect !== 'nodes') return;
    const chosen = [...root.querySelectorAll('tr[data-pg-item]')].filter(r => sel.items.has(r.dataset.pgItem));
    say(chosen.length ? plural(chosen.length, 'node', 'nodes') + ' selected: ' + fold(chosen.map(r => r.dataset.pgItem)) + '.' : 'No node selected.');
  }

  // The table of the nodes tab, [data-pg-grid], is one stop of Tab: the
  // arrow keys move between its rows and the controls of a row, Home and
  // End to the first and the last of a row, with Control to the first and
  // the last row; Space ticks the node from any cell of its row, and a
  // moves to the bar of the selection. The cell that had the focus keeps the stop.
  const cur = { item: '', col: 0 };

  // cells are the controls of a row that take the focus: the tick, the
  // links, the buttons and the words that open their help card.
  const focusable = 'input, a[href], button, [tabindex]';
  function cells(r) {
    return [...r.querySelectorAll(focusable)].filter(e => !e.disabled);
  }

  function gridRows(table) {
    return [...table.querySelectorAll('tbody tr[data-pg-item]')].filter(r => !r.hidden && cells(r).length);
  }

  // grid gives the stop of Tab to the cell of cur, or to the first cell of
  // the first row.
  function grid(root) {
    const table = root.querySelector('[data-pg-grid]');
    if (!table) return;
    const rows = gridRows(table);
    for (const e of table.querySelectorAll('tbody tr[data-pg-item]')) for (const c of e.querySelectorAll(focusable)) c.tabIndex = -1;
    if (!rows.length) return;
    const r = rows.find(x => x.dataset.pgItem === cur.item) || rows[0];
    const cs = cells(r);
    cs[Math.min(cur.col, cs.length - 1)].tabIndex = 0;
  }

  function gridKeys(ev) {
    const t = ev.target;
    const table = t.closest && t.closest('[data-pg-grid]');
    const row = table && t.closest('tbody tr[data-pg-item]');
    if (!row || ev.altKey || ev.metaKey) return;
    const rows = gridRows(table);
    let ri = rows.indexOf(row);
    let ci = cells(row).indexOf(t);
    if (ri < 0 || ci < 0) return;
    // Space ticks the node from any cell of its row; on the tick itself,
    // the checkbox does.
    if (ev.key === ' ' && !ev.ctrlKey && !t.matches('input[data-pg-sel]')) {
      const box = row.querySelector('input[data-pg-sel]');
      ev.preventDefault();
      if (box && !box.disabled) box.click();
      return;
    }
    if (ev.key === 'a' && !ev.ctrlKey && !ev.shiftKey) {
      const bar = table.closest('[data-pg-select]').querySelector('[data-pg-selbar]');
      const first = bar && !bar.hidden && bar.querySelector('a.btn:not([hidden]), summary, button');
      if (first) { ev.preventDefault(); first.focus(); }
      return;
    }
    switch (ev.key) {
      case 'ArrowDown': ri = Math.min(ri + 1, rows.length - 1); break;
      case 'ArrowUp': ri = Math.max(ri - 1, 0); break;
      case 'ArrowRight': ci++; break;
      case 'ArrowLeft': ci = Math.max(ci - 1, 0); break;
      case 'Home': if (ev.ctrlKey) ri = 0; ci = 0; break;
      case 'End': if (ev.ctrlKey) ri = rows.length - 1; ci = 1e9; break;
      default: return;
    }
    ev.preventDefault();
    const cs = cells(rows[ri]);
    const next = cs[Math.min(ci, cs.length - 1)];
    cur.item = rows[ri].dataset.pgItem;
    cur.col = cs.indexOf(next);
    grid(table.closest('[data-pg-select]'));
    next.focus();
  }

  // The cell that takes the focus, by the mouse too, keeps the stop.
  function gridFocus(ev) {
    const t = ev.target;
    const row = t.closest && t.closest('[data-pg-grid] tbody tr[data-pg-item]');
    if (!row) return;
    const ci = cells(row).indexOf(t);
    if (ci < 0) return;
    cur.item = row.dataset.pgItem;
    cur.col = ci;
    grid(row.closest('[data-pg-select]'));
  }

  // forget drops the selection of the Clusters list once a delete of its
  // clusters started, so that the bar does not offer to delete them again
  // while their tasks run.
  const clustersKey = 'sind-ui-sel:/clusters';
  function forget() {
    try { sessionStorage.removeItem(clustersKey); } catch (e) { /* private window */ }
    if (sel.key === clustersKey) sel.items.clear();
  }

  // openDialog shows the dialog at url in the layer.
  function openDialog(url) {
    htmx.ajax('GET', url, { target: '#layer', swap: 'innerHTML' });
  }

  // bulkURL is the dialog that deletes the selected clusters: the one of
  // the cluster for one, else the one for several, which lists them.
  function bulkURL(root) {
    const chosen = [...root.querySelectorAll('tr[data-pg-item]')].filter(r => sel.items.has(r.dataset.pgItem));
    if (chosen.length === 1) return chosen[0].dataset.pgOne;
    const q = new URLSearchParams();
    for (const r of chosen) q.append('clusters', r.dataset.pgItem);
    q.set('back', location.pathname + location.search);
    return '/dialogs/delete-clusters?' + q.toString();
  }

  // copy copies the text. The button says Copied for a moment, or, with
  // data-pg-copy-say, keeps its label and its sign and a toast says what
  // it copied and what to do with it.
  function copy(text, btn) {
    const done = () => {
      if (btn.dataset.pgCopySay) {
        document.dispatchEvent(new CustomEvent('sind:toast', { detail: btn.dataset.pgCopySay }));
        return;
      }
      const old = btn.textContent;
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = old; }, 1200);
    };
    if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, () => {});
  }

  // The menus of the head of a node page, of the rows of a list and the
  // realm switcher of the rail, <details data-pg-menu>: one is open at a
  // time; the arrow keys, Home and End move between its verbs, which skip
  // none, as a verb that is off says why; Escape and Tab close it, and a
  // click beside it too. A verb opens its dialog, which gives the focus back
  // to the menu's button when it closes; an entry of the switcher is a link.
  function menuItems(m) { return [...m.querySelectorAll('[role^="menuitem"]')].filter(i => !i.hidden); }

  function closeMenu(m, focus) {
    if (!m.open) return;
    m.open = false;
    if (focus) m.querySelector('summary').focus();
  }

  function closeMenus(except) {
    for (const m of document.querySelectorAll('details[data-pg-menu][open]')) if (m !== except) closeMenu(m, false);
  }

  // placeMenu lays the menu of a table row, or of the rail, over the page,
  // below its button or, where the screen ends, above it, and inside the
  // screen, as the box of the table scrolls sideways and would clip it, and
  // the rail too. The menu of the rail starts at the left of its button.
  function placeMenu(m) {
    const menu = m.querySelector('.pg-menu');
    if (!menu || !m.closest('.tw, .rail')) return;
    if (!m.open) {
      menu.classList.remove('pg-fixed');
      menu.style.left = menu.style.top = '';
      return;
    }
    menu.classList.add('pg-fixed');
    const b = m.querySelector('summary').getBoundingClientRect();
    const w = menu.offsetWidth, h = menu.offsetHeight;
    const vw = document.documentElement.clientWidth;
    let end = window.innerHeight;
    const rail = document.querySelector('.rail');
    if (rail && getComputedStyle(rail).position === 'fixed' && rail.getBoundingClientRect().top > end / 2) end = rail.getBoundingClientRect().top;
    const left = Math.max(8, Math.min(m.closest('.rail') ? b.left : b.right - w, vw - w - 8));
    const top = b.bottom + 6 + h > end - 8 && b.top - 6 - h >= 8 ? b.top - 6 - h : b.bottom + 6;
    menu.style.left = left + 'px';
    menu.style.top = top + 'px';
  }

  function menus() {
    const placeOpen = () => { for (const m of document.querySelectorAll('details[data-pg-menu][open]')) placeMenu(m); };
    window.addEventListener('scroll', placeOpen, true);
    window.addEventListener('resize', placeOpen);
    document.addEventListener('toggle', ev => {
      const m = ev.target;
      if (!(m instanceof HTMLDetailsElement) || !m.hasAttribute('data-pg-menu')) return;
      m.querySelector('summary').setAttribute('aria-expanded', String(m.open));
      placeMenu(m);
      if (!m.open) return;
      closeMenus(m);
      // A menu opened with the keyboard moves the focus to its first verb.
      if (m.dataset.pgMenuKey) {
        delete m.dataset.pgMenuKey;
        const first = menuItems(m)[0];
        if (first) first.focus();
      }
    }, true);
    document.addEventListener('keydown', ev => {
      const m = ev.target.closest && ev.target.closest('details[data-pg-menu]');
      if (!m) return;
      const items = menuItems(m);
      const i = items.indexOf(document.activeElement);
      if (ev.target.matches('summary')) {
        if (ev.key === 'ArrowDown' || ev.key === 'Enter' || ev.key === ' ') {
          if (!m.open) {
            ev.preventDefault();
            m.dataset.pgMenuKey = '1';
            m.open = true;
          } else if (ev.key === 'ArrowDown' && items[0]) {
            ev.preventDefault();
            items[0].focus();
          }
        } else if (ev.key === 'Escape') closeMenu(m, true);
        return;
      }
      let next = -1;
      switch (ev.key) {
        case 'ArrowDown': next = (i + 1) % items.length; break;
        case 'ArrowUp': next = (i - 1 + items.length) % items.length; break;
        case 'Home': next = 0; break;
        case 'End': next = items.length - 1; break;
        case 'Escape': ev.preventDefault(); closeMenu(m, true); return;
        case 'Tab': closeMenu(m, false); return;
        case ' ': if (document.activeElement.matches('a')) { ev.preventDefault(); document.activeElement.click(); } return;
        default: return;
      }
      ev.preventDefault();
      items[next].focus();
    });
    document.addEventListener('click', ev => {
      const t = ev.target;
      const m = t.closest('details[data-pg-menu]');
      closeMenus(m);
      if (!m) return;
      if (t.closest('[role="menuitem"][aria-disabled="true"]')) { ev.preventDefault(); return; }
      const item = t.closest('a[data-pg-menu-item]');
      // A link of the menu, such as a realm of the switcher, goes where it
      // leads, and the help that one opens closes the menu.
      if (!item) { if (t.closest('[role^="menuitem"]')) closeMenu(m, false); return; }
      ev.preventDefault();
      const summary = m.querySelector('summary');
      closeMenu(m, true);
      htmx.ajax('GET', item.getAttribute('href'), { target: '#layer', swap: 'innerHTML', source: summary });
    });
  }

  menus();
  document.addEventListener('keydown', gridKeys);
  document.addEventListener('focusin', gridFocus);

  document.addEventListener('DOMContentLoaded', () => {
    document.addEventListener('change', ev => {
      const t = ev.target;
      if (t.matches('input[data-pg-sel]')) {
        ensure();
        if (t.checked) sel.items.add(t.dataset.pgSel); else sel.items.delete(t.dataset.pgSel);
        draw();
        sayCount(t.closest('[data-pg-select]'));
      } else if (t.matches('input[data-pg-all]')) {
        ensure();
        for (const r of t.closest('[data-pg-select]').querySelectorAll('tr[data-pg-item]')) {
          if (t.checked && !r.hidden && selectable(r)) sel.items.add(r.dataset.pgItem); else sel.items.delete(r.dataset.pgItem);
        }
        draw();
        sayCount(t.closest('[data-pg-select]'));
      }
    });

    document.addEventListener('input', ev => {
      if (!ev.target.matches('[data-pg-filter]')) return;
      filter(true);
      keepText(true);
      draw();
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
        // A verb of More closes it; the dialog gives the focus back to More.
        const m = dlg.closest('details[data-pg-menu]');
        if (m) closeMenu(m, true);
        openDialog(dlg.getAttribute('href') + '&nodes=' + encodeURIComponent(dlg.dataset.pgNodes || ''));
        return;
      }
      const bulk = t.closest('a[data-pg-bulk]');
      if (bulk) {
        ev.preventDefault();
        openDialog(bulkURL(bulk.closest('[data-pg-select]')));
        return;
      }
      if (t.closest('[data-pg-clear]')) {
        const root = t.closest('[data-pg-select]');
        sel.items.clear();
        draw();
        sayCount(root);
        // The focus goes back to the table, as the bar hides.
        const stop = root && root.querySelector('[data-pg-grid] tbody [tabindex="0"]');
        if (stop) stop.focus();
        return;
      }
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
      // the keyboard. Its checkbox cell is not.
      const row = t.closest('tr[data-href]');
      if (row && !t.closest('a, button, input, select, label, details, .c-chk')) {
        const a = row.querySelector('a[href]');
        if (a) a.click();
      }
    });

    filter();
    draw();
  });

  // A delete of clusters that started answers with a redirect to the list
  // or to its task.
  const deletes = ['/actions/delete-clusters/run', '/actions/delete-cluster/run'];
  document.addEventListener('htmx:beforeOnLoad', ev => {
    const d = ev.detail;
    const path = (d.requestConfig && d.requestConfig.path) || '';
    if (d.xhr && d.xhr.status < 400 && d.xhr.getResponseHeader('HX-Redirect') && deletes.includes(path.split('?')[0])) forget();
  });

  // After a live update replaced a table, the filter, the links and the
  // selection are drawn again, and an open menu is laid over the page
  // again, as the morph took the place it had.
  document.addEventListener('htmx:afterSwap', () => {
    filter();
    keepText(false);
    draw();
    for (const m of document.querySelectorAll('details[data-pg-menu][open]')) placeMenu(m);
  });
})();
