---
weight: 620
title: "Testing"
description: "Test infrastructure, mocking patterns, and integration tests"
toc: true
---

## TDD workflow

Development follows test-driven development:

1. Write a failing test
2. Implement minimal code to pass
3. Refactor
4. Commit

## Unit-test sandbox

`make test` runs the unit tests in a firejail sandbox, so that a test that forgets to inject a mock fails instead of touching the host. The sandbox blocks `systemctl`, `resolvectl`, the polkit tools, `ssh` and `docker` in `/bin` and `/usr/bin`, and `/run/systemd`. A `docker` found elsewhere on `PATH` reaches no daemon either: the sandbox blocks the daemon sockets in `/run`, in `$XDG_RUNTIME_DIR` (rootless Docker) and in `~/.docker/desktop` (Docker Desktop), removes `DOCKER_HOST` and `DOCKER_CONTEXT`, and points `DOCKER_CONFIG` at no config, so that no context stored in `~/.docker/config.json`, such as Colima's, Podman's or a remote daemon's, is current.

## Dual-mode tests

Tests run in two modes, controlled by build tags:

- **Unit tests** (default) — use mock executors, no Docker required
- **Integration tests** (`-tags integration`) — run against real Docker

Three patterns provide the two modes:

- **Shared dual-mode client.** `testutil.NewClient(t)` returns a `docker.Client` and a `mock.Recorder`. In unit mode (`internal/testutil/client.go`, `!integration`) the recorder holds a mock executor; in integration mode (`client_integration.go`, `integration`) it runs the real `docker`, and its `AddResult` and `SetOnCall` do nothing. The same test then runs in both modes and checks `rec.IsIntegration()` where the two differ. Most dual-mode tests use it, in `pkg/cluster`, `pkg/mesh`, `internal/doctor`, `pkg/state` and `internal/slurm`. Its two variants are ordinary files of `internal/testutil`, not test files, so that every package can import them.
- **Package-local pairs.** A package whose dual-mode tests need helpers of their own keeps them in two files, `mode_unit_test.go` (`!integration`) and `mode_integration_test.go` (`integration`). `pkg/docker` has its own `newTestClient` there, as `internal/testutil` imports `pkg/docker`; `cmd/sind` has its command runners there, whose unit variants skip the test or panic.
- **Integration-only files.** Tests that only make sense against Docker, such as those that create clusters, sit in files with the `integration` tag alone, as `pkg/cluster/mode_integration_test.go`, `api_integration_test.go` and `monitoring_integration_test.go`, and `cmd/sind/mpi_test.go` and `quickstart_test.go`. `pkg/cluster` has no `mode_unit_test.go`.

A test that runs in both modes gets its client from `testutil.NewClient`: in unit mode a `mock.Recorder`, in integration mode the docker CLI. In unit mode it needs a fake that keeps state, such as the fake Docker of the `pkg/mesh` tests, so that it checks what the code does rather than replaying answers queued in the order the code asks for them. A test that has no such fake checks against Docker alone and starts with `testutil.SkipWithoutDocker(t)`; the unit tests of the same code check the commands and how their output is read.

## Mock executor

The `mock.Executor` (from `internal/mock`) replaces real CLI calls in unit tests. It supports two dispatch modes:

### FIFO mode

Queue results in order with `AddResult()`:

```go
m := &mock.Executor{}
m.AddResult(`{"Id":"abc123"}`, "", nil)  // first call returns this
m.AddResult("", "", nil)                  // second call returns this

client := docker.NewClient(m)
```

A call after the queue ran out gets the error `mock: unexpected call`, which code that ignores errors on purpose, such as a cleanup, takes for an answer. `m.AssertDone(t)` fails the test for such a call, and for a queued result that no call used.

### Dispatcher mode

Use `OnCall` for concurrent tests or when result dispatch depends on the command:

```go
m := &mock.Executor{
    OnCall: func(args []string, stdin string) mock.Result {
        if args[0] == "inspect" {
            return mock.Result{Stdout: `[{"Id":"abc"}]`}
        }
        return mock.Result{}
    },
}
```

`OnCall` runs under the mock's lock, one call at a time, so it may change the test's variables without locking them. A test that must hold one call while others run wraps the executor and blocks outside that lock.

### Ended contexts

As the real executor, the mock fails a call whose context is done with the context's error, and does not record it or use a queued result for it. A test that wants such calls answered, for example so that concurrent work gets equally far whichever goroutine ran first, sets `IgnoreContext: true`.

### Inspecting calls

All calls are recorded and can be inspected:

```go
assert.Equal(t, "create", m.Calls[0].Args[0])
assert.Contains(t, m.Calls[0].Args, "--name")
```

Read `Calls` once the code under test has returned. While it still runs, such as a server or a watcher that a test starts, `m.CallsSnapshot()` returns a copy of the calls so far. Queue results and set `OnCall` before the code runs; `AddResult` may also run while it does.

### Shared fake daemon

A dispatcher that answers from fixed results does not notice when the code removes what it created, or creates it twice. `internal/fakedocker` holds a stand-in for dockerd that keeps state and that the tests of several packages share, as `_test.go` helpers cannot be imported. Its `Daemon` keeps networks, and answers `docker network create`, `inspect` and `rm` as dockerd does: the commands of the realm lock's daemon part, which the tests of `pkg/state` and `cmd/sind` run. A test checks the end state, such as `d.Networks()`, instead of the calls:

```go
d := fakedocker.New(t)
unlock, err := LockRealm(ctx, "sind", opts(d.Client()))
require.NoError(t, err)
unlock()
assert.Empty(t, d.Networks())
```

`SetHook` lets a test answer some calls itself, for example to make a remove fail.

## Simulating missing resources

The real executor returns a failed command as a `*cmdexec.ExitError` that carries the exit code and what the command wrote to stderr. The mock returns a queued `*exec.ExitError` in the same shape, wrapped with the result's stderr. An executor that runs no local process, or a test double that needs a code other than 1, returns `cmdexec.NewExitError(code, stderr)`, which the mock passes on unchanged.

Docker exits 1 for many failures, so `docker.IsNotFound` and the in-use retry of `RemoveVolume` read docker's message, not only the exit code. Queue a real `*exec.ExitError` with exit code 1 together with the stderr docker prints:

```go
m.AddResult("", testutil.NoSuchContainer("sind-dev-controller"), testutil.ExitCode1(t))
m.AddResult("", testutil.NoSuchNetwork("sind-dev-net"), testutil.ExitCode1(t))
m.AddResult("", testutil.NoSuchVolume("sind-dev-config"), testutil.ExitCode1(t))
```

`testutil.ExitCode1(t)` (from `internal/testutil`) runs `sh -c "exit 1"` to obtain a real `*exec.ExitError`. An exit code 1 with any other stderr, such as `volume is in use`, is not a missing resource.

## Recording executor

The `mock.RecordingExecutor` (from `internal/mock`) wraps a real executor and records all calls with their results. Useful for observing actual CLI I/O during integration tests:

```go
rec := &mock.RecordingExecutor{Inner: &cmdexec.OSExecutor{}}
client := docker.NewClient(rec)

// ... run operations ...

// Dump all recorded calls
t.Log(rec.Dump())
```

## Progress spans and displays

The `progress/progresstest` package of [go-clikit](https://github.com/GSI-HPC/go-clikit) tests the progress spans that code reports (see [Progress spans]({{< relref "/contributing/architecture-guide#progress-spans" >}})):

```go
ctx, w := progresstest.Watch(t.Context(), t)

_, err := WorkerAdd(ctx, client, meshMgr, WorkerAddOptions{ClusterName: "dev", Count: 2, Interval: interval})

require.NoError(t, err)
w.Finish()
assert.Equal(t, `step mesh registration: ok
step nodes total=2 [fold]: ok
  target worker-[1-2] role=worker: ok
    wait ready: ok
step slurm total=2 [fold]: ok
  target worker-[1-2] role=worker: ok
    wait ready: ok
`, testutil.WithoutCalls(w.Events()))
```

- `Watch` gives the test a context with a Bus of its own. `Finish` runs `progresstest.Check` on the events, closes the Bus and returns the spans as a tree that does not depend on the order in which concurrent work ran; a test that does not call `Finish` is checked when it ends. `Check` fails the test for every promise the spans break: each span starts and ends once, under a parent that is still open; the targets of a counted step are all announced queued before the first of them runs, and as many end as its `Total`; no more run at once than its `Limit`; no text holds what a terminal would act on.
- In `cmd/sind`, pass `progresstest.Classify(progressClass(ctx))`, so that the spans get the classes sind's own Bus gives them.
- Every docker command is a call in the tree. `testutil.WithoutCalls` (from `internal/testutil`) draws the tree without the calls and what is under them, for a test whose point is not how often a wait polls.
- Without `Watch` the context has no Bus, every span does nothing, and a test sees what it saw before sind reported progress.

The display tests in `cmd/sind` replace the seams that `cmd/sind/progress.go` keeps as package variables: `onTerminal` and `intoPipe` (what a stream is), `terminalSize` and `inForeground` (asked at each frame), `displayClock`, and `startRun`, which makes the Bus and the display of a command (`cliprogress.Start`). `onTerminals` makes the streams a test names terminals, 160 columns by 40 rows with sind in the foreground; `fakeDisplays` makes each display without starting it (`cliprogress.Options.Manual`), on a clock that moves a second each time the test calls `draw`, so a test draws every frame itself on a `progresstest.Screen` and compares what the screen shows. `TestMain` unsets every `SIND_*` variable that sind reads (`envvar.All`) and points `XDG_STATE_HOME` at a temporary directory, so that a developer's own settings reach no test and no test writes the activity log into a real home; a test of the log gives itself a directory with `t.Setenv("XDG_STATE_HOME", t.TempDir())`.

## Fuzz tests

A function that reads text sind did not write has a fuzz target, a `FuzzXxx` function in the `fuzz_test.go` of its package: a cluster configuration, a `slurm.conf` or `sind-nodes.conf` that a user changed, the output of Docker and of Slurm's commands, what an image prints, an editor's MCP configuration, a node argument. A target states what holds for every input: at least that the function does not panic, and where there is one, a property of the result, such as "a configuration that validates reads back as the same cluster".

```go
func FuzzParseEntries(f *testing.F) {
    f.Add(generateCorefile("sind", nil)) // a seed
    f.Fuzz(func(t *testing.T, corefile string) {
        entries := parseEntries(corefile)
        assert.Equal(t, entries, parseEntries(generateCorefile("sind", entries)))
    })
}
```

`make test` runs each target on its seeds, as a unit test. `make fuzz` searches for new inputs: it runs every target for `FUZZTIME` (default `30s`), one after the other. The Nightly workflow runs it with three minutes per target. When a target fails, `go test` writes the input to `testdata/fuzz/<target>/` in the directory of the package and prints the command that runs it again. Fix the defect, and keep the input: add it to the seeds of the target, or commit the file, which `go test` then runs with the seeds.

A property that holds only for well-formed input belongs in a property test, which generates such input, not in a fuzz target, whose input is anything.

## Property tests

A property test states what holds for every input of a kind, and checks it on inputs that [rapid](https://pkg.go.dev/pgregory.net/rapid) draws: 100 on each run of the unit tests, other ones each time. It lives in the `property_test.go` of its package.

```go
func TestCompare_IsATotalOrder(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        a, b := name().Draw(t, "a"), name().Draw(t, "b")
        assert.Equal(t, a == b, Compare(a, b) == 0, "only equal names compare equal")
        assert.Equal(t, Compare(a, b), -Compare(b, a))
    })
}
```

| Package | Property |
|---|---|
| `internal/natsort` | `Compare` is a total order, and orders names that differ in one number by that number |
| `pkg/config` | `Parse` reads back the file that the YAML encoder writes for any config, which the web UI depends on; a second `ApplyDefaults` changes nothing, and an ID that it assigns is free |
| `internal/slurm` | The `slurm.conf` of any main section and options sets no parameter that the main section sets; `sind-nodes.conf` stays well-formed over any sequence of worker additions and removals |
| `pkg/cluster` | No two Docker resources of sind get the same name (`naming_collision_test.go`, which checks every name up to a length instead of drawing some) |

- Write a generator for the inputs that the property is about (`rapid.Custom`), and draw each value with a label: rapid prints the labelled values of the smallest input that fails.
- For a sequence of operations, keep a model next to the real thing and compare the two after each step (`t.Repeat`, as `TestNodesConf_EditsKeepTheFileWellFormed` does).
- A failure prints its seed. `go test ./<pkg> -run '^TestXxx$' -rapid.seed=<seed>` runs the same input again, and `-rapid.checks=10000` draws more inputs. rapid also writes the input to `testdata/rapid/`, which git ignores: fix the defect, and add the input to a table test.

## Integration test prerequisites

An integration test that needs Docker gets its client from `testutil.NewClient` (`newTestClient` in `pkg/docker`), which checks with `docker info`, once per test process, that `docker` reaches a daemon (`itest.RequireDocker`). A test that creates clusters also calls `prereq.Check`, which checks the Docker version and that cgroup v2 is mounted with `nsdelegate`.

When the host fails such a check, the test is skipped, so that `make test-integration` runs on a machine without Docker. With `SIND_TEST_REQUIRE_DOCKER` set, as CI sets it, the test fails instead: a runner change or a daemon that does not answer cannot pass the job with the tests skipped. A test that skips for a reason of its own, such as a Slurm release without slurmrestd, still skips.

## Integration test isolation

Integration tests use unique realms to avoid resource conflicts when running in parallel. The tests of the packages get a random realm from `testutil.Realm`, which appends eight random hex digits to a prefix:

```go
realm := testutil.Realm("it-cluster") // e.g. it-cluster-3f9a0c1e
```

The tests of `cmd/sind` instead append `testID`, the eight hex digits of the test process (`itest.RunID`), to a prefix of their own, such as `"it-e2e-" + testID`; each test has its own prefix. `pkg/docker` names its containers, networks and volumes the same way (`itName`).

This ensures tests can run concurrently without interfering with each other or with the user's sind clusters.

Each test removes what it creates, also when it fails (`t.Cleanup`). After the integration tests have passed, CI lists the containers, networks and volumes that carry the `sind.realm` label and fails when there is one: a test without cleanup, or a `sind delete` that leaves a resource behind.

## Test assertions

Tests use [testify](https://github.com/stretchr/testify):

- `assert` — for non-fatal assertions (test continues)
- `require` — for fatal assertions (test stops immediately)

```go
assert.Equal(t, "running", info.Status)
require.NoError(t, err)
```

A test of an error path names the error it expects, with `ErrorContains`, `ErrorIs` or `EqualError`. A bare `require.Error` also passes on the mock's "unexpected call" or on the test's own deadline; the linter rejects it in the tests of `pkg/cluster`.
