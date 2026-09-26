---
name: steward
description: Drive a sind pull request to a mergeable state. Covers choosing the base branch, the local checks to run before every push, updating the branch by rebasing onto its target, and triaging CI failures. Use when opening, updating, or watching a PR in this repository.
---

# Steward a sind PR

## Base branch

- Features, fixes, refactors and docs target `next`.
- Only configuration that takes effect from the default branch (e.g.
  `.github/dependabot.yml`) targets `main`.
- One logical change per PR. Agent branches are named `claude/<topic>`.

## Before every push

1. `gofmt -l cmd pkg internal` prints nothing and `make lint` reports 0 issues.
2. `go mod download && make test` passes (firejail sandbox; install firejail if missing).
3. `go vet -tags integration ./...` is clean. Integration tests need Docker, which cloud
   sessions lack, so CI is their only run.
4. If `pkg/` or `internal/` changed: `make check-coverage`. Every file there must stay
   at 100%. Install the tool with
   `go install github.com/vladopajic/go-test-coverage/v2@latest`.
5. If `docs/content/` changed: `make lint-docs`.
6. If the CLI surface, output format, config schema or labels changed: `DESIGN.md` and
   `docs/content/` are updated in the same PR.
7. Re-read the diff: SPDX header on new Go files, tests for new behaviour,
   Conventional Commit messages with bullet bodies.

## Updating the branch

- Rebase onto the PR target, never merge the target into the branch:
  `git fetch origin <target> && git rebase origin/<target>`, resolve conflicts, rerun
  the checks above, then `git push --force-with-lease`.
- Resolve `go.mod`/`go.sum` conflicts by taking the target's side and re-running
  `go mod tidy`, never by hand-editing `go.sum`.

## CI (`.github/workflows/ci.yml`)

| Job | Runs |
|-----|------|
| Lint | golangci-lint |
| Unit Test | `make test` under firejail |
| Unit Test Coverage | go-test-coverage against `.testcoverage.yml` |
| Integration Test | builds the node image with `docker buildx bake`, then `make test-integration` |
| Build | `make build` |

- Integration Test downloads the Slurm, UCX, PMIx, PRRTE and Open MPI tarballs while
  building the image. A failure in that build step with a network error on one of those
  URLs (e.g. `download.schedmd.com ... i/o timeout`) is an upstream outage, not this
  PR's fault. Confirm it by the error naming the download URL, re-run the job once, and
  report it if it fails again.
- Every other failure is this PR's to root-cause and fix. Never skip, disable or
  loosen tests or coverage thresholds.

## GitHub etiquette

- Ask the maintainer before posting any comment or review reply, and never @-mention
  anyone.
- Never merge PRs, push to `main`/`next`, or create tags or releases.
