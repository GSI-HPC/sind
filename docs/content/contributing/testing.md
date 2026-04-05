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

For methods like `ContainerExists()` that check exit codes, a plain `fmt.Errorf` won't work. You need a real `*exec.ExitError` with exit code 1:

```go
m.AddResult("", "", &exec.ExitError{ProcessState: testutil.ExitCode1(t)})
```

The `testutil.ExitCode1(t)` helper (from `internal/testutil`) runs `sh -c "exit 1"` to obtain a real `*os.ProcessState`.

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

Integration tests use unique realms to avoid resource conflicts when running in parallel. Each test package generates a random realm prefix:

```go
realm := fmt.Sprintf("test-%d-%d", os.Getpid(), rand.Int())
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
