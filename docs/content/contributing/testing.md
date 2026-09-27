---
weight: 620
title: "Testing"
icon: "science"
description: "Test infrastructure, mocking patterns, and integration tests"
toc: true
---

## TDD workflow

Development follows test-driven development:

1. Write a failing test
2. Implement minimal code to pass
3. Refactor
4. Commit

## Dual-mode tests

Tests run in two modes, controlled by build tags:

- **Unit tests** (default) — use mock executors, no Docker required
- **Integration tests** (`-tags integration`) — run against real Docker

Each package that needs both modes has two files:

```
mode_unit_test.go         // build tag: !integration
mode_integration_test.go  // build tag: integration
```

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

### Inspecting calls

All calls are recorded and can be inspected:

```go
assert.Equal(t, "create", m.Calls[0].Args[0])
assert.Contains(t, m.Calls[0].Args, "--name")
```

## Simulating missing resources

The real executor returns a failed command as a `*cmdexec.ExitError` that carries the exit code and what the command wrote to stderr. The mock returns a queued `*exec.ExitError` in the same shape, wrapped with the result's stderr.

Docker exits 1 for many failures, so `docker.IsNotFound` and `docker.IsVolumeInUse` read docker's message, not only the exit code. Queue a real `*exec.ExitError` with exit code 1 together with the stderr docker prints:

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

## Integration test isolation

Integration tests use unique realms to avoid resource conflicts when running in parallel. Each test gets a random realm from `testutil.Realm`, which appends eight random hex digits to a prefix:

```go
realm := testutil.Realm("it-cluster") // e.g. it-cluster-3f9a0c1e
```

This ensures tests can run concurrently without interfering with each other or with the user's sind clusters.

## Test assertions

Tests use [testify](https://github.com/stretchr/testify):

- `assert` — for non-fatal assertions (test continues)
- `require` — for fatal assertions (test stops immediately)

```go
assert.Equal(t, "running", info.Status)
require.NoError(t, err)
```
