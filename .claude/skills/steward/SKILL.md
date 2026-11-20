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
5. If `docs/content/` changed: `make lint-docs`, which the Docs Lint job of CI runs
   too. If anything under `docs/` or `tools/screenshots/shots.yaml` changed and Hugo
   is installed: `make check-docs`, the Docs Build job of CI. If a workflow or a shell script changed: `make lint-ci` (actionlint,
   `go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`, and shellcheck,
   `pip install shellcheck-py`). The Workflow Lint job of CI also runs `make audit-ci`
   (zizmor): fix what it reports, or list the finding in `.github/zizmor.yml` with
   the reason. If `internal/ui` or `tools/screenshots` changed and Chromium is
   installed: `make check-ui`, the UI Pages job of CI; a new page, tab or dialog gets
   a shot in `tools/screenshots/shots.yaml`. If a script of
   `internal/ui/web/static/js` that has a test in `internal/ui/jstest` changed:
   `make test-js`, which the same job runs.
6. If the CLI surface, output format, config schema or labels changed: `DESIGN.md` and
   `docs/content/` are updated in the same PR.
7. Re-read the diff: SPDX header on new Go files, tests for new behaviour,
   Conventional Commit messages with bullet bodies.
8. A new function that reads text sind did not write (a file, the output of a command,
   an argument) gets a fuzz target in the `fuzz_test.go` of its package; run it for a
   minute: `go test ./<pkg> -run '^$' -fuzz '^FuzzXxx$' -fuzztime 1m`. A rule that holds
   for every input of a kind (what one function writes, another reads back; an edit
   keeps a file well-formed) gets a property test in `property_test.go`; run it with
   `-rapid.checks=10000` once. A bug fix gets a test that fails without the fix.
   For new logic in a package that parses or generates text, run
   `MUTATION_PACKAGES=<pkg> make mutation` and add a test for each mutant in the new
   code that lived, or say in the PR why the changed code is as right as the old.
9. If the change touches the readiness wait (`internal/probe`, the broadcast loop of
   `internal/monitor`), the realm lock (`pkg/state/daemonlock.go`), the mesh network
   and its DNS container (`ensureMeshNetwork` and `ensureDNS` of `pkg/mesh/mesh.go`,
   and what `pkg/cluster/create.go` does with `Created`), the steps of a create that
   make something a delete removes (`Create`, `createResources`, `registerNodes`) or the
   delete of a cluster (`pkg/cluster/delete.go`): change the TLA+ model in `spec/tla` with it, and
   run `make check-models` (Java 11 or later). See "Design models" in
   `docs/content/contributing/testing.md`.

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
| Workflow Lint | `make lint-ci`: actionlint on the workflows, with shellcheck on their `run` steps, and shellcheck on every `*.sh`. Then `make audit-ci`: zizmor on the workflows and `.github/dependabot.yml`, with the token of the job |
| Design Models | `make check-models`: TLC on the TLA+ models of `spec/tla`; each configuration must give the result in its `\* expect:` line |
| Docs Lint | `make lint-docs`: markdownlint-cli2 on `docs/content`, on the runner's Node |
| Docs Build | `make check-docs`: Hugo builds the site, without videos and screenshots; a broken shortcode, template or `relref` fails, and so does a warning. Then `.github/scripts/check-doc-links.py` fails for a link into the site whose page or `#fragment` does not exist; it does not check links to other sites |
| UI Pages | `make test-js`: the runner's Node runs the tests of the page scripts (`internal/ui/jstest`). Then `make check-ui`: the runner's Chrome opens each shot of `tools/screenshots/shots.yaml` on `sind ui --demo --still`; an uncaught exception, a `console.error`, a failed request or a status of 400 or more fails. Then it opens 8 tabs at once, 6 of them hidden: a tab that does not show its page fails |
| Unit Test | `make test` under firejail, writing the coverage profile, then `go vet` and `go test` of `tools/screenshots`, a module of its own |
| Unit Test Coverage | go-test-coverage against `.testcoverage.yml`, on the Unit Test job's coverage profile |
| Vulnerability Check | `govulncheck ./...`: fails on a known vulnerability in code sind calls |
| Oldest Go | `go build` and `go vet` of `./pkg/...` with the Go of the `go` line of `go.mod` (not its `toolchain` line), the oldest a library caller may use |
| Image Targets | lists the bake targets and platforms the Integration Test jobs run (`docker/bake-action/subaction/matrix`) |
| Integration Test (slurm-YY-MM, platform) | one job per Slurm release line (bake target) and platform, on a native runner (`ubuntu-26.04-arm` for linux/arm64): raises `fs.inotify.max_user_instances`, `sind doctor` (its inotify check must pass too), builds that node image with `docker buildx bake` (BuildKit from `mirror.gcr.io`), pulls the Docker Hub images the tests run from `mirror.gcr.io`, then `make test-integration`, then fails when Docker still holds a container, network or volume with the `sind.realm` label |
| Integration Test | passes when every Integration Test (slurm-YY-MM, platform) job passed |
| Build | `goreleaser release --snapshot --clean` (`.goreleaser.yaml`, every release platform) |

The API workflow (`.github/workflows/api.yml`) runs for pull requests, also when a label
or the base branch changes. Its job API Compatibility runs `.github/scripts/api-check.sh`
against the base the PR merges onto (the first parent of its merge commit):
it fails on an incompatible change of a package under `pkg/`, or of go-clikit's
`progress`, unless the PR has the `breaking-api` label. A PR that means the break gets
the label from the maintainer, and its commits are marked `!` with `BREAKING CHANGE:`.

The Nightly workflow (`.github/workflows/nightly.yml`) runs from `main` each night and
checks out `next`: govulncheck, the unit tests three times in a random order, every
fuzz target for three minutes (`make fuzz`), and the mutation score (`make mutation`),
which is in the summary of the Mutation Score job and fails no run. Its Report job
keeps one open issue, labelled `nightly`, that names the jobs that failed. Run a shuffled order again with the
seed that `go test` prints: `go test -shuffle=<seed>`. An input that a fuzz target fails
on is in the summary of the Fuzz job and in its `fuzz-inputs` artifact: put it at the
same path under `testdata/fuzz/`, fix the defect, and add the input to the target's
seeds.

- A leftover that the Integration Test job names after the tests passed is a test
  without `t.Cleanup`, or a delete that leaves a resource behind: find which test made
  it by the realm in its name (`it-<prefix>-<id>`), and fix the cause.
- `TestDeleteAfterKilledCreate` (Integration Test) kills `sind create cluster` when
  a line of its plain progress appears (the start of a step, a wait); a step or a wait
  that is renamed changes `killPoints` of the test too. When the delete after a kill leaves
  something, the test has found what a killed create leaves and `sind delete cluster`
  does not remove: the log of the subtest has the progress lines of the create and
  what it left, and the failure names what the delete left. It is a defect to fix, in
  the delete or in the order of the create, not a flake.
- Integration Test fetches sources while building the image: the Slurm, UCX, PMIx,
  PRRTE and Open MPI tarballs, and a `git clone` of libjwt from github.com. A failure in
  that build step with a network error on any source fetch of the Dockerfile (e.g.
  `download.schedmd.com ... i/o timeout`, or `git clone ... benmcollins/libjwt` failing
  to connect) is an upstream outage, not this PR's fault. Confirm it by the error naming
  the URL, re-run the job once, and report it if it fails again.
- Integration Test pulls the Docker Hub images the tests run (the mesh's CoreDNS and the
  monitoring stack's four, which `go run ./tools/images` prints, and `busybox:latest`) from
  `mirror.gcr.io` and tags them with their Docker Hub names, so the tests never pull them
  from Docker Hub. A `toomanyrequests` or an `auth.docker.io` timeout from Docker Hub
  means the job pulls another Docker Hub image: take it from `mirror.gcr.io` too.
- A Vulnerability Check failure on a new advisory in a dependency or the Go toolchain
  that this PR does not touch is fixed by bumping that module (`go get <module>@<fixed>`,
  `go mod tidy`) or the toolchain in `go.mod`, in its own commit.
- A push to a PR cancels the CI run of the commit it replaces. A cancelled run is not a
  failure; look at the run of the PR's head commit.
- The image workflows (`image.yml`, `image-build.yml`) publish to GHCR and never run for
  a PR: check a change to them with `actionlint`, and `docker buildx bake --print` for
  `docker-bake.hcl`. Their `test` jobs run four integration tests on each image before
  it gets its tags, selected by name (`TESTS` in `image-build.yml`): a PR that renames
  or removes one of them changes that list too.
- The workflows pin every action to a commit SHA, which Dependabot updates, and the
  versions of goreleaser, golangci-lint, gotestsum, govulncheck, zizmor, and Hugo (in `ci.yml`
  and `docs.yml`), which it does not: bump those by hand. Dependabot also updates the Go modules at the root and
  in `tools/screenshots`, the npm packages of `video/` and the pins of
  `video/voice/requirements.txt`, all against `next`, each release 7 days after it
  came out (`cooldown`; security updates do not wait). It leaves out the Hextra docs
  theme, a Hugo module in `docs/go.mod`, because its `go mod tidy` drops the
  requirement: bump it by hand with `hugo mod get github.com/imfing/hextra@<version>` in
  `docs/`, as the markdownlint-cli2 version that `make lint-docs` runs (`Makefile`).
- A bump of hyperframes, by Dependabot or by hand, checks the undici override of
  `video/package.json` (`video/README.md`, Decisions): drop it once hyperframes pins
  undici 7.30.1 or later.
- A bump of go-clikit or go-nodeset, by Dependabot or by hand, also updates their
  version where `DESIGN.md` and `docs/content/` name it, as in the links to their docs.
- Every other failure is this PR's to root-cause and fix. Never skip, disable or
  loosen tests or coverage thresholds.

## GitHub etiquette

- Ask the maintainer before posting any comment or review reply, and never @-mention
  anyone.
- Never merge PRs, push to `main`/`next`, or create tags or releases.
