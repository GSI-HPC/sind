// SPDX-License-Identifier: LGPL-3.0-or-later
// Sindy: a seekable 2D anime avatar rig for HyperFrames compositions.
//
// Every visual property is a pure function of time t (seconds) plus the
// tracks passed in at creation, so any frame can be rendered in any order.
// No clocks, no unseeded randomness, no requestAnimationFrame.
//
//   const sindy = Sindy.create(el, { speech, expressions, gaze, seed });
//   Sindy.attach(tl, sindy, { start: 0, duration: 12 }); // drive from GSAP
//   sindy.render(3.2);                                    // or render directly
(function (global) {
  "use strict";

  const NS = "http://www.w3.org/2000/svg";

  // ---------------------------------------------------------------- math --
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const lerp = (a, b, k) => a + (b - a) * k;
  const smooth = (k) => {
    k = clamp(k, 0, 1);
    return k * k * (3 - 2 * k);
  };
  const f1 = (n) => Math.round(n * 10) / 10;

  function mulberry32(seed) {
    let a = seed >>> 0;
    return function () {
      a = (a + 0x6d2b79f5) >>> 0;
      let t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  // Smooth deterministic 1D noise in [-1, 1]: a sum of detuned sines.
  function makeNoise(seed) {
    const rnd = mulberry32(seed);
    const waves = [0, 1, 2].map((i) => ({
      f: (0.11 + rnd() * 0.09) * (i + 1) * 1.7,
      p: rnd() * Math.PI * 2,
      a: 1 / (i + 1),
    }));
    const norm = waves.reduce((s, w) => s + w.a, 0);
    return (t) => waves.reduce((s, w) => s + w.a * Math.sin(t * w.f * Math.PI * 2 + w.p), 0) / norm;
  }

  // ------------------------------------------------------------- visemes --
  // Mouth shape targets. open: jaw opening 0..1, wide: corner spread 0..1,
  // round: lip rounding 0..1, teeth: upper-teeth visibility 0..1.
  const VISEMES = {
    sil: { open: 0.0, wide: 0.45, round: 0.0, teeth: 0 },
    mbp: { open: 0.0, wide: 0.4, round: 0.1, teeth: 0 },
    aa: { open: 1.0, wide: 0.65, round: 0.05, teeth: 0.25 },
    ah: { open: 0.8, wide: 0.55, round: 0.15, teeth: 0.2 },
    ee: { open: 0.6, wide: 0.95, round: 0.0, teeth: 0.7 },
    ih: { open: 0.4, wide: 0.85, round: 0.0, teeth: 0.8 },
    oh: { open: 0.75, wide: 0.25, round: 0.85, teeth: 0 },
    ou: { open: 0.35, wide: 0.05, round: 1.0, teeth: 0 },
    fv: { open: 0.12, wide: 0.6, round: 0.0, teeth: 1 },
    th: { open: 0.22, wide: 0.6, round: 0.0, teeth: 0.8 },
    cdg: { open: 0.3, wide: 0.7, round: 0.0, teeth: 0.6 },
    sz: { open: 0.15, wide: 0.8, round: 0.0, teeth: 1 },
    ch: { open: 0.3, wide: 0.35, round: 0.6, teeth: 0.6 },
    l: { open: 0.4, wide: 0.6, round: 0.0, teeth: 0.4 },
    r: { open: 0.25, wide: 0.3, round: 0.55, teeth: 0.2 },
    w: { open: 0.15, wide: 0.05, round: 1.0, teeth: 0 },
  };

  // ---------------------------------------------------------- expressions --
  // brow: raise (-1 frown .. 1 raised), browTilt: inner-end tilt (+ = worried),
  // eye: openness multiplier, squint: lower-lid raise, happyEyes: ^^ arcs,
  // smile: mouth corner curve (-1..1), mouthOpen: resting jaw opening,
  // blush: 0..1, pupil: pupil scale, winkL/winkR: force one eye closed.
  const EXPRESSIONS = {
    neutral: { brow: 0, browTilt: 0, eye: 1, squint: 0.05, happyEyes: 0, smile: 0.35, mouthOpen: 0, blush: 0.35, pupil: 1 },
    happy: { brow: 0.35, browTilt: 0.1, eye: 0.95, squint: 0.3, happyEyes: 0, smile: 0.85, mouthOpen: 0.15, blush: 0.6, pupil: 1 },
    joy: { brow: 0.5, browTilt: 0.15, eye: 1, squint: 0, happyEyes: 1, smile: 1, mouthOpen: 0.55, blush: 0.8, pupil: 1 },
    surprised: { brow: 1, browTilt: 0.2, eye: 1.12, squint: 0, happyEyes: 0, smile: 0, mouthOpen: 0.45, blush: 0.4, pupil: 0.8 },
    thinking: { brow: 0.15, browTilt: -0.35, eye: 0.85, squint: 0.2, happyEyes: 0, smile: 0.05, mouthOpen: 0, blush: 0.3, pupil: 1 },
    concerned: { brow: 0.1, browTilt: 0.7, eye: 0.95, squint: 0.1, happyEyes: 0, smile: -0.25, mouthOpen: 0, blush: 0.3, pupil: 1 },
    smug: { brow: -0.1, browTilt: -0.2, eye: 0.8, squint: 0.35, happyEyes: 0, smile: 0.7, mouthOpen: 0, blush: 0.5, pupil: 1 },
    wink: { brow: 0.3, browTilt: 0.1, eye: 1, squint: 0.2, happyEyes: 0, smile: 0.9, mouthOpen: 0.2, blush: 0.7, pupil: 1, winkR: 1 },
  };
  const EXPR_KEYS = ["brow", "browTilt", "eye", "squint", "happyEyes", "smile", "mouthOpen", "blush", "pupil", "winkL", "winkR"];

  // ------------------------------------------------------------- palette --
  const PALETTE = {
    hairDark: "#0b5d57",
    hairMid: "#0d9488",
    hairLight: "#2dd4bf",
    hairTip: "#38bdf8",
    hairShine: "#a5f3fc",
    skin: "#fde8dc",
    skinShade: "#f4c9b8",
    skinLine: "#d99a86",
    irisTop: "#0c4a6e",
    irisMid: "#0284c7",
    irisLow: "#5ee7f9",
    lash: "#1e2a3a",
    brow: "#0f4c48",
    mouth: "#8f2a3c",
    tongue: "#e8798d",
    hoodie: "#1e293b",
    hoodieLight: "#334155",
    hoodieDark: "#0f172a",
    accentBlue: "#0ea5e9",
    accentTeal: "#14b8a6",
    accentDeep: "#0d9488",
    blush: "#fb7185",
  };

  // -------------------------------------------------------------- helpers --
  function el(name, attrs, parent) {
    const node = document.createElementNS(NS, name);
    for (const k in attrs) node.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(node);
    return node;
  }

  // Build an anime bangs edge: alternating notches and pointed strand tips.
  function strandEdge(points) {
    // points: [[x,y,kind], ...] where kind 't' = tip, 'n' = notch
    let d = `M${points[0][0]},${points[0][1]}`;
    for (let i = 1; i < points.length; i++) {
      const [x0, y0] = points[i - 1];
      const [x1, y1, kind] = points[i];
      // Strands sweep slightly outward from the face center (x=300).
      const bend = (x1 + x0) / 2 < 300 ? -1 : 1;
      if (kind === "t") {
        d += ` Q${f1(x0 + (x1 - x0) * 0.25 + bend * 4)},${f1(y0 + (y1 - y0) * 0.75)} ${x1},${y1}`;
      } else {
        d += ` Q${f1(x0 + (x1 - x0) * 0.7 + bend * 3)},${f1(y0 + (y1 - y0) * 0.3)} ${x1},${y1}`;
      }
    }
    return d;
  }

  const BANGS_EDGE = [
    [146, 236, "n"],
    [172, 292, "t"],
    [188, 240, "n"],
    [214, 284, "t"],
    [238, 236, "n"],
    [268, 300, "t"],
    [292, 246, "n"],
    [320, 294, "t"],
    [338, 238, "n"],
    [366, 282, "t"],
    [392, 240, "n"],
    [424, 290, "t"],
    [438, 236, "n"],
    [456, 262, "t"],
    [458, 230, "n"],
  ];

  // Eye geometry in local coordinates (eye center at 0,0; outer corner on -x).
  const EYE_WHITE =
    "M-46,2 C-42,-28 -12,-44 16,-42 C34,-40 44,-26 45,-12 C46,10 36,40 6,44 C-24,46 -44,28 -46,2 Z";
  const UPPER_LASH = "M-54,8 C-48,-24 -14,-46 16,-44 C34,-42 46,-28 47,-12";
  const LASH_WING = "M-46,-6 C-52,-10 -58,-8 -62,-2 C-56,-4 -52,-2 -50,4 Z";
  const LOWER_LASH = "M-30,40 C-14,47 8,47 24,40";
  const CLOSED_LID = "M-52,10 C-34,30 -2,36 22,30 C34,26 42,18 46,8";
  const HAPPY_LID = "M-46,22 C-30,-8 18,-10 40,18";

  // ---------------------------------------------------------------- rig --
  function buildSvg(root, id) {
    const P = PALETTE;
    const svg = el("svg", { viewBox: "0 0 600 800", width: "100%", height: "100%", "aria-label": "Sindy" }, null);
    svg.style.overflow = "visible";
    const defs = el("defs", {}, svg);
    const g = (name) => `${id}-${name}`;

    const hairGrad = el("linearGradient", { id: g("hair"), x1: 0, y1: 0, x2: 0, y2: 1 }, defs);
    el("stop", { offset: "0", "stop-color": P.hairMid }, hairGrad);
    el("stop", { offset: "0.55", "stop-color": P.hairLight }, hairGrad);
    el("stop", { offset: "1", "stop-color": P.hairTip }, hairGrad);

    const hairBackGrad = el("linearGradient", { id: g("hairBack"), x1: 0, y1: 0, x2: 0, y2: 1 }, defs);
    el("stop", { offset: "0", "stop-color": P.hairDark }, hairBackGrad);
    el("stop", { offset: "0.7", "stop-color": P.hairMid }, hairBackGrad);
    el("stop", { offset: "1", "stop-color": "#0891b2" }, hairBackGrad);

    const irisGrad = el("linearGradient", { id: g("iris"), x1: 0, y1: 0, x2: 0, y2: 1 }, defs);
    el("stop", { offset: "0", "stop-color": P.irisTop }, irisGrad);
    el("stop", { offset: "0.55", "stop-color": P.irisMid }, irisGrad);
    el("stop", { offset: "1", "stop-color": P.irisLow }, irisGrad);

    const blushGrad = el("radialGradient", { id: g("blush") }, defs);
    el("stop", { offset: "0", "stop-color": P.blush, "stop-opacity": "0.55" }, blushGrad);
    el("stop", { offset: "1", "stop-color": P.blush, "stop-opacity": "0" }, blushGrad);

    const hoodieGrad = el("linearGradient", { id: g("hoodie"), x1: 0, y1: 0, x2: 0, y2: 1 }, defs);
    el("stop", { offset: "0", "stop-color": P.hoodieLight }, hoodieGrad);
    el("stop", { offset: "1", "stop-color": P.hoodie }, hoodieGrad);

    const faceClip = el("clipPath", { id: g("faceClip") }, defs);
    const FACE = "M146,250 C146,350 196,430 300,474 C404,430 454,350 454,250 C454,140 384,92 300,92 C216,92 146,140 146,250 Z";
    el("path", { d: FACE }, faceClip);

    const eyeClip = el("clipPath", { id: g("eyeClip") }, defs);
    el("path", { d: EYE_WHITE }, eyeClip);

    const r = {}; // references to animated nodes

    // Hair behind everything (moves with the head).
    r.headBack = el("g", {}, svg);
    r.hairBack = el("path", {
      d:
        "M300,74 C176,74 108,156 108,292 C108,400 96,520 74,646 Q96,626 112,606 Q118,640 136,662 Q150,628 168,614 " +
        "Q184,650 206,664 Q214,626 232,606 L368,606 Q386,626 394,664 Q416,650 432,614 Q450,628 464,662 " +
        "Q482,640 488,606 Q504,626 526,646 C504,520 492,400 492,292 C492,156 424,74 300,74 Z",
      fill: `url(#${g("hairBack")})`,
    }, r.headBack);

    // Body: hoodie, neck. Breathes independently of the head.
    r.body = el("g", {}, svg);
    el("path", { d: "M266,430 L266,548 C284,562 316,562 334,548 L334,430 Z", fill: P.skin }, r.body);
    el("path", { d: "M266,440 C286,476 314,476 334,440 L334,486 C314,506 286,506 266,486 Z", fill: P.skinShade }, r.body);
    // Hood lying behind the neck.
    el("path", { d: "M196,548 C210,500 250,500 300,510 C350,500 390,500 404,548 C380,540 340,532 300,540 C260,532 220,540 196,548 Z", fill: P.hoodieDark }, r.body);
    el("path", {
      d: "M300,536 C214,536 140,552 98,606 C70,644 60,720 56,820 L544,820 C540,720 530,644 502,606 C460,552 386,536 300,536 Z",
      fill: `url(#${g("hoodie")})`,
    }, r.body);
    // Collar opening and inner shirt.
    el("path", { d: "M262,540 C276,566 324,566 338,540 Z", fill: "#e6f6f8" }, r.body);
    el("path", { d: "M248,536 C262,578 338,578 352,536", fill: "none", stroke: P.hoodieDark, "stroke-width": 10, "stroke-linecap": "round" }, r.body);
    el("path", { d: "M248,536 C262,578 338,578 352,536", fill: "none", stroke: P.hoodieLight, "stroke-width": 2, "stroke-linecap": "round", opacity: 0.8 }, r.body);
    // Drawstrings.
    el("path", { d: "M270,572 C268,620 272,650 266,690", fill: "none", stroke: P.accentTeal, "stroke-width": 5, "stroke-linecap": "round" }, r.body);
    el("path", { d: "M330,572 C332,620 328,650 334,690", fill: "none", stroke: P.accentTeal, "stroke-width": 5, "stroke-linecap": "round" }, r.body);
    el("rect", { x: 261, y: 686, width: 10, height: 18, rx: 3, fill: P.accentBlue }, r.body);
    el("rect", { x: 329, y: 686, width: 10, height: 18, rx: 3, fill: P.accentBlue }, r.body);
    // Shoulder seam highlights.
    el("path", { d: "M120,600 C150,580 180,570 214,566", fill: "none", stroke: "#475569", "stroke-width": 3, opacity: 0.6 }, r.body);
    el("path", { d: "M480,600 C450,580 420,570 386,566", fill: "none", stroke: "#475569", "stroke-width": 3, opacity: 0.6 }, r.body);
    // sind logo patch on the chest (3x3 node grid).
    const patch = el("g", { transform: "translate(392,660) scale(0.42)" }, r.body);
    const cells = [
      [0, 0, P.accentBlue, 0.92], [62, 0, P.accentBlue, 0.7], [124, 0, P.accentBlue, 0.48],
      [0, 62, P.accentTeal, 0.82], [62, 62, P.accentDeep, 1], [124, 62, P.accentTeal, 0.58],
      [0, 124, P.accentBlue, 0.48], [62, 124, P.accentBlue, 0.34], [124, 124, P.accentTeal, 0.4],
    ];
    for (const [x, y, c, o] of cells) el("rect", { x, y, width: 52, height: 52, rx: 9, fill: c, opacity: o }, patch);

    // Head (front layers).
    r.head = el("g", {}, svg);
    el("path", { d: FACE, fill: P.skin }, r.head);
    el("path", { d: "M154,318 C168,380 214,434 300,474 C386,434 432,380 446,318", fill: "none", stroke: P.skinLine, "stroke-width": 2.5, "stroke-linecap": "round", opacity: 0.55 }, r.head);
    // Shadow cast by the bangs onto the forehead.
    const shade = el("g", { "clip-path": `url(#${g("faceClip")})` }, r.head);
    r.bangShadow = el("path", { d: strandEdge(BANGS_EDGE) + " L458,120 L146,120 Z", fill: P.skinShade, transform: "translate(0,12)" }, shade);

    r.featureWrap = el("g", { "clip-path": `url(#${g("faceClip")})` }, r.head);
    r.features = el("g", {}, r.featureWrap);
    // Blush.
    r.blushL = el("ellipse", { cx: 214, cy: 392, rx: 40, ry: 18, fill: `url(#${g("blush")})` }, r.features);
    r.blushR = el("ellipse", { cx: 386, cy: 392, rx: 40, ry: 18, fill: `url(#${g("blush")})` }, r.features);
    // Nose.
    el("path", { d: "M306,368 C304,376 300,382 296,384", fill: "none", stroke: P.skinLine, "stroke-width": 3, "stroke-linecap": "round" }, r.features);

    // Eyes.
    r.eyes = [];
    for (const side of ["L", "R"]) {
      const cx = side === "L" ? 226 : 374;
      const flip = side === "L" ? 1 : -1;
      const pos = el("g", { transform: `translate(${cx},326) scale(${flip},1)` }, r.features);
      const open = el("g", {}, pos);
      el("path", { d: EYE_WHITE, fill: "#ffffff" }, open);
      const irisWrap = el("g", { "clip-path": `url(#${g("eyeClip")})` }, open);
      // Shadow of the upper lid on the eyeball.
      const iris = el("g", {}, irisWrap);
      el("ellipse", { cx: 0, cy: 6, rx: 31, ry: 39, fill: `url(#${g("iris")})` }, iris);
      el("ellipse", { cx: 0, cy: 6, rx: 31, ry: 39, fill: "none", stroke: "#082f49", "stroke-width": 3 }, iris);
      const pupil = el("ellipse", { cx: 0, cy: 8, rx: 13, ry: 18, fill: "#082f49" }, iris);
      el("path", { d: "M-24,24 C-12,40 12,40 24,24", fill: "none", stroke: "#a5f3fc", "stroke-width": 4, opacity: 0.7 }, iris);
      el("ellipse", { cx: -11, cy: -12, rx: 9, ry: 11, fill: "#ffffff" }, iris);
      el("circle", { cx: 12, cy: 22, r: 4.5, fill: "#ffffff", opacity: 0.9 }, iris);
      el("path", { d: "M-46,2 C-42,-28 -12,-44 16,-42 C34,-40 44,-26 45,-12 L45,-30 L-46,-30 Z", fill: "#0c4a6e", opacity: 0.18 }, irisWrap);
      // Lower lid (skin) that rises when squinting.
      const lowerLid = el("path", { d: "M-52,64 L-52,40 C-30,34 20,34 52,31 L52,64 Z", fill: P.skin }, open);
      el("path", { d: UPPER_LASH, fill: "none", stroke: P.lash, "stroke-width": 7, "stroke-linecap": "round" }, open);
      el("path", { d: LASH_WING, fill: P.lash }, open);
      const lowerLash = el("path", { d: LOWER_LASH, fill: "none", stroke: P.lash, "stroke-width": 2.5, "stroke-linecap": "round", opacity: 0.55 }, open);
      const closed = el("path", { d: CLOSED_LID, fill: "none", stroke: P.lash, "stroke-width": 6, "stroke-linecap": "round", opacity: 0 }, pos);
      const happy = el("path", { d: HAPPY_LID, fill: "none", stroke: P.lash, "stroke-width": 7, "stroke-linecap": "round", opacity: 0 }, pos);
      r.eyes.push({ side, cx, flip, pos, open, iris, pupil, lowerLid, lowerLash, closed, happy });
    }

    // Mouth.
    r.mouth = el("g", { transform: "translate(300,424)" }, r.features);
    r.mouthLine = el("path", { d: "", fill: "none", stroke: "#9a4552", "stroke-width": 3.5, "stroke-linecap": "round" }, r.mouth);
    r.mouthFill = el("path", { d: "", fill: P.mouth }, r.mouth);
    const mouthClipEl = el("clipPath", { id: g("mouthClip") }, defs);
    r.mouthClip = el("path", { d: "" }, mouthClipEl);
    const inner = el("g", { "clip-path": `url(#${g("mouthClip")})` }, r.mouth);
    r.tongue = el("ellipse", { cx: 0, cy: 16, rx: 16, ry: 9, fill: P.tongue }, inner);
    r.teeth = el("rect", { x: -30, y: -20, width: 60, height: 8, fill: "#ffffff" }, inner);
    r.mouthOutline = el("path", { d: "", fill: "none", stroke: "#6b1f2c", "stroke-width": 2.2, "stroke-linejoin": "round" }, r.mouth);

    // Front hair: crown with bangs.
    r.hairFront = el("g", {}, r.head);
    r.bangs = el("path", {
      d: strandEdge(BANGS_EDGE) + " C470,130 404,70 300,70 C196,70 130,130 146,236 Z",
      fill: `url(#${g("hair")})`,
    }, r.hairFront);
    // Strand separation lines in the bangs.
    for (const [x0, y0, x1, y1] of [[214, 150, 212, 280], [262, 128, 268, 296], [318, 128, 318, 290], [370, 146, 364, 280], [168, 186, 172, 286], [420, 186, 418, 284]]) {
      el("path", { d: `M${x0},${y0} Q${(x0 + x1) / 2 + 6},${(y0 + y1) / 2} ${x1},${y1}`, fill: "none", stroke: P.hairDark, "stroke-width": 2.2, opacity: 0.35 }, r.hairFront);
    }
    // Angel-ring highlight.
    el("path", { d: "M184,150 C220,118 260,108 300,108 C340,108 380,118 416,150", fill: "none", stroke: P.hairShine, "stroke-width": 9, "stroke-linecap": "round", "stroke-dasharray": "44 14 30 12 52 16 26", opacity: 0.55 }, r.hairFront);

    // Side locks (sway with a lag).
    r.lockL = el("g", {}, r.head);
    el("path", { d: "M150,206 C130,286 140,390 156,470 C164,512 158,548 140,584 C168,560 180,520 178,486 Q186,520 182,548 C200,510 198,470 188,430 C178,370 176,300 196,232 Z", fill: `url(#${g("hair")})` }, r.lockL);
    el("path", { d: "M164,250 C152,330 158,410 172,480", fill: "none", stroke: P.hairDark, "stroke-width": 2, opacity: 0.35 }, r.lockL);
    r.lockR = el("g", {}, r.head);
    el("path", { d: "M450,206 C470,286 460,390 444,470 C436,512 442,548 460,584 C432,560 420,520 422,486 Q414,520 418,548 C400,510 402,470 412,430 C422,370 424,300 404,232 Z", fill: `url(#${g("hair")})` }, r.lockR);
    el("path", { d: "M436,250 C448,330 442,410 428,480", fill: "none", stroke: P.hairDark, "stroke-width": 2, opacity: 0.35 }, r.lockR);

    // Ahoge (the antenna strand).
    r.ahoge = el("path", { d: "M302,76 C292,36 318,10 352,20 C328,24 314,42 314,78 Z", fill: P.hairMid }, r.head);

    // Hair clip in the shape of the sind node grid.
    const clip = el("g", { transform: "translate(170,178) rotate(-18) scale(0.28)" }, r.head);
    for (const [x, y, c, o] of cells) el("rect", { x, y, width: 52, height: 52, rx: 9, fill: c, opacity: Math.min(1, o + 0.25), stroke: "#ffffff", "stroke-width": 6 }, clip);

    // Brows, drawn over the bangs (anime convention).
    r.brows = [];
    for (const side of ["L", "R"]) {
      const cx = side === "L" ? 226 : 374;
      const flip = side === "L" ? 1 : -1;
      const b = el("path", { d: "M-34,6 C-18,-6 12,-8 32,0 C12,-3 -16,0 -34,6 Z", fill: P.brow, stroke: P.brow, "stroke-width": 3.5, "stroke-linejoin": "round", opacity: 0.85 }, r.features);
      r.brows.push({ side, cx, flip, node: b });
    }
    // Brows live in their own layer above the bangs (anime convention).
    r.browLayer = el("g", {}, r.head);
    for (const b of r.brows) r.browLayer.appendChild(b.node);

    // Waving arm (her left hand, viewer's right). Pivot at the elbow below frame.
    r.arm = el("g", { opacity: 0 }, svg);
    r.armRot = el("g", {}, r.arm);
    el("path", { d: "M-34,0 C-36,-120 -30,-220 -28,-300 L28,-300 C30,-220 40,-120 40,0 Z", fill: `url(#${g("hoodie")})` }, r.armRot);
    el("path", { d: "M-30,-288 L30,-288 L32,-312 L-32,-312 Z", fill: P.hoodieDark }, r.armRot);
    r.hand = el("g", { transform: "translate(0,-310)" }, r.armRot);
    const skin = { fill: P.skin, stroke: P.skinLine, "stroke-width": 2.5, "stroke-linejoin": "round" };
    el("path", Object.assign({ d: "M-22,6 C-30,-20 -48,-34 -52,-46 C-54,-54 -44,-58 -38,-50 C-30,-40 -24,-34 -18,-30 Z" }, skin), r.hand);
    for (const [x, len, rot] of [[-17, 40, -8], [-5, 47, -2], [8, 45, 3], [20, 36, 9]]) {
      el("rect", Object.assign({ x: x - 6.5, y: -46 - len, width: 13, height: len + 10, rx: 6.5, transform: `rotate(${rot},${x},-46)` }, skin), r.hand);
    }
    el("path", Object.assign({ d: "M-26,4 C-28,-20 -28,-40 -24,-52 L28,-52 C30,-36 30,-16 26,4 Z" }, skin), r.hand);
    el("path", { d: "M-8,-30 C0,-26 8,-26 14,-30", fill: "none", stroke: P.skinLine, "stroke-width": 2, opacity: 0.6 }, r.hand);

    root.appendChild(svg);
    r.svg = svg;
    return r;
  }

  // -------------------------------------------------------------- tracks --
  // speech: one segment or an array of segments, each
  //   { offset, visemes: [[start, end, viseme, weight?], ...], envelope?: {fps, values} }
  // with viseme/envelope times relative to the segment's offset (seconds).
  // expressions: [{ t, name, blend? }]  gaze: [{ t, x, y }]  (x,y in -1..1)
  function normalizeTracks(opts) {
    let segs = opts.speech || [];
    if (!Array.isArray(segs)) segs = [segs];
    const vis = [];
    const envs = [];
    for (const seg of segs) {
      const off = seg.offset || 0;
      for (const v of seg.visemes || []) {
        vis.push({ s: v[0] + off, e: v[1] + off, v: VISEMES[v[2]] ? v[2] : "sil", w: v[3] == null ? 1 : v[3] });
      }
      if (seg.envelope) envs.push({ off, fps: seg.envelope.fps, values: seg.envelope.values });
    }
    vis.sort((a, b) => a.s - b.s);
    envs.sort((a, b) => a.off - b.off);
    const expr = (opts.expressions || [{ t: 0, name: "neutral" }]).slice().sort((a, b) => a.t - b.t);
    if (!expr.length || expr[0].t > 0) expr.unshift({ t: 0, name: "neutral" });
    const gaze = (opts.gaze || [{ t: 0, x: 0, y: 0 }]).slice().sort((a, b) => a.t - b.t);
    if (!gaze.length || gaze[0].t > 0) gaze.unshift({ t: 0, x: 0, y: 0 });
    const gestures = (opts.gestures || []).slice().sort((a, b) => a.t - b.t);
    return { vis, envs, expr, gaze, gestures };
  }

  // Blink schedule: seeded intervals, occasional double blinks.
  function blinkTimes(seed, until) {
    const rnd = mulberry32(seed ^ 0x9e3779b9);
    const out = [];
    let t = 0.8 + rnd() * 1.5;
    while (t < until) {
      out.push(t);
      if (rnd() < 0.18) out.push(t + 0.32);
      t += 2.2 + rnd() * 3.4;
    }
    return out;
  }

  function blinkAmount(times, t) {
    // 0 = open, 1 = closed. 70ms close, 40ms hold, 110ms open.
    let best = 0;
    for (let i = 0; i < times.length; i++) {
      const d = t - times[i];
      if (d < -0.1) break;
      if (d < 0 || d > 0.22) continue;
      const v = d < 0.07 ? d / 0.07 : d < 0.11 ? 1 : 1 - (d - 0.11) / 0.11;
      best = Math.max(best, v);
    }
    return best;
  }

  // ------------------------------------------------------------ instance --
  let instanceCount = 0;

  function create(container, opts) {
    opts = opts || {};
    const id = `sindy${instanceCount++}`;
    const seed = opts.seed == null ? 7 : opts.seed;
    const r = buildSvg(container, id);
    const tracks = normalizeTracks(opts);
    const blinks = blinkTimes(seed, opts.maxDuration || 600);
    const nSway = makeNoise(seed + 1);
    const nNod = makeNoise(seed + 2);
    const nShift = makeNoise(seed + 3);
    const nGazeX = makeNoise(seed + 4);
    const nGazeY = makeNoise(seed + 5);

    // Viseme params at time t via triangular-kernel coarticulation.
    function envelopeAt(t) {
      // Loudness 0..1 from whichever segment covers t (smoothed over ±30ms).
      for (let k = tracks.envs.length - 1; k >= 0; k--) {
        const env = tracks.envs[k];
        if (env.off > t) continue;
        const i = Math.floor((t - env.off) * env.fps);
        if (i - 3 >= env.values.length) return null;
        let s = 0;
        let n = 0;
        for (let j = i - 3; j <= i + 3; j++) {
          if (j >= 0 && j < env.values.length) {
            s += env.values[j];
            n++;
          }
        }
        return n ? s / n : 0;
      }
      return null;
    }

    // Lip-sync tuning: kernel half-width (s) and overall gain.
    const lipKernel = opts.lipKernel || 0.035;
    const lipGain = opts.lipGain || 1.3;

    function mouthAt(t) {
      const ts = t;
      const h = lipKernel;
      const out = { open: 0, wide: 0, round: 0, teeth: 0 };
      let wsum = 0;
      const lo = ts - h;
      const hi = ts + h;
      let covered = 0;
      for (const seg of tracks.vis) {
        if (seg.e <= lo) continue;
        if (seg.s >= hi) break;
        const a = Math.max(seg.s, lo);
        const b = Math.min(seg.e, hi);
        // Integral of the triangular kernel over [a,b] approximated at 3 points.
        const k = (x) => Math.max(0, 1 - Math.abs(x - ts) / h);
        const w = ((b - a) / 6) * (k(a) + 4 * k((a + b) / 2) + k(b));
        if (w <= 0) continue;
        covered += b - a;
        const p = VISEMES[seg.v];
        out.open += p.open * seg.w * w;
        out.wide += p.wide * w;
        out.round += p.round * w;
        out.teeth += p.teeth * w;
        wsum += w;
      }
      const silW = Math.max(0, 2 * h - covered) / 2; // gaps count as silence
      const sil = VISEMES.sil;
      out.wide += sil.wide * silW;
      wsum += silW;
      if (wsum > 0) {
        out.open /= wsum;
        out.wide /= wsum;
        out.round /= wsum;
        out.teeth /= wsum;
      }
      let energy = envelopeAt(ts);
      if (energy != null) {
        out.open *= (0.5 + 0.8 * clamp(energy, 0, 1)) * lipGain;
      } else {
        energy = out.open;
      }
      out.energy = energy;
      return out;
    }

    function exprAt(t) {
      const ex = tracks.expr;
      let i = 0;
      while (i + 1 < ex.length && ex[i + 1].t <= t) i++;
      const cur = Object.assign({}, EXPRESSIONS.neutral, EXPRESSIONS[ex[i].name] || {});
      if (i === 0) return cur;
      const prev = Object.assign({}, EXPRESSIONS.neutral, EXPRESSIONS[ex[i - 1].name] || {});
      const k = smooth((t - ex[i].t) / (ex[i].blend || 0.3));
      const out = {};
      for (const key of EXPR_KEYS) out[key] = lerp(prev[key] || 0, cur[key] || 0, k);
      return out;
    }

    function gazeAt(t) {
      const gz = tracks.gaze;
      let i = 0;
      while (i + 1 < gz.length && gz[i + 1].t <= t) i++;
      let x = gz[i].x;
      let y = gz[i].y;
      if (i > 0) {
        const k = smooth((t - gz[i].t) / 0.18);
        x = lerp(gz[i - 1].x, x, k);
        y = lerp(gz[i - 1].y, y, k);
      }
      // Micro-saccades: stepped noise sampled every ~0.9s, eased quickly.
      const step = 0.9;
      const n0 = Math.floor(t / step);
      const k2 = smooth((t - n0 * step) / 0.08);
      const mx = lerp(nGazeX(n0 * step * 3.1), nGazeX((n0 + 1) * step * 3.1), k2) * 0.12;
      const my = lerp(nGazeY(n0 * step * 2.7), nGazeY((n0 + 1) * step * 2.7), k2) * 0.08;
      return { x: clamp(x + mx, -1, 1), y: clamp(y + my, -1, 1) };
    }

    function headAt(t) {
      const m = mouthAt(t);
      const gz = gazeAt(t);
      const talk = m.energy;
      const sway = nSway(t) * 2.2 + gz.x * 2.5; // degrees
      const nod = nNod(t * 1.3) * 3 + talk * 4 * Math.max(0, Math.sin(t * 9.5));
      const shift = nShift(t) * 6 + gz.x * 8;
      return { rot: sway, y: nod, x: shift, yaw: clamp(gz.x * 0.6 + nShift(t * 0.7) * 0.25, -1, 1) };
    }

    function render(t) {
      t = Math.max(0, t);
      const e = exprAt(t);
      const m = mouthAt(t);
      const gz = gazeAt(t);
      const hd = headAt(t);
      const hdLag = headAt(Math.max(0, t - 0.18));
      const breath = Math.sin((t / 4.2) * Math.PI * 2);

      // Body: breathing.
      r.body.setAttribute("transform", `translate(0,${f1(-breath * 2)}) translate(300,800) scale(1,${(1 + breath * 0.006).toFixed(4)}) translate(-300,-800)`);

      // Head: rotation about the neck, small translation, breathing follow.
      const headT = `translate(${f1(hd.x)},${f1(hd.y - breath * 3)}) rotate(${hd.rot.toFixed(2)},300,500)`;
      r.head.setAttribute("transform", headT);
      r.headBack.setAttribute("transform", `${headT} translate(${f1(-hd.yaw * 6)},0)`);
      // Fake yaw: features shift more than the face outline.
      r.features.setAttribute("transform", `translate(${f1(hd.yaw * 10)},0)`);
      r.browLayer.setAttribute("transform", `translate(${f1(hd.yaw * 10)},0)`);
      r.hairFront.setAttribute("transform", `translate(${f1(hd.yaw * 6)},0)`);
      r.bangShadow.setAttribute("transform", `translate(${f1(hd.yaw * 6)},12)`);

      // Secondary motion: locks and ahoge lag behind the head.
      const lag = hd.rot - hdLag.rot;
      const lagY = hd.y - hdLag.y;
      r.lockL.setAttribute("transform", `rotate(${(-lag * 1.6 + lagY * 0.4).toFixed(2)},176,220)`);
      r.lockR.setAttribute("transform", `rotate(${(-lag * 1.6 - lagY * 0.4).toFixed(2)},424,220)`);
      r.ahoge.setAttribute("transform", `rotate(${(-lag * 4 + Math.sin(t * 2.1) * 2 - lagY * 2).toFixed(2)},308,78)`);

      // Gestures: wave = raise (0.35s), wag, lower (0.35s).
      let armShown = false;
      for (const gst of tracks.gestures) {
        if (gst.name !== "wave") continue;
        const dur = gst.dur || 1.8;
        const d = t - gst.t;
        if (d < 0 || d > dur) continue;
        armShown = true;
        const up = smooth(d / 0.35) * smooth((dur - d) / 0.35);
        const wag = Math.sin(d * Math.PI * 2 * 2.2) * smooth(d / 0.3);
        r.arm.setAttribute("opacity", "1");
        r.arm.setAttribute("transform", `translate(${f1(hd.x * 0.5 + 476)},${f1(800 + (1 - up) * 440)})`);
        r.armRot.setAttribute("transform", `rotate(${(5 + wag * 8).toFixed(2)})`);
        r.hand.setAttribute("transform", `translate(0,-310) rotate(${(wag * 12).toFixed(2)})`);
      }
      if (!armShown) r.arm.setAttribute("opacity", "0");

      // Eyes.
      const blink = blinkAmount(blinks, t);
      for (const eye of r.eyes) {
        const wink = eye.side === "L" ? e.winkL || 0 : e.winkR || 0;
        // No alpha blending on the eyes (it reads as ghosting): the open eye
        // squashes shut, then swaps to a closed or happy (^) line.
        const happy = Math.max(e.happyEyes, wink);
        const openK = clamp(e.eye * (1 - blink) * (1 - smooth(happy * 2)), 0, 1.2);
        const isOpen = openK >= 0.18;
        const isHappy = !isOpen && happy >= 0.5;
        eye.open.setAttribute("opacity", isOpen ? "1" : "0");
        eye.open.setAttribute("transform", `translate(0,28) scale(1,${Math.max(openK, 0.05).toFixed(3)}) translate(0,-28)`);
        eye.closed.setAttribute("opacity", !isOpen && !isHappy ? "1" : "0");
        eye.happy.setAttribute("opacity", isHappy ? "1" : "0");
        // Gaze: iris offset in the eye's local frame (mirrored for the right eye).
        const ix = (gz.x * 13 + hd.yaw * 3) * eye.flip;
        const iy = gz.y * 8;
        const ps = e.pupil;
        eye.iris.setAttribute("transform", `translate(${f1(ix)},${f1(iy)})`);
        eye.pupil.setAttribute("rx", f1(13 * ps));
        eye.pupil.setAttribute("ry", f1(18 * ps));
        const sq = e.squint * 26;
        eye.lowerLid.setAttribute("transform", `translate(0,${f1(16 - sq)})`);
        eye.lowerLash.setAttribute("transform", `translate(0,${f1(-sq * 0.85)})`);
      }

      // Brows.
      for (const b of r.brows) {
        const y = 262 - e.brow * 12 + (1 - Math.min(1, e.eye)) * 4;
        const tilt = e.browTilt * 10 + (e.brow < 0 ? e.brow * 6 : 0);
        b.node.setAttribute("transform", `translate(${b.cx},${f1(y)}) scale(${b.flip},1) rotate(${(-tilt).toFixed(2)},24,0)`);
      }

      // Blush.
      const bl = clamp(e.blush, 0, 1);
      r.blushL.setAttribute("opacity", bl.toFixed(2));
      r.blushR.setAttribute("opacity", bl.toFixed(2));

      // Mouth.
      const open = clamp(Math.max(m.open, e.mouthOpen * (1 - m.energy * 0.5)), 0, 1.1);
      const round = m.round * clamp(m.open * 3, 0, 1);
      const wide = lerp(0.45, m.wide, clamp(m.open * 2.5 + m.teeth * 0.3, 0, 1));
      const smile = e.smile * (1 - round * 0.7);
      let w = lerp(34, 58, wide) * (1 - round * 0.45) * (1 + smile * 0.12);
      const h = open * lerp(40, 46, round);
      const cornerY = -smile * 6;
      const L = -w / 2;
      const R = w / 2;
      if (h < 3) {
        const d = `M${f1(L)},${f1(cornerY)} Q0,${f1(smile * 7 + h)} ${f1(R)},${f1(cornerY)}`;
        r.mouthLine.setAttribute("d", d);
        r.mouthLine.setAttribute("opacity", "1");
        r.mouthFill.setAttribute("d", "");
        r.mouthOutline.setAttribute("d", "");
        r.mouthClip.setAttribute("d", "");
        r.teeth.setAttribute("opacity", "0");
        r.tongue.setAttribute("opacity", "0");
      } else {
        const top = -h * 0.28 - smile * 2;
        const bot = h * 0.72 + smile * 3;
        const rr = round * w * 0.18;
        const d =
          `M${f1(L)},${f1(cornerY)} ` +
          `C${f1(L + w * 0.12 - rr)},${f1(top)} ${f1(R - w * 0.12 + rr)},${f1(top)} ${f1(R)},${f1(cornerY)} ` +
          `C${f1(R - w * 0.02 + rr)},${f1(bot)} ${f1(L + w * 0.02 - rr)},${f1(bot)} ${f1(L)},${f1(cornerY)} Z`;
        r.mouthLine.setAttribute("opacity", "0");
        r.mouthFill.setAttribute("d", d);
        r.mouthOutline.setAttribute("d", d);
        r.mouthClip.setAttribute("d", d);
        r.teeth.setAttribute("opacity", clamp(m.teeth * 1.2, 0, 1).toFixed(2));
        const lipTop = 0.25 * cornerY + 0.75 * top;
        r.teeth.setAttribute("y", f1(lipTop - 2));
        r.teeth.setAttribute("height", f1(clamp(h * 0.3, 4, 10) + 2));
        r.tongue.setAttribute("opacity", clamp((h - 8) / 10, 0, 1).toFixed(2));
        r.tongue.setAttribute("cy", f1(bot - 4));
        r.tongue.setAttribute("rx", f1(w * 0.32));
      }
    }

    render(0);
    return { render, svg: r.svg, tracks, mouthAt, exprAt, gazeAt };
  }

  // Drive an instance from a GSAP timeline: the tween's setter renders the
  // avatar at local time, so seeking the timeline (in any order) is exact.
  function attach(tl, sindy, opt) {
    opt = opt || {};
    const start = opt.start || 0;
    const duration = opt.duration;
    const offset = opt.offset || 0; // avatar-local time at `start`
    const clock = {
      _t: 0,
      get t() {
        return this._t;
      },
      set t(v) {
        this._t = v;
        sindy.render(v);
      },
    };
    clock.t = offset;
    tl.fromTo(clock, { t: offset }, { t: offset + duration, duration, ease: "none", immediateRender: false }, start);
    return clock;
  }

  global.Sindy = { create, attach, VISEMES, EXPRESSIONS, PALETTE };
})(typeof window !== "undefined" ? window : globalThis);
