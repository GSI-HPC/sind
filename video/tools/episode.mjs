// SPDX-License-Identifier: LGPL-3.0-or-later
// One entry point for an episode's lifecycle.
//
//   node tools/episode.mjs voice   <id|--all>             synthesize narration (cached per line)
//   node tools/episode.mjs lint    <id|--all>             hyperframes lint
//   node tools/episode.mjs check   <id|--all> [--quick]   timeline, warnings, docs drift, lint, stills
//   node tools/episode.mjs render  <id|--all> [--draft]   renders/<id>.mp4 with burned-in captions
//   node tools/episode.mjs publish <id|--all>             web MP4, poster, VTT, manifest into ../docs
//   node tools/episode.mjs hash    <id|--all>             input hash that identifies a render
//   node tools/episode.mjs ci      <id|--all> --store <dir> [--used <file>]
//
// `ci` is what the docs workflow runs: it renders an episode only when
// <store>/<id>-<hash>/ does not exist yet, then copies the published files
// into ../docs. The hash covers the episode and everything shared that can
// change its pixels or sound, so a docs-only push re-renders nothing.
// Episodes live in episodes/<id>/ and <id> matches the docs page they belong to.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { appendFileSync, cpSync, existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const docs = path.join(root, "..", "docs");
const { values: opt, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    draft: { type: "boolean" },
    all: { type: "boolean" },
    quick: { type: "boolean" },
    store: { type: "string" },
    used: { type: "string" },
  },
});
const [cmd, target] = positionals;
const usage = "usage: episode.mjs voice|lint|check|render|publish|hash|ci <id|--all> [--draft] [--quick] [--store <dir> --used <file>]";
if (!cmd || (!target && !opt.all)) {
  console.error(usage);
  process.exit(2);
}

// Inputs shared by every episode, relative to video/.
const SHARED = [
  "lib",
  "package-lock.json",
  "voice/sindy_voice.py",
  "voice/voices.json",
  "voice/lexicon.json",
  "voice/requirements.txt",
  "tools/publish.mjs",
  "tools/sync-vendor.mjs",
  "tools/browser.mjs",
];
// Generated or local-only paths inside an episode.
const SKIP = new Set(["node_modules", "vendor", "renders", "snapshots", ".hyperframes", path.join("assets", "voice")]);

function filesUnder(rel, base) {
  const abs = path.join(root, rel);
  if (!existsSync(abs)) return [];
  if (statSync(abs).isFile()) return [rel];
  return readdirSync(abs, { withFileTypes: true }).flatMap((d) => {
    const sub = path.join(rel, d.name);
    if (SKIP.has(path.relative(base, sub)) || SKIP.has(d.name)) return [];
    return d.isDirectory() ? filesUnder(sub, base) : [sub];
  });
}

function hashOf(id) {
  const dir = path.join("episodes", id);
  const files = [...filesUnder(dir, dir), ...SHARED.flatMap((p) => filesUnder(p, p))].sort();
  const h = createHash("sha256");
  for (const rel of files) {
    h.update(rel.split(path.sep).join("/") + "\0");
    h.update(readFileSync(path.join(root, rel)));
    h.update("\0");
  }
  return h.digest("hex").slice(0, 16);
}

const episodes = () => {
  const dir = path.join(root, "episodes");
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true })
    .filter((d) => d.isDirectory() && existsSync(path.join(dir, d.name, "index.html")))
    .map((d) => d.name);
};
const ids = opt.all ? episodes() : [target];
const run = (bin, args, cwd) => execFileSync(bin, args, { cwd: cwd || root, stdio: "inherit" });
const voice = (dir) => run("python3", ["voice/sindy_voice.py", "script", path.join(dir, "script.json"), "-o", path.join(dir, "assets/voice")]);
const publish = (dir, out) => {
  run("node", ["tools/sync-vendor.mjs", dir]);
  run("node", ["tools/publish.mjs", dir, "--static", out.static, "--data", out.data]);
};
const docsOut = { static: path.join(docs, "static/videos"), data: path.join(docs, "data/videos") };

// Docs pages that embed an episode with {{< video "<id>" >}}.
function docsPages(id) {
  const walk = (dir) =>
    readdirSync(dir, { withFileTypes: true }).flatMap((d) => {
      const p = path.join(dir, d.name);
      return d.isDirectory() ? walk(p) : d.name.endsWith(".md") ? [p] : [];
    });
  const re = new RegExp(`\\{\\{<\\s*video\\s+"${id}"\\s*>\\}\\}`);
  return walk(path.join(docs, "content")).filter((f) => re.test(readFileSync(f, "utf8")));
}

const clock = (t) => `${Math.floor(t / 60)}:${(t % 60).toFixed(1).padStart(4, "0")}`;

// Load the composition outside the render runtime and report what an author
// cannot see in a still: the timeline, console warnings, stale narration and
// terminal content that drifted from the docs page. Errors fail the command.
async function check(id, dir) {
  const errors = [];
  const warnings = [];
  run("node", ["tools/sync-vendor.mjs", dir]);
  if (!existsSync(path.join(dir, "assets/voice/lines.js"))) {
    console.error(`${id}: no narration, run: npm run episode -- voice ${id}`);
    return false;
  }

  const { launch } = await import("./browser.mjs");
  const browser = await launch();
  const page = await browser.newPage();
  await page.evaluateOnNewDocument(() => {
    window.__timelines = {};
  });
  page.on("console", (m) => {
    if (["warn", "warning", "error"].includes(m.type())) warnings.push(`console: ${m.text()}`);
  });
  page.on("pageerror", (e) => errors.push(`page error: ${e.message}`));
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.protocol === "file:" && !existsSync(decodeURIComponent(u.pathname))) errors.push(`missing file: ${path.relative(dir, decodeURIComponent(u.pathname))}`);
    if (u.protocol.startsWith("http")) errors.push(`fetches from the network (renders must not): ${r.url()}`);
  });
  await page.goto("file://" + path.join(dir, "index.html"));
  const ep = await page.evaluate(() => window.__episode);
  // Text that does not fit its box is cut off with an ellipsis.
  const clipped = await page.evaluate(async () => {
    await document.fonts.ready;
    return [...document.querySelectorAll(".dnode .t1, .dnode .t2")].filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent);
  });
  for (const text of clipped) warnings.push(`text cut off in a diagram node: "${text}"; widen the node (width) or shorten the text`);
  await browser.close();
  if (!ep) {
    errors.push("no window.__episode: the page must end with Episode.create(...)...done()");
    errors.forEach((e) => console.error(`  error: ${e}`));
    return false;
  }

  // Timeline.
  console.log(`\n${id}: ${clock(ep.duration)} (${ep.duration.toFixed(1)} s), ${ep.chapters.length} chapters, ${ep.lines.length} lines`);
  ep.chapters.forEach((c, i) => {
    const end = i + 1 < ep.chapters.length ? ep.chapters[i + 1].start : ep.duration;
    console.log(`  ${clock(c.start).padStart(6)}  ${(end - c.start).toFixed(1).padStart(5)} s  ${c.title}`);
    for (const l of ep.lines.filter((l) => l.start >= c.start && l.start < end))
      console.log(`  ${clock(l.start).padStart(14)}  ${l.id.padEnd(12)} ${l.text.length > 64 ? l.text.slice(0, 63) + "…" : l.text}`);
  });

  // Narration: every script line voiced, up to date and used.
  const script = JSON.parse(readFileSync(path.join(dir, "script.json"), "utf8"));
  for (const line of script.lines) {
    const said = ep.lines.find((l) => l.id === line.id);
    if (!said) warnings.push(`line "${line.id}" is in script.json but never said`);
    else if (said.text !== line.text) errors.push(`line "${line.id}" changed since it was voiced, run: npm run episode -- voice ${id}`);
  }
  for (let i = 1; i < ep.lines.length; i++) {
    const gap = ep.lines[i].start - ep.lines[i - 1].end;
    if (gap > 2.5) warnings.push(`${gap.toFixed(1)} s without narration before "${ep.lines[i].id}" at ${clock(ep.lines[i].start)}`);
  }

  // Terminal content must come from the docs page.
  const pages = docsPages(id);
  if (!pages.length) warnings.push(`no docs page embeds {{< video "${id}" >}}`);
  const docText = pages.map((f) => readFileSync(f, "utf8")).join("\n");
  const docLines = new Set(docText.split("\n").map((l) => l.trimEnd()));
  const where = (t) => `at ${clock(t)}`;
  for (const item of ep.terminal) {
    if (!pages.length) break;
    if (item.cmd != null && !docText.includes(item.cmd)) warnings.push(`command not on the docs page ${where(item.at)}: ${item.cmd}`);
    if (item.out != null)
      for (const l of item.out.split("\n").map((x) => x.trimEnd()).filter(Boolean))
        if (!docLines.has(l)) warnings.push(`output line not on the docs page ${where(item.at)}: ${l}`);
  }

  if (!opt.quick) {
    try {
      run("npx", ["hyperframes", "lint"], dir);
    } catch {
      errors.push("hyperframes lint failed");
    }
    // Two stills per chapter: mid-way and fully built just before the next one.
    const times = ep.chapters.flatMap((c, i) => {
      const end = i + 1 < ep.chapters.length ? ep.chapters[i + 1].start : ep.duration - 0.9;
      return [(c.start + end) / 2, end - 0.35];
    });
    const out = path.join(dir, "snapshots");
    rmSync(out, { recursive: true, force: true });
    try {
      run("npx", ["hyperframes", "snapshot", "--no-end", "--describe", "false", "-o", out, "--at", times.map((t) => t.toFixed(2)).join(",")], dir);
      const sheets = readdirSync(out).filter((f) => f.startsWith("contact-sheet"));
      console.log(`stills, two per chapter (middle, end): ${sheets.map((f) => path.relative(root, path.join(out, f))).join(", ")}`);
    } catch {
      errors.push("hyperframes snapshot failed");
    }
  }

  warnings.forEach((w) => console.log(`  warning: ${w}`));
  errors.forEach((e) => console.error(`  error: ${e}`));
  console.log(`${id}: ${errors.length} errors, ${warnings.length} warnings`);
  return errors.length === 0;
}

for (const id of ids) {
  const dir = path.join(root, "episodes", id);
  // Narration can be voiced before the composition exists.
  const needs = cmd === "voice" ? "script.json" : "index.html";
  if (!existsSync(path.join(dir, needs))) {
    console.error(`no episode ${id} (expected ${dir}/${needs})`);
    process.exit(1);
  }
  switch (cmd) {
    case "voice":
      voice(dir);
      break;
    case "lint":
      run("node", ["tools/sync-vendor.mjs", dir]);
      run("npx", ["hyperframes", "lint"], dir);
      break;
    case "check":
      if (!(await check(id, dir))) process.exitCode = 1;
      break;
    case "render":
      run("node", ["tools/sync-vendor.mjs", dir]);
      run("npx", ["hyperframes", "render", "-q", opt.draft ? "draft" : "standard", "-o", path.join(root, "renders", `${id}.mp4`)], dir);
      break;
    case "publish":
      publish(dir, docsOut);
      break;
    case "hash":
      console.log(`${id}-${hashOf(id)}`);
      break;
    case "ci": {
      if (!opt.store) {
        console.error(usage);
        process.exit(2);
      }
      const key = `${id}-${hashOf(id)}`;
      const entry = path.join(opt.store, key);
      if (existsSync(path.join(entry, `${id}.json`))) {
        console.log(`${key}: cached`);
      } else {
        console.log(`${key}: rendering`);
        const tmp = `${entry}.tmp`;
        rmSync(tmp, { recursive: true, force: true });
        voice(dir);
        publish(dir, { static: tmp, data: tmp });
        renameSync(tmp, entry);
      }
      mkdirSync(docsOut.static, { recursive: true });
      mkdirSync(docsOut.data, { recursive: true });
      for (const ext of ["mp4", "jpg", "vtt"]) cpSync(path.join(entry, `${id}.${ext}`), path.join(docsOut.static, `${id}.${ext}`));
      cpSync(path.join(entry, `${id}.json`), path.join(docsOut.data, `${id}.json`));
      if (opt.used) appendFileSync(opt.used, key + "\n");
      break;
    }
    default:
      console.error(usage);
      process.exit(2);
  }
}
