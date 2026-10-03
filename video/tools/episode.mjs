// SPDX-License-Identifier: LGPL-3.0-or-later
// One entry point for an episode's lifecycle.
//
//   node tools/episode.mjs voice   <id|--all>             synthesize narration (cached per line)
//   node tools/episode.mjs lint    <id|--all>             hyperframes lint
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
  options: { draft: { type: "boolean" }, all: { type: "boolean" }, store: { type: "string" }, used: { type: "string" } },
});
const [cmd, target] = positionals;
const usage = "usage: episode.mjs voice|lint|render|publish|hash|ci <id|--all> [--draft] [--store <dir> --used <file>]";
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

for (const id of ids) {
  const dir = path.join(root, "episodes", id);
  if (!existsSync(path.join(dir, "index.html"))) {
    console.error(`no episode ${id} (expected ${dir}/index.html)`);
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
