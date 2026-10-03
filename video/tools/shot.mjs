// SPDX-License-Identifier: LGPL-3.0-or-later
// Screenshot a local HTML page: node tools/shot.mjs <file.html> <out.png> [query] [width] [height]
import { createRequire } from "node:module";
import path from "node:path";
const require = createRequire(import.meta.url);
let playwright;
try { playwright = require("playwright"); } catch { playwright = require("/opt/node22/lib/node_modules/playwright"); }
const [file, out, query = "", w = "1300", h = "900"] = process.argv.slice(2);
const browser = await playwright.chromium.launch();
const page = await browser.newPage({ viewport: { width: +w, height: +h } });
page.on("console", (m) => console.log("console:", m.text()));
page.on("pageerror", (e) => console.log("pageerror:", e.message));
await page.goto("file://" + path.resolve(file) + (query ? "?" + query : ""));
await page.waitForTimeout(300);
await page.screenshot({ path: out, fullPage: true });
await browser.close();
