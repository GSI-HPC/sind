# CLAUDE.md

sind (Slurm in Docker) is a Go CLI and library that creates and manages containerized
Slurm clusters, inspired by kind. `DESIGN.md` is the authoritative design document
(CLI surface, config schema, networking, naming); `docs/content/` is the Hugo docs site.
Personal, uncommitted instructions belong in `CLAUDE.local.md` (gitignored).

## Branches and PRs

- Feature and fix PRs target `next`. `main` only moves at release, when the maintainer
  updates it from `next` and publishes a GitHub release `vX.Y.Z`.
- Exception: changes that only take effect on the default branch (e.g.
  `.github/dependabot.yml`) target `main`.
- Update a feature branch by rebasing it onto its PR target (never merge the target in),
  then `git push --force-with-lease`.
- Never push to `main`/`next`, create tags or releases, or merge PRs.
- Ask the maintainer before commenting on issues or PRs, and never @-mention anyone.
- `.claude/skills/steward` has the PR/CI routine; `.claude/skills/bump-image-deps`
  covers Slurm and other node-image version bumps.

## Commands

```bash
make build                   # ./sind-<os>-<arch>
make lint                    # golangci-lint v2 (.golangci.yml, gofmt + goimports)
go mod download && make test # unit tests, -race, sandboxed with firejail
make test-integration        # needs Docker and SIND_TEST_IMAGE (see ci.yml)
go vet -tags integration ./... # compile-check integration tests without Docker
make check-coverage          # go-test-coverage thresholds (.testcoverage.yml)
make lint-docs               # markdownlint-cli2 on docs/content
```

- `make test` runs inside firejail with systemctl, docker, ssh, resolvectl and polkit
  tools blacklisted; a unit test that forgets to inject a mock fails instead of touching
  the host. Prime the module cache first because firejail breaks DNS.
- Coverage: every file under `pkg/` and `internal/` must stay at 100%; other packages
  50%, total 80%.
- Cloud sessions have no Docker daemon and may lack firejail
  (`apt-get install -y firejail`); integration tests then only run in CI.
- If golangci-lint fails with "the Go language version ... used to build golangci-lint
  is lower than the targeted Go version", rebuild it with the toolchain from `go.mod`:
  `GOTOOLCHAIN=go$(go list -m -f '{{.GoVersion}}') go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.

## Code conventions

- Every Go file starts with `// SPDX-License-Identifier: LGPL-3.0-or-later`.
- `cmd/sind` stays thin (arg parsing, flags, output); logic lives in `pkg/cluster`.
- Every external command goes through `cmdexec.Executor`; unit tests use
  `internal/mock.Executor` (FIFO `AddResult` or `OnCall` dispatcher for concurrent code).
- Dual-mode tests: `mode_unit_test.go` (`!integration`) and `mode_integration_test.go`
  (`integration`) provide the per-package helpers.
- testify: `require` for fatal checks, `assert` otherwise. Wrap errors with
  `fmt.Errorf("context: %w", err)`. Use strong types for Docker identifiers.
- Follow the "CLI Design Guidelines" in `DESIGN.md` (verb-noun commands, positional
  cluster names, silent mutations, `-o json` on every `get` subcommand).
- Package map and patterns: `docs/content/contributing/architecture-guide.md`.

## Docs

Keep `DESIGN.md` and `docs/content/` in sync with code changes in the same PR: CLI
flags and output samples, config schema, labels, versions. `next` publishes a preview
docs site; `main` publishes the release docs.

## Commits

- Conventional Commits `<type>(<scope>): <description>`; types `feat fix docs style
  refactor test chore build ci`. One logical change per commit; bullet-list bodies that
  state what changed and why, no development narrative.
- Close issues with `Closes #N` in the commit body; they close once the commit reaches
  `main`.
- Commits by Claude Code are authored as `Claude <noreply@anthropic.com>` and carry a
  `Co-Authored-By: Claude …` trailer. Keep both; the README AI disclosure relies on them.

## sind-action

[GSI-HPC/sind-action](https://github.com/GSI-HPC/sind-action) drives this CLI in CI:
`sind version --json`, `sind doctor`, `sind create cluster --config`, `sind get cluster`
(`sind status` before v0.9.0), `sind delete cluster --all`, and `SIND_REALM`. Removing
or renaming any of these needs a matching sind-action change.
