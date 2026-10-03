# sind - Slurm in Docker

SPDX-License-Identifier: LGPL-3.0-or-later

https://github.com/GSI-HPC/sind

A CLI tool for running local Slurm clusters using Docker containers, inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker).

## Prerequisites

- Linux host with the unified cgroupv2 hierarchy at `/sys/fs/cgroup` (not systemd's hybrid mode) and the `nsdelegate` mount option (`mount -o remount,nsdelegate /sys/fs/cgroup`); sind runs on the Docker host itself
- Docker Engine 28.0+ (required for `--security-opt writable-cgroups=true` and the `gw-priority` network option), with a Docker CLI of the same release
- A rootful Docker daemon without `userns-remap`: Docker refuses `writable-cgroups` in rootless mode and with `userns-remap`, at `docker start`. `sind doctor` checks `docker info`'s `SecurityOptions` for `name=rootless` and `name=userns`, and `sind create cluster` does the same (`cluster.CheckDaemon`) before the mesh is set up or an image pulled; it also refuses a daemon whose `CgroupVersion` is `1`
- For clusters with 10+ nodes: `fs.inotify.max_user_instances >= 1024` (default 128 is too low; `sind doctor` warns below it)

## Supported Versions

- Slurm 26.05 and 25.11 (one node image per release line)
- OpenMPI 5.0 (with PMIx 6.x, PRRTE 4.x, UCX 1.20)
- libjwt 1.x, for Slurm's `auth/slurm` (identity `clientIds`)

## Overview

sind creates and manages containerized Slurm clusters for development, testing, and CI/CD workflows. Each node runs as a separate Docker container with systemd as init, providing a realistic multi-node Slurm environment without requiring bare-metal infrastructure.

### Operational Model

While the cluster configuration file resembles a Kubernetes manifest, sind is **not** a reconciling controller. The configuration is a one-shot, one-way input for cluster creation:

- `sind create cluster` interprets the manifest once to generate the cluster (via `--config FILE`, or `--config -` for stdin)
- sind does not continuously watch or reconcile cluster state
- sind does not automatically repair drift or failures

sind provides commands for inspection (`get`), modification (`create/delete worker`), and simulation (`power`) but these are imperative operations, not declarative state management.

This design is intentional: sind is a development and testing tool that aids the creation of more sophisticated Slurm cluster management tooling, not a production cluster controller.

### Container Startup

sind creates cluster resources in a specific order to ensure dependencies are available:

**Phase 1: Global Infrastructure**
1. Create `sind-mesh` network (if not exists)
2. Create and start the `sind-dns` container (if not exists), or start it (if stopped), in parallel with step 3
3. Create the `sind-ssh-config` volume (if not exists)
4. Create the `sind-ssh` container (if not exists), copy a new keypair into a new volume through it, and start it (or start it, if stopped), after `sind-dns`, whose address it resolves through

A mesh that exists but is stopped, as after a host reboot, is started, the DNS container first (see Host Reboot and Docker Daemon Restart).

**Phase 2: Cluster Resources** (concurrent pipelines, no barriers)

With `--pull`, sind pulls each distinct image of the cluster's nodes once (`docker pull`), concurrently with the preflight check, the mesh lookups and the creation of the network and volumes; the steps that run an image (the helper containers, the Slurm version check, the CVMFS check and the nodes) wait for it and create their containers without `--pull always`, so every node runs the same image and the registry is asked once per image.

1. Create cluster network → connect the realm's SSH relay container to it
2. Create config volume → write Slurm configuration, and `slurmdbd.conf` for a managed db node (managed clusters only; see Unmanaged Cluster)
3. Create munge volume → generate and write munge key (not with identity `clientIds`, whose `slurm.key` goes to the config volume)
4. Create data volume (if needed)
5. Create state volume (backup controller only)
6. Create home volume (`users` only)
7. Detect the CVMFS backend (`storage.cvmfs` only; see CVMFS Mount)

**Phase 3: Node Containers**
1. Create and start each node container in parallel
2. Start per-node systemd D-Bus monitor immediately after each container starts
3. Wait for each node to become ready, accelerated by events
4. On managed workers with identity `nssSlurm` or `clientIds`, check for nss_slurm and put it first in `/etc/nsswitch.conf` (see Identity Modes)
5. Add the cluster users and groups where the identity mode puts them (`users` and `groups` only; see Users)

Every node container starts with a short `/bin/sh` entrypoint instead of the image's own entrypoint or command. As PID 1, it moves itself into `init.scope`, enables each controller of the container's root cgroup in its `cgroup.subtree_control`, and execs `/sbin/init`. `docker exec` puts its process in the container's root cgroup unless that cgroup has controllers enabled, and a process there makes systemd's own attempt to enable them fail (cgroup v2's no internal processes rule). An exec of sind's that landed before systemd had enabled controllers would otherwise leave every unit without them, including `Delegate=yes` daemons such as slurmd.

There is no barrier between node creation and readiness probing — each node's goroutine creates its container, starts a systemd monitor, and begins probing in a single pipeline. This allows early-starting nodes to be probed while later nodes are still being created.

#### Event-Driven Readiness

sind uses two event sources to accelerate readiness detection:

- **Docker events** — a single `docker events` stream watches all cluster containers for start/die events
- **Systemd D-Bus monitors** — per-node `busctl monitor --watch-bind=yes` streams watch for unit state changes (e.g., sshd.service becoming active or slurmd.service failing). They run until the command ends, through the waits for the Slurm daemons and the accounts

When an event of the node arrives, its readiness probes re-evaluate immediately instead of waiting for the next poll tick; the events queued by then go with it, so a burst of unit changes during boot costs one probe round. Each node's wait receives only its own container's events, and the docker events stream asks only for the start, die, oom, pause and unpause actions, not for the exec events of the probes. If the event sources are unavailable, sind falls back to poll-only mode transparently.

#### Readiness Checks

| Check | Description |
|-------|-------------|
| Container running | Docker container in running state |
| systemd ready | `systemctl is-system-running` returns `running` or `degraded` |
| sshd listening | Port 22 accepting connections |
| munge ready | munge service active (not with identity `clientIds`, which masks munge) |
| slurmctld ready | `scontrol ping` reports this controller UP (controllers of managed clusters; each controller of a backup pair is checked for its own host); while it does not, a failed slurmctld unit ends the wait |
| slurmd ready | slurmd service active (managed workers only) |
| sackd ready | sackd service active (the submitter of a managed cluster with identity `clientIds`) |
| slurmdbd ready | slurmdbd service active (managed db nodes). mariadb is started before it with `systemctl enable --now`, which returns once the unit is active |
| cluster registered | `sacctmgr show cluster` on `controller` lists the cluster (`accounts` only, before the accounts are created) |

A unit that has failed does not recover on its own: a failed munge, slurmctld, slurmd, sackd or slurmdbd unit fails `sind create cluster` and `sind create worker` at once, with the tail of the unit's journal. So does a container that exits.

`--wait DURATION` (default `5m`, `0` for no limit) bounds how long `sind create cluster` and `sind create worker` wait for the nodes and Slurm: the node checks above, the Slurm daemons, and with `accounts` the cluster's registration with slurmdbd and the `sacctmgr` commands. Each node's limit counts from when its container has started, so image pulls do not count; the steps after the nodes are ready (Phase 4) end the limit after the last node container started. When a node or check is not ready in time, the command fails with status `1`, not `130`, with an error that names the node and its last failing check (`cluster dev not ready within 5m0s: waiting for worker-0: ... last probe error: probe munge: ...`). The library returns that error wrapping `cluster.ErrNotReady`; `config.Cluster.Wait` and `WorkerAddOptions.Wait` set the limit, and zero sets none.

When a check fails for good, or the limit runs out, `sind create cluster` removes the resources it created, and the mesh if this invocation set it up. If that cleanup fails too, `sind delete cluster` removes what is left.

**Phase 4: Mesh Registration, Slurm and Home Directories** (concurrent)

After all nodes are ready, sind runs mesh registration (batch DNS ║ known_hosts), Slurm enablement and, with `users`, the creation of the home directories concurrently. This is safe because Slurm uses short hostnames (`controller`, `worker-0`) resolved by Docker's embedded DNS on the cluster network, which nodes join with gateway priority 1 so that it answers before the mesh (see Cluster Network). The mesh DNS records (`*.cluster.realm.sind`) are only used for SSH relay access and host-side resolution. Unmanaged clusters skip Slurm enablement.

With a managed db node, Slurm enablement starts on the db node: mariadb, the accounting database and user, then slurmdbd, which must be active before slurmctld and slurmd are enabled (in parallel, as without a db node). slurmctld registers the cluster with slurmdbd when it starts (see Database Node). With `accounts`, sind then waits until `sacctmgr show cluster` lists the cluster and creates the Slurm accounts and associations (see Slurm Accounts).

### Design Goals

- Familiar UX for kind users
- No root/admin privileges (sudo) or privileged containers required; the Docker daemon itself must be rootful (see Prerequisites)
- SELinux compatible: clusters work on hosts whose Docker daemon labels containers, without host
  policy changes (the nodes then run unconfined, see Mount Options)
- Support for both static and dynamic Slurm node configurations

### Implementation

sind is written in Go and designed for dual use:

1. **CLI tool** - Standalone command-line interface
2. **Go library** - Embeddable package for wrapper tools and integrations

The CLI command structure is reflected in the library API, allowing programmatic access to all sind operations.

Library contract of `cluster.Create`: the caller sets up the mesh (`mesh.Manager.EnsureMesh`) and applies the config's defaults (`config.Cluster.ApplyDefaults`); `Create` validates the config itself before it creates anything, and refuses a config `realm` other than its `mesh.Manager`'s realm, the realm it creates the cluster in. The CLI resolves the realm first and sets it on the config.

Errors that a library caller branches on wrap exported sentinels, so `errors.Is` tells them apart without matching messages: `cluster.ErrClusterExists` (preflight conflicts), `ErrClusterNotFound` (a cluster or its controller is missing), `ErrNodeNotFound`, `ErrNodesConfMissing` (managed workers without `sind-nodes.conf`) and `ErrNotReady` (the `--wait` limit ran out). When `Create` or `WorkerAdd` fails and its rollback fails too, the returned error joins the rollback's failures to the original one (`errors.Join`), so the caller learns that resources were left behind.

### Go Dependencies

sind uses a minimal set of dependencies, following [kind](https://kind.sigs.k8s.io/)'s approach of favoring simplicity and compatibility.

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/pflag` | Flag library under cobra: the MCP tool flag filter, and the flag errors that exit 2 as usage errors |
| `sigs.k8s.io/yaml` | YAML configuration parsing |
| `log/slog` (stdlib) | Structured logging interface |
| `github.com/charmbracelet/log` | Colorized log output (slog handler) |
| `github.com/charmbracelet/lipgloss` | Style of the TRACE level in the log output |
| `github.com/mattn/go-isatty` | TTY detection for interactive commands |
| `github.com/njayp/ophis` | MCP server framework |
| `github.com/modelcontextprotocol/go-sdk` | MCP request and result types for the ophis tool middleware; the bearer-token check and HTTP server of `sind mcp stream` |
| `github.com/spf13/afero` | Filesystem abstraction for testability |
| `golang.org/x/sync` | Errgroup for concurrent operations |
| `golang.org/x/sys` | Advisory file locking (flock) for realm locks |

**Nodeset expansion** (e.g., `worker-[0-2,5]` → individual hostnames) is implemented internally rather than using an external library, keeping the dependency footprint small.

### Docker Interaction

sind interacts with Docker by **shelling out to the `docker` CLI** rather than using the Docker SDK for Go. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- Avoids tight coupling to Docker daemon internals

sind wraps command execution in a thin abstraction layer (`pkg/cmdexec`) using Go's `os/exec` package, with proper output handling and error reporting. The executor interface is shared across `pkg/docker`, `pkg/mesh`, and `pkg/cluster`.

A `docker.Client` runs at most 16 docker commands at once (`docker.MaxConcurrentCalls`); the others wait for a free slot. Creating a cluster probes every node in its own goroutine, and the bound keeps a large cluster from forking hundreds of docker processes that compete with the booting nodes for the daemon. It limits the commands, not the per-node goroutines, so every node still boots at once. Long-lived streams (`docker events`, `busctl monitor`) take no slot.

**Runtime support:** Docker only. Support for alternative runtimes (Podman, nerdctl) may be added later via a provider abstraction pattern.

## License

This project is licensed under the GNU Lesser General Public License v3.0 or later (LGPL-3.0-or-later).

## Commit Guidelines

The git history follows [Conventional Commits](https://www.conventionalcommits.org/) style.

### Principles

- **Fine-grained commits** - Each commit should represent a single logical change, sized for easy comprehension when reading history
- **Context-free messages** - Commit messages state facts about the change, not the development story; they are written for future readers of the history, not as a journal of the development process
- **No narrative** - Avoid "I tried X, then Y, finally Z worked"; instead state what the commit does

### Format

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`

Footer: commits written with substantial help from an AI coding tool carry a `Co-Authored-By:` trailer naming the tool (e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`); see the AI disclosure in the README.

## CLI Design Guidelines

Rules for maintaining consistency when adding new commands, flags, and output.

### Command Structure

Commands follow **verb-noun** ordering with a two-level hierarchy:

```
sind <verb> <noun> [ARGS] [FLAGS]
```

- Multi-resource verbs (`create`, `delete`, `get`, `power`) group noun subcommands
- Single-purpose verbs (`ssh`, `enter`, `exec`, `logs`, `doctor`) stand alone
- Standalone verbs are reserved for frequently-used operations that justify a short path
- Groups print their help when invoked bare and fail on an unknown subcommand (`sind get bogus` exits 2); `NewRootCommand` applies this to every group, including ones added later
- cobra's built-in `help` and `completion` commands follow the same rule: `sind completion fish-typo` fails like any group, and `sind help TOPIC` fails unless TOPIC names a command in full (`sind help bogus`, `sind help get bogus`)

### Argument Conventions

| Pattern | Positional | Default | Examples |
|---------|-----------|---------|---------|
| Cluster name | `[NAME]` or `[CLUSTER]` | `"default"` | `get cluster`, `enter`, `get auth-key` |
| Node targets | `NODES` (required) | — | `power shutdown`, `delete worker` |
| Node format | `shortname.cluster` | cluster defaults to `"default"` | `worker-0.dev`, `controller` |
| Nodeset expansion | bracket patterns | — | `worker-[0-2].dev` |
| Pass-through | after `--` separator | — | `ssh NODE -- cmd`, `exec -- cmd` |

Rules:
- Cluster names are **always positional**, never flags
- Node targets support nodeset expansion and comma-separated specs
- Use `optionalCluster` (`cobra.MaximumNArgs(1)` plus the name check) for optional cluster, `cobra.MinimumNArgs(1)` for required nodes

### Flag Conventions

- **Long-form only** by default; add short flags (`-f`) only for frequently-typed flags
- **Kebab-case** for multi-word flags: `--tmp-size`, `--cap-add`
- **Boolean flags** for mode switches: `--all`, `--pull`, `--unmanaged`
- **One persistent root flag**: `--realm` (inherited by every subcommand)
- **One persistent root counter**: `-v` (repeatable, controls log verbosity; inherited by every subcommand)
- `ssh` passes its arguments through to SSH, so `--realm` and `-v` must precede it (`sind -v ssh worker-0`); only a leading `-h` or `--help` is sind's, and an argument that begins with `--` before the `--` separator is a usage error, as SSH has no long options. `exec` parses its own flags up to its `--`

### Output Conventions

| Command type | Output | Target |
|-------------|--------|--------|
| List resources (`get`) | tabwriter table, uppercase headers, 3-space padding | stdout |
| Single value (`get auth-key`) | raw value, one line | stdout |
| Checks (`doctor`) | one ✓/✗ line per check, fix instructions below a check that did not pass | stdout |
| Mutations (`create`, `delete`, `power`) | silent on success | — |
| Errors | structured slog at error level (always visible) | stderr |
| Warnings | `Warning: ...` prefix | stderr |
| Logs (`-v`) | structured key=value, colorized on TTYs | stderr |

Rules:
- Mutations are silent — `exit 0` is the confirmation; use `-v` for progress
- Errors are always visible (slog error level is always enabled, even without `-v`), except that `ssh`, `exec`, `enter` and `logs` add none when the program they run fails: it writes its own
- The final error line, and the `Warning:` line printed when the SSH config export fails, are escaped: it can quote what docker or a container wrote, so control characters, bidirectional controls and invalid UTF-8 in it are shown as `\x1b`, `\u202e` or `\xff` instead of reaching the terminal; newline and tab are kept. The cells of `get` tables (`cell`), whose text comes from labels, containers and docker, and the details of `doctor` checks are escaped the same way; a table cell also shows tab and newline as `\t` and `\n`, which would otherwise start a column or a row. JSON output, the `sind logs` stream and key or `known_hosts` output are written unchanged
- Command output (tables, status, doctor) is monochrome — no ANSI escapes
- Log output (`-v`) is colorized on interactive terminals, plain when piped
- Unicode checkmarks (✓/✗) only in `get cluster`, `get node` and `doctor` output
- All `get` subcommands and `doctor` accept `--output|-o {human,json}`; default is `human`

Exit status and signals:
- `0` on success, `1` on failure, `2` for a usage error, `130` when SIGINT or SIGTERM interrupted the command (`cmd/sind/exitcode.go`). Scripts branch on them, so a status may be added but never renumbered
- A usage error is a command line that sind rejects before it acts: an unknown command or flag, a flag value or argument that is not valid, or the wrong number of arguments. That is every pflag parse error, wherever it happens (cobra parses the flags of the parents it traverses without its `FlagErrorFunc`, and `exec` parses its own), every `Args` check (`usageArgs` wraps them all, `requireKnownSubcommand` and `helpTopic` among them), and the checks a command makes of its arguments and flags before it acts, which return `usage(err)`: node arguments, `-o`, `--realm`, and the arguments `ssh` and `exec` parse themselves. `SIND_REALM` and the config file are not the command line, so an invalid one exits `1`
- `ssh`, `exec`, `enter` and `logs` exit with the status of the docker command they run, which `docker exec` takes from the command it ran (for `ssh`, from `ssh` and the remote command), and print no error line. A docker killed by signal N exits 128 + N, as a shell reports it, except SIGINT and SIGTERM, which exit `130` like an interrupted sind: they reach docker when sent to sind's process group, and the status must not depend on which process ends first
- SIGTERM exits `130`, not `143`, so that callers check one status for "interrupted", as in clusterctl
- A `--wait` limit that runs out is a failure, not an interrupt: `create cluster` and `create worker` exit `1`
- The first SIGINT or SIGTERM cancels the command's context; deferred cleanup (e.g. the rollback of a failed `create cluster`) still runs under `context.WithoutCancel`, bounded at 5 minutes, and a cleanup step that fails is added to the command's error
- The signal handler is removed before the context is cancelled, so a second signal gets the default action and ends sind at once, even during a hung cleanup

### Logging Conventions

Logging uses `pkg/log` with context-based injection. Silent by default. All log lines include millisecond timestamps (`HH:MM:SS.mmm`) for timing analysis.

| Level | Flag | What to log |
|-------|------|------------|
| Error | — | Always visible; command failures |
| Info | `-v` | Phase transitions: "creating cluster", "nodes ready", "slurm services enabled" |
| Debug | `-vv` | Individual operations: "waiting for node", "starting readiness probes", "enabling slurm service" |
| Trace | `-vvv` | Docker commands, probe retry attempts with error details |

Rules:
- Use `sindlog.From(ctx)` to extract the logger — never `slog.Default()`
- In errgroup goroutines, log with `gctx` not the outer `ctx`
- Log messages use lowercase, present tense: "creating network", not "Created network"
- Include identifying attrs: `"node", shortName`, `"name", netName`, `"service", svcName`

### Shell Completion

All commands that accept cluster names or node names must set `ValidArgsFunction`:

- **Cluster name commands** → `completeClusterNames`
- **Node name commands** → `completeNodeNames`
- **Commands with DisableFlagParsing** (ssh, exec) → `ValidArgsFunction` with heuristics (best-effort despite cobra limitations)

When adding a `get` subcommand with positional arg completion for a second argument
(like `logs NODE SERVICE`), write a dedicated completion function that switches on `len(args)`.

### New Command Checklist

When introducing a new command:

1. **Structure**: verb-noun ordering, consistent with existing hierarchy
2. **Args**: cluster as optional positional (default "default"), nodes as required positional
3. **Flags**: long-form, kebab-case, minimal short flags
4. **Completion**: add `ValidArgsFunction` for cluster/node args
5. **Output**: table for lists, confirmation for mutations, silence for passthrough
6. **Logging**: info for phases, debug for operations, trace for raw commands
7. **Errors**: wrap with `fmt.Errorf("context: %w", err)`, no error prefixes; an argument or flag value that is not valid returns `usage(err)` or `usagef(...)`, so that it exits 2 (`Args` checks are wrapped already)
8. **Tests**: unit test with mock executor, integration test in lifecycle test
9. **MCP**: classify the command in `mcpEffects`, or list it in `mcpExcluded` (`cmd/sind/mcp.go`)
10. **Docs**: update DESIGN.md CLI Commands section, update docs/content/

## Testing

Development follows Test-Driven Development (TDD) style:

1. Write failing test
2. Implement minimal code to pass
3. Refactor

### Requirements

- High unit test coverage for all packages
- Integration tests for CLI commands and cluster operations
- Tests run in CI for every pull request and every push to `main`

## CLI Commands

### Cluster Management

```bash
sind create cluster [NAME] [--config FILE] [--data PATH] [--pull] [--wait DURATION]
sind delete cluster [NAME]
sind delete cluster --all
sind get cluster [NAME]
sind get clusters
sind get node NODE[.CLUSTER]
sind get nodes [CLUSTER]
sind get networks
sind get realms
sind get volumes
sind get mesh
sind get dns
sind get ssh-config
sind get ssh-private-key
sind get ssh-public-key
sind get ssh-known-hosts
sind get auth-key [CLUSTER]
```

All `get` subcommands accept `--output|-o {human,json}`. The default is `human` (tabular text); `json` emits a machine-readable document.

NAME/CLUSTER defaults to `default` if omitted, except for `get nodes`, which then lists the nodes of every cluster.

`sind create cluster` reads its configuration from `--config FILE`, or from stdin with `--config -`, where empty input is an error. Without `--config` it creates the default cluster (1 controller + 1 worker). For one release, a stdin that is not a terminal is still read without `--config -`: sind first writes a deprecation `Warning:` to stderr, since it waits for the end of that input, and takes empty input for the default cluster. A wrapper that hands sind a pipe it does not mean as the configuration (e.g. `ssh HOST sind create cluster`, or a `while read` loop) redirects stdin from `/dev/null`.

`sind create cluster` validates the environment before creating, failing if conflicting resources (containers, networks, volumes with matching names) already exist.

`sind delete cluster` is idempotent and robust:
- Deleting a non-existent cluster is not an error
- Handles partial/broken clusters (e.g., failed creation)
- Removes all matching Docker resources regardless of state
- Updates `~/.local/state/sind/<realm>/known_hosts` (or `$XDG_STATE_HOME/sind/<realm>/known_hosts`) to remove deleted nodes
- Order: stops/removes containers → disconnects/removes networks → removes volumes

Example output:

```
$ sind get clusters
NAME      NODES (S/C/D/W)   SLURM     STATUS
default   4 (1/1/0/2)       26.05.4   running
dev       4 (0/1/1/2)       25.11.8   running
```

NODES column shows total count and breakdown: **S**ubmitter / **C**ontroller / **D**b / **W**orker. SLURM shows `-` when sind does not know the version, as for unmanaged clusters; `sind get cluster` does the same.

```
$ sind get nodes dev
CONTAINER            ROLE         FQDN                         IP           STATUS
sind-dev-controller  controller   controller.dev.sind.sind     172.19.0.2   running
sind-dev-db          db           db.dev.sind.sind             172.19.0.5   running
sind-dev-worker-0    worker       worker-0.dev.sind.sind       172.19.0.3   running
sind-dev-worker-1    worker       worker-1.dev.sind.sind       172.19.0.4   running
```

Without a cluster argument, `sind get nodes` lists every node in the realm and adds a `CLUSTER` column. Rows are sorted by `(cluster, role, natural-name)` so `worker-2` precedes `worker-10`:

```
$ sind get nodes
CONTAINER                CLUSTER   ROLE         FQDN                             IP           STATUS
sind-default-controller  default   controller   controller.default.sind.sind     172.19.0.2   running
sind-default-worker-0    default   worker       worker-0.default.sind.sind       172.19.0.3   running
sind-dev-controller      dev       controller   controller.dev.sind.sind         172.20.0.2   running
sind-dev-worker-2        dev       worker       worker-2.dev.sind.sind           172.20.0.3   running
sind-dev-worker-10       dev       worker       worker-10.dev.sind.sind          172.20.0.4   running
```

### Cluster Diagnostics

`sind get cluster [NAME]` displays detailed health information for a cluster:

```
$ sind get cluster dev
CLUSTER   SLURM     STATUS (R/S/P/T)
dev       25.11.8   running (4/0/0/4)

NETWORKS
NAME             DRIVER   SUBNET           GATEWAY        STATUS
sind-mesh        bridge   172.18.0.0/16    172.18.0.1     ✓
sind-dev-net     bridge   172.19.0.0/16    172.19.0.1     ✓

MESH SERVICES
NAME   CONTAINER   STATUS
dns    sind-dns    ✓
ssh    sind-ssh    ✓

MOUNTS
MOUNT        SOURCE                    TYPE       STATUS
/etc/slurm   sind-dev-config           volume     ✓
/etc/munge   sind-dev-munge            volume     ✓
/data        /home/user/project        hostPath   ✓

NODES
NAME              ROLE        IP            STATUS    SERVICES
controller.dev    controller  172.19.0.2    running   munge ✓ slurmctld ✓ sshd ✓
db.dev            db          172.19.0.5    running   mariadb ✓ munge ✓ slurmdbd ✓ sshd ✓
worker-0.dev      worker      172.19.0.3    running   munge ✓ slurmd ✓ sshd ✓
worker-1.dev      worker      172.19.0.4    running   munge ✓ slurmd ✗ sshd ✓
```

`MESH SERVICES` shows ✓ for the realm's DNS container and SSH relay while they run (`dns_ok`, `ssh_ok` in the JSON output); after a host reboot they exist but are stopped (see Host Reboot and Docker Daemon Restart).

The data row (`/data`, or the configured mount point) shows what Docker mounts there on the nodes, as `docker inspect` reports it: the bind-mounted host directory or the volume. Clusters with `users` add `/home`, the home volume `<realm>-<cluster>-home`, to `MOUNTS`. With `storage.cvmfs`, `MOUNTS` adds `/cvmfs`: source `cvmfs` of type `volume` for the volume plugin, whose status tells whether Docker finds the plugin volume, or source `/cvmfs` of type `hostPath`.

`SERVICES` lists munge and sshd for every node, plus slurmctld, slurmd, or mariadb and slurmdbd on a db node, where sind manages Slurm. Unmanaged nodes (unmanaged workers and db nodes, and every node of an unmanaged cluster) list only munge and sshd. With identity `clientIds` no node lists munge, and the submitter lists sackd. The JSON output marks each node with `"managed": true|false`; `sind get node -o json` has the same field.

Clusters with a backup controller add an `HA` column to the `NODES` table: each controller's position (`primary` or `backup`), with `*` on the controller in control. Other nodes leave it empty. The JSON output adds `"ha": {"position": "backup", "in_control": true}` to each controller's `health`; `sind get node -o json` adds the same `ha` object. Single-controller clusters show neither, and neither do unmanaged clusters: sind cannot tell which of their controllers is in control.

```
NODES
NAME                    ROLE        HA         IP            STATUS    SERVICES
controller.dev          controller  primary    172.19.0.2    running   munge ✓ slurmctld ✗ sshd ✓
controller-backup.dev   controller  backup*    172.19.0.3    running   munge ✓ slurmctld ✓ sshd ✓
worker-0.dev            worker                 172.19.0.4    running   munge ✓ slurmd ✓ sshd ✓
```

`sind get node NODE[.CLUSTER]` shows detailed health for a single node. NODE uses the format `shortName` or `shortName.cluster` (defaults to cluster "default"). Passing a full DNS FQDN (`NODE.CLUSTER.REALM.sind`) is rejected — use the bare short name or the `NODE.CLUSTER` form:

```
$ sind get node controller.dev
CONTAINER             ROLE         FQDN                       IP           STATUS
sind-dev-controller   controller   controller.dev.sind.sind   172.19.0.2   running

SERVICES
NAME        STATUS
munge       ✓
slurmctld   ✓
sshd        ✓
```

A controller of a backup pair gets an `HA` column between `ROLE` and `FQDN` (`primary` or `backup`, with `*` on the controller in control), as in `sind get cluster`.

### Host Diagnostics

`sind doctor` validates host prerequisites for running sind:

```bash
sind doctor [-o json]                    # check Docker version and mode, cgroupv2, inotify, DNS policy
```

Checks the Docker Engine version (from `docker info`), that the daemon is rootful and has no `userns-remap` (`cluster.DaemonSupport`; skipped when Docker is not reachable), that Docker runs containers on cgroup v2 (`docker info`'s `CgroupVersion`) and this host mounts cgroup2 at `/sys/fs/cgroup` with `nsdelegate` (a hybrid host, whose cgroup2 is at `/sys/fs/cgroup/unified`, fails), that `fs.inotify.max_user_instances` is at least 1024 (advisory; left out when it cannot be read or `DOCKER_HOST` is not a `unix://` socket), and that polkit allows host DNS resolution via systemd-resolved (a `warning` without asking polkit when `DOCKER_HOST` is not a `unix://` socket: the mesh bridge is on the daemon's host). The results go to stdout, one `✓`/`✗` line per check, with the commands that fix a check that did not pass below it. Exits non-zero if any required prerequisite fails, in either output format; the error line naming the failed checks goes to stderr. When Docker is not reachable, the Docker Engine detail is `not reachable: ` and the first line of docker's error (`doctor.DockerUnreachable`), with a remediation for a missing `docker` CLI, a user outside the docker group and a daemon that is not running. Details are escaped in the human output like the final error line, since they can quote docker.

`-o json` prints the checks as a JSON array in clusterctl's check model: each entry has `name` (`Docker Engine`, `Docker daemon`, `cgroupv2`, `inotify`, `DNS policy`), `status` (`ok`, `failed`, or `warning` for the advisory inotify and DNS policy checks, which never fail doctor: sind-action runs it as a gate), `detail`, and, for a check that did not pass and has a fix, `remediation` with the commands.

### Node Access

```bash
sind ssh [SSH_OPTIONS] [USER@]NODE [-- COMMAND]  # SSH into a specific node (passthrough)
sind enter [CLUSTER] [--user USER]               # Interactive shell on submitter/controller
sind exec [CLUSTER] [--user USER] -- <cmd>       # One-shot command on submitter/controller
```

NODE uses DNS-style naming (see Node Arguments). CLUSTER defaults to `default`.

`USER@` and `--user|-u USER` log in as a cluster user (see Users) instead of root. `sind ssh USER@NODE` is `ssh -l USER`; `enter` and `exec` run as USER in its home directory, `/home/USER`. `--user root` is the default. A USER that is not a valid user name is a usage error.

`sind ssh` passes the SSH options, before or after NODE, and the command after `--` through to the underlying SSH command. Any other argument before the `--` is a usage error. See the SSH section for details.

### Worker Lifecycle

```bash
sind create worker [CLUSTER] [FLAGS]    # add worker nodes
sind delete worker NODES               # remove worker nodes from cluster
```

**create worker flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--count N` | 1 | Number of nodes to add |
| `--image IMAGE` | the newest worker's image, else the controller's | Container image |
| `--cpus N` | the newest worker's, else 1 | CPU limit per node |
| `--memory SIZE` | the newest worker's, else 512m | Memory limit |
| `--tmp-size SIZE` | the newest worker's, else 256m | /tmp tmpfs size |
| `--unmanaged` | false | Don't start slurmd, don't add to slurm.conf (implied on unmanaged clusters) |
| `--pull` | false | Pull the `--image` once before creating containers; needs `--image` |
| `--wait DURATION` | 5m | How long to wait for the new workers to become ready, counted from when their containers have started; `0` for no limit (see Readiness Checks) |
| `--cap-add CAP` | the newest worker's, else none | Add Linux capability (repeatable; e.g. `SYS_ADMIN`) |
| `--cap-drop CAP` | the newest worker's, else none | Drop Linux capability (repeatable) |
| `--device PATH` | the newest worker's, else none | Expose host device (repeatable; e.g. `/dev/fuse`) |
| `--security-opt OPT` | the newest worker's, else none | Security option (repeatable) |

New workers take the shape of the cluster's newest worker, the one with the highest index among those managed like the new ones (or among all workers if none is): sind reads its image ID, CPU and memory limits, `/tmp` size, capabilities, devices and security options from `docker inspect`, so a bare `sind create worker` adds nodes like the ones the cluster has, whatever its `defaults` were. Two things are left out because sind decides them for each new worker: the `SYS_NICE` capability it adds by itself, and the security options every node gets; a seccomp profile is not inherited either, as docker reports its content rather than its file (pass it again with `--security-opt`). A cluster without workers gets the controller's image and 1 CPU, 512m and 256m. Each flag that is given replaces the inherited value; a repeatable flag replaces the whole inherited list. The cluster-wide settings, the data and CVMFS mounts, the users and the identity mode, come from the controller's labels as `docker inspect` reports them, a map (`docker ps` joins all labels with commas, which cuts a path with a comma short); a `sind.data.hostpath` that is not an absolute path is refused rather than bind-mounted.

Without `--image`, the workers run the newest worker's (or controller's) image by its ID, not by the tag it was created from, which may have moved to another image since; `--pull` therefore needs `--image`. With `--image`, sind runs `slurmctld -V` in the image (pulling it first with `--pull`) and refuses one whose Slurm version is not the cluster's (`sind.slurm.version`), as slurmd must not be newer than slurmctld; unmanaged workers and clusters without a recorded version skip this check.

`--count` must be at least 1 and `--cpus` must not be negative. These, `--pull` without `--image`, and `--cap-add`, `--cap-drop`, `--device`, `--security-opt`, `--memory` and `--tmp-size`, which are checked like the config's `capAdd`, `capDrop`, `devices`, `securityOpt`, `memory` and `tmpSize`, are usage errors that sind reports before it takes the realm lock or creates any container.

With `-v`, `sind create cluster` and `sind create worker` log an info-level `extra privileges` notice for each node that gets extra capabilities, devices or security options, or bind-mounts host directories (the data directory, the host's `/cvmfs`). It is not a warning: mutations stay silent by default.

Examples:

```bash
sind create worker                           # 1 managed node like the newest worker
sind create worker --count 3                 # 3 managed nodes
sind create worker --count 2 --unmanaged     # 2 unmanaged nodes (slurmd not started)
sind create worker --cpus 2 --memory 1g      # 1 managed node with resource limits
sind create worker dev --count 2             # 2 managed nodes in dev cluster
```

**Managed node workflow:**

By default (without `--unmanaged`), sind:
1. Reads `sind-nodes.conf` from `/etc/slurm` through the controller (fails if the controller does not run or the file is not present)
2. Creates the worker container(s)
3. Adds node definition(s) to `sind-nodes.conf`, replacing a definition of the same name
4. Reconfigures slurmctld (`scontrol reconfigure`)
5. Starts slurmd on the new node(s)

Steps 3 to 5 run while the new workers are registered with the mesh DNS and known_hosts. If any step fails or the command is interrupted after the first container exists, sind removes the new containers, their mesh entries and their `sind-nodes.conf` definitions again (and reconfigures slurmctld), so a retry starts from a clean state.

Managed nodes require the sind-generated Slurm configuration (see Generated Configuration). If `sind-nodes.conf` is missing (e.g., user replaced the config), the command fails with an error. Use `--unmanaged` to add nodes without modifying Slurm configuration. On an unmanaged cluster (see Unmanaged Cluster) every new worker is unmanaged, with or without `--unmanaged`. A controller that is stopped or frozen fails the command with an error that says so: start it with `sind power on` or `sind power unfreeze` first.

**delete worker** deletes containers entirely. Works with both managed and unmanaged nodes. For managed nodes, sind removes them from `sind-nodes.conf` and reconfigures slurmctld before deleting the container, while it removes their mesh DNS and known_hosts entries. sind needs a running controller for this: with the controller stopped or frozen, deleting a managed worker fails and removes nothing, as the node would otherwise stay in the Slurm configuration without a container. Unmanaged workers need no controller. When none of the managed workers is in `sind-nodes.conf`, sind leaves the file alone and does not reconfigure. On an unmanaged cluster, or one without `sind-nodes.conf`, it never edits the Slurm configuration or runs `scontrol`.

### Power Control

```bash
sind power shutdown NODES               # graceful shutdown
sind power cut NODES                    # hard power off
sind power on NODES                     # power on
sind power reboot NODES                 # graceful cycle (shutdown + on)
sind power cycle NODES                  # hard cycle (cut + on)
sind power freeze NODES                 # simulate unresponsive node
sind power unfreeze NODES               # resume frozen node
```

| Command | Implementation |
|---------|----------------|
| shutdown | `docker stop` (the image's stop signal, `SIGRTMIN+3` for sind-node, then SIGKILL after 10 s) |
| cut | `docker kill` (immediate SIGKILL) |
| on | `docker start` |
| reboot | `docker stop`, then as `on` |
| cycle | `docker kill`, then as `on` |
| freeze | `docker pause` (cgroup freezer) |
| unfreeze | `docker unpause` |

`docker stop` sends the image's `STOPSIGNAL`. The sind-node images set `SIGRTMIN+3`, which makes systemd (PID 1) shut the node down cleanly. A custom image without it gets SIGTERM, which systemd does not treat as a shutdown request, so the node is killed after Docker's 10-second timeout (see Custom Images).

A power command runs its Docker calls for all nodes in parallel (at most 8 at a time); `reboot` and `cycle` take every node down before they start any. A failing node does not stop the others: the command returns every failure, joined, and exits non-zero.

`on`, `reboot` and `cycle` start the realm's mesh DNS and SSH relay first if they are stopped, start the nodes, and then point the started nodes' mesh DNS records at their current cluster network addresses: Docker releases a container's address when it stops and can give it another one on start, for example after `sind create worker` took the address of a node that was powered off. They warn about nodes created with a mesh DNS address the DNS container no longer has. Since they rewrite the realm's Corefile, they take the realm lock.

Freeze/unfreeze uses Docker's cgroup freezer to suspend all processes. The container remains "running" but is completely unresponsive, simulating a hung or unreachable node.

slurmctld learns that a frozen, cut or shut down worker is gone only from failed pings, as slurmd does not sign off, and marks it `DOWN` after `SlurmdTimeout`. sind keeps Slurm's default of 300 seconds: unlike `SlurmctldTimeout`, which sind lowers only for a backup controller pair, no feature asks for a shorter one, and a short timeout marks slow but healthy nodes `DOWN` on a loaded host. Failure tests set it in `slurm.main`, e.g. `SlurmdTimeout=30`.

### Logs

```bash
sind logs NODE [--follow|-f]           # container logs (stdout/stderr)
sind logs NODE SERVICE [--follow|-f]   # journalctl for specific service
```

Examples:
```bash
sind logs controller --follow          # tail container logs
sind logs controller slurmctld         # slurmctld journal logs
sind logs worker-0 slurmd --follow    # follow slurmd logs
sind logs db slurmdbd                  # slurmdbd journal logs on the db node
```

### Utilities

```bash
sind version [--json]                  # print version information
sind doctor [-o json]                  # check host prerequisites
sind get realms                        # list active realms
sind get auth-key [CLUSTER]            # output the Slurm authentication key (base64)
sind get ssh-config                    # show SSH config path for Include
sind get mesh                          # show mesh infrastructure info
sind get dns                           # list mesh DNS records
sind get ssh-private-key               # output SSH private key
sind get ssh-public-key                # output SSH public key
sind get ssh-known-hosts               # output SSH known_hosts
```

`sind version` prints the version and commit; `--json` adds the Go version and platform (`version`, `commit`, `goVersion`, `platform`). For release builds the output is `sind <version> (<commit>)`. For dev builds `git describe --tags --always --dirty` is used as the version, embedding tag distance and commit hash directly: `sind 0.5.0-3-gabc1234-dirty`. A binary built without a version, such as one from `go install github.com/GSI-HPC/sind/cmd/sind@vX.Y.Z` (releases after v0.9.0), reports the module version the Go toolchain recorded (`sind X.Y.Z`); a plain `go build` from a checkout reports `sind dev` with its commit. The `--json` flag outputs all fields as JSON.

`sind get auth-key` outputs the key that authenticates the cluster's Slurm traffic, encoded as base64, suitable for injection into external management tooling: the munge key, or `slurm.key` with identity `clientIds`. `-o json` returns `{"type": "munge"|"slurm", "key": "<base64>"}`. It replaces `sind get munge-key`.

`sind get ssh-config` outputs the path to the SSH config file for the current realm. Add it as an `Include` in `~/.ssh/config` to enable direct SSH access to nodes.

`sind get mesh` shows mesh infrastructure info: network name, DNS container/IP/zone/image, SSH container/volume/image. The images are the ones the mesh containers run, which can be older than the ones a new mesh gets. Useful for external consumers that need to connect to sind networks.

`sind get ssh-private-key`, `sind get ssh-public-key`, and `sind get ssh-known-hosts` dump SSH credentials to stdout. This replaces the need to extract files from Docker volumes.

### MCP Server

```bash
sind mcp start                          # MCP server on stdio
sind mcp stream [--host H] [--port P]   # MCP server over HTTP (default 127.0.0.1:8080), bearer token required
sind mcp tools                          # export the tool definitions to mcp-tools.json
sind mcp {claude,vscode,cursor} {enable,disable,list}  # register sind with an editor
```

The MCP server is built with ophis and configured in `cmd/sind/mcp.go`. Each tool runs the sind binary with the command's arguments and flags.

- The server reports itself as `sind` with the version `sind version` prints.

- `sind mcp stream` listens on `127.0.0.1` by default; `--host 0.0.0.0` opts in to all interfaces. Every request needs `Authorization: Bearer <token>` (go-sdk's `auth.RequireBearerToken`, constant-time comparison), else `401`: loopback keeps out other hosts but not the other users of this one, and every tool runs sind with the Docker access of the user who started the stream. The token is `SIND_MCP_TOKEN` (printable ASCII without spaces) or, when that is unset, a new `crypto/rand` token written at every start to `$XDG_STATE_HOME/sind/mcp-token` (`~/.local/state/sind/mcp-token`), mode `0600`, through a temporary file and a rename. The stream prints the listen URL, where the token is (never the token) and a client configuration to stderr. ophis's own stream has no hook for authentication, so `runMCPStream` (`cmd/sind/mcpstream.go`) replaces its run function and keeps its flags: it runs `sind mcp start`'s ophis server on an in-memory transport, lists its tools through an MCP client session, and serves them from a second MCP server whose handlers forward each call through that session, so ophis still builds every tool and runs every call with sind's selectors and middlewares. Stopped by SIGINT or SIGTERM, it shuts down and exits 0. `sind mcp start` (stdio) is unchanged and needs no token.
- Every runnable leaf command is a tool, except `enter` and `ssh` (interactive) and `get ssh-private-key` and `get auth-key` (secrets). Command groups, the root among them, are not tools: they only print help. Neither are `help`, `completion` and the `mcp` commands, which ophis leaves out.
- Every tool carries MCP hints from `mcpEffects`: read-only (`readOnlyHint`: `get`, `logs`, `doctor`, `version`), additive (`destructiveHint: false`: `power on`, `power unfreeze`) or destructive (`destructiveHint: true`: `create`, `delete`, `exec`, and the other `power` actions). `create cluster` and `create worker` count as destructive because their flags choose the image a node runs as root, capabilities, devices, security options, a config file and a host directory mounted read-write: one call can run code with host-root power. A new command has to be classified there; a unit test fails otherwise.
- Tools for commands with `-o` (every `get` subcommand and `doctor`) always run with `-o json`, set by an ophis middleware; `-o` is not in their input schema.
- Tool input schemas leave out `-v` (a count flag, which ophis would pass as `--verbose 2`, a stray positional argument) and `logs --follow` (it never ends, and a tool returns its output only when the command exits).
- A tool call whose positional `args` hold a flag (an argument starting with `-`) is refused, because ophis appends the arguments after the flags and they would otherwise bring back `-v`, `--follow` or a `-o` that overrides `-o json`. For `exec` only the arguments before its `--` are checked.
- A tool's result holds the command's stdout, stderr and exit status (`exitCode`): a usage error reads `2`, and `sind_exec` returns the status of the command it ran.

## Node Arguments

Commands accepting node arguments use DNS-style names with optional nodeset expansion.

### Format

```
<role>.<cluster>
<role>-<N>.<cluster>
```

The cluster suffix defaults to `.default` if omitted.

### Nodeset Notation

Nodeset notation (as used in Slurm, pdsh, ClusterShell) is supported for specifying multiple nodes:

| Pattern | Expansion |
|---------|-----------|
| `worker-[0-3]` | worker-0, worker-1, worker-2, worker-3 |
| `worker-[0,2,4]` | worker-0, worker-2, worker-4 |
| `worker-[0-2,5]` | worker-0, worker-1, worker-2, worker-5 |
| `worker-[0-1].dev` | worker-0.dev, worker-1.dev |

Multiple nodesets can be comma-separated:

```bash
sind power shutdown controller,worker-[0-3]
sind power cycle worker-[0-1].dev,worker-[0-3].default
```

A pattern expands to at most 2^20 names, matching clusterctl's nodeset, so a pattern such as `worker-[0-99999999]` is rejected before any name is allocated. An expanded name that begins with `-` is rejected, since a command it is passed to could read it as an option.

### Examples

```bash
sind power shutdown controller                    # controller.default
sind power cycle worker-0                        # worker-0.default
sind power freeze worker-[0-3].dev               # 4 nodes in dev cluster
sind power reboot controller,worker-[0-1]        # multiple nodes in default
```

## Configuration Schema

### Minimal Configuration

The simplest valid configuration creates a minimal cluster with 1 controller and 1 worker node using the generic sind-node image:

```yaml
kind: Cluster
```

This is equivalent to:

```yaml
kind: Cluster
name: default
defaults:
  image: ghcr.io/gsi-hpc/sind-node:latest
nodes:
  - role: controller
  - role: worker
```

When `defaults.image` is omitted, sind uses the generic image `ghcr.io/gsi-hpc/sind-node:latest`.

### Shorthand Node Syntax

Nodes can be specified in short form when only role (and optionally count) are needed:

```yaml
nodes:
  - controller                           # just the role
  - submitter                            # optional roles work too
  - worker: 3                           # role with count
```

This is equivalent to:

```yaml
nodes:
  - role: controller
  - role: submitter
  - role: worker
    count: 3
```

The shorthand and full forms can be mixed in the same configuration.

### Full Configuration Example

```yaml
kind: Cluster
name: test-cluster                       # default: "default"
realm: sind                              # default: "sind"; --realm and SIND_REALM win

defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11 # default: sind-node:latest
  tmpSize: 256m                          # per-node /tmp tmpfs size
  cpus: 1                                # container CPU limit
  memory: 512m                           # container memory limit

storage:
  dataStorage:
    type: hostPath                       # hostPath | volume (default: from --data)
    hostPath: ./data                     # only for type=hostPath; must exist
    mountPath: /data                     # default: /data
  cvmfs: true                            # mount CVMFS read-only at /cvmfs (default: false)

groups:                                  # Linux groups for the users (default: none)
  - name: hpc
    gid: 3000                            # default: lowest free from 1000, after the users

users:                                   # Linux accounts on every node (default: none)
  - alice                                # uid: lowest free from 1000
  - name: bob
    uid: 2001
    group: hpc                           # primary group (default: private group bob)
  - name: carol
    groups: [hpc]                        # supplementary groups
    accounts: [physics]                  # Slurm associations, the first is the default (needs a managed db node)
    coordinator: [physics]               # accounts the user coordinates
    adminLevel: operator                 # operator | admin (default: none)

identity: local                          # local | nssSlurm | clientIds, or {mode, controllerUsers} (see Identity Modes)

accounts:                                # Slurm accounts (needs a managed db node, default: none)
  - name: physics
    parent: root                         # default: root; any account declared before
    limits:                              # passed to sacctmgr as key=value
      GrpTRES: cpu=4

slurm:
  main: |                                # added to slurm.conf, before sind-nodes.conf
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |                              # appended to cgroup.conf
    ConstrainRAMSpace=yes
  slurmdbd: |                            # appended to slurmdbd.conf (needs a managed db node)
    PurgeJobAfter=1month

nodes:
  - role: controller
    tmpSize: 512m                        # override default
    cpus: 1
    memory: 1g
    backupController: true               # add controller-backup (active/passive pair)

  - role: db                             # optional, at most one: MariaDB + slurmdbd

  - role: submitter                      # optional, at most one

  - role: worker
    count: 3                             # default: 1
    cpus: 2
    memory: 1g

  - role: worker
    count: 2
    managed: false                       # slurmd not started, not in slurm.conf
```

### Slurm Configuration Sections

The `slurm` key contains named sections that map to Slurm config files. Each section supports two forms:

- **String**: content appended directly to the config file (`slurm.conf` ends with the `include` of `sind-nodes.conf` after it; see Generated Configuration)
- **Map**: each key creates a fragment in a `.conf.d/` directory, included via explicit `include` directives per fragment file

| Section | Config file | sind generates defaults |
|---------|-------------|:----------------------:|
| `main` | `slurm.conf` | yes |
| `cgroup` | `cgroup.conf` | yes |
| `gres` | `gres.conf` | no |
| `topology` | `topology.conf` | no |
| `plugstack` | `plugstack.conf` | yes (always scaffolded) |
| `slurmdbd` | `slurmdbd.conf` | yes (needs a managed `db` node) |

**String form** — content appended to the config file:

```yaml
slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainRAMSpace=yes
```

**Map form** — named fragments in a `.conf.d/` directory:

```yaml
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
      SchedulerParameters=bf_continue
    resources: |
      SelectType=select/cons_tres
```

This produces:

```
/etc/slurm/
├── slurm.conf              # sind defaults + explicit includes per fragment
├── slurm.conf.d/
│   ├── resources.conf
│   └── scheduling.conf
├── sind-nodes.conf
├── cgroup.conf
├── plugstack.conf          # always: include plugstack.conf.d/*
└── plugstack.conf.d/
```

`plugstack.conf` is always created with an `include plugstack.conf.d/*` directive, and `PlugStackConfig` is always set in `slurm.conf`. This allows SPANK plugins to be dropped in without additional configuration.

Standalone sections (`gres`, `topology`) are only created when configured. They require enabling in `slurm.conf` (e.g., `GresTypes=gpu`, `TopologyPlugin=topology/tree`) via the `main` section.

Validation rules:
- Fragment names must be plain filenames (no path separators)
- Fragment names and content must not be empty

### Node Roles

| Role | Count | Required | Slurm Daemons | Description |
|------|-------|----------|---------------|-------------|
| `controller` | exactly 1 | yes | slurmctld | Cluster controller |
| `db` | 0-1 | no | mariadb, slurmdbd | Accounting database (see Database Node) |
| `submitter` | 0-1 | no | none (clients only; `sackd` with identity `clientIds`) | Job submission node |
| `worker` | 1+ | yes | slurmd | Worker nodes |

### Node Parameters

| Parameter | Scope | Default | Description |
|-----------|-------|---------|-------------|
| `image` | global + per-node | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `tmpSize` | global + per-node | `256m` | tmpfs size for /tmp, in the kernel's tmpfs syntax: a whole number with an optional `k`, `m`, `g`, `t`, `p` or `e`, or a percentage of the memory; files there count against `memory` |
| `cpus` | global + per-node | `1` | CPU limit; a managed worker's Slurm `CPUs` |
| `memory` | global + per-node | `512m` | Memory limit, without swap, in Docker's size syntax (`512m`, `2g`, `2gb`, `1.5GiB`, plain bytes); a managed worker's Slurm `RealMemory`, in MiB; `/dev/shm` gets half of it |
| `capAdd` | global + per-node | none | Extra Linux capabilities (e.g. `SYS_ADMIN`) |
| `capDrop` | global + per-node | none | Dropped Linux capabilities |
| `devices` | global + per-node | none | Host devices to expose (e.g. `/dev/fuse`) |
| `securityOpt` | global + per-node | none | Extra security options |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | controller + db + worker | `true` | Worker: start slurmd and add to slurm.conf. Db: run MariaDB and slurmdbd and configure accounting (see Database Node). Controller: `false` makes the whole cluster unmanaged (see Unmanaged Cluster) |
| `backupController` | controller only | `false` | Add a backup controller, `controller-backup` |

Per-node scalar values override the `defaults` section. List fields (`capAdd`, `capDrop`, `devices`, `securityOpt`) are **merged** with defaults rather than replacing them.

`memory` limits everything in the node: the jobs, the node's own services (systemd, journald, munge, sshd and the Slurm daemon; mariadb and slurmdbd on the db node) and the files in its tmpfs mounts (`/tmp`, `/run`, `/dev/shm`). Slurm's `RealMemory` is the whole limit, so a job that uses all the memory it may allocate, or files left in `/tmp`, can push a node past it; the kernel then OOM-kills the largest process in the node, usually the job. Raise `memory` for jobs that need much memory or `/tmp`. Nodes get no swap (`--memory-swap` equal to `--memory`), so they behave the same on hosts with and without swap.

### Validation Rules

- `nodes` - optional; if omitted, creates 1 controller + 1 worker
- `role: controller` - exactly one (auto-created if nodes omitted)
- `role: db` - at most one
- `role: submitter` - at most one
- `role: worker` - at least one (auto-created if nodes omitted)
- `count` - only valid for worker role; must not be negative, and `0` means the default, 1
- `cpus` - must not be negative, and `0` means the default; `memory` - Docker's size syntax: a number, optionally with a fraction, and an optional unit `b`, `k`, `m`, `g`, `t` or `p` (powers of 1024, either case, the last five optionally followed by `b` or `ib`), at least `6m`, Docker's minimum; `tmpSize` - a whole number with an optional unit `k`, `m`, `g`, `t`, `p` or `e`, or a percentage. These apply to `defaults` too
- `managed` - only valid for controller, db and worker roles; with `managed: false` on the controller, no worker or db node may set `managed: true` and no `slurm` section may be set
- `backupController` - only valid for controller role; with it, `slurm.main` must not set `SlurmctldHost` (or `ControlMachine`, `BackupController`, `BackupAddr`) or `StateSaveLocation`
- `slurm.slurmdbd` - requires a managed `db` node
- `accounts`, and the users' `accounts`, `coordinator` and `adminLevel` - require a managed `db` node; account names hold only lowercase letters, digits, `_` and `-`, do not start with `-`, have at most 64 characters, are unique and are not `root`; a `parent` is `root` or an account declared before; `limits` keys are among the options listed in Slurm Accounts, in any case, with non-empty values; every account a user names is declared, at most once per list; `adminLevel` is `operator` or `admin`; `coordinator` and `adminLevel` need `accounts`
- `capAdd`, `capDrop` - recognized Linux capability names (e.g. `SYS_ADMIN`, `ALL`)
- `devices` - absolute paths
- `securityOpt` - options Docker knows by name: `label=`, `apparmor=`, `seccomp=`, `no-new-privileges`, `writable-cgroups=` or `systempaths=`, each with a value (`no-new-privileges` may go without); Docker checks the values
- `storage.dataStorage` - `type` is `volume` or `hostPath`; `hostPath` requires a `hostPath`; `mountPath` is absolute
- `users`, `groups` - a name or an object; user and group names start with a lowercase letter or `_`, hold only lowercase letters, digits, `_` and `-`, and have at most 32 characters; `uid` and `gid` are between 1000 and 2147483647 and not 65534 (`nobody` in the image) or 65535 (the 16-bit -1); no two users share a name or `uid`, no two groups (private ones included) a name or `gid`; a user's `group` and `groups` are declared in `groups`, and `groups` repeats neither an entry nor `group`
- `identity` - `local`, `nssSlurm` or `clientIds`, or an object with `mode` and `controllerUsers`; `nssSlurm` and `clientIds` require a managed cluster; `controllerUsers` only with `clientIds`; a `LaunchParameters`, `AuthType`, `CredType` or `AuthInfo` that `slurm.main` sets for the mode lists sind's value (see Identity Modes)
- `name`, `realm` - valid cluster and realm names, see [Cluster and Realm Names](#cluster-and-realm-names)
- unknown keys are rejected

Validation checks a config's form, not its intent. A cluster config, like the `--data` flag, is as
trusted as a script run with Docker access: `image` runs any image's services as root,
`storage.dataStorage.hostPath` bind-mounts any host directory read-write (`/` included),
`capAdd`, `devices` and `securityOpt` grant host privileges, and the `slurm` sections set programs
Slurm runs as root (`Prolog`, `HealthCheckProgram`). sind does not gate these behind an opt-in
flag: `--data`, `image` and `sind exec` reach the same, and the docs say so instead
(cluster-config.md, Trust).

### Cluster and Realm Names

Cluster and realm names become part of Docker resource names, of DNS names (`<node>.<cluster>.<realm>.sind`) and of state paths (`$XDG_STATE_HOME/sind/<realm>/`), so each must be one DNS label as RFC 1123 defines it:

- lowercase ASCII letters, digits and `-` only; uppercase is rejected, because DNS ignores case and `Dev` and `dev` would share DNS names
- 1 to 63 characters
- not beginning or ending with `-`
- a cluster is not named `ssh`: its config volume, `<realm>-ssh-config`, would be the realm's SSH volume, which `sind delete cluster ssh` would then try to remove
- with a managed db node, the cluster name has at most 40 characters: slurmdbd names the cluster's MariaDB tables `<cluster>_<table>`, MariaDB allows 64 characters, and Slurm documents the limit of 40. A longer name would keep slurmdbd from registering the cluster

sind checks every place a name enters: the `name` and `realm` config fields, the `[CLUSTER]` argument, the cluster part of node arguments (`worker-0.dev`), `exec`'s cluster argument, `--realm` and `SIND_REALM`. The root command checks `--realm` before any command runs (`checkRealmFlag`), so it is a usage error even for a command that uses no realm, such as `version` or `get realms`. `SIND_REALM` is only checked when it is the realm in effect. The defaults `default` and `sind` are valid.

The realm in effect is `--realm`, then `SIND_REALM`, then, for `sind create cluster` only, the config's `realm`, then `sind` (`resolveRealm` in `cmd/sind/context.go`). Every other command resolves `--realm` > `SIND_REALM` > `sind`, so ranking `SIND_REALM` above the config keeps every command of a session, sind-action's among them, in the realm the environment names. A realm set only in the config does not carry over to later commands; when `get cluster` does not find a cluster in its realm, the error names the realms that hold a cluster of that name (`cluster "dev" not found in realm "sind" (it exists in realm "ci-42")`).

Resource names join realm and cluster with `-`, so realm `ci` with cluster `42-dev` and realm `ci-42` with cluster `dev` share the names `ci-42-dev-*`. The second `sind create cluster` fails its preflight check, and `sind delete cluster` only removes a network or volume whose `sind.realm` and `sind.cluster` labels, if present, name the cluster being deleted.

### Backup Controller

`backupController: true` on the controller spec runs Slurm's active/passive controller pair:

- A second controller container, `controller-backup` (compose container number 2), gets the same image, resources, volumes, capabilities, devices and security options as `controller`.
- `slurm.conf` lists `SlurmctldHost=controller` followed by `SlurmctldHost=controller-backup`, and `SlurmctldTimeout=20` unless `slurm.main` sets it.
- `StateSaveLocation` (`/var/spool/slurmctld`) is the shared volume `<realm>-<cluster>-state`, mounted on both controllers; it only exists for clusters with a backup.
- `slurmctld` is enabled on both controllers in parallel. The backup starts in backup mode because of its `SlurmctldHost` position, and takes over when the primary does not respond for `SlurmctldTimeout` seconds. A starting primary reclaims control from the backup. `sind create cluster` fails unless both controllers answer `scontrol ping` for their own host.
- The controller in control is the one that wrote the `heartbeat` file in `StateSaveLocation` (its `SlurmctldHost` index and a timestamp) within the last 60 seconds and whose `slurmctld` answers. `sind get cluster`, `sind get node`, `sind enter` and `sind exec` use this.
- `sind create worker` and `sind delete worker` edit `sind-nodes.conf` and run `scontrol reconfigure` through the primary, or through the backup when the primary container is gone or stopped.

Failover is triggered with `scontrol takeover` (graceful; the primary's `slurmctld` exits and is restarted with `systemctl start slurmctld` to hand control back) or by an outage of the primary, e.g. `sind power cut controller`, `sind power shutdown controller` or `sind power freeze controller`.

### Unmanaged Cluster

`managed: false` on the controller spec makes the whole cluster unmanaged: sind builds the nodes but leaves Slurm to the user, e.g. to test Chef or Ansible code that provisions Slurm, or to run `slurmctld -Dvvvvvv` by hand.

```yaml
nodes:
  - role: controller
    managed: false
    backupController: true               # optional: controller-backup and the state volume
  - role: worker
    count: 2
```

- sind creates the network, the volumes (config, munge, data, and state with `backupController`), the munge key and every container, `controller-backup` included, with the usual mounts: `/etc/slurm` rw on the controllers and ro elsewhere, like an NFS share, and `/etc/munge` ro.
- It writes nothing to `/etc/slurm`, does not discover the Slurm version (`sind.slurm.version` is set empty, overriding the image's label of that name; `SLURM` shows `-`), and enables no Slurm daemon. Nodes are ready once container, systemd, sshd and munge are.
- Every node is labelled `sind.managed=false`. Workers default to unmanaged; a worker with `managed: true` is rejected, and so is any `slurm` section.
- Identity `nssSlurm` and `clientIds` are rejected, and so are `accounts` and the users' `accounts`, `coordinator` and `adminLevel`, which need a managed db node (see Identity Modes and Slurm Accounts). `users` and `groups` work as with identity `local`: every node gets them and, with `users`, mounts the home volume.
- `sind create worker` adds unmanaged workers with or without `--unmanaged`. `sind delete worker` never edits the Slurm configuration or runs `scontrol`, whatever files `/etc/slurm` holds.
- `sind get cluster` and `sind get node` list only munge and sshd, report `"managed": false`, and show no HA status. Without a submitter, `sind enter` and `sind exec` target `controller` if it runs, otherwise `controller-backup`.
- With `backupController`, both controllers mount the state volume at `/var/spool/slurmctld`. For sind's names to match the user's pair, point `StateSaveLocation` there and list `SlurmctldHost=controller` before `SlurmctldHost=controller-backup`.

The runtime command `sind create controller --unmanaged` is not provided: the mode is fixed at cluster creation.

### Database Node

A `db` node runs MariaDB and slurmdbd, so the cluster records job accounting:

```yaml
nodes:
  - controller
  - db
  - worker: 2
```

- The container is `<realm>-<cluster>-db` with hostname `db`; it gets the defaults and per-node parameters like any node, but not `count` or `backupController`.
- sind writes `slurmdbd.conf` to the config volume, owned by `slurm` with mode `0600` as slurmdbd requires: `DbdHost=db`, `SlurmUser=slurm`, the log in `/var/log/slurm/slurmdbd.log`, the pid file in `/run/slurmdbd`, and MariaDB storage on `localhost` (database `slurm_acct_db`, user `slurm`). MariaDB authenticates the `slurm` account by `unix_socket`: only a process of the OS user `slurm`, which slurmdbd runs as, logs in as it, so the cluster users on the db node cannot reach the accounting database. When the `slurmdbd` section sets a `StoragePass`, as a mirrored site `slurmdbd.conf` would, sind creates the account with that password instead (passing its `mysql_native_password` hash, never the password, to `mysql`). `slurm.slurmdbd` extends it like the other sections. The `slurmdbd.conf.d` fragments of its map form get the same protection, as they can hold secrets such as a `StoragePass`: owned by `slurm` with mode `0600`, in a directory owned by `slurm` with mode `0700`. sind writes these files with their final mode, so they are never readable by others on any node, and the munge key with mode `0400`.
- `slurm.conf` gets `AccountingStorageType=accounting_storage/slurmdbd`, `AccountingStorageHost=db` and `JobAcctGatherType=jobacct_gather/cgroup`, each unless `slurm.main` sets it. sind sets no `AccountingStorageEnforce`: jobs run without users, accounts or associations, and `sacct` reports them. `accounts` declares accounts and associations (see Slurm Accounts).
- At creation, sind enables mariadb, creates the database and the `slurm` database user, enables slurmdbd and waits for it before enabling slurmctld and slurmd. slurmdbd creates its schema and slurmctld registers the cluster (`sacctmgr show cluster`) on their first start. A failed slurmdbd fails `sind create cluster` with the unit's journal tail.
- `sind get cluster` and `sind get node` report mariadb and slurmdbd for the db node, and `sind get clusters` counts it in `NODES (S/C/D/W)`.
- `managed: false` on the db node of a managed cluster makes it a bare node, labelled `sind.managed=false`, for testing your own slurmdbd provisioning while sind runs slurmctld and slurmd. sind then configures no accounting at all: no `slurmdbd.conf` (a `slurm.slurmdbd` section is rejected), no database, and none of the accounting parameters in `slurm.conf`; add them to `slurm.main` to point slurmctld at your slurmdbd. `slurmctld` starts without waiting for the db node, and `sind get cluster` lists only munge and sshd for it.
- In an unmanaged cluster the db node is a bare node like the others: sind starts neither MariaDB nor slurmdbd and writes no `slurmdbd.conf`, leaving them to the provisioning under test.

Each cluster has its own db node; clusters do not share a slurmdbd.

### Users

sind configures SSH access for root on every node. `users` adds Linux user accounts, and `groups` Linux groups for them, e.g. to run Slurm jobs as someone other than root or to test permissions between users:

```yaml
groups:
  - name: hpc
    gid: 3000                            # default: lowest free gid from 1000
users:
  - alice                                # bare name, private group alice
  - name: bob
    uid: 2001                            # default: lowest free uid from 1000
    group: hpc                           # primary group instead of a private one
  - name: carol
    groups: [hpc]                        # supplementary groups
    accounts: [physics]                  # Slurm associations, the first is the default (needs a managed db node)
    coordinator: [physics]               # accounts the user coordinates
    adminLevel: operator                 # operator | admin (default: none)

identity: local                          # local | nssSlurm | clientIds, or {mode, controllerUsers} (see Identity Modes)

accounts:                                # Slurm accounts (needs a managed db node, default: none)
  - name: physics
    parent: root                         # default: root; any account declared before
    limits:                              # passed to sacctmgr as key=value
      GrpTRES: cpu=4
```

- Every node gets each group and user with the same IDs (`groupadd --gid GID`, then `useradd --uid UID --gid GID --groups ... --shell /bin/bash`), so that a user has the same UID and GIDs across the cluster, as munge and Slurm require. The identity modes `nssSlurm` and `clientIds` keep them off some nodes (see Identity Modes).
- A user without `group` gets a private group of its own name with gid = uid; a user with `group` gets none. `groups` adds supplementary groups. Both name groups declared in `groups`.
- A user without `uid` gets the lowest ID from 1000 up that no user has and no group has as an explicit `gid`, in list order. Then each group without `gid` gets the lowest ID from 1000 up that no user or group has. IDs below 1000 belong to the image's system accounts, and 65534 to its `nobody` user and group; 65534 and 65535 are refused and never assigned. Explicit IDs keep file ownership stable across re-creates, e.g. on a host path `/data`.
- The home directories, `/home/<user>`, are on the cluster's home volume, `<realm>-<cluster>-home`, which every node mounts at `/home`, so a job's working directory and output under a home directory are the same on every node. sind creates them once, on `controller`, from `/etc/skel`, owned by the user's uid and primary gid, and puts the realm's SSH public key in each user's `~/.ssh/authorized_keys`: `sind ssh USER@NODE` and the exported `ssh_config` (`ssh -l USER`) work for users as for root.
- Jobs of the users need no extra capability with sind's default `TaskPlugin=task/cgroup`. With `task/affinity` in `slurm.main`'s `TaskPlugin`, managed workers get `--cap-add SYS_NICE` for them (see slurm.conf under Generated Configuration).
- `sind enter --user USER` and `sind exec --user USER` run as the user in its home directory, on the submitter or the controller.
- The users and groups are stored on each container as the `sind.users` and `sind.groups` labels, so `sind create worker` adds them to new workers. The home volume only exists for clusters with users.
- A user or group name that already exists in the image (`root`, `slurm`, `munge`, `wheel`, ...) fails `sind create cluster` with the `groupadd` or `useradd` error.

Users exist on unmanaged clusters too.

### Slurm Accounts

With a managed db node, `accounts` declares Slurm accounts, and the users' `accounts`, `coordinator` and `adminLevel` give them associations and roles:

```yaml
nodes: [controller, db, submitter, worker: 2]
accounts:
  - physics                              # bare name, parent root
  - name: theory
    parent: physics
    limits:
      MaxJobs: 1
users:
  - name: alice
    accounts: [theory]
  - name: bob
    accounts: [physics, theory]          # the first is the default account
    coordinator: [physics]
  - name: carol
    accounts: [physics]
    adminLevel: operator
```

Once slurmctld has registered the cluster (`sacctmgr show cluster` lists it; before that, `sacctmgr` refuses to add users), sind runs `sacctmgr -i` as root on `controller`, in this order:

```bash
sacctmgr -i add account physics
sacctmgr -i add account theory parent=physics MaxJobs=1
sacctmgr -i add user alice account=theory defaultaccount=theory
sacctmgr -i add user bob account=physics,theory defaultaccount=physics
sacctmgr -i add user carol account=physics defaultaccount=physics adminlevel=operator
sacctmgr -i add coordinator account=physics names=bob
```

- Accounts are created in list order, so a parent is declared before its children. `limits` are passed as `key=value`, in key order. Numbers are passed in plain decimal form, as YAML 1.1 reads them before sind sees them: `MaxJobs: 1` becomes `MaxJobs=1`, but `010` is octal and becomes `8`, `0x10` becomes `16`, `1e3` `1000` and `1.10` `1.1`, also for options that take text, such as `Description: 1.0`. A quoted value is passed as written.
- `limits` take these `sacctmgr add account` options, matched case-insensitively: `Description`, `Organization` and `Flags` of the account, and `Comment`, `DefaultQOS`, `Fairshare` (or `Shares`), `GrpJobs`, `GrpJobsAccrue`, `GrpSubmitJobs`, `GrpTRES`, `GrpTRESMins`, `GrpTRESRunMins`, `GrpWall`, `MaxJobs`, `MaxJobsAccrue`, `MaxSubmitJobs`, `MaxTRES` (or `MaxTRESPerJob`), `MaxTRESMins` (or `MaxTRESMinsPerJob`), `MaxTRESPerNode`, `MaxTRESRunMins`, `MaxWall` (or `MaxWallDurationPerJob`), `MinPrioThresh`, `Priority` and `QOS` (or `QosLevel`) of its association. It is an allow list because sacctmgr accepts any prefix of an option name: a deny list could not keep `A`, `Acct`, `N`, `C` or `Pa` off the name, cluster and parent sind sets, and `Where` makes `sacctmgr add account` loop forever.
- Every account a user names must be declared in `accounts`: a typo fails validation instead of creating a new account.
- The Linux users exist on the controller before `sacctmgr` runs (setupNodes), so slurmctld resolves the uids of the new associations right away; it would otherwise retry only once an hour. slurmdbd pushes the associations to slurmctld before `sacctmgr` returns, so jobs can use them as soon as `sind create cluster` returns.
- sind sets no `AccountingStorageEnforce`: jobs run without associations unless `slurm.main` enforces them, e.g. `AccountingStorageEnforce=associations,limits`. Enforcement changes which jobs run, so sind keeps Slurm's default, but `sind create cluster` prints a `Warning:` to stderr when an account sets a `Max*` or `Grp*` limit and `slurm.main`'s `AccountingStorageEnforce` lists none of `limits`, `safe` and `all`, as Slurm then ignores the limit.
- The Slurm accounts and the Linux groups are unrelated, even when they share a name.
- The commands run in one `docker exec` (a script with each argument shell-quoted), which stops at the first failing command: a failing `sacctmgr` fails `sind create cluster` with the command and its output. A `docker exec` that has not finished after 2 minutes fails it too, as does `--wait` running out.
- Accounts need a managed db node, as `slurm.slurmdbd` does: without one, or with `managed: false` on it or the controller, they fail validation.

### Identity Modes

`identity` selects which nodes get the Linux accounts, to mirror how a site resolves its users, and with that how Slurm authenticates them:

```yaml
identity: nssSlurm                       # local (default) | nssSlurm | clientIds
```

```yaml
identity:
  mode: clientIds
  controllerUsers: true                  # also create the users and groups on the controllers, for AllowGroups
```

| | `local` (default) | `nssSlurm` | `clientIds` |
|---|---|---|---|
| Slurm settings | munge | munge + `LaunchParameters=enable_nss_slurm` | `AuthType=auth/slurm` + `CredType=cred/slurm` + `AuthInfo=use_client_ids` + `enable_nss_slurm` |
| Linux accounts on | every node | controllers, submitter, db | login node: the submitter, or the controllers without one; the controllers too with `controllerUsers` |
| Secret sind distributes | munge key | munge key | `slurm.key` |
| munge | on every node | on every node | masked on every node; `sackd` on the submitter |
| Image needs | nothing new | `libnss_slurm.so.2` on managed workers | `libnss_slurm.so.2` on managed workers, `auth/slurm` and `serializer/json` (Slurm built `--with-jwt --with-json`) everywhere, `sackd` on the submitter |
| SSH as a user to | every node | controllers, submitter, db | login node |
| Mirrors a site where | every node has LDAP/SSSD | compute nodes have no directory | only login nodes have a directory |

- **Controller:** with `local` and `nssSlurm`, slurmctld resolves each job's user and groups, and the uids of Slurm users, from its own passwd and group files; for an unknown user it retries only once an hour, which is why the controller gets the users before sind creates the Slurm accounts. With `clientIds` it takes the identity from the user's token: `sackd` on the login node puts the caller's passwd and group entries into it, and slurmctld sets the uid on the user's associations before it processes the request.
- **db:** slurmdbd resolves user names itself only for its own checks (admin levels, coordinators, a user changing their default account); with `clientIds` it learns the uids from tokens. sind gives the db node the accounts in `local` and `nssSlurm`.
- **Workers:** with `nssSlurm` and `clientIds`, a managed worker has no Linux accounts. Before it creates any node, sind refuses a local sind-node image from before identity modes, one whose labels lack `sind.libjwt.version`: such an image has neither nss_slurm nor auth/slurm, and is usually a cached official tag that docker reuses without `--pull`, so the error says to pull a current one with `--pull` (or to rebuild a local build). `sind create cluster` checks the images of the managed workers, and with `clientIds` of every managed node, unless it pulls them; `sind create worker` checks the new workers' image. On each managed worker, sind then checks that the image has nss_slurm (`ldconfig -p`, or `/usr/lib64/libnss_slurm.so.2`) and fails `sind create cluster` or `sind create worker` with the image name if not, then puts `slurm` first on the `passwd` and `group` lines of `/etc/nsswitch.conf`. nss_slurm answers only for the job's user and groups, from the job credential, and only to processes inside a job step; sshd, prolog, epilog and health checks run outside one, so SSH as a user to a worker fails by design. No `/etc/nss_slurm.conf` is needed: the hostname is the Slurm node name and the spool directory is the default.
- **clientIds:** sind writes `slurm.key` (1024 random bytes, `slurm`-owned, mode `0600`) to the config volume, creates no munge volume or key, masks `munge.service` from the node entrypoint (`ln -sf /dev/null /etc/systemd/system/munge.service` before systemd starts) and waits for no munge, and enables `sackd` on the submitter like a Slurm daemon. `slurmdbd.conf` gets `AuthType=auth/slurm` and `AuthInfo=use_client_ids`. Root's client commands on the other nodes get their tokens from the SACK service of slurmctld, slurmdbd or slurmd.
- **Every node, every mode:** root and SlurmUser stay local; the `slurm` user needs the same uid on every node, which the image ensures. Unmanaged nodes (unmanaged workers and db nodes) get the Linux accounts as with `local`, as sind does not manage their Slurm. `nssSlurm` and `clientIds` need a managed cluster.
- The `slurm.conf` parameters are each set unless `slurm.main` sets it. A value `slurm.main` sets must list sind's, or validation fails, as the mode would not work without it: `LaunchParameters` must list `enable_nss_slurm`, and with `clientIds` `AuthInfo` must list `use_client_ids`, `AuthType` be `auth/slurm` and `CredType` `cred/slurm`.
- The mode is stored as the `sind.identity` label, so `sind create worker` sets up new workers the same way.
- slurmctld checks a partition's `AllowGroups` with local lookups only: with `clientIds`, `controllerUsers: true` gives the controllers the users and groups for it. sind does not parse `slurm.main` for `AllowGroups`.

## Docker Resources

### Per-Cluster Resources

| Type | Name Pattern | Example (`sind create cluster dev`) |
|------|--------------|------------------------|
| Network | `<realm>-<cluster>-net` | `sind-dev-net` |
| Controller | `<realm>-<cluster>-controller` | `sind-dev-controller` |
| Backup controller | `<realm>-<cluster>-controller-backup` | `sind-dev-controller-backup` |
| Db | `<realm>-<cluster>-db` | `sind-dev-db` |
| Submitter | `<realm>-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `<realm>-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `<realm>-<cluster>-config` | `sind-dev-config` |
| Munge volume (not with identity `clientIds`) | `<realm>-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `<realm>-<cluster>-data` | `sind-dev-data` |
| State volume (backup controller only) | `<realm>-<cluster>-state` | `sind-dev-state` |
| Home volume (`users` only) | `<realm>-<cluster>-home` | `sind-dev-home` |
| Config helper (temporary, managed clusters only) | `<realm>-<cluster>-config-helper` | `sind-dev-config-helper` |
| Munge helper (temporary, not with identity `clientIds`) | `<realm>-<cluster>-munge-helper` | `sind-dev-munge-helper` |

The helpers mount the config and munge volumes while `sind create cluster` writes the Slurm configuration and the munge key into them, using the controller's image, and are removed once the files are written. They carry the `sind.realm` and `sind.cluster` labels, so `sind delete cluster` removes one that an interrupted create left behind (`docker ps -a`).

### Global Resources (Mesh)

| Type | Name Pattern | Example | Image |
|------|-------------|---------|-------|
| Mesh network | `<realm>-mesh` | `sind-mesh` | — |
| DNS container | `<realm>-dns` | `sind-dns` | `coredns/coredns:1.14.7` |
| SSH container | `<realm>-ssh` | `sind-ssh` | sind's default node image (runs `sleep infinity`) |
| SSH volume | `<realm>-ssh-config` | `sind-ssh-config` | — (keys copied in through `<realm>-ssh` before it first starts) |

The mesh images do not follow `defaults.image`: the relay needs the `ssh` client and `bash` of sind's own node image, which custom images need not have. CoreDNS is pinned to a release; bump `DNSImage` in `pkg/mesh` by hand. `--pull` pulls the mesh images when `sind create cluster` creates the mesh containers; an existing mesh keeps its containers, and their images, until the realm's last cluster is deleted. `CleanupMesh` also removes a `<realm>-ssh-keygen` container, the key helper of earlier sind versions, if one is left over.

### Defaults

The default realm is `sind` and the default cluster name is `default`, resulting in prefixes like `sind-default-*`.

## Volume Mounts

| Volume | Mount Point | Controller | Db | Worker | Submitter |
|--------|-------------|------------|----|---------|-----------|
| `<realm>-<cluster>-config` | `/etc/slurm` | rw | ro | ro | ro |
| `<realm>-<cluster>-munge` | `/etc/munge` | ro | ro | ro | ro (not with identity `clientIds`) |
| `<realm>-<cluster>-data` | `/data` | rw | rw | rw | rw |
| `<realm>-<cluster>-state` | `/var/spool/slurmctld` | rw (backup controller pairs only) | — | — | — |
| `<realm>-<cluster>-home` | `/home` | rw | rw | rw | rw (`users` only) |
| `cvmfs` plugin volume or host `/cvmfs` | `/cvmfs` | ro | ro | ro | ro (`storage.cvmfs` only) |
| tmpfs | `/tmp` | per-node | per-node | per-node | per-node |

### Mount Options

SELinux relabeling (`:z`) is not used because containers run with `--security-opt label=disable`. This avoids expensive recursive relabeling of bind-mounted host directories, and lets systemd (PID 1) write to its cgroups, which SELinux denies a `container_t` process unless a host admin turns on the `container_manage_cgroup` boolean.

The cost: on a Docker daemon with SELinux labelling enabled (`selinux-enabled`, the default of Fedora's moby-engine), every node runs unconfined, as `spc_t` instead of `container_t`, whatever its data backend, so SELinux no longer separates the nodes from the host. A daemon without labelling confines no container either way. A `securityOpt` such as `label=type:...` cannot restore the labelling: Docker gives `disable` precedence.

Container mount flags:
```
-v <realm>-<cluster>-config:/etc/slurm:rw     # controller
-v <realm>-<cluster>-config:/etc/slurm:ro     # all others
-v <realm>-<cluster>-munge:/etc/munge:ro      # all nodes, not with identity clientIds
-v <realm>-<cluster>-data:/data:rw            # all nodes, data volume
--mount type=bind,source=/host/dir,target=/data  # all nodes, data on a host directory
-v <realm>-<cluster>-state:/var/spool/slurmctld:rw  # both controllers of a backup pair
-v <realm>-<cluster>-home:/home:rw            # all nodes, users only
--mount type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly     # storage.cvmfs: plugin
--mount type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave  # storage.cvmfs: host
--tmpfs /tmp:rw,nosuid,nodev,size=256m     # tmpSize
--tmpfs /run:exec,mode=755,size=64m        # systemd runtime, volatile journal
--tmpfs /run/lock                          # systemd lock files
```

Resource flags, for the default `cpus: 1` and `memory: 512m`:
```
--cpus 1 --memory 512m
--memory-swap 512m                         # no swap
--shm-size 256m                            # /dev/shm: half the memory
```

### Data Mount

By default, `sind create cluster` bind-mounts the current working directory as `/data` on all nodes:
```
--mount type=bind,source=/absolute/path/to/cwd,target=/data
```

The `--data` flag controls the mount source:
- `--data .` (default) — bind-mount the current working directory
- `--data /path` — bind-mount a specific host directory
- `--data volume` — use a Docker-managed volume (`<realm>-<cluster>-data`)

When a YAML config sets `storage.dataStorage.type` or `hostPath`, the config takes precedence over
`--data`; a config that sets only `mountPath` keeps the source `--data` gives.

When the resolved host directory is `/` or the user's home directory (`$HOME`), `sind create
cluster` prints a `Warning:` on stderr and goes on: every node mounts it read-write, which is
usually an accident, a create run there with the default `--data .`.

The resolved host path is stored on each container as the `sind.data.hostpath` label (empty with
the data volume), and the mount point as the `sind.data.mountpath` label, so that
dynamically added workers (`sind create worker`) inherit the same mount, and `sind get cluster`,
`enter` and `exec` use the same mount point. A relative `hostPath` from the config is resolved
against the directory `sind create cluster` runs in; `pkg/cluster` resolves it the same way for
library callers, so that Docker never reads it as a named volume.

The bind mount uses `--mount`, not `-v`: with `-v`, Docker creates a missing source directory,
empty and owned by root, while `--mount` fails, so a mistyped `--data` or a `hostPath` that does
not exist yet fails `sind create cluster` (or `sind create worker`) instead. Docker reads the
`--mount` value as a CSV record, so sind quotes a field whose path contains a comma or a quote.
The directory is resolved on the Docker host, which sind assumes is the machine it runs on: with a
Docker daemon elsewhere, such as a CI job container that shares the host's Docker socket, use
`--data volume`.

In the config, `type: hostPath` bind-mounts `hostPath` and `type: volume` uses the data volume and
ignores `hostPath`; a `hostPath` without `type` means `hostPath`.

### CVMFS Mount

`storage.cvmfs: true` mounts CVMFS read-only at `/cvmfs` on every node. Repositories mount on
demand when first accessed, as on a bare-metal client with autofs. Neither backend needs extra
container privileges. `sind create cluster` picks the backend once, in parallel with the other
preparation, and logs it:

1. **Volume plugin**, when an enabled Docker volume plugin named `cvmfs` (`cvmfs:latest` in
   `docker plugin ls`) is installed:
   ```
   --mount type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly
   ```
   The plugin runs its own CVMFS client. All clusters share the `cvmfs` volume, so sind never
   removes it.
2. **Host bind mount** otherwise, a slave of the Docker host's `/cvmfs`:
   ```
   --mount type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave
   ```
   A lookup inside a container reaches the host's autofs, and the repository it mounts
   propagates into the containers, as do autofs idle unmounts. Docker accepts `rslave` only when
   the host's `/cvmfs` is on a `shared` or `slave` mount (`findmnt -o TARGET,PROPAGATION /cvmfs`),
   the default on systemd hosts.

For the host bind mount, sind first runs `true` in a throwaway container of the controller image
with that mount. With neither backend available (no plugin, and a Docker host without `/cvmfs` or
with a `private` one), `sind create cluster` fails before creating any node, with Docker's error
and a hint to install CVMFS on the Docker host or the `cvmfs` volume plugin.

The backend is stored on each container as the `sind.cvmfs` label, so `sind create worker` mounts
CVMFS the same way without detecting it again, and `sind get cluster` lists `/cvmfs` under
`MOUNTS`.

Running the CVMFS client inside the nodes, e.g. to test the configuration management that
provisions it, is a different use case: it needs `capAdd: [SYS_ADMIN]` and `devices: [/dev/fuse]`
on those nodes instead (see Node Parameters).

### Container Labels

sind applies labels to containers for filtering and metadata:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.realm` | `sind` | Realm namespace |
| `sind.cluster` | `dev` | Cluster name |
| `sind.role` | `worker` | Node role |
| `sind.managed` | `true` | Whether sind manages Slurm on the node: `false` for unmanaged workers and db nodes and for every node of an unmanaged cluster. Nodes created before this label existed count as managed. |
| `sind.slurm.version` | `25.11.8` | Slurm version; empty for an unmanaged cluster |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path; empty with the data volume |
| `sind.data.mountpath` | `/shared` | Data mount point, `/data` by default |
| `sind.cvmfs` | `hostPath` | How the node mounts CVMFS: `volume` (plugin) or `hostPath` (host `/cvmfs`); empty without `storage.cvmfs` |
| `sind.users` | `alice:1000:1000 bob:2001:3000` | The cluster users, space-separated `name:uid:gid` entries (gid of the primary group); empty without `users` |
| `sind.groups` | `alice:1000 hpc:3000:carol` | The cluster groups, private groups included, space-separated `name:gid` entries with `:member+member...` for supplementary members; empty without `users` and `groups` |
| `sind.identity` | `clientIds` | The identity mode: `local`, `nssSlurm` or `clientIds` |

Every node container gets each of these labels, with an empty value where there is nothing to
record. Docker merges the image's labels into the container's, so a label sind left out could come
from the image: an image labelled `sind.data.hostpath=/` would otherwise make `sind create worker`
bind-mount the host's root directory on a cluster that uses the data volume. Nodes created by
earlier sind versions lack some of the labels; sind reads a missing label like an empty one.

Every node container, the mesh's DNS and SSH containers, and every network and volume also carry Docker Compose labels (`com.docker.compose.*`), so Compose-aware tools group them. The project is `<realm>-<cluster>` (`<realm>-mesh` for the mesh). A container's service is its role (`dns` or `ssh` in the mesh) and its container number is 1, N+1 for `worker-N` and 2 for `controller-backup`; networks and volumes name themselves `net`, `mesh`, the volume type (`config`, `munge`, `data`, `state`, `home`) or `ssh-config`.

### Enter and Exec

`sind enter` and `sind exec` run commands directly inside the target container via `docker exec`
with the working directory set to the data mount point (`/data` by default). This means commands operate on the shared data mount.
With `--user USER` they run as that cluster user (`docker exec -u USER`) in its home directory,
`/home/USER`, which is shared too (see Users).

`sind ssh` continues to use the SSH relay container, for a login through the node's sshd. Its ssh client runs in the relay container, so port forwarding (`-L`, `-R`, `-D`) and the files that options name (`-i`, `-F`) are the relay's; forwarding to the host goes through the exported `ssh_config` (see User SSH Client Integration).

## Networking

### Cluster Network

Each cluster has an isolated Docker bridge network:
- Name: `<realm>-<cluster>-net`
- Nodes can reach each other by container hostname

Nodes join it with gateway priority 1 (`--network name=<realm>-<cluster>-net,gw-priority=1`, Docker 28+), ahead of the mesh network's 0. Docker makes a container's hostname a DNS name on every user-defined network it joins, so the mesh holds `controller`, `db` and `worker-N` once per cluster of the realm. The embedded DNS answers a name from the first of the container's networks that knows it, ordered by gateway priority and then by network name. With the priority, the short names Slurm uses resolve to the node's own cluster whatever the cluster is called; without it, a cluster whose network name sorts after `<realm>-mesh` (`test`, `prod`) would resolve `controller` to the controllers of every cluster in the realm. A name the cluster lacks, such as `db` without a db node, still falls through to the mesh and resolves to another cluster's node. The cluster network is also the nodes' default gateway.

### Mesh Network

All nodes of a realm also join its shared mesh network, which carries the realm's DNS server and SSH relay:

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-mesh` network, starts `sind-dns` |
| Subsequent clusters | Connects cluster nodes to `sind-mesh`, updates DNS |
| Cluster deleted | Disconnects cluster nodes, updates DNS |
| Last cluster deleted | Removes `sind-dns` and `sind-mesh` network |

The mesh does not route traffic between clusters by DNS name: the mesh DNS records point at cluster network addresses, which the SSH relay (on every cluster network) and the host reach, but the nodes of other clusters do not, as Docker isolates bridge networks from each other. Nodes of different clusters reach each other on the mesh by container name (`sind-dev-controller`).

### DNS

The `sind-dns` container (CoreDNS) names every node of the realm, for the SSH relay and host-side resolution, using a realm-aware zone:

```
<realm>.sind:53
```

Records follow the pattern:
```
<node>.<cluster>.<realm>.sind → container IP
```

Nodes are configured with:
```
--dns <sind-dns-ip>
--dns-search <cluster>.<realm>.sind
```

The DNS container is lightweight and does not run systemd/sshd.

sind keeps the records in the Corefile's `hosts` block and writes them when it creates or deletes nodes, and when `sind power on`, `reboot` and `cycle` start nodes, as a node can get another address on each start. A running CoreDNS reloads the Corefile on `SIGUSR1` (`docker kill -s USR1`), without dropping queries; a stopped DNS container is started instead. The write and the reload ignore Ctrl+C, and so does the rewrite of `known_hosts`, whose `cat >` truncates the file first. Each realm-wide file has one writer at a time (the realm lock); the Corefile and `known_hosts` updates run in parallel, as they live in different containers.

#### Host DNS Resolution

When systemd-resolved runs on the host and polkit lets the user change its per-link settings (`org.freedesktop.resolve1.set-dns-servers`, `set-domains` and `revert`), `sind create cluster` makes the mesh bridge (`br-` and the first 12 characters of the mesh network's ID) a resolver link:

```
resolvectl dns <bridge> <sind-dns-ip>
resolvectl domain <bridge> ~<realm>.sind default.<realm>.sind
```

`~<realm>.sind` is a routing domain: the host sends queries for `*.<realm>.sind` to the realm's CoreDNS, and the link does not become a default route for other queries. `default.<realm>.sind` is a search domain, which systemd-resolved tries for single-label lookups from any process on the host, so a bare `controller` resolves to the `default` cluster's controller; each realm with a mesh adds its own. The setup is best-effort: without systemd-resolved, the polkit authorization or the bridge interface, sind skips it with a debug log line. `sind doctor` checks the polkit policy. Deleting the realm's last cluster runs `resolvectl revert <bridge>` before it removes the mesh network. The CLI turns it on with `mesh.Manager.HostDNS`, which is off by default for library callers.

### Host Reboot and Docker Daemon Restart

sind sets no restart policy: nodes keep the power state `sind power` gave them, and after a host reboot or a Docker daemon restart the mesh containers and the nodes stay stopped. `sind get cluster` shows a stopped DNS container or relay with ✗. `sind power on` starts the realm's DNS container, then the relay, then the nodes, re-registers the nodes' DNS records, and reapplies host DNS; `sind create cluster` and `sind create worker` also start a stopped mesh, DNS first.

The DNS container's address is fixed into every node's and the relay's `--dns` when Docker creates them. Started before anything else on the mesh, the DNS container gets its old address back: it was the first container on the mesh, and Docker hands out the lowest free address. When it gets another one, sind warns (`Warning:` on stderr) and names the containers that still use the old address; they resolve neither `*.<realm>.sind` names nor external names until their clusters are created again (the relay with the realm's mesh, after the last cluster is deleted). A pinned DNS address (`--ip`) would need a mesh network with a user-defined subnet.

### SSH

The `sind-ssh` container provides SSH access to all cluster nodes. It runs sind's default node image with `sleep infinity` instead of systemd, on the mesh network, and joins each cluster network.

#### Global SSH Resources

| Resource | Purpose |
|----------|---------|
| `sind-ssh` container | SSH client for accessing nodes |
| `sind-ssh-config` volume | SSH keypair and known_hosts |

The `sind-ssh-config` volume contains:

| File | Description |
|------|-------------|
| `id_ed25519` | Private key (generated on first cluster creation) |
| `id_ed25519.pub` | Public key (injected into node images) |
| `known_hosts` | Host keys of all nodes (updated dynamically) |

#### Lifecycle

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-ssh-config` volume and `sind-ssh` container, copies a new keypair into the volume through the container, starts it |
| Node created | Collects sshd host key, appends to `known_hosts` |
| Node deleted | Removes entry from `known_hosts` |
| Last cluster deleted | Removes `sind-ssh` container and `sind-ssh-config` volume |

#### Public Key Injection and Host Key Collection

When sind creates a node, it waits for sshd to start, then appends the public key from `sind-ssh-config` to root's `authorized_keys` and collects the host key that sshd serves, in one `docker exec`, as the cluster's setup waits for its slowest node:

```bash
docker exec <node> sh -c 'mkdir -p /root/.ssh && printf "%s\n" "$1" >> /root/.ssh/authorized_keys && ssh-keyscan -t ed25519 localhost' sh "$pubkey"
```

The public key is an argument of the shell, not part of its script. The `localhost` field is dropped and the key is added to `known_hosts` with the node's DNS name:

```
controller.dev.sind.sind ssh-ed25519 AAAA...
worker-0.dev.sind.sind ssh-ed25519 AAAA...
```

#### User Access

sind configures SSH access for root and for the cluster users (see Users): the realm's key is in root's and in each user's `authorized_keys`. Anything beyond that (sudo, passwords, SSH keys for logins between nodes) is left to the user.

#### sind ssh Implementation

`sind ssh` executes SSH via the `sind-ssh` container:

```bash
sind ssh [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
```

Internally:

```bash
docker exec -i [-t] sind-ssh ssh [SSH_OPTIONS] <node>.<cluster>.<realm>.sind [COMMAND [ARGS...]]  # -t only when stdin and stdout are terminals
```

`-t` needs a terminal at both ends: through a pseudo-terminal, the output gets CRLF line endings and the remote stderr is merged into stdout, which would corrupt output that a script captures or redirects in an interactive shell.

SSH options are passed through verbatim, before or after the node. sind reads them as ssh's getopt does: an option that takes a value (`-B -D -E -F -I -J -L -O -P -Q -R -S -W -b -c -e -i -l -m -o -p -w`) takes the rest of its argument (`-p2222`, `-vL`) or else the next one. The node is the one argument before `--` that is neither an option nor an option's value. Another one is a usage error that names it: ssh would run `sind ssh worker-0 hostname` as `ssh worker-0 hostname`, but sind takes a remote command only after `--`. A `USER@` before the node becomes `-l USER` after the other SSH options. Examples:

```bash
sind ssh worker-0                           # interactive shell
sind ssh alice@worker-0                     # as cluster user alice
sind ssh worker-0.dev                       # node in dev cluster
sind ssh -v worker-0                        # verbose SSH
sind ssh worker-0 -- hostname               # run command
sind ssh -t worker-0 -- top                 # force TTY allocation
```

ssh runs in the relay container, so `-L`, `-R` and `-D` forward ports of the relay, which the host cannot reach, and `-i`, `-F` or `-E` name files in the relay. Port forwarding to the host uses the exported `ssh_config` instead (`ssh -F "$(sind get ssh-config)" -L 8080:localhost:80 controller.default.sind.sind`).

#### User SSH Client Integration

sind exports SSH configuration per realm to `$XDG_STATE_HOME/sind/<realm>/` (defaulting to `~/.local/state/sind/<realm>/`; a relative `XDG_STATE_HOME` is ignored, as the XDG Base Directory specification requires) for integration with the user's SSH client:

| File | Description |
|------|-------------|
| `ssh_config` | SSH config snippet |
| `id_ed25519` | Private key (copy from volume) |
| `known_hosts` | Host keys (copy from volume) |
| `lock` | Advisory realm lock (see Realm Advisory Locking) |

The generated `ssh_config` (for default realm `sind`; sind writes the state directory's absolute path):

```
Host controller controller.* controller-backup controller-backup.* db db.* submitter submitter.* worker-*
    CanonicalizeHostname yes
    CanonicalDomains default.sind.sind sind.sind

Host *.sind.sind
    IdentityFile /home/user/.local/state/sind/sind/id_ed25519
    UserKnownHostsFile /home/user/.local/state/sind/sind/known_hosts
    User root
    StrictHostKeyChecking yes

Host controller.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/controller.default.sind.sind/22; cat <&3 & cat >&3; kill $!'

Host worker-0.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/worker-0.default.sind.sind/22; cat <&3 & cat >&3; kill $!'
```

The first block enables short-name resolution for the default realm: `ssh controller` expands to `controller.default.sind.sind`, and `ssh controller.dev` expands to `controller.dev.sind.sind`. ssh looks the candidates up in the host's DNS, which knows them where sind has pointed systemd-resolved at the mesh DNS for `*.<realm>.sind`, and reads the config again for the name it found. The block applies only to host names shaped like a node's, the node names alone or followed by `.<cluster>`, so ssh does not look up every other host it connects to under the sind domains first; `CanonicalizeMaxDots` keeps its default, 1, as these names have at most one dot. Other realms get no such block; use full names such as `controller.dev.ci.sind` there. While a node of that name exists, its short name reaches it rather than a host of the same name on the user's network.

Each node in the realm's `known_hosts` gets a `Host` block of its own with the relay's `ProxyCommand`, the node's name written into it. ssh substitutes `%h` into a `ProxyCommand` unquoted and runs it in a shell, and a wildcard `Host *.sind.sind` matches any name with that suffix, so a host name with shell syntax in it, e.g. from a git submodule URL, would have run in the user's shell and in the relay's bash (the CVE-2023-51385 pattern). A name in `known_hosts` that is not `<node>.<cluster>.<realm>.sind` with lowercase host name labels gets no block. The file is rewritten whenever a cluster or worker of the realm is created or deleted, as `known_hosts` is.

To find the path for a realm, use `sind get ssh-config`. Add to the **top** of `~/.ssh/config` (before any `Host` or `Match` blocks, as an `Include` after one belongs to that block, and as ssh uses the first value it gets for each option) for a single realm:

```
Include ~/.local/state/sind/sind/ssh_config
```

Or include all realms at once using a wildcard (supported by OpenSSH's `Include`):

```
Include ~/.local/state/sind/*/ssh_config
```

This allows direct use of standard SSH tools:

```bash
ssh controller.default.sind.sind
ssh worker-0.dev.sind.sind hostname
scp file.txt controller.dev.sind.sind:/tmp/
ssh -L 8080:localhost:80 controller.default.sind.sind   # port forwarding to the host
```

sind updates these files automatically when clusters or nodes are created/deleted, reading the private key and `known_hosts` from the relay container in one `docker exec`; a relay container that no longer exists means the realm has no cluster. When the last cluster in a realm is deleted, `ssh_config`, `id_ed25519` and `known_hosts` are removed; the realm directory stays, as it holds the realm's `lock` file.

## Command Routing

Interactive sessions are routed based on cluster configuration:

| Command | Target Node |
|---------|-------------|
| `sind ssh <node>` | explicit node |
| `sind enter [cluster] [--user USER]` | submitter (if exists) → controller in control (falls back to `controller`) |
| `sind exec [cluster] [--user USER] -- <cmd>` | submitter (if exists) → controller in control (falls back to `controller`) |

On an unmanaged cluster sind does not know which controller is in control; `enter` and `exec` then target `controller` if it runs, otherwise `controller-backup`.

### sind enter

Opens an interactive shell on the submitter (or, without a submitter, on the controller in control: `controller-backup` after a failover of a backup pair, otherwise `controller`). Unlike `sind ssh`, it runs `bash -l` through `docker exec` rather than the SSH relay, in `/data`, or as `--user` in its home directory (see Enter and Exec).

### sind exec

One-shot command execution on the same target as `sind enter`, through `docker exec` in `/data` rather than the SSH relay.

## Container Images

### Generic Image

sind provides a generic multi-role image that works for all node types, built for each supported Slurm release line:

```
ghcr.io/gsi-hpc/sind-node:latest                # newest release line
ghcr.io/gsi-hpc/sind-node:<YY>.<MM>             # newest patch release of a line, e.g. 25.11
ghcr.io/gsi-hpc/sind-node:<YY>.<MM>.<patch>     # a patch release, e.g. 25.11.8
```

`latest` is the default image when `defaults.image` is not specified in the cluster configuration.

The generic image:
- Published for linux/amd64 and linux/arm64
- Based on Rocky Linux 10
- Builds Slurm, OpenMPI, PMIx, PRRTE, UCX and libjwt from source
- Contains the Slurm daemons (slurmctld, slurmdbd, slurmd), munge, sshd, MariaDB and a full MPI stack
- Slurm is built with `--with-pmix` for native PMIx job launch support
- sind enables the appropriate services based on node role

The `Dockerfile` uses a multi-stage build with a shared `builder-base` stage. UCX and PMIx build in parallel, PRRTE and Slurm depend on PMIx, and OpenMPI depends on all three. UCX, PMIx, PRRTE, OpenMPI and libjwt versions are pinned as `ARG` defaults in the Dockerfile and mirrored in `docker-bake.hcl`. The Slurm version and tarball checksum are build arguments without defaults: `SLURM_RELEASES` in `docker-bake.hcl` lists one Slurm release per supported release line, newest first, and each becomes a bake target `slurm-<YY>-<MM>` tagged `<version>` and `<YY>.<MM>`, the first one also `latest`. Targets build for linux/amd64 and linux/arm64; CI builds each platform on a native runner and merges them into one multi-platform image per tag, while `make image` builds for the host platform only.

### Custom Images

Custom images must provide:

**All roles:**
- systemd as init at `/sbin/init`, and `/bin/sh`: sind starts each node with its own `/bin/sh` entrypoint, which execs `/sbin/init` (see Container Startup), so node containers do not use the image's `ENTRYPOINT` and `CMD`. sind's helper containers run commands in the image directly, so it should not set an `ENTRYPOINT` that wraps them
- `STOPSIGNAL SIGRTMIN+3`, systemd's shutdown request, so that `sind power shutdown` and `sind power reboot` shut the node down cleanly
- sshd service (enabled, sind injects authorized_keys at runtime)
- `/etc/shadow` readable by root without `CAP_DAC_OVERRIDE` (e.g. `0400 root:root`; Rocky's default `0000` is not). On nodes with `apparmor=unconfined`, the host's `unix-chkpwd` AppArmor profile (e.g. Ubuntu 24.04) denies that capability, and sshd's `pam_unix` account check would refuse root
- munge service (enabled)
- Slurm client tools (srun, sbatch, squeue, etc.)
- Slurm's `mpi/pmix` plugin (`mpi_pmix.so`, built when Slurm finds PMIx, `--with-pmix`): the generated `slurm.conf` sets `MpiDefault=pmix`, and without the plugin every `srun` fails with "Invalid MPI type 'pmix'". An image without it needs `MpiDefault=none` in `slurm.main`

**Per-role requirements:**

| Role | Additional Requirements |
|------|------------------------|
| controller | slurmctld (installed, not enabled) |
| db | mariadb-server (MariaDB 10.4 or later, for the built-in `unix_socket` authentication) with the `mysql` client, root access to MariaDB over its local socket without a password, slurmdbd (installed, not enabled) with a unit that creates `/run/slurmdbd` for the `slurm` user, a `slurm` user with the same uid as in the controller's image (sind sets `slurmdbd.conf`'s owner from there), and `/var/log/slurm` writable by it |
| worker | slurmd (installed, not enabled) |
| submitter | Slurm client tools only |

sind enables Slurm services based on the node's role once every node is ready (`systemctl enable --now`). Services should be installed but not enabled in the image.

The Slurm requirements apply to managed clusters only. sind neither runs nor queries Slurm on an unmanaged cluster, so its image may leave Slurm for the provisioning under test to install.

The repository's `Dockerfile`, which builds the official images, serves as the reference.

## Generated Configuration

### Munge

During `sind create cluster`, before starting any containers, sind generates a random munge key and writes it to the `<realm>-<cluster>-munge` volume. This ensures all nodes share the same key from first boot. With identity `clientIds` there is no munge volume or key: sind writes `slurm.key`, the key of `auth/slurm` and `cred/slurm`, to the config volume instead (see Identity Modes).

### Slurm Configuration

sind auto-generates a minimal Slurm configuration based on cluster topology and writes it to the `<realm>-<cluster>-config` volume. For an unmanaged cluster it writes nothing: the volume stays empty for the user's own configuration (see Unmanaged Cluster).

#### Multi-file Configuration

sind generates a multi-file configuration structure:

```
/etc/slurm/
├── slurm.conf              # main config
├── sind-nodes.conf         # sind-managed node definitions
├── cgroup.conf             # cgroupv2 configuration
├── plugstack.conf          # SPANK plugin config (always created)
├── plugstack.conf.d/       # SPANK plugin fragments (always created)
├── slurm.conf.d/           # main config fragments (if slurm.main is a map)
├── cgroup.conf.d/          # cgroup fragments (if slurm.cgroup is a map)
├── gres.conf               # generic resources (if slurm.gres is set)
├── gres.conf.d/            # gres fragments (if slurm.gres is a map)
├── topology.conf           # network topology (if slurm.topology is set)
├── topology.conf.d/        # topology fragments (if slurm.topology is a map)
├── slurmdbd.conf           # accounting daemon config (if a managed db node exists)
├── slurmdbd.conf.d/        # slurmdbd fragments (if slurm.slurmdbd is a map)
└── slurm.key               # auth/slurm key (identity clientIds only)
```

#### slurm.conf

sind writes `slurm.conf` once, at `sind create cluster`:

```
# Generated by sind
ClusterName=<cluster>
SlurmctldHost=controller
SlurmUser=slurm
StateSaveLocation=/var/spool/slurmctld
SlurmdSpoolDir=/var/spool/slurmd
PlugStackConfig=/etc/slurm/plugstack.conf

ProctrackType=proctrack/cgroup
TaskPlugin=task/cgroup
MpiDefault=pmix
ReturnToService=2
DefMemPerCPU=<the smallest RealMemory/CPUs of the managed workers>

<identity parameters: identity nssSlurm and clientIds (see Identity Modes)>
<accounting parameters: a managed db node (see Database Node)>

<slurm.main: its content, or an include line per fragment>
include /etc/slurm/sind-nodes.conf
```

- The first block is fixed, as sind's volumes, images and readiness checks depend on it; a backup controller adds `SlurmctldHost=controller-backup` and `SlurmctldTimeout=20` (see Backup Controller).
- Every other parameter is set unless `slurm.main` sets it, so that a value there replaces sind's instead of following it (Slurm would take the later one and log an error for the duplicate).
- `sind-nodes.conf` is included after `slurm.main`, so that the `NodeName=DEFAULT` and `PartitionName=DEFAULT` lines of `slurm.main`, which apply only to the lines after them, reach sind's nodes and partition `all`, e.g. `PartitionName=DEFAULT DefaultTime=00:30:00` for a default time limit. slurmctld reads every node before any partition, so a partition in `slurm.main` may name sind's nodes (`Nodes=worker-[0-1]` or `ALL`).
- `TaskPlugin=task/cgroup` leaves out `task/affinity`. A node's CPUs are a CPU quota (`--cpus`), not a cpuset, so every worker sees all host CPUs, and `task/affinity` would bind the tasks of every worker, of every cluster on the host, to the same first host CPUs. To test CPU binding (`--cpu-bind`), set `TaskPlugin=task/cgroup,task/affinity` in `slurm.main`. Managed workers then get `--cap-add SYS_NICE`, unless their `capDrop` lists `SYS_NICE` or `ALL`: slurmstepd sets each task's CPU affinity after the task has switched to the job's user, and root needs `CAP_SYS_NICE`, which Docker drops by default, to change the affinity of another user's process; without it, every job of a user other than root fails with `task_g_set_affinity` ("Slurmd could not execve job"). `sind create worker` reads the `TaskPlugin` from `slurm.conf` and the files it includes on the config volume, so workers added later match. Nothing else adds `SYS_NICE`.
- `MpiDefault=pmix` makes `srun` launch through PMIx, which needs Slurm's `mpi/pmix` plugin (see Custom Images); set `MpiDefault=none` in `slurm.main` for an image without it.
- `ReturnToService=2` returns a `DOWN` worker to service when its slurmd registers with a valid configuration, e.g. after `sind power cut` and `sind power on`.
- `DefMemPerCPU` is the smallest `RealMemory`/`CPUs` over the managed workers, in MB, rounded down, and is left out when `slurm.main` sets `DefMemPerCPU` or `DefMemPerNode`. Slurm's defaults, `SelectType=select/cons_tres` with `SelectTypeParameters=CR_Core_Memory`, make memory a consumable resource, and without a default a job that does not ask for memory gets all of a node's memory: a worker would run one such job at a time, whatever its `cpus`. With it, each CPU of every worker can run such a job. A worker's `RealMemory` is its whole container memory limit, without a reserve for its daemons or its `/tmp`, which is a tmpfs and counts against the limit. sind writes `slurm.conf` once, so a worker added later with less memory per CPU runs fewer jobs without `--mem` at a time than it has CPUs.

#### sind-nodes.conf

This file contains node and partition definitions for sind-managed nodes, e.g. for three workers with `cpus: 2` and `memory: 1g`:

```
# Generated by sind
NodeName=worker-0 CPUs=2 RealMemory=1024 State=UNKNOWN
NodeName=worker-1 CPUs=2 RealMemory=1024 State=UNKNOWN
NodeName=worker-2 CPUs=2 RealMemory=1024 State=UNKNOWN
PartitionName=all Nodes=worker-0,worker-1,worker-2 Default=YES
```

- A worker's `CPUs` and `RealMemory` are its container's `cpus` and `memory` (in MiB); see Node Parameters.
- Partition `all` holds every managed worker and is the default partition. It sets nothing else, so Slurm's defaults apply (`MaxTime=UNLIMITED`, `State=UP`, no `DefaultTime`) unless a `PartitionName=DEFAULT` line in `slurm.main` changes them. When `slurm.main` declares a default partition of its own (a `PartitionName` line other than `DEFAULT` with `Default=YES`), sind writes `Default=NO` on `all`: slurmctld takes the last default partition it reads, and `sind-nodes.conf` comes last.
- `sind create cluster` generates the file; `sind create worker` adds new nodes (unless `--unmanaged`), replacing a definition of the same name, and removes them again when it fails; `sind delete worker` removes them (for managed nodes). Both rewrite only the `Nodes=` list of the partition line and keep everything else, so edits to the other lines survive. To change sind's nodes and partition, prefer `NodeName=DEFAULT` and `PartitionName=DEFAULT` lines in `slurm.main`; their `CPUs`, `RealMemory` and `Default` stay sind's.
- Nodes with `managed: false` in the cluster config are excluded from `sind-nodes.conf`. To add custom node definitions, put them in `slurm.main` or in a separate file included from `slurm.conf`.

#### cgroup.conf

sind generates a minimal `cgroup.conf`, `CgroupPlugin=autodetect`, which selects cgroup v2. With `ProctrackType=proctrack/cgroup`, `TaskPlugin=task/cgroup` and `JobAcctGatherType=jobacct_gather/cgroup`, Slurm puts every job and step into a cgroup to track its processes and gather its usage, but it constrains nothing. Constraints are opt-in through the `slurm.cgroup` section:

- `ConstrainRAMSpace=yes` limits each job to the memory it was allocated: `--mem`, or `DefMemPerCPU` per CPU (see slurm.conf). A worker's `RealMemory` is its whole container memory limit, with no reserve for slurmd, the other daemons and `/tmp`, so jobs that together use all of it can make the container's OOM killer hit the daemons. Leave room with a smaller `DefMemPerCPU` in `slurm.main`.
- `ConstrainCores=yes` confines each job to a cpuset of its allocated CPUs. sind limits workers with a CPU quota, not a cpuset, so every worker sees all host CPUs, and Slurm maps a worker's CPU N to host CPU N: the jobs of every worker, of every cluster on the host, run on the same low-numbered host CPUs. Use it to test the setting, not for throughput. `task/affinity` binds the same way (see slurm.conf).

#### slurmdbd.conf

Only generated for a cluster with a managed db node (not with `managed: false`), together with the accounting parameters in `slurm.conf`; see Database Node. It authenticates with `AuthType=auth/munge`, or with identity `clientIds` with `AuthType=auth/slurm` and `AuthInfo=use_client_ids`; `CredType` is a `slurm.conf` parameter only.

#### User Customization

sind delivers a working starter configuration. The `slurm` config key allows extending it declaratively at creation time (see Slurm Configuration Sections above). For post-creation changes, the `/etc/slurm` volume is writable on the controller node.

Users may:
- Use `slurm.main`, `slurm.cgroup`, etc. to extend config at creation time
- Edit config files directly after creation (sind does not modify them after creation)
- Add additional include files for custom configuration
- Replace the entire configuration (but `sind create worker` will then fail for managed nodes)

## Slurm Version Discovery

sind does not manage Slurm versions directly—the version is implicit in the chosen container images. sind discovers the Slurm version of the controller's image to show it in CLI output (the `SLURM` column, `slurm_version` in JSON) and to label the node containers with it. The generated configuration does not depend on the version.

sind skips the discovery for unmanaged clusters: the Slurm the user provisions may differ from the one in the image.

### Discovery Method

While it creates the cluster network and volumes, sind runs an ephemeral container of the controller's image:

```bash
docker run --rm <controller image> slurmctld -V
# Output: "slurm 26.05.4"
```

The version must have the form `X.Y.Z`, optionally with a pre-release suffix such as `-0rc1` (`[0-9A-Za-z.]`); any other output, such as a version followed by a terminal control sequence from an untrusted image, fails the create.

The discovered version is stored as a label on each node container:

```
--label sind.slurm.version=26.05.4
```

Workers added with `sind create worker` copy the controller's label. Without `--image` they run an image the cluster already runs, by ID. With `--image`, sind discovers the image's version the same way and refuses managed workers whose version differs from the label. At cluster creation, nodes whose `image` differs from the controller's carry the controller's version too: `sind create cluster` does not discover versions per image or compare them.

## DNS Naming Convention

The mesh DNS uses a realm-aware hierarchical namespace:

```
<role>.<cluster>.<realm>.sind
<role>-<N>.<cluster>.<realm>.sind
```

The hierarchy is: `node . cluster . realm . sind`

Each realm gets its own CoreDNS zone (`<realm>.sind`), and nodes within a cluster are configured with `--dns-search <cluster>.<realm>.sind` so short names resolve within the cluster.

Examples (default realm `sind`):
- `controller.default.sind.sind`
- `submitter.default.sind.sind`
- `worker-0.default.sind.sind`
- `worker-1.default.sind.sind`
- `controller.dev.sind.sind`

Examples (custom realm `ci-42`):
- `controller.default.ci-42.sind`
- `worker-0.dev.ci-42.sind`

Within a cluster, short names resolve via the search domain: a node in the `dev` cluster of realm `sind` can reach `controller` without the full `controller.dev.sind.sind`.

## Realm Advisory Locking

Mutating operations acquire a per-realm advisory lock (flock) to prevent concurrent modifications to shared realm state. The lock file is stored at:

```
$XDG_STATE_HOME/sind/<realm>/lock    # default: ~/.local/state/sind/<realm>/lock
```

### Protected operations

- `sind create cluster`
- `sind delete cluster` (single and `--all`)
- `sind create worker`
- `sind delete worker`
- `sind power on`, `sind power reboot` and `sind power cycle` (they start a stopped mesh and rewrite DNS records)

Read-only operations (`get`, `logs`, `ssh`, etc.) and the other `power` commands do not acquire the lock.

### Library callers

The lock is `state.LockRealm` in `pkg/state`, which also resolves the state directory (`state.Dir`, `state.RealmDir`). `cluster.Create`, `cluster.Delete`, `cluster.WorkerAdd` and `cluster.WorkerRemove` take no lock themselves: their caller holds the realm lock for the whole operation, for `Create` from before `mesh.Manager.EnsureMesh`, as the CLI does. Without it, concurrent calls in one realm lose each other's DNS records, `known_hosts` entries and `sind-nodes.conf` lines, or remove each other's resources. The lock is a `flock(2)` on a file in the user's state directory, so it serializes the goroutines of one process and the sind commands of one user, but not two users, or two `XDG_STATE_HOME`s, that share one Docker daemon: such clients use separate realms.

### Behavior

- Lock is attempted non-blocking first; if free, the operation proceeds immediately
- If another operation holds the lock, sind prints `Warning: waiting for another sind command in realm "<realm>" to finish` to stderr, at every verbosity, and blocks until the lock is released: waiting is a state the user may have to act on, so it is not left to `-v`. The wait has no timeout
- Lock is released when the operation completes (success or failure)
- Context cancellation (e.g., Ctrl+C) unblocks a waiting operation

### Realm independence

Locks are per-realm. Operations in different realms run concurrently without contention. This makes realm-based CI isolation safe for parallel jobs.

## Future Features

### Cluster Lifecycle Commands

Planned commands for suspending and resuming clusters without destroying them:

```bash
sind stop cluster [NAME]               # stop all containers, preserve volumes
sind start cluster [NAME]              # start previously stopped cluster
```

**stop cluster:**
- Stops all node containers (`docker stop`)
- Preserves all volumes (config, munge, data)
- Preserves network configuration
- Cluster appears as "stopped" in `sind get clusters`

**start cluster:**
- Starts previously stopped containers
- Nodes rejoin mesh network
- DNS records restored
- Slurm daemons resume normal operation

This enables resource conservation when clusters are not actively in use without losing cluster state or configuration.
