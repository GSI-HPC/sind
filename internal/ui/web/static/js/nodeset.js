// SPDX-License-Identifier: LGPL-3.0-or-later

// Node sets as sind's commands take them, for the scripts of the pages and
// of the dialogs (window.sindNodeSet): parts folds node names into the
// parts of a node set, fold joins them with commas, as the CLI takes them,
// and expand reads a node set back into names.
(() => {
  'use strict';

  // parts folds node names into the parts of one node set: worker-0
  // worker-1 worker-3 become worker-[0-1,3]; a name without a number stays
  // as it is, first.
  function parts(names) {
    const groups = new Map();
    const out = [];
    for (const n of names) {
      const m = /^(.*?-)(\d+)$/.exec(n);
      if (!m) { out.push(n); continue; }
      if (!groups.has(m[1])) groups.set(m[1], []);
      groups.get(m[1]).push(+m[2]);
    }
    for (const [prefix, nums] of groups) {
      nums.sort((a, b) => a - b);
      const ranges = [];
      for (let i = 0; i < nums.length; i++) {
        let j = i;
        while (j + 1 < nums.length && nums[j + 1] === nums[j] + 1) j++;
        ranges.push(i === j ? String(nums[i]) : nums[i] + '-' + nums[j]);
        i = j;
      }
      out.push(nums.length === 1 ? prefix + nums[0] : prefix + '[' + ranges.join(',') + ']');
    }
    return out;
  }

  // fold writes node names as one node set: controller,worker-[4-5].
  function fold(names) { return parts(names).join(','); }

  // expand reads a node set as the CLI takes it, such as
  // controller,worker-[0-3,5]: the names, or null where it is not one.
  function expand(text) {
    const items = [];
    let depth = 0, cur = '';
    for (const ch of text) {
      if (ch === '[') depth++;
      if (ch === ']') depth--;
      if (depth < 0 || depth > 1) return null;
      if (depth === 0 && (ch === ',' || /\s/.test(ch))) {
        if (cur) items.push(cur);
        cur = '';
      } else cur += ch;
    }
    if (depth !== 0) return null;
    if (cur) items.push(cur);
    const out = [];
    for (const item of items) {
      let names = [''];
      const re = /\[([^\]]*)\]|([^[\]]+)/g;
      let m;
      while ((m = re.exec(item))) {
        if (m[2] !== undefined) { names = names.map(n => n + m[2]); continue; }
        const nums = [];
        for (const part of m[1].split(',')) {
          const r = /^(\d+)(?:-(\d+))?$/.exec(part.trim());
          if (!r) return null;
          const from = +r[1], to = r[2] === undefined ? from : +r[2];
          if (to < from || to - from > 4096) return null;
          const width = r[1].length > 1 && r[1][0] === '0' ? r[1].length : 0;
          for (let i = from; i <= to; i++) nums.push(String(i).padStart(width, '0'));
        }
        names = names.flatMap(n => nums.map(x => n + x));
      }
      out.push(...names);
    }
    return out;
  }

  window.sindNodeSet = Object.freeze({ parts, fold, expand });
})();
