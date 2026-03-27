---
weight: 640
title: "Conventions"
icon: "gavel"
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

## Pull requests

- One logical change per PR
- Include tests for new functionality
- Ensure all existing tests pass (`go test ./...`)
- Follow the commit message conventions above
