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

CI runs for every pull request and every push to `main` and `next`.

| Job | Runs |
|-----|------|
| Lint | golangci-lint |
| Unit Test | `make test` under firejail, writing the coverage profile |
| Unit Test Coverage | go-test-coverage against `.testcoverage.yml`, on the Unit Test job's coverage profile |
| Vulnerability Check | `govulncheck ./...`: fails on a known vulnerability in code sind calls |
| Image Targets | lists the bake targets and platforms the Integration Test jobs run (`docker/bake-action/subaction/matrix`) |
| Integration Test (slurm-YY-MM, platform) | one job per Slurm release line (bake target) and platform, on a native runner (`ubuntu-24.04-arm` for linux/arm64): `sind doctor`, builds that node image with `docker buildx bake` (BuildKit from `mirror.gcr.io`), pulls the Docker Hub images the tests run from `mirror.gcr.io`, then `make test-integration` |
| Integration Test | passes when every Integration Test (slurm-YY-MM, platform) job passed |
| Build | `make build` |
| Release Snapshot | `goreleaser release --snapshot --clean` (`.goreleaser.yaml`, every release platform) |

- Integration Test fetches sources while building the image: the Slurm, UCX, PMIx,
  PRRTE and Open MPI tarballs, and a `git clone` of libjwt from github.com. A failure in
  that build step with a network error on any source fetch of the Dockerfile (e.g.
  `download.schedmd.com ... i/o timeout`, or `git clone ... benmcollins/libjwt` failing
  to connect) is an upstream outage, not this PR's fault. Confirm it by the error naming
  the URL, re-run the job once, and report it if it fails again.
- Integration Test pulls the Docker Hub images the tests run (the mesh's CoreDNS, which
  it reads from `pkg/mesh`'s `DNSImage`, and `busybox:latest`) from `mirror.gcr.io` and
  tags them with their Docker Hub names, so the tests never pull them from Docker Hub.
  A `toomanyrequests` or an `auth.docker.io` timeout from Docker Hub means the job pulls
  another Docker Hub image: take it from `mirror.gcr.io` too.
- A Vulnerability Check failure on a new advisory in a dependency or the Go toolchain
  that this PR does not touch is fixed by bumping that module (`go get <module>@<fixed>`,
  `go mod tidy`) or the toolchain in `go.mod`, in its own commit.
- A push to a PR cancels the CI run of the commit it replaces. A cancelled run is not a
  failure; look at the run of the PR's head commit.
- The image workflows (`image.yml`, `image-build.yml`) publish to GHCR and never run for
  a PR: check a change to them with `actionlint`, and `docker buildx bake --print` for
  `docker-bake.hcl`.
- The workflows pin every action to a commit SHA, which Dependabot updates, and the
  versions of goreleaser, golangci-lint, gotestsum, govulncheck, Hugo and the docs theme
  (with its sha256), which it does not: bump those by hand.
- Every other failure is this PR's to root-cause and fix. Never skip, disable or
  loosen tests or coverage thresholds.

## GitHub etiquette

- Ask the maintainer before posting any comment or review reply, and never @-mention
  anyone.
- Never merge PRs, push to `main`/`next`, or create tags or releases.
