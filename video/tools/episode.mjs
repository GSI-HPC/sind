// SPDX-License-Identifier: LGPL-3.0-or-later
// One entry point for an episode's lifecycle (also what a docs CI job calls).
//
//   node tools/episode.mjs voice   <id|--all>          synthesize narration (cached per line)
//   node tools/episode.mjs lint    <id|--all>          hyperframes lint
//   node tools/episode.mjs render  <id> [--draft]      renders/<id>.mp4 with burned-in captions
//   node tools/episode.mjs publish <id|--all>          web MP4, poster, VTT, manifest into docs/
//
// Episodes live in episodes/<id>/ and <id> matches the docs page they belong to.
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const docs = path.join(root, "..", "docs");
const [cmd, target, ...rest] = process.argv.slice(2);
const usage = "usage: episode.mjs voice|lint|render|publish <id|--all> [--draft]";
if (!cmd || !target) {
  console.error(usage);
  process.exit(2);
}

const all = () =>
  readdirSync(path.join(root, "episodes"), { withFileTypes: true })
    .filter((d) => d.isDirectory() && existsSync(path.join(root, "episodes", d.name, "index.html")))
    .map((d) => d.name);
const ids = target === "--all" ? all() : [target];
const run = (bin, args, cwd) => execFileSync(bin, args, { cwd: cwd || root, stdio: "inherit" });

for (const id of ids) {
  const dir = path.join(root, "episodes", id);
  if (!existsSync(path.join(dir, "index.html"))) {
    console.error(`no episode ${id} (expected ${dir}/index.html)`);
    process.exit(1);
  }
  switch (cmd) {
    case "voice":
      run("python3", ["voice/sindy_voice.py", "script", path.join(dir, "script.json"), "-o", path.join(dir, "assets/voice")]);
      break;
    case "lint":
      run("node", ["tools/sync-vendor.mjs", dir]);
      run("npx", ["hyperframes", "lint"], dir);
      break;
    case "render":
      run("node", ["tools/sync-vendor.mjs", dir]);
      run("npx", ["hyperframes", "render", "-q", rest.includes("--draft") ? "draft" : "standard", "-o", path.join(root, "renders", `${id}.mp4`)], dir);
      break;
    case "publish":
      run("node", ["tools/sync-vendor.mjs", dir]);
      run("node", ["tools/publish.mjs", dir, "--static", path.join(docs, "static/videos"), "--data", path.join(docs, "data/videos")]);
      break;
    default:
      console.error(usage);
      process.exit(2);
  }
}
