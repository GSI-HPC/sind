// SPDX-License-Identifier: LGPL-3.0-or-later

// A document in memory for the tests of the page scripts that draw
// elements (make test-js): the elements, attributes and selectors that
// web/static/js/gantt.js uses, and no more. It has no layout: an element
// has the box that a test gives it, and no box otherwise. It is not a
// browser; the UI Pages job opens the pages in one (make check-ui).
'use strict';

// compound reads one part of a selector without spaces: a tag, classes
// and attributes, as "path.a", ".g-empty" or '[data-g="body"]'.
function compound(text) {
  const c = { tag: '', classes: [], attrs: [] };
  const re = /^([a-zA-Z][\w-]*)|\.([\w-]+)|\[([\w-]+)(?:="([^"]*)")?\]/y;
  let at = 0;
  while (at < text.length) {
    re.lastIndex = at;
    const m = re.exec(text);
    if (!m || (m[1] && at > 0)) throw new Error('selector that the test document does not know: ' + text);
    if (m[1]) c.tag = m[1].toLowerCase();
    else if (m[2]) c.classes.push(m[2]);
    else c.attrs.push([m[3], m[4]]);
    at = re.lastIndex;
  }
  return c;
}

class Element {
  constructor(tag, svg) {
    // An HTML element has its tag in capitals, an SVG element as written.
    this.tagName = svg ? tag : tag.toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.style = {};
    this.title = '';
    this.hidden = false;
    this.box = { left: 0, width: 0 };
    this._attrs = new Map();
    this._text = '';
    // dataset.key is the attribute data-key.
    this.dataset = new Proxy({}, {
      get: (_, k) => (this._attrs.has('data-' + k) ? this._attrs.get('data-' + k) : undefined),
      set: (_, k, v) => { this._attrs.set('data-' + k, String(v)); return true; },
      deleteProperty: (_, k) => { this._attrs.delete('data-' + k); return true; },
    });
  }

  get className() { return this._attrs.get('class') || ''; }
  set className(v) { this._attrs.set('class', String(v)); }
  getAttribute(k) { return this._attrs.has(k) ? this._attrs.get(k) : null; }
  setAttribute(k, v) { this._attrs.set(k, String(v)); }
  removeAttribute(k) { this._attrs.delete(k); }

  // The text of an element is its own, or that of its children in order.
  get textContent() { return this.children.length ? this.children.map(c => c.textContent).join('') : this._text; }
  set textContent(v) { this.replaceChildren(); this._text = String(v); }
  // A test reads no markup: the script sets it for one icon only.
  set innerHTML(v) { this.replaceChildren(); this._text = ''; }

  get firstChild() { return this.children[0] || null; }
  get lastChild() { return this.children[this.children.length - 1] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const list = this.parentNode.children;
    return list[list.indexOf(this) + 1] || null;
  }
  remove() {
    if (!this.parentNode) return;
    const list = this.parentNode.children;
    list.splice(list.indexOf(this), 1);
    this.parentNode = null;
  }
  insertBefore(e, ref) {
    e.remove();
    const at = ref ? this.children.indexOf(ref) : this.children.length;
    if (at < 0) throw new Error('insertBefore: the reference is no child');
    this.children.splice(at, 0, e);
    e.parentNode = this;
    this._text = '';
    return e;
  }
  appendChild(e) { return this.insertBefore(e, null); }
  append(...list) { list.forEach(e => this.appendChild(e)); }
  prepend(...list) { const first = this.firstChild; list.forEach(e => this.insertBefore(e, first)); }
  replaceChild(e, old) { this.insertBefore(e, old); old.remove(); return old; }
  replaceChildren(...list) {
    this.children.slice().forEach(c => c.remove());
    this.append(...list);
  }

  _is(c) {
    if (c.tag && this.tagName.toLowerCase() !== c.tag) return false;
    const have = this.className.split(/\s+/);
    return c.classes.every(x => have.includes(x)) &&
      c.attrs.every(([k, v]) => this._attrs.has(k) && (v === undefined || this._attrs.get(k) === v));
  }
  // matches knows a selector of parts with spaces between them: the last
  // part is the element, and each part before it one of its ancestors.
  matches(selector) {
    const parts = selector.trim().split(/\s+/).map(compound);
    if (!this._is(parts.pop())) return false;
    let up = this.parentNode;
    while (parts.length && up) {
      if (up._is(parts[parts.length - 1])) parts.pop();
      up = up.parentNode;
    }
    return !parts.length;
  }
  closest(selector) {
    for (let e = this; e; e = e.parentNode) if (e.matches(selector)) return e;
    return null;
  }
  querySelectorAll(selector) {
    const out = [];
    const walk = e => e.children.forEach(c => { if (c.matches(selector)) out.push(c); walk(c); });
    walk(this);
    return out;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  getBoundingClientRect() { return this.box; }
}

// install puts a document, and the globals of a page that the scripts
// use, on globalThis. The result has the body of the document, the frame
// callbacks that the scripts asked for, and dispatch, which calls the
// listeners of an event of the document.
function install() {
  const listeners = new Map();
  const page = { body: new Element('body'), frames: [], width: 1200, reducedMotion: true };
  globalThis.Element = Element;
  globalThis.window = globalThis;
  globalThis.document = {
    hidden: false,
    createElement: tag => new Element(tag),
    createElementNS: (_, tag) => new Element(tag, true),
    querySelector: selector => page.body.querySelector(selector),
    getElementById: id => page.body.querySelector('[id="' + id + '"]'),
    addEventListener: (type, fn) => listeners.set(type, (listeners.get(type) || []).concat(fn)),
  };
  globalThis.requestAnimationFrame = fn => { page.frames.push(fn); return page.frames.length; };
  globalThis.matchMedia = () => ({ matches: page.reducedMotion });
  Object.defineProperty(globalThis, 'innerWidth', { get: () => page.width, configurable: true });
  page.dispatch = (type, ev) => (listeners.get(type) || []).forEach(fn => fn(ev));
  return page;
}

module.exports = { Element, install };
