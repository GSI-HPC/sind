// SPDX-License-Identifier: LGPL-3.0-or-later

// The node picker of the dialogs (act-ntable): the node set field and the
// ticks follow each other (nodeset.js), the quick picks tick their nodes,
// and in the Power dialog the actions that fit no ticked node are off.
// Change nodes opens the picker of a short confirmation, with the actions
// on top. The form posts the ticks; the node set field has no name.
(() => {
  'use strict';

  function form(el) { return el && el.closest('form.modal'); }
  function rows(f) { return [...f.querySelectorAll('tr[data-act-node]')]; }
  function box(r) { return r.querySelector('input[type="checkbox"]'); }
  function ticked(f) { return rows(f).filter(r => box(r).checked); }

  const NS = () => window.sindNodeSet;

  // offs turns off the button of each action that fits none of the
  // ticked nodes, as their rows say; the action that is picked stays on.
  // The preview writes the line that says why (#act-offs).
  function offs(f) {
    const on = ticked(f);
    const why = on.map(r => { try { return JSON.parse(r.dataset.actOffs || '{}'); } catch (e) { return {}; } });
    for (const radio of f.querySelectorAll('input[name="op"]')) {
      const fits = on.length === 0 || why.some(w => !w[radio.value]);
      radio.closest('label').classList.toggle('off', !fits);
      radio.disabled = !fits && !radio.checked;
    }
  }

  // write puts the ticked nodes into the node set field.
  function write(f) {
    const field = f.querySelector('[data-act-nodeset]');
    if (field) field.value = NS().fold(ticked(f).map(r => r.dataset.actNode));
    const err = f.querySelector('#act-nodeset-err');
    if (err) err.textContent = '';
  }

  // tick ticks exactly the nodes of names, and tells the form that they
  // changed, which asks for a new preview.
  function tick(f, names) {
    const want = new Set(names);
    for (const r of rows(f)) box(r).checked = want.has(r.dataset.actNode);
    offs(f);
  }

  // read ticks the nodes of the node set field, and says which it does
  // not know.
  function read(f, field) {
    const err = f.querySelector('#act-nodeset-err');
    const names = NS().expand(field.value);
    if (names === null) {
      err.textContent = 'Write a node set, such as worker-[0-3] or controller,worker-1.';
      return;
    }
    const have = new Set(rows(f).map(r => r.dataset.actNode));
    const unknown = names.filter(n => !have.has(n));
    const cl = f.querySelector('input[name="cluster"]');
    err.textContent = unknown.length ? (cl ? cl.value : 'The cluster') + ' has no node ' + NS().fold(unknown) + '.' : '';
    tick(f, names.filter(n => have.has(n)));
  }

  document.addEventListener('click', ev => {
    const change = ev.target.closest('[data-act-change]');
    if (change) {
      const f = form(change);
      const pick = f.querySelector('#act-pick');
      pick.hidden = false;
      change.setAttribute('aria-expanded', 'true');
      // The actions leave their closed section for the top of the
      // picker, as when the dialog opens without nodes.
      const other = f.querySelector('[data-act-other]');
      if (other) other.replaceWith(other.querySelector('.act-opseg'));
      f.querySelector('[data-act-target]').hidden = true;
      const field = pick.querySelector('[data-act-nodeset]');
      (field || pick.querySelector('input')).focus();
      return;
    }
    const qp = ev.target.closest('[data-act-pick]');
    if (qp) {
      const f = form(qp);
      tick(f, qp.dataset.actPick.split(',').filter(Boolean));
      write(f);
      f.dispatchEvent(new Event('change', { bubbles: true }));
    }
  });

  document.addEventListener('change', ev => {
    const f = form(ev.target);
    if (!f) return;
    if (ev.target.matches('tr[data-act-node] input[type="checkbox"]')) {
      write(f);
      offs(f);
    }
    if (ev.target.matches('input[name="op"]')) offs(f);
  });

  document.addEventListener('input', ev => {
    if (ev.target.matches('[data-act-nodeset]')) read(form(ev.target), ev.target);
  });

  // A dialog that has just come in shows what fits its ticks.
  function setUp() {
    const f = document.querySelector('#act-dialog form.modal');
    if (f && !f.dataset.actNodesUp && f.querySelector('tr[data-act-node]')) {
      f.dataset.actNodesUp = '1';
      offs(f);
    }
  }
  document.addEventListener('htmx:afterSettle', setUp);
  document.addEventListener('DOMContentLoaded', setUp);
})();
