---
weight: 640
title: "Conventions"
description: "Commit style, code patterns, and contribution guidelines"
toc: true
---

## Commit messages

The project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

[optional body]
```

### Types

`feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`

### Principles

- **Fine-grained commits** — each commit is a single logical change
- **Context-free messages** — state what the commit does, not the development story
- **No narrative** — avoid "I tried X, then Y"; state the result
- **Bullet lists** in the body, not prose paragraphs

### Examples

```
feat(cluster): add worker node lifecycle commands

- sind create worker with --count, --image, --unmanaged flags
- sind delete worker with nodeset expansion
- Update sind-nodes.conf and reconfigure slurmctld for managed nodes
```

```
fix(probe): handle systemd degraded state as ready

- systemctl is-system-running returns "degraded" when non-critical
  units fail, which is expected in containers
```

### AI-assisted commits

Commits written with substantial help from an AI coding tool carry a `Co-Authored-By:` trailer naming the tool:

```
fix(mesh): tolerate stopped DNS container when updating records

- ...

Co-Authored-By: Claude <noreply@anthropic.com>
```

Commits made by Anthropic's Claude Code agent are additionally authored as `Claude <noreply@anthropic.com>`. Please follow the same convention in your contributions.

## Code style

- Standard Go conventions (`gofmt`, `go vet`)
- Use `testify` for assertions (`assert` for non-fatal, `require` for fatal)
- Strong types for Docker identifiers (`ContainerName`, `NetworkName`, etc.)
- Error wrapping with `fmt.Errorf("context: %w", err)`

## License headers

All Go source files include the SPDX header:

```go
// SPDX-License-Identifier: LGPL-3.0-or-later
```

## Documentation

The docs are a Hugo site in `docs/`. A page that tells how to do a task shows its steps for the CLI or for the web UI, as the reader selects at the top of the page. The browser keeps the choice for all pages, and a link with `?via=ui` selects the web UI. Write the two variants of the steps in a `ways` block:

```markdown
## Add workers

{{</* ways */>}}
{{</* cli */>}}

    sind create worker [CLUSTER] [FLAGS]

{{</* /cli */>}}
{{</* ui */>}}

1. On the page of the cluster, press **Add workers…**.
2. ...

{{</* uishot "dialog-create-worker" */>}}

{{</* /ui */>}}
{{</* /ways */>}}
```

- Keep the heading and the text that is true for both outside the block. Only the steps change.
- A web UI variant has one to four numbered steps, one screenshot and, for a dialog with fields, a table of its fields and their flags.
- A page that the web UI cannot do sets `webui: none` in its front matter. It then shows a note in web UI mode: `webuiNote`, or a default.
- `uishot "NAME"` shows a screenshot of `sind ui`, light or dark as the page, with the alt text of the shot. `NAME` is a shot of `tools/screenshots/shots.yaml`; add one there for a new screenshot. The docs workflow takes the screenshots, and `make docs-shots` takes them locally, which needs Chromium. They are not committed.

## Pull requests

- Target the `next` branch; `main` only moves at release
- One logical change per PR
- Include tests for new functionality
- Ensure `make lint`, `make test` and `make check-coverage` pass
- Follow the commit message conventions above
