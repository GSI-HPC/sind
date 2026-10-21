// SPDX-License-Identifier: LGPL-3.0-or-later

// The Logs tab of the cluster page: one EventSource on /logs/{realm}/{cluster}
// that appends each line to a <pre> as text. It follows (keeps the scroll at
// the bottom) until the reader scrolls up or presses Pause, and keeps the
// last 2,000 lines. The stream closes when the tab goes away.
(() => {
  'use strict';

  const MAX_LINES = 2000;

  function init(root) {
    const $ = sel => root.querySelector(sel);
    const nodeSel = $('[data-logs-node]');
    const sourceSel = $('[data-logs-source]');
    const unitFld = $('[data-logs-unit-fld]');
    const unitSel = $('[data-logs-unit]');
    const followBtn = $('[data-logs-follow]');
    const pauseBtn = $('[data-logs-pause]');
    const box = $('[data-logs-box]');
    const state = $('[data-logs-state]');
    const cmd = $('[data-logs-cmd]');
    const realm = root.dataset.realm;
    const cluster = root.dataset.cluster;
    const prefix = root.dataset.cmdPrefix;
    let es = null;
    let following = true;
    let paused = false;
    let backlog = [];
    let n = 0;

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
      if (units.includes(old)) unitSel.value = old;
      else unitSel.value = units.length ? units[0] : '';
      unitFld.hidden = sourceSel.value !== 'journal';
    }

    function showCommand() {
      const unit = sourceSel.value === 'journal' ? unitSel.value : '';
      cmd.textContent = prefix + nodeSel.value + '.' + cluster + (unit ? ' ' + unit : '') + ' -f -n 200';
    }

    function addLine(text, cls) {
      const el = document.createElement('span');
      el.className = cls;
      el.textContent = text + '\n';
      box.append(el);
      n++;
      while (n > MAX_LINES) { box.firstElementChild.remove(); n--; }
    }

    function flush() {
      for (const [text, cls] of backlog) addLine(text, cls);
      backlog = [];
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
      const q = new URLSearchParams({ node: nodeSel.value, source: sourceSel.value });
      if (sourceSel.value === 'journal' && unitSel.value) q.set('unit', unitSel.value);
      es = new EventSource('/logs/' + encodeURIComponent(realm) + '/' + encodeURIComponent(cluster) + '?' + q);
      const mine = es;
      setState('Connecting…');
      mine.onopen = () => setState(paused ? 'Paused' : 'Following');
      mine.addEventListener('line', ev => push(ev.data, lineClass(ev.data)));
      mine.addEventListener('notice', ev => push(ev.data, 'logs-notice'));
      mine.addEventListener('end', () => { mine.close(); setState('The log ended'); });
      // The browser would reconnect and send the last 200 lines again.
      mine.onerror = () => { mine.close(); if (es === mine) { es = null; setState('Not connected. Change a choice to connect again.'); } };
    }

    function clear() {
      box.replaceChildren();
      backlog = [];
      n = 0;
    }

    function reselect() {
      clear();
      showCommand();
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
    sourceSel.addEventListener('change', () => { fillUnits(); reselect(); });
    unitSel.addEventListener('change', reselect);
    followBtn.addEventListener('click', () => { following = true; setPaused(false); });
    pauseBtn.addEventListener('click', () => setPaused(true));
    $('[data-logs-clear]').addEventListener('click', clear);
    $('[data-logs-copy]').addEventListener('click', ev => {
      if (navigator.clipboard) navigator.clipboard.writeText(cmd.textContent).then(() => {
        ev.target.textContent = 'Copied';
        setTimeout(() => { ev.target.textContent = 'Copy'; }, 1500);
      }, () => {});
    });
    // Scrolling up stops the pinning; reaching the bottom brings it back.
    box.addEventListener('scroll', () => {
      following = box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
    });
    window.addEventListener('pagehide', stop);

    // The tab can be swapped out of the page: stop when the root leaves.
    new MutationObserver(() => { if (!root.isConnected) stop(); })
      .observe(document.body, { childList: true, subtree: true });

    fillUnits();
    showCommand();
    start();
  }

  function scan(node) {
    for (const root of node.querySelectorAll('[data-logs]:not([data-logs-ready])')) {
      root.dataset.logsReady = '1';
      init(root);
    }
  }

  document.addEventListener('DOMContentLoaded', () => scan(document));
  // htmx swaps in a tab body after the page has loaded.
  document.addEventListener('htmx:afterSwap', ev => scan(ev.target));
})();
