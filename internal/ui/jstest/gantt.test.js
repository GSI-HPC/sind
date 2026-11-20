// SPDX-License-Identifier: LGPL-3.0-or-later

// Tests of web/static/js/gantt.js (make test-js): what the summary and
// the timeline of the run page say for the data of a run. The script
// draws into the document of dom.js, one frame for each call of draw, and
// the tests read the texts and the classes of the elements. The data has
// the fields that runGantt of the server writes.
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const { Element, install } = require('./dom.js');

const page = install();
require('../web/static/js/gantt.js');
// The script asks for its first frame when it loads.
const frame = page.frames[0];

const origin = 1700000000000;
let frameAt = 0;

// open gives the document a new run page without a run, and returns the
// elements that the script draws into. The script starts anew on a new
// page: no row, no open step and no zoom of the page before.
function open() {
  const add = (parent, tag, attrs) => {
    const e = new Element(tag);
    Object.entries(attrs).forEach(([k, v]) => e.setAttribute(k, v));
    parent.appendChild(e);
    return e;
  };
  page.body.replaceChildren();
  page.width = 1200;
  const g = { conn: add(page.body, 'span', { id: 'conn' }), src: add(page.body, 'span', { 'data-gantt': '{}' }) };
  g.g = add(page.body, 'div', { class: 'gantt', 'data-g': 'gantt' });
  for (const n of ['clock', 'hl', 'counts', 'note', 'eta', 'whole', 'ticks', 'over', 'cur']) g[n] = add(g.g, 'div', { 'data-g': n });
  g.body = add(g.g, 'div', { class: 'g-body', 'data-g': 'body' });
  return g;
}

// draw gives the page the data of a run and draws one frame, at seconds
// after the run began on the clock of the server.
function draw(g, run, seconds) {
  g.src.setAttribute('data-gantt', JSON.stringify({ origin, state: 'running', ...run }));
  globalThis.sindClock = { now: () => origin + seconds * 1000 };
  frame(frameAt += 1000);
}

const texts = e => e.children.map(c => c.textContent);
// leaves are the texts of the elements below e that have a text of their own.
function leaves(e) {
  if (!e.children.length) return e.textContent ? [e.textContent] : [];
  return e.children.flatMap(leaves);
}
// rows are the rows of the timeline: their key, class, label and result.
function rows(g) {
  return g.body.children.filter(e => e.matches('.g-row')).map(e => {
    const [lbl, trk, res] = e.children;
    return { key: e.dataset.key, cls: e.className, lbl: leaves(lbl), res: texts(res), segs: trk.children.map(s => s.className) };
  });
}
const row = (g, key) => rows(g).find(r => r.key === key);
// ticks are the labels of the axis, without the label of the clock.
const ticks = g => g.ticks.children.filter(e => e.matches('.tk')).map(e => e.textContent);
const summary = g => ({ clock: g.clock.textContent, hl: g.hl.textContent, counts: texts(g.counts), note: g.note.textContent.trim(), noteClass: g.note.className.trim(), eta: g.eta.textContent });

// The spans of a run, with times in seconds.
const ms = v => (v == null ? undefined : v * 1000);
const span = (k, i, p, n, r, e, more) => ({ i, p, k, n, q: ms(more && more.q != null ? more.q : r) || 0, r: ms(r), e: ms(e), c: e == null ? '' : 'ended', ...more, to: ms(more && more.to) });
const command = (n, e) => span('command', 'c', '', n, 0, e);
const step = (i, n, r, e, more) => span('step', i, 'c', n, r, e, more);
const target = (i, p, node, r, e, more) => span('target', i, p, node, r, e, { node, ...more });
const call = (i, p, n, r, e, more) => span('call', i, p, n, r, e, more);
const wait = (i, p, n, r, e, more) => span('wait', i, p, n, r, e, more);
const pull = (i, p, image, r, e, more) => span('target', i, p, image, r, e, { u: 'bytes', ...more });
const samples = list => list.map(([t, a, z]) => [t * 1000, a, z]);

const MB = 1e6, GB = 1e9;

// A create of a cluster with 2 nodes: the network is there, the
// controller is ready, and worker-0 waits for munge.
function createRun(more) {
  return {
    last: 12000,
    spans: [
      command('create cluster'),
      step('net', 'network', 0.25, 1),
      step('nodes', 'nodes', 1, null, { tot: 2 }),
      target('t0', 'nodes', 'controller', 1, 6, { role: 'controller' }),
      target('t1', 'nodes', 'worker-0', 1, null, { role: 'worker' }),
      call('k1', 't1', 'docker create', 1, 2),
      call('k2', 't1', 'docker start', 2, 3),
      wait('w1', 't1', 'ready', 10, null, { m: 'munge', to: 300 }),
    ],
    ...more,
  };
}

test('a run that goes on: the step that runs, the counts and the wait of a node', () => {
  const g = open();
  draw(g, createRun(), 12.5);
  assert.deepEqual(summary(g), {
    clock: '12s',
    hl: 'nodes · 1 of 2 done',
    counts: ['1 of 2 nodes ready', '1 of 2 steps ok'],
    note: 'worker-0 waits for munge: 2s of its 5m00s limit.',
    noteClass: 'note',
    eta: '',
  });
  // The step that this page saw run shows its targets.
  assert.deepEqual(rows(g).map(r => r.key), ['net', 'nodes', 't0', 't1']);
  assert.deepEqual(row(g, 'net'), { key: 'net', cls: 'g-row srow ok', lbl: ['network'], res: ['0.7s', ''], segs: ['bar ok'] });
  assert.deepEqual(row(g, 'nodes').lbl, ['nodes', '1/2']);
  assert.deepEqual(row(g, 'nodes').res, ['1/2', 'worker-0 waits for munge']);
  assert.deepEqual(row(g, 't0'), { key: 't0', cls: 'g-row trow ok', lbl: ['controller'], res: ['5.0s'], segs: ['bar ok'] });
  assert.deepEqual(row(g, 't1').lbl, ['worker-0', 'worker']);
  assert.deepEqual(row(g, 't1').res, ['waits for munge · 2.5s of 5m00s']);
  // Its calls that ended and the wait that goes on, in the state of the node.
  assert.deepEqual(row(g, 't1').segs, ['phs c0 running', 'phs c1 running', 'phs c2 pw live running']);
});

test('the note warns at half of the limit of a wait, and more at 80 %', () => {
  const g = open();
  const run = at => createRun({ last: at * 1000, spans: createRun().spans.map(s => (s.i === 'w1' ? { ...s, to: 20000 } : s)) });
  draw(g, run(19), 19);
  assert.equal(g.note.className, 'note ');
  draw(g, run(20), 20);
  assert.equal(g.note.className, 'note warn');
  assert.equal(g.note.textContent, 'worker-0 waits for munge: 10s of its 20s limit.');
  draw(g, run(26), 26);
  assert.equal(g.note.className, 'note bad');
});

test('the note says when no event came for more than 4 seconds, and the call that runs', () => {
  const g = open();
  const spans = [command('create cluster'), step('nodes', 'nodes', 1), target('t1', 'nodes', 'worker-0', 1), call('k1', 't1', 'docker create', 1)];
  draw(g, { last: 2000, spans }, 6);
  assert.equal(g.note.textContent.trim(), '');
  draw(g, { last: 2000, spans }, 8.5);
  assert.equal(g.note.textContent, 'No new event for 6s. worker-0: docker create runs, and reports no progress.');
  assert.deepEqual(row(g, 't1').res, ['starts its container · 7.5s · no progress reported']);
});

test('a run that waits for the realm lock has no counts', () => {
  const g = open();
  draw(g, { last: 3000, spans: [command('create cluster'), wait('l', 'c', 'realm lock', 0.5)] }, 3.5);
  assert.deepEqual(summary(g), { clock: '3s', hl: 'starting', counts: [], note: 'Waits for the realm lock for 3s.', noteClass: 'note', eta: '' });
  // A step that has no node yet counts as a step only.
  draw(g, { last: 3000, spans: [command('create cluster'), step('nodes', 'nodes', 3)] }, 3.5);
  assert.deepEqual(texts(g.counts), ['0 of 1 step ok']);
});

// A pull of one image of 1 GB: the first byte after 2 seconds, 200 MB
// after 3 seconds, and 300 MB more in the last second.
function pullRun(more, image) {
  return {
    last: 4000,
    spans: [
      command('create cluster'),
      step('pull', 'pull images', 1, null),
      pull('p0', 'pull', 'ghcr.io/gsi-hpc/sind-node:25.11', 1, null, {
        z: GB, w: samples([[1, 0, GB], [2, 100 * MB, GB], [3, 200 * MB, GB], [4, 500 * MB, GB]]), ...image,
      }),
    ],
    ...more,
  };
}

test('a pull: the bytes, the rate over the last second and the time left', () => {
  const g = open();
  draw(g, pullRun(), 4.25);
  assert.deepEqual(summary(g), {
    clock: '4s',
    hl: 'pull images · 0 of 1 done · 500 MB at 300 MB/s',
    counts: ['0 of 1 image pulled', '0 of 1 step ok'],
    note: '',
    noteClass: 'note',
    // 500 MB to go at 300 MB/s, and no earlier run to compare with.
    eta: 'more than 1s left',
  });
  assert.deepEqual(row(g, 'pull').res, ['0/1 · 500 MB', '300 MB/s']);
  // The row leaves the registry of sind out of the name of the image.
  assert.deepEqual(row(g, 'p0'), {
    key: 'p0', cls: 'g-row trow running pull last', lbl: ['sind-node:25.11'],
    res: ['downloading', '500 MB / 1.0 GB', '300 MB/s', '3.2s'], segs: ['wt', 'bar '],
  });
});

test('the time left: from the earlier runs, and "more than" for a size that is a guess', () => {
  const g = open();
  draw(g, pullRun({ usualN: 3, usualRun: 20000 }), 4.25);
  assert.equal(g.eta.textContent, 'about 15s left');
  draw(g, pullRun({ usualN: 3, usualRun: 20000 }, { m: 'size estimated' }), 4.25);
  assert.equal(g.eta.textContent, 'more than 15s left');
  assert.equal(row(g, 'p0').res[1], '500 MB / ≈1.0 GB');
  // The pull needs longer than the earlier runs took in all.
  draw(g, pullRun({ usualN: 3, usualRun: 5000 }), 4.25);
  assert.equal(g.eta.textContent, 'about 1s left');
  draw(g, pullRun({ usualN: 3, usualRun: 4000 }), 4.25);
  assert.equal(g.eta.textContent, 'longer than usual (4s)');
});

test('a pull without a new sample for more than 2 seconds has no rate', () => {
  const g = open();
  draw(g, pullRun({ last: 7000 }), 7);
  assert.equal(g.hl.textContent, 'pull images · 0 of 1 done · 500 MB');
  assert.deepEqual(row(g, 'pull').res, ['0/1 · 500 MB', 'no bytes for 3s']);
  assert.deepEqual(row(g, 'p0').res, ['downloading', '500 MB / 1.0 GB', 'none 3s', '6.0s']);
  assert.equal(g.eta.textContent, '');
});

test('a pull that has no byte yet waits for its first byte', () => {
  const g = open();
  draw(g, pullRun({ last: 2000 }, { w: samples([[1, 0, 0], [1.5, 0, GB]]) }), 2);
  assert.equal(g.note.textContent, 'sind-node:25.11 waits for the first byte.');
  assert.deepEqual(row(g, 'p0').res, ['waiting', '', '', '1.0s']);
  assert.deepEqual(row(g, 'p0').segs, ['wt']);
});

test('a pull that extracts: the bar ends where the amount last grew', () => {
  const g = open();
  draw(g, pullRun({ last: 6000 }, { m: 'extracting', w: samples([[1, 0, GB], [2, 500 * MB, GB], [3, GB, GB], [6, GB, GB]]) }), 6);
  assert.deepEqual(row(g, 'p0').res.slice(0, 2), ['extracting', '1.0 GB / 1.0 GB']);
  assert.deepEqual(row(g, 'p0').segs, ['wt', 'bar ', 'up']);
});

test('a pull that ended shows the time to extract when its amount stood for more than 1.5 seconds', () => {
  const g = open();
  const w = samples([[1, 0, GB], [2, 500 * MB, GB], [3, GB, GB]]);
  // The page sees the step run, so the step shows its target.
  draw(g, pullRun({ last: 3000 }, { w }), 3);
  const ended = e => ({
    last: e * 1000,
    spans: [command('create cluster'), step('pull', 'pull images', 1, e), pull('p0', 'pull', 'ghcr.io/gsi-hpc/sind-node:25.11', 1, e, { a: GB, z: GB, w })],
  });
  draw(g, ended(4.75), 5);
  assert.deepEqual(row(g, 'p0').segs, ['wt', 'bar ok', 'up done']);
  assert.deepEqual(row(g, 'p0').res, ['done', '1.0 GB / 1.0 GB', '', '3.7s']);
  draw(g, ended(4.5), 5);
  assert.deepEqual(row(g, 'p0').segs, ['wt', 'bar ok']);
});

// A create that ended: 1.2 GB pulled, 2 nodes.
function endedRun(more) {
  return {
    last: 20000, end: 20000, state: 'ok', usualN: 3, usualRun: 18000, usual: { nodes: 9000 },
    spans: [
      command('create cluster', 20),
      step('pull', 'pull images', 0.5, 5),
      pull('p0', 'pull', 'ghcr.io/gsi-hpc/sind-node:25.11', 0.5, 5, { a: 1.2 * GB, z: 1.2 * GB }),
      step('nodes', 'nodes', 5, 19.5),
      target('t0', 'nodes', 'controller', 5, 12),
      target('t1', 'nodes', 'worker-0', 5, 19.5),
    ],
    ...more,
  };
}

test('a run that ended: how long it took, what it pulled, and the longest step', () => {
  const g = open();
  draw(g, endedRun(), 500);
  assert.deepEqual(summary(g), {
    clock: '20s',
    hl: 'done in 20s · 1.2 GB pulled',
    counts: ['1 of 1 image pulled', '2 of 2 nodes ready', '2 of 2 steps ok'],
    note: 'Earlier runs took 18s. The longest step was nodes (14s, usually 9.0s).',
    noteClass: 'note ok',
    eta: '',
  });
  assert.equal(g.clock.className, 'clock done ok');
  // A step that this page did not see run stays closed.
  assert.deepEqual(rows(g).map(r => r.key), ['pull', 'nodes']);
  assert.deepEqual(row(g, 'pull').res, ['1/1 · 4.5s', '1.2 GB · 267 MB/s']);
  assert.deepEqual(row(g, 'nodes').res, ['2/2 · 14s', 'usually 9.0s']);
  // The axis shows the whole run, and the label of the clock hides the
  // tick that it covers.
  assert.deepEqual(ticks(g), ['0s', '5s', '10s', '15s', '20s']);
  assert.deepEqual(g.ticks.children.map(e => e.className), ['tk', 'tk', 'tk', 'tk', 'tk gone', 'nowp ok']);
});

test('a run that failed: the node, its error, and the step that opens by itself', () => {
  const g = open();
  draw(g, endedRun({
    last: 31000, end: 31000, state: 'failed',
    spans: [
      command('create cluster', 31),
      step('nodes', 'nodes', 1, 31, { c: 'failed' }),
      target('t0', 'nodes', 'controller', 1, 12),
      target('t1', 'nodes', 'worker-0', 1, 30, { c: 'failed', ec: 'timeout', err: 'munge did not start' }),
      target('t2', 'nodes', 'worker-1', 1, 31, { c: 'canceled' }),
    ],
  }), 500);
  assert.deepEqual(summary(g), {
    clock: '31s',
    hl: 'failed in 31s',
    counts: ['1 of 3 nodes ready', '0 of 1 step ok', '1 timed out', '1 stopped'],
    note: 'worker-0 failed after 30s: munge did not start',
    noteClass: 'note bad',
    eta: '',
  });
  // The target that failed is the first row of the step.
  assert.deepEqual(rows(g).map(r => r.key), ['nodes', 't1', 't0', 't2']);
  assert.deepEqual(row(g, 't1').res, ['munge did not start']);
  assert.deepEqual(row(g, 't2').res, ['30s']);
  assert.equal(row(g, 't2').cls, 'g-row trow canceled last');
  assert.deepEqual(row(g, 'nodes').res, ['1/3 · 1 failed · 30s', 'failed']);
});

test('a run that was canceled is "stopped", and one without an end in the log says so', () => {
  const g = open();
  const spans = [
    command('delete cluster', 12),
    step('nodes', 'nodes', 1, 12, { c: 'canceled' }),
    target('t0', 'nodes', 'controller', 1, 12, { c: 'canceled' }),
    step('net', 'network', 2, 12, { c: 'canceled' }),
  ];
  draw(g, { last: 12000, end: 12000, state: 'canceled', status: 'canceled', spans }, 500);
  assert.equal(g.hl.textContent, 'stopped after 12s');
  // The count of what was stopped has the targets, not the steps.
  assert.deepEqual(texts(g.counts), ['0 of 1 node removed', '0 of 2 steps ok', '1 stopped']);
  assert.equal(g.note.textContent, 'The longest step was nodes (11s).');
  assert.equal(g.note.className, 'note warn');
  draw(g, { last: 12000, end: 12000, state: 'canceled', status: 'canceled', unfinished: true, spans }, 500);
  assert.equal(g.note.textContent, 'The longest step was nodes (11s).');
  draw(g, { last: 12000, end: 12000, state: 'canceled', unfinished: true, spans }, 500);
  assert.equal(g.note.textContent, 'The event log holds no end of this run; its last event came after 12s.');
});

test('a hidden span shows after 1 second, or when it failed', () => {
  const g = open();
  const spans = (e, c) => [command('power on'), step('s', 'check', 1, e, { h: true, c })];
  draw(g, { last: 1500, spans: spans() }, 1.75);
  assert.deepEqual(rows(g), []);
  assert.equal(g.body.textContent, 'No steps yet.');
  assert.equal(g.hl.textContent, 'starting');
  draw(g, { last: 2000, spans: spans() }, 2);
  assert.deepEqual(rows(g).map(r => r.key), ['s']);
  assert.equal(g.body.querySelector('.g-empty'), null);
  assert.equal(g.hl.textContent, 'check · 1s');

  const h = open();
  draw(h, { last: 1250, spans: spans(1.25, 'failed') }, 1.5);
  assert.deepEqual(rows(h).map(r => r.key), ['s']);
  draw(h, { last: 1250, spans: spans(1.25, 'ended') }, 1.5);
  assert.deepEqual(rows(h), []);
});

test('the axis of a run that goes on grows once the clock passes 88 % of it', () => {
  const g = open();
  const run = { last: 0, spans: [command('create cluster'), step('s', 'nodes', 0)] };
  draw(g, { ...run, last: 8000 }, 8);
  assert.deepEqual(ticks(g), ['0s', '2s', '4s', '6s', '8s', '10s']);
  draw(g, { ...run, last: 9000 }, 9);
  assert.deepEqual(ticks(g), ['0s', '2s', '4s', '6s', '8s', '10s', '12s', '14s']);
  draw(g, { ...run, last: 100000 }, 100);
  assert.deepEqual(ticks(g), ['0s', '15s', '30s', '45s', '1m', '1m15', '1m30', '1m45', '2m']);
  // Past the last horizon of 4 hours, the axis ends at the next hour.
  draw(g, { ...run, last: 13000000 }, 13000);
  assert.deepEqual(ticks(g), ['0s', '1h', '2h', '3h', '4h', '5h']);
  assert.equal(g.clock.textContent, '3h36m');
  // A narrow window has at most 4 parts.
  page.width = 700;
  draw(g, { ...run, last: 8000 }, 8);
  assert.deepEqual(ticks(g), ['0s', '5s', '10s']);
});

test('the clock stands while the live updates are gone, and at the last event of a suspended run', () => {
  const g = open();
  g.conn.textContent = 'Reconnecting';
  draw(g, createRun(), 12.5);
  draw(g, createRun(), 40);
  assert.equal(g.clock.textContent, '12s');
  assert.equal(g.note.className, 'note bad');
  assert.match(g.note.textContent, /^Live updates stopped/);
  g.conn.textContent = '';
  draw(g, createRun({ last: 40000 }), 40);
  assert.equal(g.clock.textContent, '40s');

  draw(g, createRun({ suspended: true }), 40);
  assert.equal(g.clock.textContent, '12s');
  assert.equal(g.note.textContent, 'Suspended: the command is stopped, and goes on once it is resumed.');
  assert.equal(g.eta.textContent, '');
});

test('a click on a step closes and opens its targets', () => {
  const g = open();
  draw(g, createRun(), 12.5);
  const nodes = () => g.body.children.find(e => e.dataset.key === 'nodes');
  assert.equal(nodes().getAttribute('aria-expanded'), 'true');
  page.dispatch('click', { target: nodes().children[0] });
  draw(g, createRun(), 12.5);
  assert.deepEqual(rows(g).map(r => r.key), ['net', 'nodes']);
  assert.equal(nodes().getAttribute('aria-expanded'), 'false');
  page.dispatch('click', { target: nodes().children[0] });
  draw(g, createRun(), 12.5);
  assert.deepEqual(rows(g).map(r => r.key), ['net', 'nodes', 't0', 't1']);
});

test('a click on the track of a step shows the step alone, and Escape the whole run again', () => {
  const g = open();
  draw(g, endedRun(), 500);
  assert.equal(g.whole.hidden, true);
  const track = () => g.body.children.find(e => e.dataset.key === 'pull').children[1];
  // The step ran from 0.5 s to 5 s: the axis adds a margin of 0.5 s.
  page.dispatch('click', { target: track() });
  draw(g, endedRun(), 500);
  assert.deepEqual(ticks(g), ['0s', '1s', '2s', '3s', '4s', '5s']);
  assert.equal(g.whole.hidden, false);
  page.dispatch('keydown', { key: 'Escape' });
  draw(g, endedRun(), 500);
  assert.deepEqual(ticks(g), ['0s', '5s', '10s', '15s', '20s']);
  assert.equal(g.whole.hidden, true);
  // A second click on the track, and a click on "whole run", do the same.
  page.dispatch('click', { target: track() });
  page.dispatch('click', { target: track() });
  draw(g, endedRun(), 500);
  assert.equal(g.whole.hidden, true);
  page.dispatch('click', { target: track() });
  page.dispatch('click', { target: g.whole });
  draw(g, endedRun(), 500);
  assert.equal(g.whole.hidden, true);
});

test('a step of more than 8 targets stays closed, and its strip shows at most 30 of them', () => {
  const g = open();
  const many = n => [command('create cluster'), step('nodes', 'nodes', 1), ...Array.from({ length: n }, (_, i) => target('t' + i, 'nodes', 'worker-' + i, 1))];
  draw(g, { last: 2000, spans: many(8) }, 2);
  assert.equal(rows(g).length, 9);
  const h = open();
  draw(h, { last: 2000, spans: many(31) }, 2);
  assert.deepEqual(rows(h).map(r => r.key), ['nodes']);
  assert.deepEqual(row(h, 'nodes').lbl, ['nodes', '0/31', '+1']);
  assert.deepEqual(row(h, 'nodes').res, ['0/31', '31 running']);
  const strip = h.body.querySelector('.strip');
  assert.equal(strip.children.length, 31);
  assert.equal(strip.children[0].title, 'worker-0: running');
});

test('a run that ended stands still: a frame without a change draws nothing', () => {
  const g = open();
  draw(g, endedRun(), 500);
  g.clock.textContent = 'not drawn again';
  draw(g, endedRun(), 500);
  assert.equal(g.clock.textContent, 'not drawn again');
  // New data of the run draws it again.
  draw(g, endedRun({ title: 'changed' }), 500);
  assert.equal(g.clock.textContent, '20s');
});

// The cases of ../testdata/runtexts.json are also the cases of a Go test
// (TestSharedRunTexts), which reads them with the functions of the server:
// the script and the server write the same text.
const shared = JSON.parse(fs.readFileSync(path.join(__dirname, '../testdata/runtexts.json'), 'utf8'));

test('a duration reads as the server writes it', () => {
  const g = open();
  // The result of a node that ended is the time from its start to its end.
  const took = (from, to) => {
    const spans = [command('create cluster'), step('nodes', 'nodes', 0), target('t0', 'nodes', 'controller', from / 1000, to / 1000), target('t1', 'nodes', 'worker-0', 0)];
    draw(g, { last: to, spans }, to / 1000 + 1);
    return row(g, 't0').res[0];
  };
  for (const c of shared.durations) assert.equal(took(c.from, c.to), c.text, 'from ' + c.from + ' to ' + c.to + ' ms');
});

test('a size reads as the server writes it', () => {
  const g = open();
  // The amount of an image that is pulled, beside one that is not yet.
  const amount = bytes => {
    const spans = [
      command('create cluster'), step('pull', 'pull images', 1),
      pull('p0', 'pull', 'sind-node:25.11', 1, 2, { a: bytes }), pull('p1', 'pull', 'sind-node:26.05', 1),
    ];
    draw(g, { last: 2000, spans }, 2.5);
    return row(g, 'p0').res[1];
  };
  for (const c of shared.sizes) assert.equal(amount(c.bytes), c.text, c.bytes + ' bytes');
});
