// SPDX-License-Identifier: LGPL-3.0-or-later
// Publish an episode for the docs site: a web-sized WebM (AV1 video, Opus
// audio: free codecs that browsers decode without extra packages) without
// burned-in captions, a poster, WebVTT captions, and a JSON manifest with
// chapters.
//
//   node tools/publish.mjs <project-dir> --static <dir> --data <dir> [--master <mp4>] [--poster <sec>]
//
// The episode composition must call Scenes.episode(...), which publishes
// window.__episode (id, title, duration, chapters, caption cues).
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import { launch } from "./browser.mjs";

const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    static: { type: "string" },
    data: { type: "string" },
    master: { type: "string" },
    poster: { type: "string", default: "2.6" },
    crf: { type: "string", default: "40" },
    preset: { type: "string", default: "10" },
  },
});
const project = path.resolve(positionals[0] || ".");
if (!values.static || !values.data) {
  console.error("usage: publish.mjs <project-dir> --static <dir> --data <dir> [--master <mp4>] [--poster <sec>]");
  process.exit(2);
}

// 1. Episode metadata, read from the composition outside the render runtime.
const browser = await launch();
const page = await browser.newPage();
await page.evaluateOnNewDocument(() => {
  window.__timelines = {};
});
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
await page.goto("file://" + path.join(project, "index.html"));
const ep = await page.evaluate(() => window.__episode);
await browser.close();
if (!ep) {
  console.error(`no window.__episode in ${project}/index.html ${errors.join("; ")}`);
  process.exit(1);
}

// 2. Master render without burned-in captions (the docs player shows the VTT track).
let master = values.master;
if (!master) {
  master = path.join(mkdtempSync(path.join(tmpdir(), "sindy-")), "master.mp4");
  execFileSync("npx", ["hyperframes", "render", "-q", "standard", "--variables", '{"captions":false}', "-o", master], {
    cwd: project,
    stdio: "inherit",
  });
}

// 3. Web encode, poster, captions, manifest.
mkdirSync(values.static, { recursive: true });
mkdirSync(values.data, { recursive: true });
const out = (ext) => path.join(values.static, `${ep.id}.${ext}`);
const ff = (args) => execFileSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...args], { stdio: "inherit" });
// SVT-AV1 preset 10 at CRF 40, tuned for visual quality: about 4 MB per
// minute, smaller and closer to the master than H.264 at CRF 28, and faster
// than real time on 4 vCPUs. The index goes to the front for seeking.
ff(["-i", master, "-c:v", "libsvtav1", "-preset", values.preset, "-crf", values.crf, "-svtav1-params", "tune=0", "-pix_fmt", "yuv420p",
  "-c:a", "libopus", "-b:a", "64k", "-ac", "1", "-cues_to_front", "1", out("webm")]);
ff(["-ss", values.poster, "-i", master, "-frames:v", "1", "-vf", "scale=1280:-2", "-q:v", "3", out("jpg")]);

const stamp = (t) => {
  const ms = Math.round(t * 1000);
  const h = String(Math.floor(ms / 3600000)).padStart(2, "0");
  const m = String(Math.floor(ms / 60000) % 60).padStart(2, "0");
  const s = String(Math.floor(ms / 1000) % 60).padStart(2, "0");
  return `${h}:${m}:${s}.${String(ms % 1000).padStart(3, "0")}`;
};
const vtt = ["WEBVTT", ""];
ep.cues.forEach((c, i) => vtt.push(String(i + 1), `${stamp(c.start)} --> ${stamp(c.end)}`, c.text, ""));
writeFileSync(out("vtt"), vtt.join("\n"));

const clock = (t) => `${Math.floor(t / 60)}:${String(Math.floor(t % 60)).padStart(2, "0")}`;
const manifest = {
  title: ep.title,
  duration: ep.duration,
  length: clock(ep.duration),
  bytes: statSync(out("webm")).size,
  chapters: ep.chapters.map((c) => ({ title: c.title, start: c.start, time: clock(c.start) })),
};
writeFileSync(path.join(values.data, `${ep.id}.json`), JSON.stringify(manifest, null, 2) + "\n");
console.log(`published ${ep.id}: ${clock(ep.duration)}, ${(manifest.bytes / 1048576).toFixed(1)} MB, ${ep.cues.length} cues, ${ep.chapters.length} chapters`);
