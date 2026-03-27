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

The `docker.MockExecutor` replaces the real Docker CLI in unit tests. It supports two dispatch modes:

### FIFO mode

Queue results in order with `AddResult()`:

```go
mock := &docker.MockExecutor{}
mock.AddResult(`{"Id":"abc123"}`, "", nil)  // first call returns this
mock.AddResult("", "", nil)                  // second call returns this

client := docker.NewClient(mock)
```

### Dispatcher mode

Use `OnCall` for concurrent tests or when result dispatch depends on the command:

```go
mock := &docker.MockExecutor{
    OnCall: func(args []string, stdin string) docker.MockResult {
        if args[0] == "inspect" {
            return docker.MockResult{Stdout: `[{"Id":"abc"}]`}
        }
        return docker.MockResult{}
    },
}
```

### Inspecting calls

All calls are recorded and can be inspected:

```go
assert.Equal(t, "create", mock.Calls[0].Args[0])
assert.Contains(t, mock.Calls[0].Args, "--name")
```

## Simulating missing resources

For methods like `ContainerExists()` that check exit codes, a plain `fmt.Errorf` won't work. You need a real `*exec.ExitError` with exit code 1:

```go
mock.AddResult("", "", &exec.ExitError{ProcessState: exitCode1(t)})
```

The `exitCode1(t)` helper runs `sh -c "exit 1"` to obtain a real `*os.ProcessState`. This helper is defined per test package (not shared).

## Recording executor

The `docker.RecordingExecutor` wraps a real executor and records all calls with their results. Useful for observing actual Docker CLI I/O during integration tests:

```go
rec := &docker.RecordingExecutor{Inner: &docker.OSExecutor{}}
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
