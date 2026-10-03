// SPDX-License-Identifier: LGPL-3.0-or-later
// Launch the Chrome that HyperFrames renders with, through puppeteer-core, so
// tools need no second browser. HYPERFRAMES_BROWSER_PATH overrides the binary.
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import puppeteer from "puppeteer-core";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

export function browserPath() {
  const env = process.env.HYPERFRAMES_BROWSER_PATH;
  if (env && existsSync(env)) return env;
  // `browser path` finds or downloads chrome-headless-shell and prints its path last.
  const out = execFileSync("npx", ["hyperframes", "browser", "path"], { cwd: root, encoding: "utf8" });
  return out.trim().split("\n").pop().trim();
}

export function launch() {
  return puppeteer.launch({
    executablePath: browserPath(),
    headless: true,
    args: ["--no-sandbox", "--allow-file-access-from-files"],
  });
}
