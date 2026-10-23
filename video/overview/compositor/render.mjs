// SPDX-License-Identifier: LGPL-3.0-or-later

// Render the overview video from its recordings: load the compositor in
// headless Chrome, take every frame and encode it with SVT-AV1.
//
//   node render.mjs --rec DIR --out DIR [--voice DIR] [--workers N] [--from S] [--to S]
//   node render.mjs --rec DIR --out DIR [--voice DIR] --stills 12,64.5,...
//
// --voice names the narration that sindy_voice.py made from narration.json;
// each beat lasts as long as its line. It writes video.json (duration,
// chapters, music energy, sound effects, narration cues, poster time,
// missing recordings), poster.jpg, thumb.jpg (with a play
// button) and video.mkv (no audio);
// build.sh adds the sound track. With --stills it only writes JPEG stills.
// CHROME names the browser (default: /usr/bin/google-chrome).

import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import puppeteer from "puppeteer-core";

const here = path.dirname(fileURLToPath(import.meta.url));
const { values: a } = parseArgs({
  options: {
    rec: { type: "string" }, out: { type: "string" }, workers: { type: "string" },
    from: { type: "string" }, to: { type: "string" }, fps: { type: "string", default: "30" },
    stills: { type: "string" }, crf: { type: "string", default: "36" }, voice: { type: "string" },
  },
});
if (!a.rec || !a.out) {
  console.error("usage: render.mjs --rec DIR --out DIR [--workers N] [--from S] [--to S] [--stills T,...]");
  process.exit(2);
}
const rec = path.resolve(a.rec), out = path.resolve(a.out);
fs.mkdirSync(out, { recursive: true });
const fps = Number(a.fps);

// The recordings, the scenario files and the Slurm release lines of the
// node images, for the compositor.
const read = f => (fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, "utf8")) : null);
const scenario = path.join(here, "..", "scenario");
const files = Object.fromEntries(fs.readdirSync(scenario).map(f => [f, fs.readFileSync(path.join(scenario, f), "utf8")]));
const bake = fs.readFileSync(path.join(here, "..", "..", "..", "docker-bake.hcl"), "utf8");
const slurm = [...bake.matchAll(/version\s*=\s*"(\d+\.\d+)\.\d+"/g)].map(m => m[1]);
const voice = a.voice ? Object.fromEntries((read(path.join(a.voice, "index.json")) || []).map(l => [l.id, l.duration])) : {};
const REC = { term: read(path.join(rec, "terminal.json")) || { shots: [] }, ui: read(path.join(rec, "ui", "capture.json")) || { frames: [], events: [] }, files, slurm, voice };
fs.writeFileSync(path.join(rec, "data.js"), "window.REC = " + JSON.stringify(REC) + ";\n");
const link = path.join(here, "rec");
try { fs.unlinkSync(link); } catch { /* none yet */ }
fs.symlinkSync(rec, link, "dir");

// One browser for each worker: Chrome does not paint a page in the
// background, so pages of one browser cannot take screenshots at once.
const browsers = [];
const launch = () => puppeteer.launch({
  executablePath: process.env.CHROME || "/usr/bin/google-chrome",
  headless: true,
  args: ["--no-sandbox", "--allow-file-access-from-files", "--hide-scrollbars", "--force-color-profile=srgb", "--font-render-hinting=none"],
  defaultViewport: { width: 1920, height: 1080, deviceScaleFactor: 1 },
  protocolTimeout: 120000,
}).then(b => { browsers.push(b); return b; });
const closeAll = () => Promise.all(browsers.map(b => b.close()));
async function open() {
  const browser = await launch();
  const page = (await browser.pages())[0] || await browser.newPage();
  const errors = [];
  page.on("pageerror", e => errors.push(e.message));
  page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
  await page.goto("file://" + path.join(here, "index.html"), { waitUntil: "load" });
  await page.evaluate(() => window.ready);
  if (errors.length) throw new Error("compositor: " + errors.join("; "));
  return page;
}
const shot = (page, t, file, quality = 92) => page.evaluate(t => window.renderAt(t), t)
  .then(() => page.screenshot({ type: "jpeg", quality, optimizeForSpeed: true, ...(file ? { path: file } : {}) }));

const first = await open();
const video = await first.evaluate(() => window.VIDEO);
fs.writeFileSync(path.join(out, "video.json"), JSON.stringify(video, null, 1));
if (video.missing.length) console.warn("missing recordings: " + video.missing.join(", "));
console.log(`duration ${video.duration}s, ${video.chapters.length} chapters, ${video.sfx.length} sounds`);

if (a.stills) {
  for (const t of a.stills.split(",").map(Number)) await shot(first, t, path.join(out, `still-${t.toFixed(1)}.jpg`));
  await closeAll();
  process.exit(0);
}
await shot(first, video.poster, path.join(out, "poster.jpg"), 90);
// The README's thumbnail: the poster with a play button.
await first.evaluate(() => document.body.classList.add("thumb"));
await shot(first, video.poster, path.join(out, "thumb.jpg"), 90);
await first.evaluate(() => document.body.classList.remove("thumb"));

// Frames [f0, f1) in workers, each into its own AV1 segment, then joined.
const f0 = Math.round(Number(a.from || 0) * fps), f1 = Math.round(Number(a.to || video.duration) * fps);
const n = Number(a.workers || Math.max(1, Math.min(4, Math.floor(os.cpus().length / 2))));
const per = Math.ceil((f1 - f0) / n);
const started = Date.now();
let done = 0;
async function worker(k, page) {
  const s = f0 + k * per, e = Math.min(f1, s + per);
  if (s >= e) return null;
  const seg = path.join(out, `seg-${k}.mkv`);
  const ff = spawn("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", "-f", "image2pipe", "-framerate", String(fps), "-c:v", "mjpeg", "-i", "-",
    "-c:v", "libsvtav1", "-preset", "7", "-crf", a.crf, "-pix_fmt", "yuv420p10le", "-g", String(fps * 8),
    "-svtav1-params", "tune=0:lp=2", seg], { stdio: ["pipe", "inherit", "inherit"], env: { ...process.env, SVT_LOG: "1" } });
  const closed = new Promise((ok, fail) => ff.on("close", c => (c === 0 ? ok() : fail(new Error(`ffmpeg exit ${c}`)))));
  for (let f = s; f < e; f++) {
    const buf = await shot(page, f / fps);
    if (!ff.stdin.write(buf)) await new Promise(r => ff.stdin.once("drain", r));
    if (++done % 300 === 0) {
      const el = (Date.now() - started) / 1000;
      console.log(`${done}/${f1 - f0} frames, ${(done / el).toFixed(1)} fps, ${Math.round((f1 - f0 - done) / (done / el))}s left`);
    }
  }
  ff.stdin.end();
  await closed;
  return seg;
}
const pages = [first];
for (let k = 1; k < n; k++) pages.push(await open());
const segs = (await Promise.all(pages.map((p, k) => worker(k, p)))).filter(Boolean);
await closeAll();
fs.writeFileSync(path.join(out, "segments.txt"), segs.map(s => `file '${s}'`).join("\n") + "\n");
await new Promise((ok, fail) => spawn("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", path.join(out, "segments.txt"), "-c", "copy", path.join(out, "video.mkv")], { stdio: "inherit" })
  .on("close", c => (c === 0 ? ok() : fail(new Error(`concat exit ${c}`)))));
segs.forEach(s => fs.unlinkSync(s));
fs.unlinkSync(path.join(out, "segments.txt"));
console.log(`rendered ${f1 - f0} frames in ${Math.round((Date.now() - started) / 1000)}s`);
