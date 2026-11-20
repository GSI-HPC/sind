// SPDX-License-Identifier: LGPL-3.0-or-later

// Tests of web/static/js/nodeset.js (make test-js). The cases of
// ../nodefold/testdata/nodesets.json are also the cases of a Go test, which
// reads them with go-nodeset as the CLI does: the script and the CLI read
// them alike.
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

// The script is one of a page: it puts its functions on window.
globalThis.window = {};
require('../web/static/js/nodeset.js');
const { parts, fold, expand } = globalThis.window.sindNodeSet;

const cases = JSON.parse(fs.readFileSync(path.join(__dirname, '../nodefold/testdata/nodesets.json'), 'utf8'));

const sorted = names => [...names].sort();

test('fold writes the node set of each case, and expand reads it back', () => {
  for (const c of cases.folds) {
    assert.equal(fold(c.names), c.set, JSON.stringify(c.names));
    assert.deepEqual(sorted(expand(c.set)), sorted(c.names), c.set);
  }
});

test('expand reads the nodes of each case, in the order of the text', () => {
  for (const c of cases.reads) assert.deepEqual(expand(c.text), c.names, c.text);
});

test('expand gives null for a text that is no node set', () => {
  for (const text of cases.rejects) assert.equal(expand(text), null, text);
});

test('parts gives one part for each name without a number and for each prefix', () => {
  assert.deepEqual(parts(['worker-1', 'db', 'gpu-0', 'worker-0', 'controller']), ['db', 'controller', 'worker-[0-1]', 'gpu-0']);
  assert.deepEqual(parts(['a-1-2', 'a-1-3', 'a-2-2']), ['a-1-[2-3]', 'a-2-2']);
  assert.deepEqual(parts(['w1', 'w2']), ['w1', 'w2']);
  assert.deepEqual(parts([]), []);
});

test('expand knows no step, as the comment of the script says', () => {
  assert.equal(expand('worker-[0-10/2]'), null);
});

test('expand gives null where the node set names too many nodes', () => {
  // One range of at most 4097 numbers.
  assert.equal(expand('w[0-4096]').length, 4097);
  assert.equal(expand('w[0-4097]'), null);
  // At most 65536 names in all: 16 * 4096 fit, one more does not.
  const sixteen = Array.from({ length: 16 }, (_, i) => 'w' + i + '-[1-4096]').join(',');
  assert.equal(expand(sixteen).length, 65536);
  assert.equal(expand(sixteen + ',x'), null);
  // One list of ranges: 16 * 4096 numbers fit, one more does not.
  const list = Array.from({ length: 16 }, () => '1-4096').join(',');
  assert.equal(expand('w[' + list + ']').length, 65536);
  assert.equal(expand('w[' + list + ',5]'), null);
  // Two ranges in one name multiply.
  assert.equal(expand('w[0-255]-[0-255]').length, 65536);
  assert.equal(expand('w[0-256]-[0-255]'), null);
  assert.equal(expand('w[0-4096]-[0-4096]'), null);
});

// rng is mulberry32: the same numbers in each run, so that a failure
// comes again.
function rng(seed) {
  return () => {
    seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('expand reads back the nodes of a cluster that fold wrote, each one time', () => {
  const next = rng(1);
  const pick = n => Math.floor(next() * n);
  for (let round = 0; round < 2000; round++) {
    // The nodes of a cluster, as sind names them, in any order.
    const all = ['controller', 'controller-backup', 'db', 'api', 'submitter'];
    for (let i = 0; i < 1 + pick(40); i++) all.push('worker-' + i);
    const names = all.filter(() => next() < 0.5);
    for (let i = names.length - 1; i > 0; i--) {
      const j = pick(i + 1);
      [names[i], names[j]] = [names[j], names[i]];
    }
    const set = fold(names);
    const back = expand(set);
    assert.notEqual(back, null, set);
    assert.deepEqual(sorted(back), sorted(names), set);
    // A set of nodes has one text: the order of the names does not change
    // the ranges.
    assert.equal(sorted(parts(names)).join(','), sorted(parts(sorted(names))).join(','), set);
  }
});
