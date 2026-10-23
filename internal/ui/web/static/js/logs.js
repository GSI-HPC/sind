// SPDX-License-Identifier: LGPL-3.0-or-later

// The Logs tab of the cluster page: one EventSource on /logs/{realm}/{cluster}
// that appends each line to a <pre> as text. It follows (keeps the scroll at
// the bottom) until the reader scrolls up or presses Pause, and keeps the
// last lines (data-keep). The filter and "errors and warnings only" hide
// lines without dropping them; Copy lines and Download take the lines
// shown. The stream closes when the tab goes away. The node, the source,
// the unit, the line count and the filter go into the address, as the
// server reads them, so that a reload or a shared link shows the same log.
(() => {
  'use strict';

  function init(root) {
    const $ = sel => root.querySelector(sel);
    const nodeSel = $('[data-logs-node]');
    const sourceSel = $('[data-logs-source]');
    const unitFld = $('[data-logs-unit-fld]');
    const unitSel = $('[data-logs-unit]');
    const linesSel = $('[data-logs-lines]');
    const qInp = $('[data-logs-q]');
    const errChk = $('[data-logs-errors]');
    const countEl = $('[data-logs-count]');
    const said = $('[data-logs-said]');
    const followBtn = $('[data-logs-follow]');
    const pauseBtn = $('[data-logs-pause]');
    const box = $('[data-logs-box]');
    const state = $('[data-logs-state]');
    const cmd = $('[data-logs-cmd]');
    const realm = root.dataset.realm;
    const cluster = root.dataset.cluster;
    const sind = root.dataset.sind;
    const suffix = root.dataset.suffix;
    const MAX_LINES = Number(root.dataset.keep) || 10000;
    let es = null;
    let following = true;
    let paused = false;
    let backlog = [];
    let n = 0;
    // total counts the log lines in the box, shown those the filter shows.
    let total = 0;
    let shown = 0;
    let saidTimer = 0;

    function setState(text) { state.textContent = text; }

    function fillUnits() {
      const opt = nodeSel.selectedOptions[0];
      const units = opt && opt.dataset.units ? opt.dataset.units.split(' ') : [];
      const old = unitSel.value;
      unitSel.replaceChildren();
      for (const u of ['', ...units]) {
        const o = document.createElement('option');
        o.value = u;
        o.textContent = u === '' ? 'all units' : u;
        unitSel.append(o);
      }
      if (old === '' || units.includes(old)) unitSel.value = old;
      else unitSel.value = units.length ? units[0] : '';
      showUnit();
    }

    function showUnit() { unitFld.hidden = sourceSel.value !== 'journal'; }

    // showCommand writes the command of a terminal that shows the same log,
    // as logsCommand in Go does.
    function showCommand() {
      const arg = nodeSel.value + suffix;
      const n = ' -f -n ' + linesSel.value;
      if (sourceSel.value !== 'journal') cmd.textContent = sind + ' logs ' + arg + n;
      else if (unitSel.value) cmd.textContent = sind + ' logs ' + arg + ' ' + unitSel.value + n;
      else cmd.textContent = sind + ' ssh ' + arg + ' -- journalctl' + n;
    }

    // keepInAddress writes the choices into the address of the page.
    function keepInAddress() {
      const u = new URL(location.href);
      const q = u.searchParams;
      q.set('tab', 'logs');
      q.set('node', nodeSel.value);
      if (sourceSel.value === 'journal') {
        q.set('source', 'journal');
        q.set('unit', unitSel.value);
      } else {
        q.delete('source');
        q.delete('unit');
      }
      if (linesSel.value !== linesSel.options[0].value) q.set('lines', linesSel.value);
      else q.delete('lines');
      // The server refuses a value longer than maxlength in bytes (UTF-8),
      // so such a filter stays out of the address.
      if (qInp.value && new TextEncoder().encode(qInp.value).length <= qInp.maxLength) q.set('q', qInp.value);
      else q.delete('q');
      if (errChk.checked) q.set('level', 'warn');
      else q.delete('level');
      history.replaceState(history.state, '', u);
    }

    // matches tells whether the filter shows a line. Notices always show.
    function matches(el) {
      if (!el.classList.contains('logs-line')) return true;
      if (errChk.checked && !el.classList.contains('logs-err') && !el.classList.contains('logs-warn')) return false;
      const q = qInp.value.trim().toLowerCase();
      return q === '' || el.textContent.toLowerCase().includes(q);
    }

    function filtering() { return errChk.checked || qInp.value.trim() !== ''; }

    function showCount() {
      const lines = total === 1 ? ' line' : ' lines';
      countEl.textContent = filtering() ? shown + ' of ' + total + lines : total + lines;
    }

    function addLine(text, cls) {
      const el = document.createElement('span');
      el.className = cls;
      el.textContent = text + '\n';
      el.hidden = !matches(el);
      box.append(el);
      n++;
      if (cls !== 'logs-notice') { total++; if (!el.hidden) shown++; }
      while (n > MAX_LINES) {
        const old = box.firstElementChild;
        if (old.classList.contains('logs-line')) { total--; if (!old.hidden) shown--; }
        old.remove();
        n--;
      }
    }

    // refilter applies the filter to every line, and says how many show.
    function refilter() {
      shown = 0;
      for (const el of box.children) {
        el.hidden = !matches(el);
        if (!el.hidden && el.classList.contains('logs-line')) shown++;
      }
      showCount();
      keepInAddress();
      clearTimeout(saidTimer);
      saidTimer = setTimeout(() => {
        said.textContent = filtering() ? 'Showing ' + shown + ' of ' + total + ' lines.' : 'Showing all ' + total + ' lines.';
      }, 400);
    }

    // shownText is the text of the log lines that the filter shows.
    function shownText() {
      let text = '';
      for (const el of box.querySelectorAll('.logs-line:not([hidden])')) text += el.textContent;
      return text;
    }

    function fileName() {
      let name = nodeSel.value + suffix + '-' + sourceSel.value;
      if (sourceSel.value === 'journal' && unitSel.value) name += '-' + unitSel.value;
      return name + '.log';
    }

    function flash(btn, text) {
      const old = btn.textContent;
      btn.textContent = text;
      setTimeout(() => { btn.textContent = old; }, 1500);
    }

    function flush() {
      for (const [text, cls] of backlog) addLine(text, cls);
      backlog = [];
      showCount();
      if (following) box.scrollTop = box.scrollHeight;
    }

    function push(text, cls) {
      if (paused) {
        backlog.push([text, cls]);
        if (backlog.length > MAX_LINES) backlog.shift();
        return;
      }
      backlog.push([text, cls]);
      if (backlog.length === 1) requestAnimationFrame(flush);
    }

    function lineClass(text) {
      if (/\b(error|fail(ed|ure)?|fatal|panic)\b/i.test(text)) return 'logs-line logs-err';
      if (/\bwarn(ing)?\b/i.test(text)) return 'logs-line logs-warn';
      return 'logs-line';
    }

    function stop() {
      if (es) { es.close(); es = null; }
    }

    function start() {
      stop();
      const q = new URLSearchParams({ node: nodeSel.value, source: sourceSel.value, lines: linesSel.value });
      if (sourceSel.value === 'journal' && unitSel.value) q.set('unit', unitSel.value);
      es = new EventSource('/logs/' + encodeURIComponent(realm) + '/' + encodeURIComponent(cluster) + '?' + q);
      const mine = es;
      setState('Connecting…');
      mine.onopen = () => setState(paused ? 'Paused' : 'Following');
      mine.addEventListener('line', ev => push(ev.data, lineClass(ev.data)));
      mine.addEventListener('notice', ev => push(ev.data, 'logs-notice'));
      mine.addEventListener('end', () => { mine.close(); setState('The log ended'); });
      // The browser would reconnect and send the first lines again.
      mine.onerror = () => { mine.close(); if (es === mine) { es = null; setState('Not connected. Change a choice to connect again.'); } };
    }

    function clear() {
      box.replaceChildren();
      backlog = [];
      n = total = shown = 0;
      showCount();
    }

    function reselect() {
      clear();
      showCommand();
      keepInAddress();
      start();
    }

    function setPaused(p) {
      paused = p;
      followBtn.classList.toggle('on', !p);
      pauseBtn.classList.toggle('on', p);
      followBtn.setAttribute('aria-pressed', String(!p));
      pauseBtn.setAttribute('aria-pressed', String(p));
      if (es) setState(p ? 'Paused' : 'Following');
      if (!p) flush();
    }

    nodeSel.addEventListener('change', () => { fillUnits(); reselect(); });
    sourceSel.addEventListener('change', () => { showUnit(); reselect(); });
    unitSel.addEventListener('change', reselect);
    linesSel.addEventListener('change', reselect);
    qInp.addEventListener('input', refilter);
    errChk.addEventListener('change', refilter);
    followBtn.addEventListener('click', () => { following = true; setPaused(false); });
    pauseBtn.addEventListener('click', () => setPaused(true));
    $('[data-logs-clear]').addEventListener('click', clear);
    $('[data-logs-copy]').addEventListener('click', ev => {
      if (navigator.clipboard) navigator.clipboard.writeText(cmd.textContent).then(() => {
        ev.target.textContent = 'Copied';
        setTimeout(() => { ev.target.textContent = 'Copy'; }, 1500);
      }, () => {});
    });
    $('[data-logs-copy-lines]').addEventListener('click', ev => {
      const text = shownText();
      if (!text) { said.textContent = 'No lines to copy.'; return; }
      if (navigator.clipboard) navigator.clipboard.writeText(text).then(() => {
        flash(ev.target, 'Copied');
        said.textContent = 'Copied the lines shown.';
      }, () => { said.textContent = 'Could not copy. Use Download.'; });
    });
    $('[data-logs-download]').addEventListener('click', ev => {
      const text = shownText();
      if (!text) { said.textContent = 'No lines to download.'; return; }
      const a = document.createElement('a');
      a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
      a.download = fileName();
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
      flash(ev.target, 'Saved');
    });
    // Scrolling up stops the pinning; reaching the bottom brings it back.
    box.addEventListener('scroll', () => {
      following = box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
    });
    window.addEventListener('pagehide', stop);

    // The tab can be swapped out of the page: stop when the root leaves.
    new MutationObserver(() => { if (!root.isConnected) stop(); })
      .observe(document.body, { childList: true, subtree: true });

    // The server drew the choices of the address.
    showUnit();
    showCommand();
    showCount();
    start();
  }

  function scan(node) {
    for (const root of node.querySelectorAll('[data-logs]:not([data-logs-ready])')) {
      root.dataset.logsReady = '1';
      init(root);
    }
  }

  // A live update of the page morphs the tab in again: the panel that this
  // script runs stays as it is, with its stream and its lines.
  if (window.Idiomorph) {
    Idiomorph.defaults.callbacks.beforeNodeMorphed = old =>
      !(old instanceof Element && old.hasAttribute('data-logs-ready'));
  }

  document.addEventListener('DOMContentLoaded', () => scan(document));
  // htmx swaps in a tab body after the page has loaded.
  document.addEventListener('htmx:afterSwap', ev => scan(ev.target));
})();
