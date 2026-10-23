// SPDX-License-Identifier: LGPL-3.0-or-later

// A small terminal emulator for the recorded shots: enough of xterm for
// bash, sind's progress tree and the tools of the scenario (SGR colours,
// cursor moves, erase, wrap, tabs). Escape sequences it does not know are
// dropped. Term.at(t) shows the screen after every chunk up to time t.

(function () {
  "use strict";

  // The 16 colours, in the palette of the video.
  const BASE = [
    "#1e293b", "#f87171", "#4ade80", "#fbbf24", "#60a5fa", "#c084fc", "#2dd4bf", "#cbd5e1",
    "#64748b", "#fca5a5", "#86efac", "#fde68a", "#93c5fd", "#d8b4fe", "#5eead4", "#f8fafc",
  ];
  const FG = "#d7dee9", BG = "#0b1222";

  function xterm256(n) {
    if (n < 16) return BASE[n];
    if (n >= 232) { const v = 8 + (n - 232) * 10; return `rgb(${v},${v},${v})`; }
    n -= 16;
    const s = [0, 95, 135, 175, 215, 255];
    return `rgb(${s[Math.floor(n / 36)]},${s[Math.floor(n / 6) % 6]},${s[n % 6]})`;
  }

  const PLAIN = { fg: null, bg: null, b: false, d: false, i: false, u: false, inv: false };
  const esc = s => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

  class Screen {
    constructor(cols, rows) {
      this.cols = cols; this.rows = rows;
      this.lines = [[]]; this.r = 0; this.c = 0; this.wrap = false;
      this.st = PLAIN; this.show = true; this.saved = [0, 0];
      this.rest = ""; // an escape sequence split across chunks
    }

    line(r) { while (this.lines.length <= r) this.lines.push([]); return this.lines[r]; }
    top() { return Math.max(0, this.lines.length - this.rows); }

    put(ch) {
      if (this.wrap) { this.r++; this.c = 0; this.wrap = false; }
      const l = this.line(this.r);
      while (l.length < this.c) l.push([" ", PLAIN]);
      l[this.c] = [ch, this.st];
      if (this.c === this.cols - 1) this.wrap = true; else this.c++;
    }

    erase(r, from, to) {
      const l = this.line(r);
      for (let j = from; j < Math.min(to, l.length); j++) l[j] = [" ", PLAIN];
      if (to >= l.length) l.length = Math.min(l.length, from);
    }

    sgr(ps) {
      let s = { ...this.st };
      if (!ps.length) ps = [0];
      for (let i = 0; i < ps.length; i++) {
        const p = ps[i];
        if (p === 0 || isNaN(p)) s = { ...PLAIN };
        else if (p === 1) s.b = true;
        else if (p === 2) s.d = true;
        else if (p === 3) s.i = true;
        else if (p === 4) s.u = true;
        else if (p === 7) s.inv = true;
        else if (p === 22) s.b = s.d = false;
        else if (p === 23) s.i = false;
        else if (p === 24) s.u = false;
        else if (p === 27) s.inv = false;
        else if (p >= 30 && p <= 37) s.fg = BASE[p - 30];
        else if (p >= 90 && p <= 97) s.fg = BASE[p - 82];
        else if (p === 39) s.fg = null;
        else if (p >= 40 && p <= 47) s.bg = BASE[p - 40];
        else if (p >= 100 && p <= 107) s.bg = BASE[p - 92];
        else if (p === 49) s.bg = null;
        else if ((p === 38 || p === 48) && ps[i + 1] === 5) { s[p === 38 ? "fg" : "bg"] = xterm256(ps[i + 2]); i += 2; }
        else if ((p === 38 || p === 48) && ps[i + 1] === 2) { s[p === 38 ? "fg" : "bg"] = `rgb(${ps[i + 2]},${ps[i + 3]},${ps[i + 4]})`; i += 4; }
      }
      this.st = s;
    }

    csi(priv, ps, f) {
      const n = (ps[0] || 1);
      switch (f) {
        case "m": if (!priv) this.sgr(ps); break;
        case "A": this.r = Math.max(this.top(), this.r - n); this.wrap = false; break;
        case "B": this.r += n; this.line(this.r); this.wrap = false; break;
        case "C": this.c = Math.min(this.cols - 1, this.c + n); this.wrap = false; break;
        case "D": this.c = Math.max(0, this.c - n); this.wrap = false; break;
        case "E": this.r += n; this.c = 0; this.line(this.r); this.wrap = false; break;
        case "F": this.r = Math.max(this.top(), this.r - n); this.c = 0; this.wrap = false; break;
        case "G": this.c = Math.min(this.cols - 1, n - 1); this.wrap = false; break;
        case "H": case "f":
          this.r = this.top() + (ps[0] || 1) - 1; this.c = Math.min(this.cols - 1, (ps[1] || 1) - 1);
          this.line(this.r); this.wrap = false; break;
        case "K": {
          const m = ps[0] || 0;
          if (m === 0) this.erase(this.r, this.c, Infinity);
          else if (m === 1) this.erase(this.r, 0, this.c + 1);
          else this.erase(this.r, 0, Infinity);
          break;
        }
        case "J": {
          const m = ps[0] || 0;
          if (m === 0) { this.erase(this.r, this.c, Infinity); this.lines.length = this.r + 1; }
          else if (m >= 2) { // a clear screen scrolls the old screen away
            for (let i = 0; i < this.rows; i++) this.lines.push([]);
            this.r = this.lines.length - this.rows; this.lines.length = this.r + 1; this.c = 0;
            for (let i = 1; i < this.rows; i++) this.lines.push([]);
          }
          break;
        }
        case "h": case "l": if (priv && ps[0] === 25) this.show = f === "h"; break;
        case "s": this.saved = [this.r, this.c]; break;
        case "u": [this.r, this.c] = this.saved; break;
        default: break;
      }
    }

    write(s) {
      s = this.rest + s; this.rest = "";
      let i = 0;
      while (i < s.length) {
        const ch = s[i];
        if (ch === "\x1b") {
          const m = /^\x1b(?:\[([?>=]?)([0-9;:]*)[ -/]*([@-~])|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-Za-z]|[78=>cDEM])/.exec(s.slice(i, i + 256));
          if (!m) {
            if (s.length - i < 64) { this.rest = s.slice(i); return; } // wait for the rest
            i++; continue;
          }
          const seq = m[0];
          if (m[3]) this.csi(m[1], m[2] ? m[2].split(/[;:]/).map(x => (x === "" ? NaN : Number(x))) : [], m[3]);
          else if (seq === "\x1b7") this.saved = [this.r, this.c];
          else if (seq === "\x1b8") [this.r, this.c] = this.saved;
          else if (seq === "\x1bM") this.r = Math.max(this.top(), this.r - 1);
          i += seq.length; continue;
        }
        if (ch === "\r") { this.c = 0; this.wrap = false; }
        else if (ch === "\n") { this.r++; this.c = 0; this.line(this.r); this.wrap = false; } // as with onlcr
        else if (ch === "\b") { this.c = Math.max(0, this.c - 1); this.wrap = false; }
        else if (ch === "\t") { this.c = Math.min(this.cols - 1, (Math.floor(this.c / 8) + 1) * 8); }
        else if (ch >= " " && ch !== "\x7f") {
          const cp = s.codePointAt(i);
          const g = String.fromCodePoint(cp);
          this.put(g);
          i += g.length; continue;
        }
        i++;
      }
    }

    html(cursor) {
      const top = Math.max(0, Math.max(this.lines.length, this.r + 1) - this.rows);
      let out = "";
      for (let i = top; i < top + this.rows; i++) {
        const l = this.lines[i] || [];
        const n = Math.max(l.length, cursor && i === this.r ? this.c + 1 : 0);
        let row = "", key = null;
        for (let j = 0; j < n; j++) {
          const [ch, st] = l[j] || [" ", PLAIN];
          const cur = cursor && i === this.r && j === this.c;
          let fg = st.fg || FG, bg = st.bg;
          if (st.inv) { [fg, bg] = [bg || BG, fg]; }
          if (cur) { fg = BG; bg = "#cbd5e1"; }
          const style = `color:${fg}${bg ? ";background:" + bg : ""}${st.b ? ";font-weight:700" : ""}${st.d ? ";opacity:.6" : ""}${st.i ? ";font-style:italic" : ""}${st.u ? ";text-decoration:underline" : ""}`;
          if (style !== key) { if (key !== null) row += "</span>"; row += `<span style="${style}">`; key = style; }
          row += esc(ch);
        }
        if (key !== null) row += "</span>";
        out += row + "\n";
      }
      return out;
    }
  }

  // A terminal fed by a list of [time, text] chunks, sorted by time.
  class Term {
    constructor(node, cols, rows) {
      this.node = node; this.cols = cols; this.rows = rows;
      this.feed = []; this.reset(); this.shown = null;
    }
    add(t, s) { this.feed.push([t, s]); }
    reset() { this.scr = new Screen(this.cols, this.rows); this.i = 0; this.t = -Infinity; }
    at(t, cursor) {
      if (t < this.t) this.reset();
      this.t = t;
      while (this.i < this.feed.length && this.feed[this.i][0] <= t) this.scr.write(this.feed[this.i++][1]);
      const html = this.scr.html(cursor && this.scr.show);
      if (html !== this.shown) { this.node.innerHTML = html; this.shown = html; }
    }
  }

  window.Term = Term;
  window.TermScreen = Screen;
})();
