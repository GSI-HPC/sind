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
  ├── root.go      Root command, persistent --realm, --progress, --progress-log and -v flags, TraverseChildren
  ├── context.go   Dependency injection via context
  ├── exitcode.go  Exit statuses, usage errors (exit 2), child exit status of ssh/exec/enter/logs
  ├── logging.go   Logger construction from -v verbosity
  ├── lock.go      Realm lock for mutating commands (pkg/state)
  ├── completion.go Shell completion for cluster/node names
  ├── nodeargs.go  Node argument parsing (go-nodeset expressions, cluster suffix)
  ├── sshexport.go SSH config export to ~/.local/state/sind/
  ├── output.go    -o/--output handling (human, json)
  ├── progress.go  Progress of create, delete and power: withProgress, the flags and variables handed to go-clikit's cliprogress, the streams under a display
  ├── mcp.go       MCP server setup (ophis): tool selection, JSON output, annotations
  ├── mcpstream.go sind mcp stream: HTTP server with a bearer token, forwarding to ophis
  ├── worker.go    Worker create/delete commands
  └── *.go         One file per command group

internal/mock/     Test doubles for cmdexec.Executor
  ├── mock.go      mock.Executor (FIFO + OnCall dispatch)
  ├── recorder.go  mock.Recorder (mock in unit mode, OSExecutor in integration mode)
  └── recording.go mock.RecordingExecutor, RecordedCall

internal/hostname/ DNS label check behind config.CheckName (cluster and realm names) and pkg/ssh's ssh_config host names

internal/testutil/ Shared test helpers
  ├── testutil.go  ExitCode1, NoSuchContainer/Network/Volume, Ptr[T]
  ├── client.go    NewClient, Realm (unit test variants)
  └── client_integration.go NewClient, Realm (integration test variants)

pkg/cmdexec/       Command executor abstraction
  ├── exec.go      Executor interface, OSExecutor (tees output to the progress span of the call)
  ├── output.go    WithStdoutAsData: a command's stdout is data, not lines to show
  ├── stream.go    Process, Start (long-lived commands with streamed stdout)
  ├── exiterror.go ExitError (exit code + stderr of a failed command)
  └── logging.go   LoggingExecutor (TRACE-level command logging)

pkg/docker/        Docker CLI wrapper
  ├── client.go    Client type, run/exists helpers, each docker command a hidden progress call
  ├── container.go Container operations
  ├── network.go   Network operations
  ├── volume.go    Volume operations
  ├── image.go     Image operations
  ├── info.go      docker info (daemon version, cgroup version, security options, OS and kernel)
  ├── endpoint.go  Daemon endpoint the docker CLI uses (DOCKER_HOST, docker context)
  ├── plugin.go    Volume plugin queries (CVMFS)
  └── labels.go    Docker Compose compatibility labels

pkg/cluster/       Cluster operations (orchestration)
  ├── create.go    Cluster creation flow
  ├── delete.go    Cluster deletion; DeleteAll's pool of clusters (fanout.Map)
  ├── get.go       Listing clusters, nodes, networks, volumes
  ├── status.go    Health status collection
  ├── diagnostics.go Low-level diagnostics helpers used by get cluster/node
  ├── errors.go    Error sentinels for library callers (ErrClusterExists ... ErrNetworkFull), rollback bound
  ├── notfound.go  Cluster-not-found error naming the realms that hold the cluster
  ├── readiness.go The --wait limit of Create and WorkerAdd
  ├── ha.go        Controller pair (backup controller) position and control state
  ├── db.go        Accounting services (mariadb, slurmdbd) on the db node
  ├── api.go       slurmrestd setup steps of the api node
  ├── accounts.go  Slurm accounts, associations and coordinators (sacctmgr)
  ├── users.go     Linux users and groups, their labels, home directories
  ├── identity.go  Identity modes: nss_slurm on workers, sackd on the submitter
  ├── cvmfs.go     CVMFS backend detection and mount arguments
  ├── worker.go    Worker add
  ├── worker_remove.go Worker remove
  ├── power.go     Power state operations; the pool of each action (fanout.Map)
  ├── node.go      Node initialization and setup
  ├── setup.go     In-container node setup (nss_slurm, slurmrestd, users, SSH) in one docker exec
  ├── discovery.go Cluster/node discovery queries, VolumeType
  ├── resources.go Resource creation helpers
  ├── types.go     Shared types (Cluster, Node, State)
  ├── ssh.go       ssh/enter/exec arg building and target selection
  ├── logs.go      Log command arg building
  ├── dns.go       Node DNS names and search domain
  ├── naming.go    Resource naming conventions
  ├── progress.go  Progress span helpers of the pools (startStep, startTargets, nodeTargets, endSpan), joinFailures, WithPanicLog
  ├── rollback.go  The rollback step of a failed Create or WorkerAdd
  └── preflight.go Pre-creation validation

pkg/config/        YAML configuration parsing and validation
pkg/doctor/        Host prerequisite checks (Docker version, where the daemon runs, cgroupv2 and the
                   nsdelegate probe, inotify)
pkg/log/           Context-based structured logging (slog)
pkg/mesh/          Global infrastructure (mesh network, DNS records, SSH relay and keypair, host DNS)
pkg/monitor/       Event-driven Docker and systemd watchers for readiness
pkg/probe/         Node readiness probes
pkg/retry/         Bounded exponential-backoff helper
pkg/slurm/         Slurm and slurmdbd config generation, sind-nodes.conf editing, version
                   discovery, munge key, slurm.key and jwt_hs256.key generation, sacctmgr
                   account commands
pkg/ssh/           SSH key injection, host key collection, ssh_config export
pkg/state/         sind's state directory and the realm lock (flock, and the <realm>-lock network on the daemon) that library callers of Create/Delete/WorkerAdd/WorkerRemove hold
```

## Dependency flow

```
cmd/sind → pkg/cluster → pkg/cmdexec
                       → pkg/config  → internal/hostname
                       → pkg/docker  → pkg/cmdexec
                                     → pkg/log
                       → pkg/doctor  → pkg/docker
                                     → pkg/cmdexec
                       → pkg/log
                       → pkg/mesh    → pkg/docker
                                     → pkg/cmdexec
                                     → pkg/config
                                     → pkg/log
                                     → pkg/retry
                       → pkg/monitor → pkg/docker
                                     → pkg/cmdexec
                                     → pkg/log
                       → pkg/probe   → pkg/docker
                                     → pkg/config
                                     → pkg/log
                                     → pkg/monitor
                       → pkg/retry
                       → pkg/slurm   → pkg/docker
                                     → pkg/config
                       → pkg/ssh     → pkg/docker
                                     → internal/hostname
                       → pkg/state   → pkg/docker
                                     → pkg/log
         → pkg/doctor
         → pkg/state
         → github.com/GSI-HPC/go-clikit/termtext
         → github.com/GSI-HPC/go-nodeset
```

`cmd/sind` also imports `pkg/cmdexec`, `pkg/config`, `pkg/docker`, `pkg/log`, `pkg/mesh`, `pkg/probe` and `pkg/ssh` directly.

The `pkg/cmdexec` package provides the executor abstraction at the bottom of the stack. `pkg/docker` wraps Docker CLI commands and `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl). The `pkg/cluster` package orchestrates everything. `pkg/doctor` runs host prerequisite checks directly from `cmd/sind` (no cluster orchestration); `pkg/cluster` uses its nsdelegate probe in the create preflight. `pkg/monitor` streams Docker and systemd events for event-driven readiness. `pkg/retry` is a leaf helper used wherever dockerd async cleanup requires retry. The `internal/mock` and `internal/testutil` packages are test-only and not part of the production dependency graph. `cmd/sind` escapes the final error line, `get` table cells and `doctor` details with the `termtext` package of [go-clikit](https://github.com/GSI-HPC/go-clikit). It parses node arguments (`worker-[0-3]!worker-2`) with [go-nodeset](https://github.com/GSI-HPC/go-nodeset) in `nodeargs.go`.

The packages that report progress, `pkg/cluster`, `pkg/cmdexec`, `pkg/docker`, `pkg/mesh` and `pkg/probe`, import go-clikit's `progress` package, and `pkg/cluster` its `fanout` package for the pools of the power commands and `DeleteAll`; only `cmd/sind` imports `progress/display`, which shows the spans (see [Progress spans](#progress-spans)).

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
6. **Show its progress** if it changes clusters and asks nothing: wrap `RunE` with `withProgress(...)`, and have the operation report its work as progress spans (see [Progress spans](#progress-spans))
7. **Classify it for MCP** in `mcpEffects` (read-only, additive or destructive), or list it in `mcpExcluded` if it is interactive or prints a secret (`cmd/sind/mcp.go`); a unit test fails for an unclassified tool
8. **Write tests** for both the CLI layer and the cluster operation

The CLI layer should be thin — argument parsing, flag handling, and output formatting. Business logic belongs in `pkg/cluster/`.

## Adding a Docker operation

1. **Add the method** to `pkg/docker/client.go` (or the appropriate resource file)
2. **Follow the pattern**: call `c.run()` or `c.runWithStdin()`, parse output. Both report the command as a hidden progress call named after its subcommand, and both wait for one of the client's `docker.MaxConcurrentCalls` slots, so a command must not wait for another docker command while it runs; long-lived streams go through `Executor.Start` and take no slot
3. **Use strong types**: `ContainerName`, `NetworkName`, `VolumeName`, etc.
4. **Write unit tests** using `mock.Executor`

## Key patterns

### Executor abstraction

All external commands go through the `cmdexec.Executor` interface (from `pkg/cmdexec`), making every operation testable, with one exception (see below):

```go
type Executor interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
    RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error)
    Start(ctx context.Context, name string, args ...string) (*Process, error)
}
```

`pkg/docker` uses an executor for Docker CLI calls. `pkg/mesh` uses a separate executor for system commands (resolvectl, systemctl, pkcheck). The CLI wraps the mesh executor in a `LoggingExecutor` to emit TRACE-level logs for system commands.

The exception is `dockerExec` in `cmd/sind/ssh.go`, used by `ssh`, `enter`, `exec` and `logs`. It runs `docker` through `os/exec` with the terminal's stdin, stdout and stderr attached, because `Executor` only captures output, and unlike the calls through `docker.Client` it logs no TRACE line. Its unit tests cannot use `mock.Executor`: they put the test binary first on `PATH` as a fake `docker`, controlled by the `SIND_TEST_DOCKER_*` variables in `cmd/sind/main_test.go`.

### Context-based dependency injection

The CLI layer injects dependencies via Go context:

```go
ctx = withClient(ctx, client)
ctx = withMeshMgr(ctx, meshMgr)
ctx = sindlog.With(ctx, logger)     // injected by PersistentPreRunE
```

Commands retrieve them with `clientFrom(ctx)` and `meshMgrFrom(ctx, ...)`. The logger is injected automatically by the root command's `PersistentPreRunE` based on the `-v` flag count; under a progress display, `withProgress` replaces it, and the stderr `withStderr` stored, with writers that go above the display.

### Structured logging

The `pkg/log` package provides context-based logging via `slog`. All `pkg/` code extracts the logger from context — never from `slog.Default()`:

```go
log := sindlog.From(ctx)
log.InfoContext(ctx, "creating cluster", "name", cfg.Name)
log.DebugContext(ctx, "waiting for node", "node", shortName)
log.Log(ctx, sindlog.LevelTrace, "docker", "cmd", strings.Join(args, " "))
```

When no logger is in the context (library use without the CLI), `From` returns a no-op logger. In errgroup goroutines, use `gctx` (not the outer `ctx`) for log calls.

### Progress spans

Work that takes a while reports what it does as progress spans of [go-clikit](https://github.com/GSI-HPC/go-clikit)'s `progress` package, on the context: `progress.Start(ctx, kind, name, opts...)` returns the span's context, under which the spans of the work below it start, and the span, which the work ends with its error (`span.End(err)`). Without a Bus in the context, as for a library caller or a test that watches none, `Start` returns a nil span, and every method of a nil span does nothing, so the code needs no branch for it.

- A step (`progress.KindStep`) is a phase of the command; a step with the `Fold` flag and a `Total` counts its targets (`KindTarget`: a node, an image). A wait (`KindWait`) can have a bound (`progress.Timeout`) and a message that says what it waits for; a call (`KindCall`) is one request, such as a docker command.
- Announce every target of a counted step queued (`progress.Queued()`) before the first of them runs, then mark each running (`span.Run()`) when its work starts: a pool that starts each target in its own goroutine as it takes its place breaks the counts a display draws, and `progresstest.Check` fails it.
- `pkg/cluster/progress.go` has the helpers of the pools written by hand: `startStep` starts a step, `startTargets` and `nodeTargets` announce its targets, and `endSpan` ends a span canceled (`stopped`) when a sibling's failure or an interrupt stopped its work, rather than failed with `signal: killed`.
- A pool that tries every item and keeps going after a failure runs on go-clikit's `fanout.Map`, which announces its targets as `Check` requires, bounds them with `Limit`, and turns a panic in one item into that item's error (`sind panicked; this is a bug, please report it: ...`) with its stack in the Bus's panic log, or without a Bus where `cluster.WithPanicLog` says (`withStderr` sets the command's stderr). The power commands and `DeleteAll` pass `joinFailures` as its `Summarize`, which joins each item's error worded as before, so the error text and `errors.Is` do not change.
- `cmd/sind` alone decides how the spans are shown: `withProgress` (`progress.go`) runs the command in a span of its own and makes the Bus through go-clikit's `cliprogress` package, with the display `--progress` asks for and the event log `--progress-log` names (which `cliprogress` opens privately), and tears them down before `run` prints the error line.
- `pkg/` never writes to stdout or stderr: a display owns the bottom rows of the terminal, and only the writers `cmd/sind` hands out, cobra's streams, `stderrFrom(ctx)` and the logger, go above it.

### Resource naming

All resource names are derived from cluster name and realm via functions in `pkg/cluster/naming.go`. Never hardcode resource name prefixes.
