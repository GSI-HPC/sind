// SPDX-License-Identifier: LGPL-3.0-or-later
// Print mouth-open statistics for a voice line: node tools/mouth-stats.mjs <line.json>
import { readFileSync } from "node:fs";
import path from "node:path";
import { launch } from "./browser.mjs";

const line = JSON.parse(readFileSync(process.argv[2], "utf8"));
const browser = await launch();
const page = await browser.newPage();
await page.setContent("<div id=a style='width:300px;height:400px'></div>");
await page.addScriptTag({ path: path.resolve("lib/sindy.js") });
const out = await page.evaluate((line) => {
  const s = Sindy.create(document.getElementById("a"), { speech: { offset: 0, visemes: line.visemes, envelope: line.envelope } });
  const vals = [];
  for (let t = 0; t < line.duration; t += 1 / 30) vals.push(s.mouthAt(t).open);
  return vals;
}, line);
await browser.close();
const sorted = [...out].sort((a, b) => a - b);
const q = (p) => sorted[Math.floor(p * (sorted.length - 1))].toFixed(2);
console.log(`frames ${out.length}  p10 ${q(0.1)}  p50 ${q(0.5)}  p75 ${q(0.75)}  p90 ${q(0.9)}  max ${q(1)}`);
console.log(out.slice(0, 60).map((v) => "▁▂▃▄▅▆▇█"[Math.min(7, Math.floor(v * 8))]).join(""));
