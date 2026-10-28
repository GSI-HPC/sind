// SPDX-License-Identifier: LGPL-3.0-or-later

// Script of the progress display. The server draws every page and fragment
// but the summary and the timeline of the run page, which gantt.js draws;
// this file does eight small things that need the browser: it counts the
// clocks of runs that go on (an element with data-since, the start in
// milliseconds since 1970), it sizes the bars finer than the whole percent
// of their classes, it puts the progress of the run in the title of the
// tab, it announces when a command starts or ends, it opens and closes the
// list of the task dock, it keeps and closes the question of Stop, it
// opens the run of the page of a task once there is one, and it hides the
// rows of "Needs you" that are fixed, the cards of a fix and the strip of
// the dock (data-hide-at), shows what waits for them (data-show-at), and
// counts down their seconds (data-until). It evaluates no text and needs
// no inline script.
(() => {
  'use strict';

  // clock is the time of the server, in milliseconds since 1970: the
  // browser's, moved by how far the server's was off when it drew the page
  // (data-now of the body). The clocks of runs then agree with the times
  // that the server writes, also when its clock stands, as in the still
  // demo.
  const skew = (() => {
    const now = Number(document.body && document.body.dataset.now);
    return now > 0 ? now - Date.now() : 0;
  })();
  function clock() { return Date.now() + skew; }

  // dur writes a duration as the server's runDur does: 3.4s, 42s, 5m07s, 2h03m.
  function dur(ms) {
    const d = Math.max(ms, 0);
    const pad = n => (n < 10 ? '0' : '') + n;
    if (d < 10000) {
      const t = Math.floor(d / 100);
      return Math.floor(t / 10) + '.' + (t % 10) + 's';
    }
    if (d < 60000) return Math.floor(d / 1000) + 's';
    if (d < 3600000) return Math.floor(d / 60000) + 'm' + pad(Math.floor(d % 60000 / 1000)) + 's';
    return Math.floor(d / 3600000) + 'h' + pad(Math.floor(d % 3600000 / 60000)) + 'm';
  }

  // The list of the dock, Commands, opens from the button of the top bar
  // and from the strip: a popover below the button, a sheet on a phone. It
  // is open while #cmds has the class open, which the live updates of the
  // list leave alone, and the buttons say so (aria-expanded). Escape and a
  // click outside close it; Escape gives the focus back to the button that
  // opened it.
  let opener = null;
  function cmds() { return document.getElementById('cmds'); }
  function drawDock() {
    const box = cmds();
    if (!box) return;
    if (box.classList.contains('open') && !document.getElementById('cmds-pop')) box.classList.remove('open');
    const open = String(box.classList.contains('open'));
    document.querySelectorAll('[data-dock-toggle]').forEach(b => {
      if (b.getAttribute('aria-expanded') !== open) b.setAttribute('aria-expanded', open);
    });
  }
  function openDock(btn) {
    const box = cmds();
    const pop = document.getElementById('cmds-pop');
    if (!box || !pop) return;
    opener = btn;
    box.classList.add('open');
    drawDock();
    pop.focus({ preventScroll: true });
  }
  function closeDock(refocus) {
    const box = cmds();
    if (!box || !box.classList.contains('open')) return;
    box.classList.remove('open');
    drawDock();
    const back = opener && opener.isConnected && opener.getClientRects().length ? opener : document.querySelector('.cmds-btn');
    if (refocus && back) back.focus();
    opener = null;
  }
  // Stop asks in the page (template run-stop): Keep running and Escape
  // close the question, and so does a click elsewhere. A live update
  // leaves it open, but keepStops closes it once its button stops another
  // task, as when the strip shows another command.
  const stopOf = new WeakMap();
  document.addEventListener('toggle', ev => {
    const d = ev.target;
    if (d instanceof HTMLDetailsElement && d.matches('.stopc') && d.open) stopOf.set(d, d.dataset.stopTask);
  }, true);
  function keepStops() {
    document.querySelectorAll('details.stopc[open]').forEach(d => {
      if (stopOf.has(d) && stopOf.get(d) !== d.dataset.stopTask) d.open = false;
    });
  }
  function closeStops(except) {
    document.querySelectorAll('details.stopc[open]').forEach(d => { if (d !== except) d.open = false; });
  }
  function closeStop(d) {
    d.open = false;
    d.querySelector('summary').focus();
  }
  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element)) return;
    const keep = ev.target.closest('[data-stop-keep]');
    if (keep) closeStop(keep.closest('details.stopc'));
    else closeStops(ev.target.closest('details.stopc'));
  });
  document.addEventListener('keydown', ev => {
    const d = ev.key === 'Escape' && ev.target instanceof Element && ev.target.closest('details.stopc[open]');
    if (d) closeStop(d);
  });

  document.addEventListener('click', ev => {
    if (!(ev.target instanceof Element)) return;
    const btn = ev.target.closest('[data-dock-toggle]');
    const box = cmds();
    const open = box && box.classList.contains('open');
    if (btn) {
      if (open) closeDock(true); else openDock(btn);
      return;
    }
    if (open && (ev.target.closest('[data-dock-close]') || !ev.target.closest('#cmds-pop'))) closeDock(false);
  });
  document.addEventListener('keydown', ev => {
    if (ev.key !== 'Escape' || !(ev.target instanceof Element) || ev.target.closest('details.stopc[open]')) return;
    const box = cmds();
    if (!box || !box.classList.contains('open') || !(box.contains(ev.target) || ev.target.closest('[data-dock-toggle]'))) return;
    ev.preventDefault();
    closeDock(true);
  });
  // The list closes when the focus leaves it for another control, as Tab
  // past its last one does.
  document.addEventListener('focusout', ev => {
    const box = cmds();
    const to = ev.relatedTarget;
    if (!box || !box.classList.contains('open') || !(to instanceof Element)) return;
    if (!box.contains(to) && !to.closest('[data-dock-toggle]')) closeDock(false);
  });
  // A × or Clear ended takes its button away with what it clears, and a
  // command that ends takes its Stop and Show progress: the focus then goes
  // to the list, or with no list left, to its button or the jump box
  // (app.js leaves the focus of #cmds, data-own-focus, to this). After a
  // clear, the page says what went.
  let inDock = null;
  let cleared = '';
  document.addEventListener('focusin', ev => {
    const box = cmds();
    inDock = box && ev.target instanceof Element && box.contains(ev.target) ? ev.target : null;
  });
  document.addEventListener('submit', ev => {
    const f = ev.target;
    const to = f instanceof HTMLFormElement && f.closest('#cmds-pop') ? f.getAttribute('action') || '' : '';
    if (/\/tasks\/clear$/.test(to)) cleared = 'Cleared the commands that ended.';
    else if (/\/dismiss$/.test(to)) cleared = 'Cleared.';
  }, true);
  function refocus() {
    const box = cmds();
    if (!box || !inDock || inDock.isConnected || (document.activeElement && document.activeElement !== document.body)) return;
    inDock = null;
    const pop = box.classList.contains('open') && document.getElementById('cmds-pop');
    [pop, document.querySelector('.cmds-btn'), document.querySelector('header .jump'), document.getElementById('main')].some(el => {
      if (el) el.focus({ preventScroll: true });
      return el && document.activeElement === el;
    });
    if (cleared) document.dispatchEvent(new CustomEvent('sind:say', { detail: cleared }));
    cleared = '';
  }

  // The page of a task opens its run once the event log holds it, in its
  // place in the history (data-run-open), so that Back leaves the run in one
  // step.
  let leaving = false;
  function openRun() {
    const el = document.querySelector('[data-run-open]');
    const to = el && el.dataset.runOpen;
    if (leaving || !to || !to.startsWith('/runs/')) return;
    leaving = true;
    location.replace(to);
  }

  // set gives a property of the style of el a value, if it has another.
  function set(el, prop, value) {
    if (el.style[prop] !== value) el.style[prop] = value;
  }

  // place sizes each bar with a data-fill, in hundredths of a percent.
  function place() {
    document.querySelectorAll('[data-fill]').forEach(el => set(el, 'width', Number(el.dataset.fill) / 100 + '%'));
  }

  // recount says again how many rows of "Needs you" are open and fixed,
  // once a row that is fixed hid, as the server's NeedsText does; with no
  // row left, the panel shows only the commands that run, or nothing.
  function recount() {
    document.querySelectorAll('[data-needs]').forEach(sec => {
      const head = sec.querySelector('[data-needs-count]');
      if (!head) return;
      const open = Number(head.dataset.needsOpen) || 0;
      const fixed = sec.querySelectorAll('[data-needs-fixed]:not(.gone)').length;
      const text = open + ' open' + (fixed ? ' · ' + fixed + ' fixed' : '');
      if (head.textContent !== text) head.textContent = text;
      sec.classList.toggle('pg-needs-none', open === 0 && fixed === 0);
    });
  }

  function tick() {
    const now = clock();
    place();
    document.querySelectorAll('[data-hide-at]').forEach(el => {
      if (now >= Number(el.dataset.hideAt) && !el.classList.contains('gone')) el.classList.add('gone');
    });
    document.querySelectorAll('[data-show-at]').forEach(el => {
      if (now >= Number(el.dataset.showAt) && !el.classList.contains('shown')) el.classList.add('shown');
    });
    // The seconds that a card still shows, as "12 s".
    document.querySelectorAll('[data-until]').forEach(el => {
      const text = Math.max(0, Math.ceil((Number(el.dataset.until) - now) / 1000)) + ' s';
      if (el.textContent !== text) el.textContent = text;
    });
    recount();
    document.querySelectorAll('[data-since]').forEach(el => {
      const since = Number(el.dataset.since);
      if (!(since > 0)) return;
      const text = dur(now - since);
      if (el.textContent === text) return;
      // Changing the text node leaves the observers below alone.
      if (el.firstChild && el.firstChild.nodeType === Node.TEXT_NODE) el.firstChild.nodeValue = text;
      else el.textContent = text;
    });
  }

  // The width that place gave a bar stays until its next tick, so that the
  // bar does not jump back to its class. The question of Stop stays open
  // (keepStops).
  Idiomorph.defaults.callbacks.beforeAttributeUpdated = (name, el) => {
    if (name === 'open' && el instanceof Element && el.matches('details.stopc')) return false;
    return name !== 'style' || !(el instanceof HTMLElement && 'fill' in el.dataset);
  };

  function title() {
    const el = document.querySelector('[data-doc-title]');
    if (el && document.title !== el.dataset.docTitle) document.title = el.dataset.docTitle;
  }

  // The commands that were announced, by their data-say-key, with the
  // state that was: kept for the tab, so that the next page announces a
  // command that ended meanwhile, and only once.
  const saidKey = 'sind-ui-said';
  let said = {};
  try { said = JSON.parse(sessionStorage.getItem(saidKey) || '{}') || {}; } catch (e) { said = {}; }
  let firstScan = true;

  // announce puts one sentence in the live region #say for each command
  // whose state changed: started, stopping, done, failed or stopped. The
  // clocks and the progress lines are outside any live region, so they
  // change in silence. A command that the page shows when it opens is
  // announced only if it started a moment ago, as after a dialog's Run.
  function announce() {
    const region = document.getElementById('say');
    if (!region) return;
    const out = [];
    const now = clock();
    document.querySelectorAll('[data-say-key]').forEach(el => {
      const key = el.dataset.sayKey;
      const state = el.dataset.sayState;
      const prev = said[key];
      if (prev === state) return;
      said[key] = state;
      if (prev === undefined && firstScan) {
        const since = Number(el.dataset.saySince);
        if (!(state === 'running' && since > 0 && now - since < 10000)) return;
      }
      if (el.dataset.say && !out.includes(el.dataset.say)) out.push(el.dataset.say);
    });
    firstScan = false;
    const keys = Object.keys(said);
    if (keys.length > 200) {
      // Forget the oldest.
      keys.slice(0, keys.length - 100).forEach(k => { delete said[k]; });
    }
    try { sessionStorage.setItem(saidKey, JSON.stringify(said)); } catch (e) { /* private window */ }
    // app.js tells them, with what the same live update changed.
    if (out.length) document.dispatchEvent(new CustomEvent('sind:say', { detail: out.join(' ') }));
  }

  function update() {
    title();
    tick();
    announce();
    openRun();
  }

  document.addEventListener('DOMContentLoaded', () => {
    const main = document.getElementById('main');
    // A morph changes text and attributes in place, so the observers
    // watch those too: a clock the server drew is counted again before the
    // browser paints it.
    const what = { childList: true, characterData: true, attributes: true, attributeFilter: ['data-since', 'data-doc-title', 'data-say-state', 'data-stop-task'], subtree: true };
    if (main) new MutationObserver(() => { update(); keepStops(); }).observe(main, what);
    for (const id of ['tasks', 'cmds']) {
      const dock = document.getElementById(id);
      if (dock) new MutationObserver(() => { tick(); announce(); drawDock(); keepStops(); refocus(); }).observe(dock, what);
    }
    drawDock();
    update();
    // Tenths of a second, as the terminal draws them.
    setInterval(tick, 100);
  });
})();
