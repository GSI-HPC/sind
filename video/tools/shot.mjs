// SPDX-License-Identifier: LGPL-3.0-or-later
// Screenshot a local HTML page: node tools/shot.mjs <file.html> <out.png> [query] [width] [height]
import path from "node:path";
import { launch } from "./browser.mjs";

const [file, out, query = "", w = "1300", h = "900"] = process.argv.slice(2);
const browser = await launch();
const page = await browser.newPage();
await page.setViewport({ width: +w, height: +h });
page.on("console", (m) => console.log("console:", m.text()));
page.on("pageerror", (e) => console.log("pageerror:", e.message));
await page.goto("file://" + path.resolve(file) + (query ? "?" + query : ""));
await new Promise((r) => setTimeout(r, 300));
await page.screenshot({ path: out, fullPage: true });
await browser.close();
