---
weight: 630
title: "Architecture Guide"
icon: "account_tree"
description: "Package structure and how to add new features"
toc: true
---

## Package map

```
cmd/sind/          CLI commands (cobra)
  ├── main.go      Entry point
  ├── root.go      Root command, --realm and -v flags (root-local, TraverseChildren)
  ├── context.go   Dependency injection via context
  ├── logging.go   Logger construction from -v verbosity
  ├── lock.go      Per-realm advisory locking (flock)
  ├── completion.go Shell completion for cluster/node names
  ├── nodeargs.go  Node argument parsing
  ├── sshexport.go SSH config export to ~/.local/state/sind/
  ├── worker.go    Worker create/delete commands
  └── *.go         One file per command group

internal/mock/     Test doubles for cmdexec.Executor
  ├── mock.go      mock.Executor (FIFO + OnCall dispatch)
  ├── recorder.go  mock.RecordingExecutor for integration tests
  └── recording.go Recorded call types

internal/testutil/ Shared test helpers
  ├── testutil.go  ExitCode1, Ptr[T], realm helpers
  ├── client.go    NewClient (unit test client factory)
  └── client_integration.go NewClient (integration test variant)

pkg/cmdexec/       Command executor abstraction
  ├── exec.go      Executor interface, OSExecutor
  └── logging.go   LoggingExecutor (TRACE-level command logging)

pkg/docker/        Docker CLI wrapper
  ├── client.go    Client type, run/exists helpers
  ├── container.go Container operations
  ├── network.go   Network operations
  ├── volume.go    Volume operations
  └── image.go     Image operations

pkg/cluster/       Cluster operations (orchestration)
  ├── create.go    Cluster creation flow
  ├── delete.go    Cluster deletion
  ├── get.go       Listing clusters, nodes, networks, volumes
  ├── status.go    Health status collection
  ├── worker.go    Worker add
  ├── worker_remove.go Worker remove
  ├── power.go     Power state operations
  ├── node.go      Node initialization and setup
  ├── discovery.go Cluster/node discovery queries
  ├── resources.go Resource creation helpers
  ├── types.go     Shared types (SlurmService, VolumeType, etc.)
  ├── ssh.go       SSH arg building, host key collection
  ├── logs.go      Log command arg building
  ├── dns.go       DNS record management
  ├── naming.go    Resource naming conventions
  └── preflight.go Pre-creation validation

pkg/config/        YAML configuration parsing and validation
pkg/log/           Context-based structured logging (slog)
pkg/mesh/          Global infrastructure (mesh network, DNS, SSH)
pkg/probe/         Node readiness probes
pkg/nodeset/       Nodeset expansion (worker-[0-3])
pkg/slurm/         Slurm config generation and version discovery
pkg/ssh/           SSH key generation and injection
```

## Dependency flow

```
cmd/sind → pkg/cluster → pkg/docker  → pkg/cmdexec
                       → pkg/config
                       → pkg/log
                       → pkg/mesh   → pkg/docker
                                    → pkg/cmdexec
                       → pkg/probe  → pkg/docker
                       → pkg/slurm  → pkg/docker
                       → pkg/ssh    → pkg/docker
                       → pkg/nodeset
```

The `pkg/cmdexec` package provides the executor abstraction at the bottom of the stack. `pkg/docker` wraps Docker CLI commands and `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl). The `pkg/cluster` package orchestrates everything. The `internal/mock` and `internal/testutil` packages are test-only and not part of the production dependency graph.

## Adding a new CLI command

1. **Create the command file** in `cmd/sind/` (e.g., `mycommand.go`)
2. **Define the cobra command** with `Use`, `Short`, `Args`, and `RunE`
3. **Wire it up** in `root.go` via `cmd.AddCommand(newMyCommand())`
4. **Use context helpers** to get the Docker client and mesh manager:

   ```go
   client := clientFrom(cmd.Context())
   realm := realmFromFlag(cmd)
   meshMgr := meshMgrFrom(ctx, client, realm)
   ```

5. **Implement the operation** in `pkg/cluster/` (not in `cmd/sind/`)
6. **Write tests** for both the CLI layer and the cluster operation

The CLI layer should be thin — argument parsing, flag handling, and output formatting. Business logic belongs in `pkg/cluster/`.

## Adding a Docker operation

1. **Add the method** to `pkg/docker/client.go` (or the appropriate resource file)
2. **Follow the pattern**: call `c.run()` or `c.runWithStdin()`, parse output
3. **Use strong types**: `ContainerName`, `NetworkName`, `VolumeName`, etc.
4. **Write unit tests** using `mock.Executor`

## Key patterns

### Executor abstraction

All external commands go through the `cmdexec.Executor` interface (from `pkg/cmdexec`), making every operation testable:

```go
type Executor interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
    RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error)
}
```

`pkg/docker` uses an executor for Docker CLI calls. `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl, pkcheck). The CLI wraps the mesh executor in a `LoggingExecutor` to emit TRACE-level logs for system commands.

### Context-based dependency injection

The CLI layer injects dependencies via Go context:

```go
ctx = withClient(ctx, client)
ctx = withMeshManager(ctx, meshMgr)
ctx = sindlog.With(ctx, logger)     // injected by PersistentPreRunE
```

Commands retrieve them with `clientFrom(ctx)` and `meshMgrFrom(ctx, ...)`. The logger is injected automatically by the root command's `PersistentPreRunE` based on the `-v` flag count.

### Structured logging

The `pkg/log` package provides context-based logging via `slog`. All `pkg/` code extracts the logger from context — never from `slog.Default()`:

```go
log := sindlog.From(ctx)
log.InfoContext(ctx, "creating cluster", "name", cfg.Name)
log.DebugContext(ctx, "waiting for node", "node", shortName)
log.Log(ctx, sindlog.LevelTrace, "docker", "cmd", strings.Join(args, " "))
```

When no logger is in the context (library use without the CLI), `From` returns a no-op logger. In errgroup goroutines, use `gctx` (not the outer `ctx`) for log calls.

### Resource naming

All resource names are derived from cluster name and realm via functions in `pkg/cluster/naming.go`. Never hardcode resource name prefixes.
