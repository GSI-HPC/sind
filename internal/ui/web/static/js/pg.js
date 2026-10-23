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

  // draw shows the selection in the table and in its bar, with only the
  // actions that fit the selected rows.
  function draw() {
    const root = document.querySelector('[data-pg-select]');
    if (!root) return;
    ensure();
    const rows = [...root.querySelectorAll('tr[data-pg-item]')];
    // A row that is gone is no longer selected; before the first rows
    // arrive, the selection stays.
    if (rows.length) {
      const names = new Set(rows.map(r => r.dataset.pgItem));
      for (const n of [...sel.items]) if (!names.has(n)) sel.items.delete(n);
    }
    for (const r of rows) {
      const on = sel.items.has(r.dataset.pgItem);
      r.classList.toggle('pg-sel', on);
      r.querySelector('input[data-pg-sel]').checked = on;
    }
    const chosen = rows.filter(r => sel.items.has(r.dataset.pgItem));
    const shown = rows.filter(r => !r.hidden);
    const all = root.querySelector('input[data-pg-all]');
    if (all) {
      all.checked = shown.length > 0 && chosen.length === shown.length;
      all.indeterminate = chosen.length > 0 && chosen.length < shown.length;
    }
    const bar = root.querySelector('[data-pg-selbar]');
    bar.hidden = chosen.length === 0;
    bar.querySelector('[data-pg-expr]').textContent = root.dataset.pgSelect === 'nodes'
      ? fold(chosen.map(r => r.dataset.pgItem))
      : chosen.map(r => r.dataset.pgLabel).join(', ');
    // A verb shows when it fits a selected row, and acts on those it fits:
    // only workers can be drained, resumed and deleted. Delete names what
    // it deletes.
    for (const a of bar.querySelectorAll('a[data-pg-fit]')) {
      const fit = new Set(a.dataset.pgFit.split(' '));
      const fits = chosen.filter(r => r.dataset.pgMatch.split(' ').some(w => fit.has(w)));
      const noun = a.dataset.pgNoun || 'node';
      a.hidden = fits.length === 0;
      if (a.hasAttribute('data-pg-delete')) {
        a.textContent = a.dataset.pgVerb + ' ' + (fits.length === 1 ? fits[0].dataset.pgItem : plural(fits.length, noun, noun + 's')) + '…';
      } else {
        a.textContent = a.dataset.pgVerb + (fits.length < chosen.length ? ' ' + plural(fits.length, noun, noun + 's') : '') + '…';
      }
      a.dataset.pgNodes = fits.map(r => r.dataset.pgItem).sort().join(',');
    }
    const bulk = bar.querySelector('a[data-pg-bulk]');
    if (bulk) bulk.textContent = chosen.length > 1 ? 'Delete ' + chosen.length + ' clusters…' : 'Delete…';
    saveSel();
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
    if (window.htmx) window.htmx.ajax('GET', url, { target: '#layer', swap: 'innerHTML' });
    else location.assign(url);
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
        ensure();
        if (t.checked) sel.items.add(t.dataset.pgSel); else sel.items.delete(t.dataset.pgSel);
        draw();
      } else if (t.matches('input[data-pg-all]')) {
        ensure();
        for (const r of t.closest('[data-pg-select]').querySelectorAll('tr[data-pg-item]')) {
          if (t.checked && !r.hidden) sel.items.add(r.dataset.pgItem); else sel.items.delete(r.dataset.pgItem);
        }
        draw();
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
        openDialog(dlg.getAttribute('href') + '&nodes=' + encodeURIComponent(dlg.dataset.pgNodes || ''));
        return;
      }
      const bulk = t.closest('a[data-pg-bulk]');
      if (bulk) {
        ev.preventDefault();
        openDialog(bulkURL(bulk.closest('[data-pg-select]')));
        return;
      }
      if (t.closest('[data-pg-clear]')) { sel.items.clear(); draw(); return; }
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
      if (row && !t.closest('a, button, input, select, label, .c-chk')) {
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
  // selection are drawn again.
  document.addEventListener('htmx:afterSwap', () => {
    filter();
    keepText(false);
    draw();
  });
})();
