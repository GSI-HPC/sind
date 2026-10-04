// SPDX-License-Identifier: LGPL-3.0-or-later
// Copy shared runtime files into each HyperFrames project's vendor/ directory.
// Compositions must not fetch from CDNs at render time, and HyperFrames serves
// only the project directory, so every project gets its own copy.
//   node tools/sync-vendor.mjs [project-dir ...]   (default: every episode in episodes/)
import { cpSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const nm = path.join(root, "node_modules");
const files = {
  "gsap.min.js": path.join(nm, "gsap/dist/gsap.min.js"),
  "sindy.js": path.join(root, "lib/sindy.js"),
  "scenes.js": path.join(root, "lib/scenes.js"),
  "episode.js": path.join(root, "lib/episode.js"),
  "scenes.css": path.join(root, "lib/scenes.css"),
  "fonts/inter-400.woff2": path.join(nm, "@fontsource/inter/files/inter-latin-400-normal.woff2"),
  "fonts/inter-600.woff2": path.join(nm, "@fontsource/inter/files/inter-latin-600-normal.woff2"),
  "fonts/inter-800.woff2": path.join(nm, "@fontsource/inter/files/inter-latin-800-normal.woff2"),
  "fonts/jetbrains-mono-400.woff2": path.join(nm, "@fontsource/jetbrains-mono/files/jetbrains-mono-latin-400-normal.woff2"),
  "fonts/jetbrains-mono-700.woff2": path.join(nm, "@fontsource/jetbrains-mono/files/jetbrains-mono-latin-700-normal.woff2"),
};

let projects = process.argv.slice(2);
if (!projects.length) {
  const dir = path.join(root, "episodes");
  projects = readdirSync(dir, { withFileTypes: true })
    .filter((d) => d.isDirectory() && existsSync(path.join(dir, d.name, "hyperframes.json")))
    .map((d) => path.join(dir, d.name));
}
for (const p of projects) {
  for (const [dst, src] of Object.entries(files)) {
    if (!existsSync(src)) continue;
    const out = path.join(p, "vendor", dst);
    mkdirSync(path.dirname(out), { recursive: true });
    cpSync(src, out);
  }
  console.log(`vendor synced: ${path.relative(root, p) || "."}`);
}
