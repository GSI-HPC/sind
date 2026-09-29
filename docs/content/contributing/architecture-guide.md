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
  ├── root.go      Root command, persistent --realm and -v flags, TraverseChildren
  ├── context.go   Dependency injection via context
  ├── logging.go   Logger construction from -v verbosity
  ├── lock.go      Per-realm advisory locking (flock)
  ├── completion.go Shell completion for cluster/node names
  ├── nodeargs.go  Node argument parsing
  ├── sshexport.go SSH config export to ~/.local/state/sind/
  ├── output.go    -o/--output handling (human, json)
  ├── mcp.go       MCP server setup (ophis): tool selection, JSON output, annotations
  ├── worker.go    Worker create/delete commands
  └── *.go         One file per command group

internal/mock/     Test doubles for cmdexec.Executor
  ├── mock.go      mock.Executor (FIFO + OnCall dispatch)
  ├── recorder.go  mock.Recorder (mock in unit mode, OSExecutor in integration mode)
  └── recording.go mock.RecordingExecutor, RecordedCall

internal/hostname/ DNS label check behind config.CheckName (cluster and realm names)

internal/termtext/ Escaping of untrusted text for the terminal (final error line)

internal/testutil/ Shared test helpers
  ├── testutil.go  ExitCode1, NoSuchContainer/Network/Volume, Ptr[T]
  ├── client.go    NewClient, Realm (unit test variants)
  └── client_integration.go NewClient, Realm (integration test variants)

pkg/cmdexec/       Command executor abstraction
  ├── exec.go      Executor interface, OSExecutor
  ├── stream.go    Process, Start (long-lived commands with streamed stdout)
  ├── exiterror.go ExitError (exit code + stderr of a failed command)
  └── logging.go   LoggingExecutor (TRACE-level command logging)

pkg/docker/        Docker CLI wrapper
  ├── client.go    Client type, run/exists helpers
  ├── container.go Container operations
  ├── network.go   Network operations
  ├── volume.go    Volume operations
  ├── image.go     Image operations
  └── labels.go    Docker Compose compatibility labels

pkg/cluster/       Cluster operations (orchestration)
  ├── create.go    Cluster creation flow
  ├── delete.go    Cluster deletion
  ├── get.go       Listing clusters, nodes, networks, volumes
  ├── status.go    Health status collection
  ├── diagnostics.go Low-level diagnostics helpers used by get cluster/node
  ├── ha.go        Controller pair (backup controller) position and control state
  ├── worker.go    Worker add
  ├── worker_remove.go Worker remove
  ├── power.go     Power state operations
  ├── node.go      Node initialization and setup
  ├── discovery.go Cluster/node discovery queries, VolumeType
  ├── resources.go Resource creation helpers
  ├── types.go     Shared types (Cluster, Node, State)
  ├── ssh.go       ssh/enter/exec arg building and target selection
  ├── logs.go      Log command arg building
  ├── dns.go       Node DNS names and search domain
  ├── naming.go    Resource naming conventions
  └── preflight.go Pre-creation validation

pkg/config/        YAML configuration parsing and validation
pkg/doctor/        Host prerequisite checks (Docker version, cgroupv2, DNS policy)
pkg/log/           Context-based structured logging (slog)
pkg/mesh/          Global infrastructure (mesh network, DNS records, SSH relay and keypair, host DNS)
pkg/monitor/       Event-driven Docker and systemd watchers for readiness
pkg/nodeset/       Nodeset expansion (worker-[0-3])
pkg/probe/         Node readiness probes
pkg/retry/         Bounded exponential-backoff helper
pkg/slurm/         Slurm config generation and version discovery
pkg/ssh/           SSH key injection, host key collection, ssh_config export
```

## Dependency flow

```
cmd/sind → pkg/cluster → pkg/docker   → pkg/cmdexec
         → pkg/doctor  → pkg/config
                       → pkg/log
                       → pkg/mesh    → pkg/docker
                                     → pkg/cmdexec
                                     → pkg/retry
                       → pkg/monitor → pkg/docker
                                     → pkg/cmdexec
                       → pkg/probe   → pkg/docker
                                     → pkg/monitor
                       → pkg/retry
                       → pkg/slurm   → pkg/docker
                       → pkg/ssh     → pkg/docker
         → pkg/nodeset
```

The `pkg/cmdexec` package provides the executor abstraction at the bottom of the stack. `pkg/docker` wraps Docker CLI commands and `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl). The `pkg/cluster` package orchestrates everything. `pkg/doctor` runs host prerequisite checks directly from `cmd/sind` (no cluster orchestration). `pkg/monitor` streams Docker and systemd events for event-driven readiness. `pkg/retry` is a leaf helper used wherever dockerd async cleanup requires retry. The `internal/mock` and `internal/testutil` packages are test-only and not part of the production dependency graph. `internal/termtext` is a leaf used only by `cmd/sind` to escape the final error line; it is adapted from clusterctl and meant to be replaced by the shared go-clikit termtext package.

## Adding a new CLI command

1. **Create the command file** in `cmd/sind/` (e.g., `mycommand.go`)
2. **Define the cobra command** with `Use`, `Short`, `Args`, and `RunE`. An `Args` check that fails exits 2, a usage error; `RunE` returns `usage(err)` or `usagef(...)` for an argument or flag value it rejects, so that it exits 2 too
3. **Wire it up** in `root.go` via `cmd.AddCommand(newMyCommand())`
4. **Use context helpers** to get the Docker client and mesh manager:

   ```go
   ctx := cmd.Context()
   client := clientFrom(ctx)
   realm, err := realmFromFlag(cmd)
   if err != nil {
       return err
   }
   meshMgr := meshMgrFrom(ctx, client, realm)
   ```

5. **Implement the operation** in `pkg/cluster/` (not in `cmd/sind/`)
6. **Classify it for MCP** in `mcpEffects` (read-only, additive or destructive), or list it in `mcpExcluded` if it is interactive or prints a secret (`cmd/sind/mcp.go`); a unit test fails for an unclassified tool
7. **Write tests** for both the CLI layer and the cluster operation

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
    Start(ctx context.Context, name string, args ...string) (*Process, error)
}
```

`pkg/docker` uses an executor for Docker CLI calls. `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl, pkcheck). The CLI wraps the mesh executor in a `LoggingExecutor` to emit TRACE-level logs for system commands.

### Context-based dependency injection

The CLI layer injects dependencies via Go context:

```go
ctx = withClient(ctx, client)
ctx = withMeshMgr(ctx, meshMgr)
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
