// The video course's pages (docs/content/course/) copy their commands and
// output from the reference pages. An episode that only course pages embed
// must therefore also match a page outside the course, or its copy went
// stale. Runs after the library's grounding check (avatars.json "checks").
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

function markdown(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((d) => {
    const p = path.join(dir, d.name);
    return d.isDirectory() ? markdown(p) : d.name.endsWith(".md") ? [p] : [];
  });
}

export default async ({ id, episode, project, warn }) => {
  const content = path.resolve(project.root, "../docs/content");
  const course = path.join(content, "course") + path.sep;
  const embed = new RegExp(`\\{\\{<\\s*video\\s+"${id}"\\s*>\\}\\}`);
  const files = markdown(content);
  const pages = files.filter((f) => embed.test(readFileSync(f, "utf8")));
  if (!pages.length || !pages.every((f) => f.startsWith(course))) return;
  const refText = files.filter((f) => !f.startsWith(course)).map((f) => readFileSync(f, "utf8")).join("\n");
  const refLines = new Set(refText.split("\n").map((l) => l.trimEnd()));
  for (const item of episode.terminal || []) {
    const at = typeof item.at === "number" ? ` at ${item.at.toFixed(1)} s` : "";
    if (item.cmd != null && !refText.includes(item.cmd)) warn(`command on no reference page outside docs/content/course${at}: ${item.cmd}`);
    if (item.out == null) continue;
    for (const l of String(item.out).split("\n").map((x) => x.trimEnd()).filter(Boolean)) {
      if (!refLines.has(l)) warn(`${item.code ? `line of ${item.code}` : "output line"} on no reference page outside docs/content/course${at}: ${l}`);
    }
  }
};
