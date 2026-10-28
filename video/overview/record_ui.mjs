// SPDX-License-Identifier: LGPL-3.0-or-later

// Record the sind ui tour of the overview video: drive a headless Chrome
// through the pages and dialogs of a running sind ui, and keep the frames of
// a screencast with their times, the beats (marks) and where the pointer
// moved and clicked. The compositor plays the frames and draws the pointer.
//
//   node record_ui.mjs URL OUTDIR
//
// URL is what sind ui printed, with its token. CHROME names the browser
// (default: /usr/bin/google-chrome). The tour expects the clusters of record.py:
// dev with monitoring, ha, bench (failed) and ci/default.

import fs from "node:fs";
import path from "node:path";
import puppeteer from "puppeteer-core";

const [url, out] = process.argv.slice(2);
if (!url || !out) {
  console.error("usage: record_ui.mjs URL OUTDIR");
  process.exit(2);
}
const base = new URL(url).origin;
const W = 1600, H = 900, DPR = 1.5;
fs.mkdirSync(path.join(out, "f"), { recursive: true });

const browser = await puppeteer.launch({
  executablePath: process.env.CHROME || "/usr/bin/google-chrome",
  headless: true,
  args: ["--no-sandbox", `--window-size=${W},${H}`, "--hide-scrollbars"],
  defaultViewport: { width: W, height: H, deviceScaleFactor: DPR },
});
const ctx = browser.defaultBrowserContext();
await ctx.overridePermissions(base, ["clipboard-read", "clipboard-write", "clipboard-sanitized-write"]);
const page = await browser.newPage();
// sind ui in its light theme; the tour switches it to dark at the end.
await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: "light" }]);
await page.goto(url, { waitUntil: "networkidle2" });

// The screencast sends a frame only when the page changes: a 1 px element
// that changes colour keeps a steady rate, lower while a long run plays,
// which the video shows faster.
await page.evaluateOnNewDocument(() => {
  const start = () => {
    const d = document.createElement("div");
    d.style.cssText = "position:fixed;left:0;top:0;width:1px;height:1px;opacity:.01;z-index:2147483647;pointer-events:none";
    document.body.appendChild(d);
    let id = 0;
    window.__tick = hz => { clearInterval(id); id = setInterval(() => { d.style.background = d.style.background === "red" ? "blue" : "red"; }, 1000 / hz); };
    window.__tick(Number(sessionStorage.getItem("__hz") || 30));
  };
  if (document.body) start(); else document.addEventListener("DOMContentLoaded", start);
});
const hz = async n => page.evaluate(n => { sessionStorage.setItem("__hz", String(n)); window.__tick && window.__tick(n); }, n);

const cdp = await page.createCDPSession();
const frames = [], events = [];
let n = 0;
const T0 = Date.now() / 1000;
const now = () => +(Date.now() / 1000 - T0).toFixed(3);
// A frame keeps the time Chrome took it, not the time it arrived.
cdp.on("Page.screencastFrame", async ({ data, metadata, sessionId }) => {
  const t = metadata.timestamp ? +(metadata.timestamp - T0).toFixed(3) : now();
  const f = `f/${String(n++).padStart(5, "0")}.jpg`;
  fs.writeFileSync(path.join(out, f), Buffer.from(data, "base64"));
  frames.push([t, f]);
  try { await cdp.send("Page.screencastFrameAck", { sessionId }); } catch { /* closed */ }
});
await page.reload({ waitUntil: "networkidle2" });
await cdp.send("Page.startScreencast", { format: "jpeg", quality: 82, maxWidth: W * DPR, maxHeight: H * DPR, everyNthFrame: 1 });

const mark = (name, extra = {}) => { events.push({ t: now(), name, ...extra }); console.log(now().toFixed(1), name); };
const wait = ms => new Promise(r => setTimeout(r, ms));
// A gap is footage the video leaves out, such as a page that loads: the
// video fades from the frame before the gap to the frame after it.
async function gap(fn, settle = 300) {
  const t = now();
  await fn();
  await wait(settle);
  events.push({ t, name: "gap", end: now() });
}
const go = (p, settle) => gap(() => page.goto(base + p, { waitUntil: "networkidle2" }), settle);

// Move the pointer to a point, as a person would: eased, in MOVE seconds.
// The compositor draws the pointer along the same curve.
const MOVE = 0.6;
let px = W * 0.62, py = H * 0.6;
async function moveTo(x, y, label) {
  mark("move", { x, y, label, dur: MOVE });
  const x0 = px, y0 = py, steps = 24;
  for (let i = 1; i <= steps; i++) {
    const k = i / steps, e = k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2;
    await page.mouse.move(x0 + (x - x0) * e, y0 + (y - y0) * e);
    await wait((MOVE * 1000) / steps);
  }
  px = x; py = y;
}
// Move the pointer to an element and rest on it.
async function point(sel, label) {
  const el = await page.waitForSelector(sel, { visible: true, timeout: 20000 });
  await el.scrollIntoViewIfNeeded();
  const b = await el.boundingBox();
  const x = b.x + b.width / 2, y = b.y + b.height / 2;
  await moveTo(x, y, label);
  await wait(250);
  return { el, x, y, b };
}
// Move the pointer off a field, so that it does not hide what is typed.
async function park(sel) {
  const b = await (await page.$(sel)).boundingBox();
  await moveTo(b.x + b.width + 36, b.y + b.height + 28, "away");
}
async function click(sel, label, opts = {}) {
  const { x, y } = await point(sel, label);
  mark("click", { x, y, label });
  await page.mouse.click(x, y, opts);
}
// Choose an option of a select. Its list is native and the screencast
// does not show it, so the closed select steps through the options to the
// value instead, and then tells the page.
async function select(sel, value, label) {
  const { x, y } = await point(sel, label);
  mark("click", { x, y, label });
  const steps = await page.evaluate((sel, value) => {
    const el = document.querySelector(sel), to = [...el.options].findIndex(o => o.value === value);
    return to < 0 ? [] : Array.from({ length: Math.abs(to - el.selectedIndex) }, (_, k) => el.selectedIndex + Math.sign(to - el.selectedIndex) * (k + 1));
  }, sel, value);
  for (const i of steps) {
    await page.evaluate((sel, i) => { document.querySelector(sel).selectedIndex = i; }, sel, i);
    await wait(300);
  }
  await page.evaluate(sel => document.querySelector(sel).dispatchEvent(new Event("change", { bubbles: true })), sel);
}
async function type(text, delay = 90) {
  mark("type", { text });
  await page.keyboard.type(text, { delay });
}

// ------------------------------------------------------------- the tour
mark("overview"); await wait(6000);

mark("dialog");
await click('a[href="/dialogs/create-cluster"]', "New cluster…"); await wait(2500);
await click("#act-name", "name"); await page.keyboard.down("Control"); await page.keyboard.press("a"); await page.keyboard.up("Control");
await park("#act-name"); await type("lab", 140); await wait(1200);
mark("api"); await click("#act-api", "api"); await wait(1800);
mark("workers"); await click("#act-w0-count", "workers");
await page.keyboard.down("Control"); await page.keyboard.press("a"); await page.keyboard.up("Control");
await park("#act-w0-count"); await type("3", 150); await wait(2200);
mark("slurmconf"); await click("#act-sec-slurm > summary", "Slurm config"); await wait(2500);
mark("yaml"); await click("button[data-act-yaml-edit]", "Edit YAML"); await wait(3000);
await click("button[data-act-yaml-discard]", "Discard YAML edits"); await wait(1200);
mark("create"); await click("#act-ok", "Create");
await page.waitForFunction(() => location.pathname.startsWith("/runs/"), { timeout: 30000 });
mark("run"); await hz(6);
await page.waitForFunction(() => /\b(done|failed) in\b/.test(document.body.innerText), { timeout: 900000, polling: 500 });
await hz(15); mark("rundone"); await wait(3500);

mark("failed");
await go("/activity"); await wait(1200);
// The dock keeps the card of the run until it is hidden.
for (const b of await page.$$('#tasks form[action$="/dismiss"] button')) {
  const r = await b.boundingBox();
  if (!r) continue;
  await moveTo(r.x + r.width / 2, r.y + r.height / 2, "Hide");
  await wait(250);
  mark("click", { x: px, y: py, label: "Hide" });
  await page.mouse.click(px, py);
  await wait(900);
}
const failed = await page.evaluate(() => {
  const a = [...document.querySelectorAll('a[href^="/runs/"]')]
    .find(a => /\bfailed\b/i.test(a.closest("tr,li,article,.row")?.innerText || ""));
  return a && a.getAttribute("href");
});
if (failed) await click(`a[href="${failed}"]`, "failed run");
await wait(5000);

mark("cluster"); await go("/clusters/sind/dev"); await wait(5500);

mark("nodes");
await click('a.tab[href$="tab=nodes"]', "Nodes"); await wait(1500);
for (const w of ["worker-1", "worker-2", "worker-3"]) { await click(`input[data-pg-sel="${w}"]`, w); await wait(500); }
await wait(1800);
mark("power"); await click('[data-pg-selbar] a[data-pg-fit="reboot"]', "Reboot 3 workers…"); await wait(3500);
await page.keyboard.press("Escape"); await page.evaluate(() => document.querySelector("[data-act-close]")?.click()); await wait(800);

mark("logs");
await go("/clusters/sind/dev?tab=logs"); await wait(1200);
await select("#logs-node", "worker-0", "worker-0"); await wait(1500);
// the journal of a worker shows its slurmd unit first
await select("#logs-source", "journal", "journal"); await wait(1200);
await point("#logs-unit", "slurmd"); await wait(3500);
await click("button[data-logs-pause]", "Pause"); await wait(1500);
await click("button[data-logs-follow]", "Follow"); await wait(2000);

mark("connect");
await go("/clusters/sind/dev?tab=connect"); await wait(1500);
await click(".cmd:nth-of-type(1) button[data-pg-copy], button[data-pg-copy]", "Copy"); await wait(3000);

mark("mesh"); await go("/mesh"); await wait(3800);
mark("topology"); await go("/topology"); await wait(2500); await page.mouse.wheel({ deltaY: 420 }); await wait(2000);
mark("monitoring"); await go("/monitoring"); await wait(3800);
mark("doctor"); await go("/doctor"); await wait(3800);

await go("/"); mark("jump"); await wait(1200);
await click("button[data-palette-open]", "Jump"); await wait(400);
await type("worker-3", 120); await wait(2200);
await page.keyboard.press("Escape"); await wait(500);
mark("help"); await click("button[data-help-toggle]", "Help"); await wait(3500);
await page.keyboard.press("Escape"); await wait(600);
mark("theme"); await click("button[data-theme-toggle]", "Light or dark"); await wait(3500);

// Grafana of the realm, through host DNS: the Slurm cluster dashboard of dev.
// GRAFANA=0 skips it, for a test against sind ui --demo.
if (process.env.GRAFANA !== "0") {
// Each dashboard shows once its panels have drawn.
mark("grafana");
await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: "dark" }]);
const grafana = d => gap(() => page.goto(`http://grafana.sind.sind:3000/d/${d}&from=now-5m&to=now&kiosk&theme=dark`,
  { waitUntil: "networkidle2", timeout: 60000 }), 2500);
await grafana("sind-slurm-cluster?var-realm=sind&var-cluster=dev&refresh=15s");
await wait(6000);
await grafana("sind-nodes?var-realm=sind&var-cluster=dev");
await wait(5500);
}
mark("end");

await cdp.send("Page.stopScreencast");
fs.writeFileSync(path.join(out, "capture.json"), JSON.stringify({ frames, events, viewport: [W, H], dpr: DPR }));
await browser.close();
