# sind - Slurm in Docker

SPDX-License-Identifier: LGPL-3.0-or-later

https://github.com/GSI-HPC/sind

A CLI tool for running local Slurm clusters using Docker containers, inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker).

## Prerequisites

- Linux host with cgroupv2 and `nsdelegate` mount option (`mount -o remount,nsdelegate /sys/fs/cgroup`)
- Docker Engine 28.0+ (required for `--security-opt writable-cgroups=true`)

## Supported Slurm Versions

- Slurm 25.11

## Overview

sind creates and manages containerized Slurm clusters for development, testing, and CI/CD workflows. Each node runs as a separate Docker container with systemd as init, providing a realistic multi-node Slurm environment without requiring bare-metal infrastructure.

### Operational Model

While the cluster configuration file resembles a Kubernetes manifest, sind is **not** a reconciling controller. The configuration is a one-shot, one-way input for cluster creation:

- `sind create cluster` interprets the manifest once to generate the cluster (via `--config FILE` or piped to stdin)
- sind does not continuously watch or reconcile cluster state
- sind does not automatically repair drift or failures

sind provides commands for inspection (`get`), modification (`create/delete worker`), and simulation (`power`) but these are imperative operations, not declarative state management.

This design is intentional: sind is a development and testing tool that aids the creation of more sophisticated Slurm cluster management tooling, not a production cluster controller.

### Container Startup

sind creates cluster resources in a specific order to ensure dependencies are available:

**Phase 1: Global Infrastructure**
1. Create `sind-mesh` network (if not exists)
2. Start `sind-dns` container (if not exists)
3. Create `sind-ssh-config` volume and generate keypair (if not exists)
4. Start `sind-ssh` container (if not exists)

**Phase 2: Cluster Resources**
1. Create cluster network (`sind-<cluster>-net`)
2. Create cluster volumes (config, munge, data)
3. Generate munge key and Slurm configuration

**Phase 3: Node Containers**
1. Start all node containers in parallel
2. Wait for each node to become ready

Node containers start in parallel because the global infrastructure (DNS, SSH) is already available. Slurm daemons handle transient connection failures during cluster bootstrap.

sind waits for each node to become ready before returning success:

| Check | Description |
|-------|-------------|
| Container running | Docker container in running state |
| systemd ready | `systemctl is-system-running` returns `running` or `degraded` |
| sshd listening | Port 22 accepting connections |
| slurmctld ready | `scontrol ping` succeeds (controller only) |
| slurmd ready | slurmd service active (worker only) |

If any node fails to become ready within the timeout, `sind create cluster` fails and reports which nodes/checks failed. Partial clusters are not automatically cleaned up—use `sind delete cluster` to remove.

### Design Goals

- Familiar UX for kind users
- No root/admin privileges required
- SELinux compatible
- Support for both static and dynamic Slurm node configurations

### Implementation

sind is written in Go and designed for dual use:

1. **CLI tool** - Standalone command-line interface
2. **Go library** - Embeddable package for wrapper tools and integrations

The CLI command structure is reflected in the library API, allowing programmatic access to all sind operations.

### Go Dependencies

sind uses a minimal set of dependencies, following [kind](https://kind.sigs.k8s.io/)'s approach of favoring simplicity and compatibility.

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `sigs.k8s.io/yaml` | YAML configuration parsing |
| `log/slog` (stdlib) | Structured logging interface |
| `github.com/charmbracelet/log` | Colorized log output (slog handler) |
| `github.com/mattn/go-isatty` | TTY detection for interactive commands |
| `github.com/njayp/ophis` | MCP server framework |
| `github.com/spf13/afero` | Filesystem abstraction for testability |
| `golang.org/x/sys` | Advisory file locking (flock) for realm locks |

**Nodeset expansion** (e.g., `worker-[0-2,5]` → individual hostnames) is implemented internally rather than using an external library, keeping the dependency footprint small.

### Docker Interaction

sind interacts with Docker by **shelling out to the `docker` CLI** rather than using the Docker SDK for Go. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- Avoids tight coupling to Docker daemon internals

sind wraps command execution in a thin abstraction layer (`pkg/cmdexec`) using Go's `os/exec` package, with proper output handling and error reporting. The executor interface is shared across `pkg/docker`, `pkg/mesh`, and `pkg/cluster`.

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

## CLI Design Guidelines

Rules for maintaining consistency when adding new commands, flags, and output.

### Command Structure

Commands follow **verb-noun** ordering with a two-level hierarchy:

```
sind <verb> <noun> [ARGS] [FLAGS]
```

- Multi-resource verbs (`create`, `delete`, `get`, `power`) group noun subcommands
- Single-purpose verbs (`status`, `ssh`, `enter`, `exec`, `logs`, `doctor`) stand alone
- Standalone verbs are reserved for frequently-used operations that justify a short path

### Argument Conventions

| Pattern | Positional | Default | Examples |
|---------|-----------|---------|---------|
| Cluster name | `[NAME]` or `[CLUSTER]` | `"default"` | `status`, `enter`, `get nodes` |
| Node targets | `NODES` (required) | — | `power shutdown`, `delete worker` |
| Node format | `shortname.cluster` | cluster defaults to `"default"` | `worker-0.dev`, `controller` |
| Nodeset expansion | bracket patterns | — | `worker-[0-2].dev` |
| Pass-through | after `--` separator | — | `ssh NODE -- cmd`, `exec -- cmd` |

Rules:
- Cluster names are **always positional**, never flags
- Node targets support nodeset expansion and comma-separated specs
- Use `cobra.MaximumNArgs(1)` for optional cluster, `cobra.MinimumNArgs(1)` for required nodes

### Flag Conventions

- **Long-form only** by default; add short flags (`-f`) only for frequently-typed flags
- **Kebab-case** for multi-word flags: `--tmp-size`, `--munge-key`
- **Boolean flags** for mode switches: `--all`, `--pull`, `--unmanaged`
- **One root-local flag**: `--realm` (local to root, inherited via `TraverseChildren`)
- **One root-local counter**: `-v` (repeatable, controls log verbosity)

### Output Conventions

| Command type | Output | Target |
|-------------|--------|--------|
| List resources (`get`) | tabwriter table, uppercase headers, 3-space padding | stdout |
| Single value (`get munge-key`) | raw value, one line | stdout |
| Mutations (`create`, `delete`, `power`) | silent on success | — |
| Errors | structured slog at error level (always visible) | stderr |
| Warnings | `Warning: ...` prefix | stderr |
| Logs (`-v`) | structured key=value, colorized on TTYs | stderr |

Rules:
- Mutations are silent — `exit 0` is the confirmation; use `-v` for progress
- Errors are always visible (slog error level is always enabled, even without `-v`)
- Command output (tables, status, doctor) is monochrome — no ANSI escapes
- Log output (`-v`) is colorized on interactive terminals, plain when piped
- Unicode checkmarks (✓/✗) only in `status` and `doctor` output
- No `--json` or `--format` yet — human-readable only

### Logging Conventions

Logging uses `pkg/log` with context-based injection. Silent by default. All log lines include millisecond timestamps (`HH:MM:SS.mmm`) for timing analysis.

| Level | Flag | What to log |
|-------|------|------------|
| Error | — | Always visible; command failures |
| Info | `-v` | Phase transitions: "creating cluster", "nodes ready", "slurm services enabled" |
| Debug | `-vv` | Individual operations: "waiting for node", "creating network", "enabling slurmd" |
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
7. **Errors**: wrap with `fmt.Errorf("context: %w", err)`, no error prefixes
8. **Tests**: unit test with mock executor, integration test in lifecycle test
9. **Docs**: update DESIGN.md CLI Commands section, update docs/content/

## Testing

Development follows Test-Driven Development (TDD) style:

1. Write failing test
2. Implement minimal code to pass
3. Refactor

### Requirements

- High unit test coverage for all packages
- Integration tests for CLI commands and cluster operations
- Tests run in CI for every commit

## CLI Commands

### Cluster Management

```bash
sind create cluster [NAME] [--config FILE] [--data PATH] [--pull]
sind delete cluster [NAME]
sind delete cluster --all
sind get clusters
sind get nodes [CLUSTER]
sind get networks
sind get volumes
sind get ssh-config
```

NAME/CLUSTER defaults to `default` if omitted.

`sind create cluster` validates the environment before creating, warning or failing if conflicting resources (containers, networks, volumes with matching names) already exist.

`sind delete cluster` is idempotent and robust:
- Deleting a non-existent cluster is not an error
- Handles partial/broken clusters (e.g., failed creation)
- Removes all matching Docker resources regardless of state
- Updates `~/.sind/known_hosts` to remove deleted nodes
- Order: stops/removes containers → disconnects/removes networks → removes volumes

Example output:

```
$ sind get clusters
NAME      NODES (S/C/W)   SLURM   STATUS
default   4 (1/1/2)       25.11   running
dev       3 (0/1/2)       25.11   running
```

NODES column shows total count and breakdown: **S**ubmitter / **C**ontroller / **W**orker.

```
$ sind get nodes dev
NAME              ROLE        STATUS
controller.dev    controller  running
worker-0.dev      worker      running
worker-1.dev      worker      running
```

### Cluster Diagnostics

```bash
sind status [CLUSTER]
```

Displays detailed health information for a cluster:

```
$ sind status dev
CLUSTER   STATUS (R/S/P/T)
dev       running (3/0/0/3)

NETWORKS
NAME             DRIVER   SUBNET           GATEWAY        STATUS
sind-mesh        bridge   172.18.0.0/16    172.18.0.1     ✓
sind-dev-net     bridge   172.19.0.0/16    172.19.0.1     ✓

MESH SERVICES
NAME   CONTAINER   STATUS
dns    sind-dns    ✓

MOUNTS
MOUNT        SOURCE                    TYPE       STATUS
/etc/slurm   sind-dev-config           volume     ✓
/etc/munge   sind-dev-munge            volume     ✓
/data        /home/user/project        hostPath   ✓

NODES
NAME              ROLE        IP            CONTAINER   MUNGE  SSHD   SERVICES
controller.dev    controller  172.18.0.2    running     ✓      ✓      slurmctld ✓
worker-0.dev      worker      172.18.0.3    running     ✓      ✓      slurmd ✓
worker-1.dev      worker      172.18.0.4    running     ✓      ✓      slurmd ✗
```

### Node Access

```bash
sind ssh [SSH_OPTIONS] NODE [-- COMMAND]  # SSH into a specific node (passthrough)
sind enter [CLUSTER]                      # Interactive shell on submitter/controller
sind exec [CLUSTER] -- <cmd>              # One-shot command on submitter/controller
```

NODE uses DNS-style naming (see Node Arguments). CLUSTER defaults to `default`.

`sind ssh` passes all options and arguments through to the underlying SSH command. See the SSH section for details.

### Worker Lifecycle

```bash
sind create worker [CLUSTER] [FLAGS]    # add worker nodes
sind delete worker NODES               # remove worker nodes from cluster
```

**create worker flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--count N` | 1 | Number of nodes to add |
| `--image IMAGE` | cluster default | Container image |
| `--cpus N` | cluster default (1) | CPU limit per node |
| `--memory SIZE` | cluster default (512m) | Memory limit |
| `--tmp-size SIZE` | 256m | /tmp tmpfs size |
| `--unmanaged` | false | Don't start slurmd, don't add to slurm.conf |
| `--pull` | false | Pull images before creating containers |

Examples:

```bash
sind create worker                           # 1 managed node with cluster defaults
sind create worker --count 3                 # 3 managed nodes
sind create worker --count 2 --unmanaged     # 2 unmanaged nodes (slurmd not started)
sind create worker --cpus 2 --memory 1g      # 1 managed node with resource limits
sind create worker dev --count 2             # 2 managed nodes in dev cluster
```

**Managed node workflow:**

By default (without `--unmanaged`), sind:
1. Verifies `sind-nodes.conf` exists in `/etc/slurm` (fails if not present)
2. Creates the worker container(s)
3. Appends node definition(s) to `sind-nodes.conf`
4. Reconfigures slurmctld (`scontrol reconfigure`)
5. Starts slurmd on the new node(s)

Managed nodes require the sind-generated Slurm configuration (see Generated Configuration). If `sind-nodes.conf` is missing (e.g., user replaced the config), the command fails with an error. Use `--unmanaged` to add nodes without modifying Slurm configuration.

**delete worker** deletes containers entirely. Works with both managed and unmanaged nodes. For managed nodes, sind removes them from `sind-nodes.conf` and reconfigures slurmctld before deleting the container.

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
| shutdown | `docker stop` (SIGTERM, then SIGKILL) |
| cut | `docker kill` (immediate SIGKILL) |
| on | `docker start` |
| reboot | `docker stop` + `docker start` |
| cycle | `docker kill` + `docker start` |
| freeze | `docker pause` (cgroup freezer) |
| unfreeze | `docker unpause` |

Freeze/unfreeze uses Docker's cgroup freezer to suspend all processes. The container remains "running" but is completely unresponsive, simulating a hung or unreachable node.

### Logs

```bash
sind logs NODE [--follow]              # container logs (stdout/stderr)
sind logs NODE SERVICE [--follow]      # journalctl for specific service
```

Examples:
```bash
sind logs controller --follow          # tail container logs
sind logs controller slurmctld         # slurmctld journal logs
sind logs worker-0 slurmd --follow    # follow slurmd logs
```

### Utilities

```bash
sind get munge-key [CLUSTER]           # output munge key (base64)
sind get ssh-config                    # show SSH config path for Include
```

`sind get munge-key` outputs the cluster's munge key encoded as base64, suitable for injection into external management tooling.

`sind get ssh-config` outputs the path to the SSH config file for the current realm. Add it as an `Include` in `~/.ssh/config` to enable direct SSH access to nodes.

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
realm: sind                              # default: "sind"

defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11.2  # default: sind-node:latest
  tmpSize: 256m                          # per-node /tmp tmpfs size
  cpus: 1                                # container CPU limit
  memory: 512m                           # container memory limit

storage:
  dataStorage:
    type: volume                         # volume | hostPath
    hostPath: ./data                     # only if type=hostPath
    mountPath: /data                     # default: /data

slurm:
  main: |                                # appended to slurm.conf
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |                              # appended to cgroup.conf
    ConstrainCores=yes

nodes:
  - role: controller
    tmpSize: 512m                        # override default
    cpus: 1
    memory: 1g

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

- **String**: content appended directly to the config file
- **Map**: each key creates a fragment in a `.conf.d/` directory, included via `include <name>.conf.d/*`

| Section | Config file | sind generates defaults |
|---------|-------------|:----------------------:|
| `main` | `slurm.conf` | yes |
| `cgroup` | `cgroup.conf` | yes |
| `gres` | `gres.conf` | no |
| `topology` | `topology.conf` | no |
| `plugstack` | `plugstack.conf` | yes (always scaffolded) |

**String form** — content appended to the config file:

```yaml
slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainCores=yes
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
├── slurm.conf              # sind defaults + include slurm.conf.d/*
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
| `submitter` | 0-1 | no | none (clients only) | Job submission node |
| `worker` | 1+ | yes | slurmd | Worker nodes |

### Node Parameters

| Parameter | Scope | Default | Description |
|-----------|-------|---------|-------------|
| `image` | global + per-node | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `tmpSize` | global + per-node | `256m` | tmpfs size for /tmp |
| `cpus` | global + per-node | `1` | CPU limit |
| `memory` | global + per-node | `512m` | Memory limit |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | worker only | `true` | Start slurmd and add to slurm.conf |

### Validation Rules

- `nodes` - optional; if omitted, creates 1 controller + 1 worker
- `role: controller` - exactly one (auto-created if nodes omitted)
- `role: submitter` - at most one
- `role: worker` - at least one (auto-created if nodes omitted)
- `count` - only valid for worker role

## Docker Resources

### Per-Cluster Resources

| Type | Name Pattern | Example (`sind create cluster dev`) |
|------|--------------|------------------------|
| Network | `<realm>-<cluster>-net` | `sind-dev-net` |
| Controller | `<realm>-<cluster>-controller` | `sind-dev-controller` |
| Submitter | `<realm>-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `<realm>-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `<realm>-<cluster>-config` | `sind-dev-config` |
| Munge volume | `<realm>-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `<realm>-<cluster>-data` | `sind-dev-data` |

### Global Resources (Mesh)

| Type | Name Pattern | Example |
|------|-------------|---------|
| Mesh network | `<realm>-mesh` | `sind-mesh` |
| DNS container | `<realm>-dns` | `sind-dns` |
| SSH container | `<realm>-ssh` | `sind-ssh` |
| SSH volume | `<realm>-ssh-config` | `sind-ssh-config` |

### Defaults

The default realm is `sind` and the default cluster name is `default`, resulting in prefixes like `sind-default-*`.

## Volume Mounts

| Volume | Mount Point | Controller | Worker | Submitter |
|--------|-------------|------------|---------|-----------|
| `sind-<cluster>-config` | `/etc/slurm` | rw | ro | ro |
| `sind-<cluster>-munge` | `/etc/munge` | ro | ro | ro |
| `sind-<cluster>-data` | `/data` | rw | rw | rw |
| tmpfs | `/tmp` | per-node | per-node | per-node |

### Mount Options

SELinux relabeling (`:z`) is not used because containers run with `--security-opt label=disable`. This avoids expensive recursive relabeling of bind-mounted host directories.

Container mount flags:
```
-v sind-<cluster>-config:/etc/slurm:rw     # controller
-v sind-<cluster>-config:/etc/slurm:ro     # all others
-v sind-<cluster>-munge:/etc/munge:ro      # all nodes
-v sind-<cluster>-data:/data:rw            # all nodes
--tmpfs /tmp:rw,nosuid,nodev,size=1g       # configurable size
--tmpfs /run:exec,mode=755                 # systemd runtime
--tmpfs /run/lock                          # systemd lock files
```

### Data Mount

By default, `sind create cluster` bind-mounts the current working directory as `/data` on all nodes:
```
-v /absolute/path/to/cwd:/data:rw
```

The `--data` flag controls the mount source:
- `--data .` (default) — bind-mount the current working directory
- `--data /path` — bind-mount a specific host directory
- `--data volume` — use a Docker-managed volume (`sind-<cluster>-data`)

When a YAML config specifies `storage.dataStorage`, the config takes precedence over `--data`.

The resolved host path is stored on each container as the `sind.data.hostpath` label so that
dynamically added workers (`sind create worker`) inherit the same mount.

### Container Labels

sind applies labels to containers for filtering and metadata:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.realm` | `sind` | Realm namespace |
| `sind.cluster` | `dev` | Cluster name |
| `sind.role` | `worker` | Node role |
| `sind.slurm.version` | `25.11.4` | Slurm version |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path |

### Enter and Exec

`sind enter` and `sind exec` run commands directly inside the target container via `docker exec`
with the working directory set to `/data`. This means commands operate on the shared data mount.

`sind ssh` continues to use the SSH relay container for full SSH access (port forwarding, etc.).

## Networking

### Cluster Network

Each cluster has an isolated Docker bridge network:
- Name: `sind-<cluster>-net`
- Nodes can reach each other by container hostname

### Mesh Network

All clusters automatically join a shared mesh network for cross-cluster communication:

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-mesh` network, starts `sind-dns` |
| Subsequent clusters | Connects cluster nodes to `sind-mesh`, updates DNS |
| Cluster deleted | Disconnects cluster nodes, updates DNS |
| Last cluster deleted | Removes `sind-dns` and `sind-mesh` network |

### DNS

The `sind-dns` container (CoreDNS) provides name resolution across meshed clusters using a realm-aware zone:

```
<realm>.sind:53
```

Records follow the pattern:
```
<role>.<cluster>.<realm>.sind → container IP
```

Nodes are configured with:
```
--dns <sind-dns-ip>
--dns-search <cluster>.<realm>.sind
```

The DNS container is lightweight and does not run systemd/sshd.

### SSH

The `sind-ssh` container provides SSH access to all cluster nodes. It is a lightweight container (no systemd) that runs on the mesh network.

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
| First cluster created | Creates `sind-ssh-config` volume, generates keypair, starts `sind-ssh` container |
| Node created | Collects sshd host key, appends to `known_hosts` |
| Node deleted | Removes entry from `known_hosts` |
| Last cluster deleted | Removes `sind-ssh` container and `sind-ssh-config` volume |

#### Host Key Collection

When sind creates a node, it waits for sshd to start, then collects the host key:

```bash
docker exec <node> cat /etc/ssh/ssh_host_ed25519_key.pub
```

The key is added to `known_hosts` with the node's DNS name:

```
controller.dev.sind.sind ssh-ed25519 AAAA...
worker-0.dev.sind.sind ssh-ed25519 AAAA...
```

#### Public Key Injection

The public key from `sind-ssh-config` is injected into nodes via:

```bash
docker exec <node> mkdir -p /root/.ssh
docker exec <node> sh -c 'cat >> /root/.ssh/authorized_keys' < pubkey
```

This happens after container start, before host key collection.

#### User Access

sind only configures SSH access for the root user. Additional user management (creating users, distributing SSH keys, configuring sudo, etc.) is left to the user.

#### sind ssh Implementation

`sind ssh` executes SSH via the `sind-ssh` container:

```bash
sind ssh [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
```

Internally:

```bash
docker exec -it sind-ssh ssh [SSH_OPTIONS] <node>.<realm>.sind [COMMAND [ARGS...]]
```

All SSH options and arguments are passed through verbatim. Examples:

```bash
sind ssh worker-0                           # interactive shell
sind ssh worker-0.dev                       # node in dev cluster
sind ssh -v worker-0                        # verbose SSH
sind ssh worker-0 -- hostname               # run command
sind ssh -t worker-0 -- top                 # force TTY allocation
sind ssh -L 8080:localhost:80 controller     # port forwarding
```

#### User SSH Client Integration

sind exports SSH configuration per realm to `$XDG_STATE_HOME/sind/<realm>/` (defaulting to `~/.local/state/sind/<realm>/`) for integration with the user's SSH client:

| File | Description |
|------|-------------|
| `ssh_config` | SSH config snippet |
| `id_ed25519` | Private key (copy from volume) |
| `known_hosts` | Host keys (copy from volume) |

The generated `ssh_config` (for default realm `sind`):

```
Host *.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/%h/22; cat <&3 & cat >&3; kill $!'
    IdentityFile ~/.local/state/sind/sind/id_ed25519
    UserKnownHostsFile ~/.local/state/sind/sind/known_hosts
    User root
    StrictHostKeyChecking yes
    CanonicalizeHostname yes
    CanonicalDomains default.sind.sind sind.sind
    CanonicalizeMaxDots 2
```

The `Canonicalize*` directives enable short-name resolution for the default realm: `ssh controller` expands to `controller.default.sind.sind`, and `ssh controller.dev` expands to `controller.dev.sind.sind`. For custom realms, the `CanonicalDomains` list reflects that realm's clusters.

To find the path for a realm, use `sind get ssh-config`. Add to the **top** of `~/.ssh/config` (before any `Host` or `Match` blocks) for a single realm:

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
```

sind updates these files automatically when clusters or nodes are created/deleted. When the last cluster in a realm is deleted, the files and realm directory are removed.

## Command Routing

Interactive sessions are routed based on cluster configuration:

| Command | Target Node |
|---------|-------------|
| `sind ssh <node>` | explicit node |
| `sind enter [cluster]` | submitter (if exists) → controller |
| `sind exec [cluster] -- <cmd>` | submitter (if exists) → controller |

### sind enter

Opens an interactive shell on the submitter (or controller if no submitter configured). Equivalent to `sind ssh submitter` or `sind ssh controller`.

### sind exec

One-shot command execution. Equivalent to `sind ssh <target> -- <cmd>`.

## Container Images

### Generic Image

sind provides an generic multi-role image that works for all node types:

```
ghcr.io/gsi-hpc/sind-node:latest
ghcr.io/gsi-hpc/sind-node:<slurm-version>
```

This is the default image when `defaults.image` is not specified in the cluster configuration.

The generic image:
- Based on Rocky Linux 10
- Builds Slurm from source for each release
- Contains all daemons (slurmctld, slurmd)
- sind enables the appropriate services based on node role

The `Dockerfile` and `docker-bake.hcl` are in the repository root.

### Custom Images

Custom images must provide:

**All roles:**
- systemd as init (PID 1)
- sshd service (enabled, sind injects authorized_keys at runtime)
- munge service (enabled)
- Slurm client tools (srun, sbatch, squeue, etc.)

**Per-role requirements:**

| Role | Additional Requirements |
|------|------------------------|
| controller | slurmctld (installed, not enabled) |
| worker | slurmd (installed, not enabled) |
| submitter | Slurm client tools only |

sind enables Slurm services at container start based on the node's role. Services should be installed but not enabled in the image.

Example Dockerfiles are provided in the `images/` directory.

## Generated Configuration

### Munge

During `sind create cluster`, before starting any containers, sind generates a random munge key and writes it to the `sind-<cluster>-munge` volume. This ensures all nodes share the same key from first boot.

### Slurm Configuration

sind auto-generates a minimal Slurm configuration based on cluster topology and writes it to the `sind-<cluster>-config` volume.

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
└── topology.conf           # network topology (if slurm.topology is set)
```

The main `slurm.conf` always contains:

```
include /etc/slurm/sind-nodes.conf
PlugStackConfig=/etc/slurm/plugstack.conf
```

#### sind-nodes.conf

This file contains node and partition definitions for sind-managed nodes. sind assumes exclusive ownership of this file:

- `sind create cluster` generates initial node definitions here
- `sind create worker` appends new nodes (unless `--unmanaged`)
- `sind delete worker` removes nodes (for managed nodes)

Users should not edit `sind-nodes.conf` directly. To add custom node definitions, create a separate file and add an include directive to `slurm.conf`.

Nodes with `managed: false` in the cluster config are excluded from `sind-nodes.conf`.

#### cgroup.conf

sind generates a `cgroup.conf` for cgroupv2 support on worker nodes. This enables resource isolation and accounting for jobs.

#### User Customization

sind delivers a working starter configuration. The `slurm` config key allows extending it declaratively at creation time (see Slurm Configuration Sections above). For post-creation changes, the `/etc/slurm` volume is writable on the controller node.

Users may:
- Use `slurm.main`, `slurm.cgroup`, etc. to extend config at creation time
- Edit config files directly after creation (sind does not modify them after creation)
- Add additional include files for custom configuration
- Replace the entire configuration (but `sind create worker` will then fail for managed nodes)

## Slurm Version Discovery

sind does not manage Slurm versions directly—the version is implicit in the chosen container images. However, sind discovers the Slurm version before cluster creation to:

1. Generate version-appropriate configuration (slurm.conf)
2. Display version information in CLI output
3. Store version metadata on containers and volumes

### Discovery Method

Before creating any cluster resources, sind runs an ephemeral container to discover the Slurm version:

```bash
docker run --rm <image> scontrol --version
# Output: "slurm 25.11.0"
```

This happens once per unique image in the cluster configuration. The discovered version is then stored as labels on cluster resources:

```
--label sind.slurm.version=25.11.0
```

### Version Consistency

When the cluster configuration specifies multiple images (e.g., different images per role), sind discovers the version from each unique image. If images report different Slurm versions, sind logs a warning but continues with cluster creation. The controller image's version is used for configuration generation.

Mismatched Slurm versions can cause subtle runtime issues, but users may have legitimate reasons for mixed versions (e.g., testing rolling upgrades).

### Config Adaptation

sind maintains awareness of version-specific configuration changes and generates compatible slurm.conf. This includes handling deprecated parameters and new required parameters across Slurm versions.

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

Read-only operations (`get`, `status`, `logs`, `ssh`, etc.) do not acquire the lock.

### Behavior

- Lock is attempted non-blocking first; if free, the operation proceeds immediately
- If another operation holds the lock, sind logs `"waiting for another operation to complete"` (info level) and blocks until the lock is released
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

### Database Role (slurmdbd)

Planned support for a dedicated database node role:

```yaml
nodes:
  - role: db                           # slurmdbd + MariaDB
  - role: controller
  - role: worker
    count: 3
```

The `db` role would run slurmdbd and MariaDB for job accounting. sind would:
- Generate `slurmdbd.conf` with appropriate settings
- Configure `slurm.conf` to use the accounting database
- Initialize the MariaDB database schema

This enables testing of Slurm accounting features and multi-cluster federation scenarios.

