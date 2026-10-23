# sind - Slurm in Docker

SPDX-License-Identifier: LGPL-3.0-or-later

https://github.com/GSI-HPC/sind

A CLI tool for running local Slurm clusters using Docker containers, inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker).

## Prerequisites

- Linux host with the unified cgroupv2 hierarchy at `/sys/fs/cgroup` (not systemd's hybrid mode) and the `nsdelegate` mount option (`mount -o remount,nsdelegate /sys/fs/cgroup`), which `sind create cluster` and `sind create worker` check on the daemon's kernel before they create a node (`cluster.CheckNsdelegate`; see Container Startup); sind runs on the Docker host itself, not against Docker Desktop or a remote daemon, which `sind doctor` reports with a warning (see Host Diagnostics)
- Docker Engine 28.0+ (required for `--security-opt writable-cgroups=true` and the `gw-priority` network option), with a Docker CLI of the same release
- A rootful Docker daemon without `userns-remap`: Docker refuses `writable-cgroups` in rootless mode and with `userns-remap`, at `docker start`. `sind doctor` checks `docker info`'s `SecurityOptions` for `name=rootless` and `name=userns`, and `sind create cluster` does the same (`cluster.CheckDaemon`) before the mesh is set up or an image pulled; it also refuses a daemon whose `CgroupVersion` is `1`, and one whose `ServerVersion` is older than 28.0, which would refuse every node at `docker create`
- For clusters with 10+ nodes: `fs.inotify.max_user_instances >= 1024` (default 128 is too low; `sind doctor` warns below it)

## Supported Versions

- Slurm 26.05 and 25.11 (one node image per release line)
- OpenMPI 5.0 (with PMIx 6.x, PRRTE 4.x, UCX 1.20)
- libjwt 1.x, for Slurm's `auth/slurm` (identity `clientIds`)
- llhttp (Rocky Linux 10), for slurmrestd on the `api` node (Slurm 26.05 and later)
- Monitoring (opt-in, see Monitoring): Slurm 26.05 and later; Grafana Alloy 1.20, Prometheus 3.15, Loki 3.7 and Grafana 13.2, pinned in `pkg/observability`

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

With `--pull`, sind pulls each distinct image of the cluster's nodes once (`docker pull`), concurrently with the preflight check, the mesh lookups and the creation of the network and volumes; the steps that run an image (the helper containers, the Slurm version check, the CVMFS check, the nsdelegate check and the nodes) wait for it and create their containers without `--pull always`, so every node runs the same image and the registry is asked once per image. Without `--pull`, sind does the same for the distinct images the daemon does not have (`docker image inspect`), so that no node container's `docker create` pulls one: it would hold one of the docker client's call slots for as long as it pulls, and the readiness checks of the nodes that have started need them. `sind create worker` pulls an `--image` the daemon does not have the same way.

1. Create cluster network → connect the realm's SSH relay container and its external containers (see External Containers) to it
2. Create config volume → write Slurm configuration, `slurmdbd.conf` for a managed db node and `jwt_hs256.key` for a managed api node (managed clusters only; see Unmanaged Cluster)
3. Create munge volume → generate and write munge key (not with identity `clientIds`, whose `slurm.key` goes to the config volume)
4. Create data volume (if needed)
5. Create state volume (backup controller only)
6. Create home volume (`users` only)
7. Detect the CVMFS backend (`storage.cvmfs` only; see CVMFS Mount)
8. Check that the Docker host mounts cgroup2 with `nsdelegate` (`cluster.CheckNsdelegate`)
9. Create the realm's journal volume `<realm>-journal` (if not exists) → make the nodes' journal directories in it (`monitoring` only; see Monitoring)

The nsdelegate check runs `cat /proc/self/mounts` in a throwaway container of the controller's image (`--network none`, and `--entrypoint cat`, as a custom image may set an entrypoint) and fails with the commands that enable the option, the ones `sind doctor` prints. Docker mounts a cgroup filesystem in every container, so a mount table without one is output `docker run` lost, which it can do for a container that exits at once on a busy daemon: the check runs the container again, up to three times, rather than report cgroup v1. It reads the kernel the daemon runs on, wherever that is: `nsdelegate` is an option of the whole cgroup2 hierarchy (the kernel's `cgroup-v2.rst` calls it system wide, set only by a mount or remount in the init namespace and ignored on other mounts), and the kernel lists it in every cgroup2 mount, including the one Docker gives each container in its own cgroup namespace. Without the check, a host without `nsdelegate` gets as far as the nodes and fails later, as a node or Slurm daemon that does not become ready. The check runs alongside the other steps of this phase, after the pull with `--pull`, so a create that passes it waits for no extra container start; a create that fails it rolls back the network, volumes and helper containers made by then, before any node container exists. Running it before them instead would make every create wait for a container start, for a failure that is rare: systemd mounts cgroup2 with `nsdelegate` by default.

A monitored cluster (`monitoring: true`) adds three things to this phase. The pull takes the monitoring stack's four images along with the node images, the missing ones or with `--pull` all of them, so that Phase 4 and its `--wait` limit do not wait for a download. The image probe replaces the Slurm version check: one throwaway container per distinct image that runs slurmctld or a managed slurmd, or a managed db node's MariaDB, checks that the image can serve the HTTP endpoints the stack uses and reports the Slurm version (see Monitoring), and an image that cannot fails the create with `cluster.ErrImageUnsupported` before any node container exists. And step 9 runs the journal helper, `<realm>-<cluster>-journal-helper`, a throwaway container of the controller's image without a network, which removes what an earlier cluster of the name left in `<cluster>/` of the journal volume and makes an empty directory `<cluster>/<node>` for each node; it runs after the preflight check, which rejects a cluster that exists, so it never clears a live cluster's journals.

**Phase 3: Node Containers**
1. Create and start each node container in parallel, on its cluster network and the realm's mesh (see Cluster Network)
2. Start per-node systemd D-Bus monitor immediately after each container starts
3. Wait for each node to become ready, accelerated by events
4. Set each node up in one `docker exec`, a shell script that runs these steps in order and stops at the first command that fails:
   1. On managed workers with identity `nssSlurm` or `clientIds`, check for nss_slurm and put it first in `/etc/nsswitch.conf` (see Identity Modes)
   2. On a managed api node, check for slurmrestd and its unit, then add the drop-in that lets it start in the container (see API Node)
   3. Add the cluster users and groups where the identity mode puts them (`users` and `groups` only; see Users)
   4. Append the realm's SSH public key to root's `authorized_keys` and collect the host key (see Public Key Injection and Host Key Collection)

The script writes a marker line to stderr before each step, so that a failure names its step and shows what that step wrote, with the exit status of the command that failed (`node worker-0: adding users: exit status 9: groupadd: group 'munge' already exists`). It runs after the node's readiness wait, as ssh-keyscan needs sshd, and nss_slurm and the users are in place before Phase 4 starts the Slurm daemons and creates the Slurm accounts. A node takes the same docker calls from `docker create` to its host key whatever its role or identity mode: create, start, the readiness probes, an inspect for its addresses (alongside the setup) and the setup exec.

Every node container starts with a short `/bin/sh` entrypoint instead of the image's own entrypoint or command. As PID 1, it moves itself into `init.scope`, enables each controller of the container's root cgroup in its `cgroup.subtree_control`, and execs `/sbin/init`. On a monitored cluster it first writes the journald drop-in `/etc/systemd/journald.conf.d/zz-sind-monitoring.conf` with `SystemMaxUse=64M`, and the node mounts its directory of the journal volume at `/var/log/journal` (see Monitoring). `docker exec` puts its process in the container's root cgroup unless that cgroup has controllers enabled, and a process there makes systemd's own attempt to enable them fail (cgroup v2's no internal processes rule). An exec of sind's that landed before systemd had enabled controllers would otherwise leave every unit without them, including `Delegate=yes` daemons such as slurmd.

There is no barrier between node creation and readiness probing — each node's goroutine creates its container, starts a systemd monitor, and begins probing in a single pipeline. This allows early-starting nodes to be probed while later nodes are still being created.

#### Event-Driven Readiness

sind uses two event sources to accelerate readiness detection:

- **Docker events** — a single `docker events` stream watches all cluster containers for start/die events
- **Systemd D-Bus monitors** — per-node `busctl monitor --watch-bind=yes` streams watch for unit state changes (e.g., sshd.service becoming active or slurmd.service failing). They run until the command ends, through the waits for the Slurm daemons and the accounts

When an event of the node arrives, its readiness probes re-evaluate immediately instead of waiting for the next poll tick; the events queued by then go with it, so a burst of unit changes during boot costs one probe round. A poll tick takes the events queued by then before its round as well, so the round runs the probes they concern. Each node's wait receives only its own container's events, and the docker events stream asks only for the start, die, oom, pause and unpause actions, not for the exec events of the probes. If the event sources are unavailable, sind falls back to poll-only mode transparently.

Each round runs a node's checks in order and stops at the first that fails. Within one wait, a check that has passed is not run again in later rounds while the events can tell that what it checks has changed:

- The sshd, munge and Slurm daemon checks follow their systemd units (`sshd.service`, `munge.service`, ...): an event of the unit, which became active again or failed, runs the check again, and for munge and the Slurm daemons a failed unit then ends the wait (see Readiness Checks). Events of other units run no passed check again.
- Any other event of the container (start, oom, pause, unpause) runs every passed check again; a die event ends the wait. The container and systemd checks have no unit, so only these events run them again.
- When the events may be incomplete, the passed checks run again: after the wait's event buffer was full, as the watcher drops what a full subscriber has no room for, and in every round once a monitor has stopped (the docker events stream, or the node's busctl monitor, failed or ended, e.g. in an image without `busctl`), once the watcher has stopped, and in poll-only mode, where nothing tells a wait what changed.

While systemd boots, a round then costs one `docker exec`, the systemd check, instead of a `docker inspect` and the exec, and once systemd runs, a round that waits for munge no longer repeats the systemd and sshd checks. The trade-off: a change that no event reports goes unnoticed until the wait ends, such as a unit that stops without failing (the systemd monitor reports only units that became active or failed). As the wait ends once every check has passed, that window is the time from a check passing to the last one passing, and the steps after the wait (the node setup's ssh-keyscan, the Slurm daemons) fail on such a node with their own errors. A final round that runs every check again before the wait ends would close the window, at the price of the calls the skipping saves in the deciding round; skipping in poll-only mode too would keep a container that exits from failing the wait at once, as the container check would not run again.

#### Readiness Checks

| Check | Description |
|-------|-------------|
| Container running | Docker container in running state |
| systemd ready | `systemctl is-system-running` returns `running` or `degraded` |
| sshd listening | Port 22 accepting connections |
| munge ready | munge service active (not with identity `clientIds`, which masks munge) |
| slurmctld ready | `scontrol ping` reports this controller UP (controllers of managed clusters; each controller of a backup pair is checked for its own host); while it does not, a failed slurmctld unit ends the wait |
| slurmctld HTTP | `GET /metrics/nodes` on port 6817 answers 2xx on `controller`, `GET /livez` on `controller-backup`, whose standby slurmctld redirects the metrics (monitored clusters only, after slurmctld ready). The request goes through bash's `/dev/tcp`, as the sshd check does; a failure names `CommunicationParameters` and `SlurmctldDebug`, as slurmctld logs a missing HTTP parser only at debug level |
| slurmd ready | slurmd service active (managed workers only) |
| sackd ready | sackd service active (the submitter of a managed cluster with identity `clientIds`) |
| slurmrestd ready | slurmrestd service active and port 6820 accepting connections (managed api node) |
| slurmdbd ready | slurmdbd service active (managed db nodes). mariadb is started before it with `systemctl enable --now`, which returns once the unit is active |
| cluster registered | `sacctmgr show cluster` on `controller` lists the cluster (`accounts` only, before the accounts are created) |

A unit that has failed does not recover on its own: a failed munge, slurmctld, slurmd, sackd, slurmrestd or slurmdbd unit fails `sind create cluster` and `sind create worker` at once, with the tail of the unit's journal. So does a container that exits.

`--wait DURATION` (default `5m`, `0` for no limit) bounds how long `sind create cluster` and `sind create worker` wait for the nodes and Slurm: the node checks above, the Slurm daemons, and with `accounts` the cluster's registration with slurmdbd and the `sacctmgr` commands. Each node's limit counts from when its container has started, so image pulls do not count; the steps after the nodes are ready (Phase 4) end the limit after the last node container started. When a node or check is not ready in time, the command fails with status `1`, not `130`, with an error that names the node and its last failing check (`cluster "dev" not ready within 5m0s: node worker-0: timed out; last probe error: probe munge: ...`; `sind create worker` says `workers of cluster "dev" not ready within …`). The library returns that error wrapping `cluster.ErrNotReady`; `config.Cluster.Wait` and `WorkerAddOptions.Wait` set the limit, and zero sets none.

When a check fails for good, or the limit runs out, `sind create cluster` removes the resources it created, and the mesh if this invocation set it up and no other cluster uses it (a library caller can reuse one `mesh.Manager` for several creates). If that cleanup fails too, `sind delete cluster` removes what is left.

**Phase 4: Mesh Registration, Slurm and Home Directories** (concurrent)

After all nodes are ready, sind runs mesh registration (batch DNS ║ known_hosts), Slurm enablement, with `users` the creation of the home directories and, with `monitoring`, the monitoring stack (the progress step `monitoring`: create or start the realm's stack, wait until it is ready, and sync Alloy with the new nodes) concurrently. This is safe because Slurm uses short hostnames (`controller`, `worker-0`) resolved by Docker's embedded DNS on the cluster network, which nodes join with gateway priority 1 so that it answers before the mesh (see Cluster Network). The mesh DNS records (`*.cluster.realm.sind`) are only used for SSH relay access and host-side resolution. Unmanaged clusters skip Slurm enablement.

With a managed db node, Slurm enablement starts on the db node: mariadb, the accounting database and user, then slurmdbd, which must be active before slurmctld and slurmd are enabled (in parallel, as without a db node). slurmrestd on a managed api node, and sackd on the submitter with identity `clientIds`, are enabled alongside slurmctld and slurmd. slurmctld registers the cluster with slurmdbd when it starts (see Database Node). With `accounts`, sind then waits until `sacctmgr show cluster` lists the cluster and creates the Slurm accounts and associations (see Slurm Accounts).

On a monitored cluster, the stack's mesh DNS names (`grafana.<realm>.sind`, `prometheus.<realm>.sind`, `loki.<realm>.sind`) are registered once the concurrent group has finished: `AddDNSRecords` reads, edits and rewrites the Corefile, and the realm lock serializes commands but not the goroutines of one, so a second writer running beside the nodes' registration would lose one set of records.

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

Errors that a library caller branches on wrap exported sentinels, so `errors.Is` tells them apart without matching messages: `cluster.ErrClusterExists` (preflight conflicts), `ErrClusterNotFound` (a cluster or its controller is missing), `ErrNodeNotFound`, `ErrNodesConfMissing` (managed workers without `sind-nodes.conf`), `ErrNotReady` (the `--wait` limit ran out), `ErrNetworkFull` (the nodes do not fit on the realm's mesh or the cluster network, see Limits), `ErrImageUnsupported` (an image of a monitored cluster cannot serve the HTTP endpoints the monitoring stack uses) and `ErrNoMonitoring` (`GetMonitoring` in a realm without a monitoring stack). When `Create` or `WorkerAdd` fails and its rollback fails too, the returned error joins the rollback's failures to the original one (`errors.Join`), so the caller learns that resources were left behind.

The operations report their progress as go-clikit spans on the context they are given (see Progress Display): a library caller that puts a progress Bus in it (`progress.WithBus`) gets the spans the CLI draws, and without one every span is a no-op.

### Go Dependencies

sind uses a minimal set of dependencies, following [kind](https://kind.sigs.k8s.io/)'s approach of favoring simplicity and compatibility.

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/pflag` | Flag library under cobra: the MCP tool flag filter, and the flag errors that exit 2 as usage errors |
| `go.yaml.in/yaml/v3` | YAML configuration parsing (YAML 1.2) |
| `log/slog` (stdlib) | Structured logging interface |
| `github.com/charmbracelet/log` | Colorized log output (slog handler) |
| `github.com/charmbracelet/lipgloss` | Style of the TRACE level in the log output, and the colour profile of stderr for the log lines a progress display carries and for the progress theme |
| `github.com/muesli/termenv` | The colour profiles lipgloss reports, which pick how many colours the progress theme draws in |
| `github.com/GSI-HPC/go-clikit` | Progress spans, their displays and the event log (`progress`, `progress/display`, `progress/cliprogress`, v0.4.0; see Progress Display); the pools of the power commands and `delete cluster --all` (`fanout`); escaping of the text sind prints from docker, containers and other clients (`termtext`): the final error line, `Warning:` lines, `get` table cells and `doctor` details; the strict command tree, the usage errors that exit 2 and the build info of `sind version` (`cobratree`) |
| `github.com/mattn/go-isatty` | TTY detection for interactive commands and the progress display |
| `github.com/njayp/ophis` | MCP server framework |
| `github.com/tailscale/hujson` | `sind mcp <editor> enable` and `disable`: edits an editor's MCP configuration (JSON with comments and trailing commas) in place, keeping its other settings, comments and formatting |
| `github.com/modelcontextprotocol/go-sdk` | MCP request and result types for the ophis tool middleware; the bearer-token check and HTTP server of `sind mcp stream` |
| `github.com/spf13/afero` | Filesystem abstraction for testability |
| `golang.org/x/crypto` | The OpenSSH encoding of the SSH relay's ed25519 keypair (`ssh`) |
| `golang.org/x/sync` | Errgroup for concurrent operations |
| `golang.org/x/sys` | Realm lock: advisory file locking (flock), and the check whether a lock holder still runs (kill) |
| `github.com/GSI-HPC/go-nodeset` | Node arguments: ClusterShell node set expressions (ranges, steps, set operators) |

**Node sets** in node arguments (e.g., `worker-[0-2,5]!worker-1`) are parsed by go-nodeset, an implementation of the ClusterShell syntax with set operators; see [Node Arguments](#node-arguments).

### Docker Interaction

sind interacts with Docker by **shelling out to the `docker` CLI** rather than using the Docker SDK for Go. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- Avoids tight coupling to Docker daemon internals

sind wraps command execution in a thin abstraction layer (`pkg/cmdexec`) using Go's `os/exec` package, with proper output handling and error reporting. The executor interface is shared across `pkg/docker`, `pkg/mesh`, and `pkg/cluster`.

A `docker.Client` runs at most 16 docker commands at once (`docker.MaxConcurrentCalls`); the others wait for a free slot. Creating a cluster probes every node in its own goroutine, and the bound keeps a large cluster from forking hundreds of docker processes that compete with the booting nodes for the daemon. It limits the commands, not the per-node goroutines, so every node still boots at once. Long-lived streams (`docker events`, `busctl monitor`) take no slot.

Each docker command of a `docker.Client` is also a hidden progress call (see Progress Display), named after its subcommand, with the verb of a management command (`docker exec`, `docker network create`) and never its arguments: queued while it waits for a slot, running once it has one, and ended with docker's exit code when it failed. `cmdexec.OSExecutor` tees what a command writes through `progress.Tee`, so that a span that shows lines, a target of `pull images` or a part of the mesh, shows the newest line docker wrote: its standard error, and its standard output only for `docker pull`, which writes its status there; every other command writes data there, an ID, a name or the JSON of `inspect`, which `docker.Client` marks as such (`cmdexec.WithStdoutAsData`), so that the row of the relay shows the pull its `docker create` makes and not the container's ID. `docker pull` runs without `--quiet` for that, and its output is still not kept, apart from what the layer count reads: `docker.Client.PullImage` installs a line parser in the context (`cmdexec.WithLineParser`, which `OSExecutor` hands to the `progress.Tee` of both streams) that counts the layers docker writes status lines for (`<12 hex digits>: Pulling fs layer`, `Pull complete`, `Already exists`, and the others; a line or status it does not know is ignored) and reports them on the target of the image as work in items, `progress.Update(Work(Items, layers told of))` and `SetAmount(layers done)`, so that the event log has `amount`, `size` and `unit` and never a line. docker prints no byte counts where it is no terminal (the CLI drops the progress of `Downloading` and `Extracting` there), so layers are the unit; a pull that writes no layer, as one of an image the daemon has, ends as one item of one.

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

- Multi-resource verbs (`create`, `delete`, `get`, `power`, `connect`, `disconnect`) group noun subcommands
- `mcp` groups the MCP server commands (`start`, `stream`, `tools`) and one command per editor (`sind mcp claude enable`)
- Single-purpose verbs (`ssh`, `enter`, `exec`, `logs`, `doctor`) stand alone
- Standalone verbs are reserved for frequently-used operations that justify a short path
- Groups print their help when invoked bare and fail on an unknown subcommand (`sind get bogus` exits 2), naming the closest subcommand or, for the removed `sind status`, the command that replaced it (`cobratree.Strict` in `NewRootCommand`, once the tree is built, so it covers every group). They fail with `--help` too (`sind get bogus --help`): a group's help flag (`groupHelp`, which `groupHelpFlags` gives every group after `cobratree.Strict`) reads as unset to cobra, which would print the help before it checks the arguments, and the group's `RunE` prints it once the check has passed
- cobra's built-in `help` and `completion` commands follow the same rule, as `cobratree.Strict` adds them before it makes the tree strict: `sind completion fish-typo` fails like any group, and `sind help TOPIC` fails unless TOPIC names a command in full (`sind help bogus`, `sind help get bogus`) or a topic of `sind help topics`

### Argument Conventions

| Pattern | Positional | Default | Examples |
|---------|-----------|---------|---------|
| Cluster name | `[NAME]` or `[CLUSTER]` | `"default"` | `get cluster`, `enter`, `get auth-key` |
| Node targets | `NODES` (required) | — | `power shutdown`, `delete worker` |
| Node format | `shortname.cluster` | cluster defaults to `"default"` | `worker-0.dev`, `controller` |
| Node sets | ranges and set operators | — | `worker-[0-2].dev`, `worker-[0-3]!worker-2` |
| Pass-through | after `--` separator | — | `ssh NODE -- cmd`, `exec -- cmd` |

Rules:
- Cluster names are **always positional**, never flags
- Node targets are node set expressions; a command joins its node arguments with a space, which is a union
- Use `optionalCluster` (`cobra.MaximumNArgs(1)` plus the name check) for optional cluster, read with `clusterArg` (the default cluster without one), `cobra.MinimumNArgs(1)` for required nodes, and `noArgs` for none: it says `accepts no arguments, received N` in the words of cobra's other arity checks, where `cobra.NoArgs` calls the argument an unknown command

### Flag Conventions

- **Long-form only** by default; add short flags (`-f`) only for frequently-typed flags
- **Kebab-case** for multi-word flags: `--tmp-size`, `--cap-add`
- **Boolean flags** for mode switches: `--all`, `--pull`, `--unmanaged`
- **Three persistent root flags**: `--realm`, `--progress` and `--progress-log` (inherited by every subcommand). `PersistentPreRunE` checks `--realm` and `--progress` for every command before it acts (`checkRealmFlag`, `checkProgressFlag`), so a value that is not valid is a usage error even for a command that does not use it; `--progress-log` is opened only by the commands that show progress, as opening it creates the file
- **One persistent root counter**: `-v` (repeatable, controls log verbosity; inherited by every subcommand)
- `ssh` passes its arguments through to SSH, so the root flags and `-v` must precede it (`sind -v ssh worker-0`); only a leading `-h` or `--help` is sind's, and an argument that begins with `--` before the `--` separator is a usage error, as SSH has no long options. `exec` parses its own flags up to its `--`. Their usage lines put `[flags]` where the flags go (`DisableFlagsInUseLine`): `sind exec [flags] [CLUSTER] -- COMMAND [ARGS...]`, and none for `ssh`, whose help says that sind's flags go before it

### Output Conventions

| Command type | Output | Target |
|-------------|--------|--------|
| List resources (`get`) | table by display width (`cliout.Table`), uppercase headers, 2-space padding | stdout |
| Single value (`get auth-key`) | raw value, one line | stdout |
| Checks (`doctor`) | one ✓/✗/! line per check (`!` for an advisory check that did not pass), fix instructions below a check that did not pass | stdout |
| Mutations (`create`, `delete`, `power`, `connect`, `disconnect`) | nothing on success where stderr is not a terminal; on a terminal, the progress display of the commands that have one (see Progress Display) | stderr |
| Errors | structured slog at error level (always visible) | stderr |
| Warnings | `Warning: ...` prefix | stderr |
| Notes | `No <things> found in <where>.` below a table that lists nothing; `Note: ...` for any other remark on the output (`get ssh-config`) | stderr |
| Logs (`-v`) | structured key=value, colorized on TTYs | stderr |
| Progress (`--progress`) | live tree, counter or plain lines; a summary line for a command that failed or was interrupted | stderr |

Rules:
- Mutations are silent where stderr is not a terminal, unless `--progress plain` asks for lines — `exit 0` is the confirmation; use `-v` for logs. On a terminal they draw their progress, which leaves one line for each step they finished and, when they fail, a summary line (see Progress Display); without a display, stderr carries byte for byte what it would without progress
- Errors are always visible (slog error level is always enabled, even without `-v`), except that `ssh`, `exec`, `enter` and `logs` add none when the program they run fails: it writes its own
- A docker CLI that is missing or cannot reach the daemon fails every command with the same line, which names no step and no exit status: `Docker is not reachable: ` and the line of docker's error that says so (`docker CLI not found in PATH` for a missing CLI), then ``; run `sind doctor` for the fix`` (`docker.UnreachableError`, matched by `errors.Is(err, docker.ErrUnreachable)`; every `docker.Client` call classifies its error)
- The final error line, and the `Warning:` line printed when the SSH config export fails, are escaped: it can quote what docker or a container wrote, so control characters, bidirectional controls and invalid UTF-8 in it are shown as `\x1b`, `\u202e` or `\xff` instead of reaching the terminal; newline and tab are kept. The cells of `get` tables (`cell`), whose text comes from labels, containers and docker, and the details of `doctor` checks are escaped the same way; a table cell also shows tab and newline as `\t` and `\n`, which would otherwise start a column or a row. JSON output, the `sind logs` stream and key or `known_hosts` output are written unchanged
- Listings are in natural order of their names, digits by value (`dev2` before `dev10`, `worker-2` before `worker-10`, `internal/natsort`), in tables and JSON alike: clusters, realms, networks, volumes, the clusters of `get monitoring` and of `delete cluster --all`, shell completion, and `get dns` by host name; `get nodes` orders by cluster, then role, then name
- An empty table cell is `-`, such as the IP of a stopped node or the `HA` of a worker, so that every row has all its columns for `awk` and `cut`; JSON output keeps `""`
- A table that lists nothing still prints its header on stdout, and a note on stderr says so and where sind looked, so that a lone header is not taken for a failure (`noteNone`): `No clusters found in realm "sind".`, and the same for `get nodes`, `get networks`, `get volumes`, `get dns`, `get realms` (`No realms found.`), the `CLUSTERS` table of `get monitoring` (`No monitored clusters found ...`) and `mcp <editor> list`, which names the file it read (`No MCP servers found in "<path>".`), as a mistyped `--config-path` reads as an empty file. JSON output has an empty list and no note
- Command output (tables, status, doctor) is monochrome — no ANSI escapes
- Log output (`-v`) is colorized on interactive terminals, plain when piped
- Unicode checkmarks (✓/✗; `!` for the warnings of `doctor`) only in `get cluster`, `get node` and `doctor` output, and in the progress display, which draws ASCII marks unless the locale is UTF-8
- All `get` subcommands and `doctor` accept `--output|-o {human,json}`; default is `human`

Exit status and signals:
- `0` on success, `1` on failure, `2` for a usage error, `130` when SIGINT or SIGTERM interrupted the command (`cmd/sind/exitcode.go`). Scripts branch on them, so a status may be added but never renumbered
- A usage error is a command line that sind rejects before it acts: an unknown command or flag, a flag value or argument that is not valid, or the wrong number of arguments (the removed `sind status` points to `get cluster`). An error in the arguments of a command, the wrong number of them or one that is not valid, ends with the command's usage line, `(usage: sind power on NODES)`: `cobratree.Strict` adds it to every `Args` check, and `usageLines`, through `withUsageLine`, to the errors a command's `RunE` marks with `argsError` (node arguments, the arguments `ssh` and `exec` parse themselves, `delete cluster --all NAME`). An error in a flag value does not get it, as the usage line does not show the flags. That is every pflag parse error, wherever it happens (cobra parses the flags of the parents it traverses without its `FlagErrorFunc`, and `exec` parses its own), every `Args` check (`cobratree.Strict` wraps them all, those of the groups and of `help` among them), and the checks a command makes of its arguments and flags before it acts, which return `cobratree.Usage(err)`: node arguments, `-o`, `--realm`, `--progress`, `--progress-log`, and the arguments `ssh` and `exec` parse themselves. A flag value outside the words a flag takes reads `invalid --output value "yaml": want human or json` (`invalidFlagValue`, for `-o`, `--type` and `--progress`), and one pflag cannot parse as the flag's type reads the same, with what it wants instead of Go's parser error (`invalid --wait value "5": want a duration with a unit, such as 90s, 5m or 1h30m`; `flagError`, the root's `FlagErrorFunc`, for int, count, bool and duration flags). A value the CLI checks like a config setting names the flag (`device "dev/fuse" in --device: host path must be absolute`). `SIND_REALM` and the config file are not the command line, so an invalid one exits `1`; `SIND_PROGRESS` and `SIND_PROGRESS_LOG` fail no command (see Progress Display)
- `ssh`, `exec`, `enter` and `logs` exit with the status of the docker command they run, which `docker exec` takes from the command it ran (for `ssh`, from `ssh` and the remote command), and print no error line. A docker killed by signal N exits 128 + N, as a shell reports it, except SIGINT and SIGTERM, which exit `130` like an interrupted sind: they reach docker when sent to sind's process group, and the status must not depend on which process ends first
- SIGTERM exits `130`, not `143`, so that callers check one status for "interrupted", as in clusterctl
- A `--wait` limit that runs out is a failure, not an interrupt: `create cluster` and `create worker` exit `1`
- The first SIGINT or SIGTERM cancels the command's context; deferred cleanup (e.g. the rollback of a failed `create cluster`) still runs under `context.WithoutCancel`, bounded at 5 minutes, and a cleanup step that fails is added to the command's error
- The signal handler is removed before the context is cancelled, so a second signal gets the default action and ends sind at once, even during a hung cleanup
- On a terminal, the progress display says `interrupting` once the first signal has arrived, and the summary line says the command was `canceled`

### Logging Conventions

Logging uses `pkg/log` with context-based injection. Silent by default. All log lines include millisecond timestamps (`HH:MM:SS.mmm`) for timing analysis.

Logs and progress are separate channels: the logger writes the records `-v` asks for, and a progress display shows the spans the work reports (see Progress Display). Under a display the logger is rebuilt on the display's terminal (`Terminal.Lines`), so every record, from whatever goroutine, is written whole above the display and never into it, in the colours of the real stderr; without one it writes to stderr as before. The logger takes its colours from the colour profile of stderr, which lipgloss reads from the environment, and never asks the terminal for its colours: charmbracelet/log would have termenv query a terminal it writes to, and a pseudo-terminal that answers nothing, as under `script`, `expect` or `docker run -t`, would hold every command for 10 seconds and keep the queries in its output.

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
- What a user watches while a command runs is a progress span, not a log line: a display shows spans, and `-v` shows logs
- What a user must know although the command went on, such as a skipped step or a seccomp profile a new worker does not inherit, is a warning, not a log record: `pkg/` reports it with `sindlog.Warn(ctx, msg)` (or `mesh.Manager.Warn`, `state.LockOptions.OnWarning`), which the CLI prints as a `Warning:` line on stderr at every verbosity (`withStderr`); a library caller without that sink gets it at slog's warn level. Level Warn is for operator detail only

### Progress Display

`create cluster`, `create worker`, `delete cluster` (also `--all`), `delete realm`, `delete worker`, `connect container`, `disconnect container` and the `power` commands show their progress on stderr while they run, with the `progress`, `progress/display` and `progress/cliprogress` packages of [go-clikit](https://github.com/GSI-HPC/go-clikit) v0.4.0, as clusterctl does. `withProgress` (`cmd/sind/progress.go`) decorates their `RunE`: the command runs in a progress span of its own, named by its path below the root (`create cluster`), and the code it calls in `pkg/` reports its work under that span as steps, targets, waits and calls (`progress.Start` on the command's context; without a Bus in the context, as for a library caller, every span is a no-op). `ssh`, `enter`, `exec`, `logs`, `mcp`, `get`, `doctor`, `version` and `completion` never show progress.

`--progress MODE`, else `SIND_PROGRESS`, else `auto`, says how stderr shows it:

| Mode | Shows |
|------|-------|
| `auto` | `tty` where stderr is a terminal, `TERM` is not `dumb` and stdout goes into no pipe or socket, whose reader (`less`, `grep`) writes to the same terminal; nothing elsewhere |
| `tty` | The live tree: a few rows at the bottom of the terminal, redrawn as the work goes on, and a line above them for each step once it has finished; the counter's line on a terminal smaller than 40 columns or 8 rows |
| `counter` | One line, redrawn as the work goes on, with how far each counted step under way has got and how long the command has run: `stopping · 9/24 · 8 running · 7 queued · 0:07.3` |
| `plain` | A line for each step that starts or ends, each wait with a limit as it starts, each target that fails, and every ten seconds for each counted step under way, after the time since the command started and with no escape codes; it needs no terminal |
| `none` | Nothing |

- `--progress tty` or `counter` where neither can be drawn, as stderr is not a terminal or `TERM` is `dumb`, is a usage error, so that whoever asked learns why nothing shows, and so is a mode `--progress` does not take; `checkProgressFlag` refuses both in `PersistentPreRunE`, before any command acts, also one that shows no progress.
- `SIND_PROGRESS` is set once, in a profile or for a CI job, and inherited by commands that never asked, so it fails no command: `tty` or `counter` where neither can be drawn shows nothing, and a value sind does not take shows nothing and says so in one line on stderr (`sind: SIND_PROGRESS is "plian"; it takes one of auto, tty, counter, plain, none; no progress is shown`). Only a command that shows progress reads it.
- Without a display (`none`, and `auto` where stderr is not a terminal: a pipe, a file, a CI log, sind-action, an MCP tool call), sind makes no progress Bus unless an event log is asked for, and stderr carries byte for byte what it would without progress.
- The tree and the counter draw nothing in their first second: a command done by then leaves nothing on the terminal. A hidden span, such as a docker command (a call named after its subcommand, `docker exec`), is drawn only once it has run for a second. Only steps leave a line, with their targets that failed below it: a hidden step only when it failed or took a second or longer, and no step that ends well within a tenth of a second with nothing drawn below it.
- On success the tree leaves only the lines of its finished steps, the counter nothing and `plain` its lines, and `exit 0` says the rest. A command that failed or was interrupted after a second or longer also gets, under any display, a summary line (`display.Summary`) below the display and above the final error line: `sind: create cluster: failed in 38s: 5 ok, 1 failed, 2 canceled`. The Bus, the display, the event log and the summary live inside the decorated `RunE`, which takes them down before it returns, and so before `run` prints the error line: cobra does not run `PersistentPostRunE` for a command that failed.
- Under a display, everything the command writes goes through the display's `Terminal`, which takes the tree or the counter off first: cobra's stderr (`Warning:` lines, help), stdout when it is the same terminal, the context's stderr (`withStderr`: the warnings `pkg/` reports, the realm lock's and the mesh's among them) and the logger (see Logging Conventions). Nothing in `pkg/` writes to a stream itself.
- On a terminal the displays and the summary draw go-clikit's Classic theme: its marks in green, red, amber and blue, `⋅` and `⋯` as separators, and a mark in front of the summary (`sind: ✗ create cluster: failed in 38s: ...`). It draws in 256 colours, in the terminal's own 16 where the colour profile of stderr shows no more, and in its marks alone under `NO_COLOR` (`progressTheme`, from the profile lipgloss reads for the logger). Off a terminal, and on one whose `TERM` is `dumb`, the displays draw plain text, as the lines then go to a log.
- The tree draws ASCII marks, and plain lines join the parts of a path with `>` instead of `›`, unless `LC_ALL`, `LC_CTYPE` or `LANG` names UTF-8 (`cliprogress.UTF8Locale`). It draws its clocks in tenths of a second, `0:41.3` for the command and `4.2s` for a running step, so that every frame differs while anything runs; the summary and the durations of finished steps keep their formats. go-clikit asks the terminal for its size at each frame and draws nothing in the background of a terminal (`cliprogress.TerminalSize`, `cliprogress.InForeground`); sind passes both the stream and decides nothing about them.
- Once the first SIGINT or SIGTERM has arrived, the command's row says `interrupting`, with how many running targets will stop and how many queued ones will not start, and every span that fails from then on ends canceled (`progressClass`), as a docker command the interrupt killed fails with `signal: killed` alone. Otherwise a usage error is of the class `usage`, `cluster.ErrNotReady` (`--wait`) a `timeout`, and any other failure the target's.
- `create cluster --config -` takes the display off the terminal while it reads stdin, which may be the terminal (`progress.Suspend`).
- `--progress-log FILE`, else `SIND_PROGRESS_LOG`, appends the command's events to FILE as JSON lines, go-clikit's [event log](https://github.com/GSI-HPC/go-clikit/blob/v0.4.0/doc/event-log.md) version 1 (`progress.NewLog`, with sind's version), with any mode, `none` included, and writes nothing on stderr. `cliprogress` opens it privately: it creates the file with mode `0600` and appends to it (`O_APPEND`), so that the commands of a CI job can share one log, each a `run` of its own, and refuses a file that is not the user's, that others can read or write (`run chmod 600 <path>`), that has other names (`<path> has other names than this one (N hard links)`), or that a link or a named pipe of another user leads to; it writes `/dev/stdout`, `/dev/stderr`, `/dev/fd/N` and `/proc/self/fd/N` through a duplicate of that descriptor. A log on the file the display draws on, such as `/dev/stderr` on the terminal, goes through the display's `Terminal`, so that its lines, written from the log's goroutine, land above the display and never in it. A log the flag names that cannot be used is a usage error before the command acts; one the variable names is a note on stderr, and no log. `--progress-log ""` writes no log even when the variable names one, and `SIND_PROGRESS_LOG=` with an empty value writes none either.
- Without `--progress-log` and `SIND_PROGRESS_LOG` (neither given nor set, even to empty), the command writes the activity log, `$XDG_STATE_HOME/sind/activity.jsonl` (`~/.local/state/sind/activity.jsonl`; `state.ActivityLogPath`), so that a tool that watches the state directory sees every command, whichever way it was started. `withActivityLog` (`cmd/sind/progress.go`) makes the state directory with mode `0700`, through `state.PrepareActivityDir`, and hands `cliprogress` the log as `Options.DefaultLog` with `RotateAt` of 8 MiB. `cliprogress` moves a log larger than that to `activity.jsonl.1` under an `flock` of `activity.jsonl.lock`, so that commands that run at once rotate it once, and opens it with mode `0600`, refusing one that is not the user's, has other names or can be read or written by others, without a note. It changes nothing on stdout or stderr: a log that cannot be prepared or opened is left out, at most with a debug log line, and never fails a command. `TRACEPARENT` and `TRACESTATE` are continued as for any log. A log that cannot be written once the command runs fails nothing and says nothing.
- `TRACEPARENT` and `TRACESTATE` name the W3C trace the event log continues (`progress.ParseTraceContext`). A command that makes a Bus, as it shows progress or writes an event log, takes both out of its environment (`takeTraceContext`, from `cliprogress.Options.Trace`), so docker and the other programs it runs inherit neither: sind sends its spans nowhere else, and a child that ran under one would hang what it traces under a parent no tracing system holds. Without a Bus sind has no spans, and the programs it runs inherit both, so that their spans belong under the caller's.
- The command span, named after the command (`create cluster`), carries in its `node` field what the command works on, for a reader of the event log such as sind ui (`withProgressOn`): the cluster of `create cluster` (its NAME, else the name in its `--config` file; a config read from stdin names it when the span ends), `create worker` and `delete cluster` (none with `--all`), the clusters of the nodes of `power` and `delete worker`, joined with `,`, and the containers of `connect container` and `disconnect container`. No display draws it.
- `create cluster` reports, under its command span: the hidden step `mesh` (`mesh.Manager.EnsureMesh`), with a target for each part, `network`, `dns`, `volume` and `relay`, announced queued before the first runs, skipped as `exists` or `running` when it had nothing to do, so that a mesh in place leaves nothing, and showing in the row of the DNS container and of the relay the lines of the pull their `docker create` makes; `pull images`, only when sind pulls, with a target for each image that shows the lines of `docker pull` and its layers done out of those told of, as work in items; the hidden step `preflight` beside it (the checks, `createResources`, `resolveInfra`, `DetectCVMFS`, `CheckNsdelegate`), canceled when the pull failed, and failed when the inspection of the images that comes before any pull did, as no step shows that one; `nodes`, with a target for each node (its short name and role) and under it the node's docker calls, its wait `ready` and its call `setup` (`setupNode`); then `mesh registration`, `slurm` (a target for each managed node, the db node's announced with the others), `accounts` (with `accounts`), `home directories` (with `users`) and `monitoring` (with `monitoring`: the realm's monitoring stack and the sync of Alloy). `create worker` reports `pull images`, `nodes`, `mesh registration` and `slurm` the same way, and on a monitored cluster `monitoring` before `nodes`.
- The wait `ready` (`probe.UntilReady`) is bounded by what is left until the context's deadline, the `--wait` limit, in whole seconds, and its message names the check that fails now, updated when that changes. Plain lines say when it starts, with its bound, and when it fails or is interrupted.
- The `nodes` and `slurm` pools stay fail-fast errgroups with the same errors: a node stopped by a sibling's failure or an interrupt ends canceled with its own error as text (`endSpan`), not failed with `signal: killed`; when a db node fails, the nodes not started end canceled with the step.
- The rollback of a failed `create cluster` or `create worker` is the step `rollback` (`startRollback`, and the removal of a mesh `EnsureMesh` made in `cmd/sind/create.go`), ended ok or with what it could not undo, which is a failure also after an interrupt (`rollbackFailure` classes it by its own error, not by `progressClass`), as the rollback runs on; the command's error and what it logs are the same as without it.
- The power commands report the step their pool is named by, and `delete cluster --all` the step `clusters`, with a target for each node or cluster (see Power Control and Cluster Management).
- `delete cluster` reports, each step only when it has something to do: `mesh deregistration` (`deregisterHostnames`, when other clusters keep the mesh; failures are warnings and the step ends ok), `nodes` (`DeleteContainers`, a target for each container named by its node's short name, with its role), `network` (`removeClusterNetwork`: detach, then remove), `volumes` (`DeleteVolumes`, a target for each volume), `monitoring` (`forgetMonitoredNodes`, hidden without a cluster) and, with the realm's last cluster, the steps `monitoring stack` and `monitoring volumes`, both hidden, around `mesh` (`cleanupRealm`). `delete worker` reports `slurm` (`removeNodesConf`) beside `mesh deregistration`, then `nodes` and `monitoring`. The targets of a removal are announced queued before the first runs, and end failed, or canceled when the end of the context stopped them, each by its own outcome. Below a target of `clusters` (`delete cluster --all`) these steps announce no targets: a target counts in every counting step above it, so they would break the counts of the pool (`progresstest.Check`). The same steps run under `rollback` when a create undoes its work.
- `connect container` and `disconnect container` report the step `connecting` or `disconnecting` (`eachExternal`), with a target for each container, named by it and announced queued before the first runs. The containers are done one after another; one that was on every network of the realm already (`connect`) or on none (`disconnect`) is skipped, as `connected` or `not connected`, and the first failure ends the step, the targets after it canceled with it.
- The wait for the realm lock is the wait `realm lock`, whose message names the holder of the daemon lock; the `Warning:` line stays (see Realm Advisory Locking).
- MCP tool calls draw no progress. They write the activity log like any command, and a call that comes with a progress token gets its progress as MCP progress notifications (see MCP Server).

### Shell Completion

All commands that accept cluster names or node names must set `ValidArgsFunction`:

- **Cluster name commands** → `completeClusterNames`
- **Node name commands** → `completeNodeNames`; `completeNodeName` for a command that takes one node (`get node`), which completes nothing after it, and `completeWorkerNames` for `delete worker`, which removes workers only
- **Commands with DisableFlagParsing** (ssh, exec) → `ValidArgsFunction` with heuristics (best-effort despite cobra limitations)
- **External containers** → `completeContainers`: `connect container` offers the containers outside sind that are not on the mesh yet (`cluster.ConnectableContainers`), `disconnect container` those that are (`cluster.ExternalContainers`), each but the ones already named
- **Commands without arguments** complete nothing: `NewRootCommand` gives every leaf without a `ValidArgsFunction` `cobra.NoFileCompletions` (`noFileCompletions`), where the shell would offer file names
- **Aliases**: cobra completes none, so the `power` group completes `off` once a prefix of it is typed (`completeAliases`)

When adding a `get` subcommand with positional arg completion for a second argument
(like `logs NODE SERVICE`), write a dedicated completion function that switches on `len(args)`. `logs NODE SERVICE` offers the services of the node's role, those `get node` checks (`cluster.NodeServices`).

### New Command Checklist

When introducing a new command:

1. **Structure**: verb-noun ordering, consistent with existing hierarchy
2. **Args**: cluster as optional positional (default "default"), nodes as required positional
3. **Flags**: long-form, kebab-case, minimal short flags
4. **Completion**: add `ValidArgsFunction` for cluster/node args
5. **Output**: table for lists, nothing on success for mutations but their progress display, silence for passthrough
6. **Logging**: info for phases, debug for operations, trace for raw commands
7. **Errors**: wrap with `fmt.Errorf("context: %w", err)`, no error prefixes; an argument or flag value that is not valid returns `usage(err)` or `usagef(...)`, so that it exits 2 (`Args` checks are wrapped already)
8. **Tests**: unit test with mock executor, integration test in lifecycle test
9. **MCP**: classify the command in `mcpEffects`, or list it in `mcpExcluded` (`cmd/sind/mcp.go`)
10. **Progress**: a command that changes clusters and asks nothing wraps its `RunE` with `withProgress`, and the work it calls reports its steps, targets and waits as progress spans, every target of a counted step announced queued before the first of them runs (see Progress Display)
11. **Docs**: update DESIGN.md CLI Commands section, update docs/content/

## Testing

Development follows Test-Driven Development (TDD) style:

1. Write failing test
2. Implement minimal code to pass
3. Refactor

### Requirements

- High unit test coverage for all packages
- Integration tests for CLI commands and cluster operations
- Tests run in CI for every pull request and every push to `main` and `next`

## CLI Commands

### Cluster Management

```bash
sind create cluster [NAME] [--config FILE] [--data PATH] [--pull] [--monitoring] [--wait DURATION]
sind delete cluster [NAME]
sind delete cluster --all
sind delete realm
sind connect container CONTAINER...
sind disconnect container CONTAINER...
sind get cluster [NAME]
sind get clusters
sind get node NODE
sind get nodes [CLUSTER]
sind get networks
sind get realms
sind get volumes
sind get mesh
sind get coredns-config
sind get monitoring
sind get dns
sind get ssh-config
sind get ssh-private-key
sind get ssh-public-key
sind get ssh-known-hosts
sind get auth-key [CLUSTER] [--type munge|slurm|jwt]
```

All `get` subcommands accept `--output|-o {human,json}`. The default is `human` (tabular text); `json` emits a machine-readable document.

NAME/CLUSTER defaults to `default` if omitted, except for `get nodes`, which then lists the nodes of every cluster.

`sind create cluster` reads its configuration from `--config FILE`, or from stdin with `--config -`, where empty input is an error. An empty `--config ""` is a usage error, as is an empty `--data`: an unset variable in `--config "$CFG"` would otherwise create the default cluster, or read a piped stdin. Without `--config` it creates the default cluster (1 controller + 1 worker). Reading a stdin that is not a terminal without `--config -` is deprecated in v0.11.0 and will be removed: until then sind still reads it, but first writes a deprecation `Warning:` to stderr, since it waits for the end of that input, and takes empty input for the default cluster. A wrapper that hands sind a pipe it does not mean as the configuration (e.g. `ssh HOST sind create cluster`, or a `while read` loop) redirects stdin from `/dev/null`.

`--monitoring` collects the cluster's metrics and logs in the realm's monitoring stack (see Monitoring). It is ORed into the config's `monitoring` key before validation: either turns monitoring on, and `--monitoring=false` does not turn off a config's `monitoring: true`. With an unmanaged config it fails like the key does, with exit status 1, as the conflict is with the config file.

`sind create cluster` validates the environment before creating, failing if conflicting resources (containers, networks, volumes with matching names) already exist, or if the nodes would not fit on the realm's mesh or the cluster network, Docker bridge networks of at most 1,023 containers (see Limits under Networking).

`sind delete cluster` is idempotent and robust:
- Deleting a non-existent cluster is not an error
- Handles partial/broken clusters (e.g., failed creation)
- Removes all matching Docker resources regardless of state
- Updates `~/.local/state/sind/<realm>/known_hosts` (or `$XDG_STATE_HOME/sind/<realm>/known_hosts`) to remove deleted nodes
- Order: stops/removes containers → disconnects/removes networks → removes volumes
- Before it removes a network, it disconnects every container still attached, running or stopped (`docker ps -a --filter network=`): the SSH relay, and containers outside sind that were attached with `docker network connect`, such as kind nodes. Those are detached, not removed, as Docker does not remove a network with active endpoints, and a stopped container that still names a removed network fails to start
- The last cluster of a realm takes the mesh with it; its nodes are then not removed from the mesh DNS and `known_hosts` first
- It updates the exported SSH configuration (see User SSH Client Integration) also when it fails partway, as `--all` does

`sind delete cluster --all` deletes every cluster of the realm in parallel, then the mesh, the monitoring stack and the exported SSH configuration, as `sind delete realm` does. It finds the clusters by the `sind.cluster` labels of their containers, networks and volumes, and removes the mesh even when no cluster is left, as after a killed create. A cluster that fails to delete does not stop the others: the rest are deleted (and removed from the mesh DNS and `known_hosts`), the mesh stays, and the command exits non-zero with every failure. It deletes at most 4 clusters at a time, on go-clikit's `fanout.Map`, which a display shows as the step `clusters` with a target for each. Once the command is interrupted it starts no further cluster, and each one left out fails as one tried then would, `deleting cluster <name>: listing containers: context canceled`. A panic while deleting a cluster becomes that cluster's error, `sind panicked; this is a bug, please report it: ...`, with a line that names the cluster and the stack on stderr, and the other clusters finish: the command exits 1, where the panic used to end sind with Go's report and status 2.

`sind delete realm` deletes everything sind made for the realm: every cluster, the mesh, the monitoring stack and the exported SSH configuration, as `sind delete cluster --all` does, and fails the same way. It takes the realm from `--realm`, `SIND_REALM` or the default, as every command does, and no arguments: `sind delete realm ci-42` is a usage error that points to `sind --realm ci-42 delete realm`. Containers outside sind attached to the realm's networks are detached, not removed.

`sind connect container` and `sind disconnect container` manage external containers, see External Containers.

Example output:

```
$ sind get clusters
NAME     NODES (S/C/D/A/W)  SLURM    STATUS
default  5 (1/1/0/1/2)      26.05.5  running
dev      4 (0/1/1/0/2)      25.11.8  running
```

NODES column shows total count and breakdown: **S**ubmitter / **C**ontroller / **D**b / **A**pi / **W**orker; the JSON output has `submitters`, `controllers`, `dbs`, `apis` and `workers`. SLURM shows `-` when sind does not know the version, as for unmanaged clusters; `sind get cluster` does the same.

```
$ sind get nodes dev
CONTAINER            ROLE        FQDN                      IP          STATUS
sind-dev-controller  controller  controller.dev.sind.sind  172.19.0.2  running
sind-dev-db          db          db.dev.sind.sind          172.19.0.5  running
sind-dev-worker-0    worker      worker-0.dev.sind.sind    172.19.0.3  running
sind-dev-worker-1    worker      worker-1.dev.sind.sind    172.19.0.4  running
```

Without a cluster argument, `sind get nodes` lists every node in the realm and adds a `CLUSTER` column. Rows are sorted by `(cluster, role, natural-name)` so `worker-2` precedes `worker-10`:

```
$ sind get nodes
CONTAINER                CLUSTER  ROLE        FQDN                          IP          STATUS
sind-default-controller  default  controller  controller.default.sind.sind  172.19.0.2  running
sind-default-worker-0    default  worker      worker-0.default.sind.sind    172.19.0.3  running
sind-dev-controller      dev      controller  controller.dev.sind.sind      172.20.0.2  running
sind-dev-worker-2        dev      worker      worker-2.dev.sind.sind        172.20.0.3  running
sind-dev-worker-10       dev      worker      worker-10.dev.sind.sind       172.20.0.4  running
```

### Cluster Diagnostics

`sind get cluster [NAME]` displays detailed health information for a cluster:

```
$ sind get cluster dev
CLUSTER  SLURM    STATUS (R/S/P/T)
dev      25.11.8  running (4/0/0/4)

NETWORKS
NAME          DRIVER  SUBNET         GATEWAY     STATUS
sind-mesh     bridge  172.18.0.0/16  172.18.0.1  ✓
sind-dev-net  bridge  172.19.0.0/16  172.19.0.1  ✓

MESH SERVICES
NAME  CONTAINER  STATUS
dns   sind-dns   ✓
ssh   sind-ssh   ✓

MOUNTS
MOUNT       SOURCE              TYPE      STATUS
/etc/slurm  sind-dev-config     volume    ✓
/etc/munge  sind-dev-munge      volume    ✓
/data       /home/user/project  hostPath  ✓

NODES
NAME            ROLE        IP          STATUS   SERVICES
controller.dev  controller  172.19.0.2  running  munge ✓ slurmctld ✓ sshd ✓
db.dev          db          172.19.0.5  running  mariadb ✓ munge ✓ slurmdbd ✓ sshd ✓
worker-0.dev    worker      172.19.0.3  running  munge ✓ slurmd ✓ sshd ✓
worker-1.dev    worker      172.19.0.4  running  munge ✓ slurmd ✗ sshd ✓
```

`MESH SERVICES` shows ✓ for the realm's DNS container and SSH relay while they run (`dns_ok`, `ssh_ok` in the JSON output); after a host reboot they exist but are stopped (see Host Reboot and Docker Daemon Restart). When the realm has a monitoring stack, `MESH SERVICES` adds a row for each of its four containers, with ✓ while the service is ready (its readiness endpoint, see Monitoring), and the JSON output adds `grafana_ok`, `prometheus_ok`, `loki_ok` and `alloy_ok` next to `dns_ok` and `ssh_ok`; without a stack the rows and the fields are left out. The JSON output has `"monitoring": true|false` at the top: whether a node of the cluster mounts the realm's journal volume.

```
MESH SERVICES
NAME        CONTAINER        STATUS
dns         sind-dns         ✓
ssh         sind-ssh         ✓
grafana     sind-grafana     ✓
prometheus  sind-prometheus  ✓
loki        sind-loki        ✓
alloy       sind-alloy       ✓

MOUNTS
MOUNT             SOURCE                   TYPE      STATUS
/etc/slurm        sind-dev-config          volume    ✓
/etc/munge        sind-dev-munge           volume    ✓
/data             /home/user/project       hostPath  ✓
/var/log/journal  sind-journal:dev/<node>  volume    ✓
```

A monitored cluster lists the journal mount under `MOUNTS`: the realm's journal volume, and the cluster's directory in it, where each node mounts its own (`subpath` in the JSON output, `dev`). `docker inspect`'s top-level `Mounts` hide a volume's subpath, so sind reads it from the nodes' `HostConfig.Mounts`.

The data row (`/data`, or the configured mount point) shows what Docker mounts there on the nodes, as `docker inspect` reports it: the bind-mounted host directory or the volume. Clusters with `users` add `/home`, the home volume `<realm>-<cluster>-home`, to `MOUNTS`. With `storage.cvmfs`, `MOUNTS` adds `/cvmfs`: source `cvmfs` of type `volume` for the volume plugin, whose status tells whether Docker finds the plugin volume, or source `/cvmfs` of type `hostPath`.

`SERVICES` lists munge and sshd for every node, plus slurmctld, slurmd, mariadb and slurmdbd on a db node, or slurmrestd on an api node, where sind manages Slurm. Unmanaged nodes (unmanaged workers, db and api nodes, and every node of an unmanaged cluster) list only munge and sshd. With identity `clientIds` no node lists munge, and the submitter lists sackd. The JSON output marks each node with `"managed": true|false`; `sind get node -o json` has the same field.

Clusters with a backup controller add an `HA` column to the `NODES` table: each controller's position (`primary` or `backup`), with `*` on the controller in control. Other nodes show `-`. The JSON output adds `"ha": {"position": "backup", "in_control": true}` to each controller's `health`; `sind get node -o json` adds the same `ha` object. Single-controller clusters show neither, and neither do unmanaged clusters: sind cannot tell which of their controllers is in control.

```
NODES
NAME                   ROLE        HA       IP          STATUS   SERVICES
controller.dev         controller  primary  172.19.0.2  running  munge ✓ slurmctld ✗ sshd ✓
controller-backup.dev  controller  backup*  172.19.0.3  running  munge ✓ slurmctld ✓ sshd ✓
worker-0.dev           worker      -        172.19.0.4  running  munge ✓ slurmd ✓ sshd ✓
```

The `STATUS` of a node, in `sind get cluster`, `sind get node` and `sind get nodes`, and its JSON `status`, map its container's state (`containerStateToState`): `running`, `stopped` (exited, dead or created), `paused`, or `unknown` for another state, such as `restarting`, which the header counts under `S`. The cluster's state in the header and in `sind get clusters` uses the same words, plus `mixed` and `empty`.

`sind get node NODE[.CLUSTER]` shows detailed health for a single node. NODE uses the format `shortName` or `shortName.cluster` (defaults to cluster "default"). Passing a full DNS FQDN (`NODE.CLUSTER.REALM.sind`) is rejected — use the bare short name or the `NODE.CLUSTER` form:

```
$ sind get node controller.dev
CONTAINER            ROLE        FQDN                      IP          STATUS
sind-dev-controller  controller  controller.dev.sind.sind  172.19.0.2  running

SERVICES
NAME       STATUS
munge      ✓
slurmctld  ✓
sshd       ✓
```

A controller of a backup pair gets an `HA` column between `ROLE` and `FQDN` (`primary` or `backup`, with `*` on the controller in control), as in `sind get cluster`.

### Monitoring Stack

`sind get monitoring` shows the realm's monitoring stack (see Monitoring): each service's container, address and state, and the clusters it collects.

```
$ sind get monitoring
SERVICE     CONTAINER        ADDRESS                           STATUS
grafana     sind-grafana     http://grafana.sind.sind:3000     running
prometheus  sind-prometheus  http://prometheus.sind.sind:9090  running
loki        sind-loki        http://loki.sind.sind:3100        running
alloy       sind-alloy       http://172.18.0.9:12345           running

CLUSTERS
NAME     NODES  SLURM
default  3      26.05.5
dev      5      26.05.5
```

`ADDRESS` is the service's URL by its mesh DNS name, and Alloy's by its address, as Alloy has no DNS name; `-` for a stopped Alloy. sind keeps no record of whether host DNS works, so the JSON output has both the URL and the address. `STATUS` uses words, as the CLI guidelines keep ✓/✗ to `get cluster`, `get node` and `doctor`: the container's state, `not ready` for a running container whose readiness check fails, `unchecked` for a running container while Grafana, which runs the checks, does not, and `missing` for a container that does not exist, also as the JSON `status`. `CLUSTERS` lists the clusters whose nodes mount the realm's journal volume, with the number of those nodes and the Slurm version of the controller. A realm without a stack fails with `no monitoring stack in realm "sind"` and exit status 1 (`cluster.ErrNoMonitoring`).

```json
{
  "realm": "sind",
  "services": [
    {"name": "grafana", "container": "sind-grafana", "image": "grafana/grafana:13.2.4",
     "ip": "172.18.255.253", "url": "http://grafana.sind.sind:3000", "status": "running", "ready": true, "checked": true}
  ],
  "clusters": [{"name": "default", "nodes": 3, "slurm_version": "26.05.5"}]
}
```

### Host Diagnostics

`sind doctor` validates host prerequisites for running sind:

```bash
sind doctor [-o json]                    # check Docker version, mode and host, cgroupv2, inotify, DNS policy
```

Checks the Docker Engine version (from `docker info`), that the daemon is rootful and has no `userns-remap` (`cluster.DaemonSupport`; skipped when Docker is not reachable), where the daemon runs (`doctor.LocateDaemon`; skipped when Docker is not reachable), that Docker runs containers on cgroup v2 (`docker info`'s `CgroupVersion`) and the Docker host mounts cgroup2 at `/sys/fs/cgroup` with `nsdelegate` (a hybrid host, whose cgroup2 is at `/sys/fs/cgroup/unified`, fails), that `fs.inotify.max_user_instances` is at least 1024 (advisory; left out when it cannot be read or the daemon runs elsewhere), and that polkit allows host DNS resolution via systemd-resolved (a `warning` without asking polkit when the daemon runs elsewhere: the mesh bridge is on the daemon's host). The results go to stdout, one `✓`, `✗` or `!` line per check (`!` marks a `warning`, an advisory check that did not pass), with the commands that fix a check that did not pass below it. Exits non-zero if any required prerequisite fails, in either output format; the error line names the failed checks as the output prints them (`checks failed: Docker Engine, cgroupv2`) and goes to stderr. When Docker is not reachable, the Docker Engine detail is `not reachable: ` and the first line of docker's error (`doctor.DockerUnreachable`), with a remediation for a missing `docker` CLI, a user outside the docker group and a daemon that is not running. Details are escaped in the human output like the final error line, since they can quote docker.

The `Docker host` check tells whether the daemon runs on the machine sind runs on. It takes the endpoint the docker CLI takes: `DOCKER_HOST`, else the context that `DOCKER_CONTEXT` names, else the current context of the CLI's configuration (`docker context use`), else the default socket; without `DOCKER_HOST`, `docker context inspect` resolves the context, without contacting the daemon (`docker.Client.Endpoint`). The daemon runs elsewhere when that endpoint is not a `unix://` socket (`tcp://`, `ssh://`), when `docker info` reports Docker Desktop (`OperatingSystem` `Docker Desktop` or `Name` `docker-desktop`), whose daemon runs in a VM, or when `docker info`'s `KernelVersion` is not this machine's `/proc/sys/kernel/osrelease`. A container shares its host's kernel, so a CI job in a container that reaches the host's daemon through a mounted socket counts as local, and a socket forwarded from a host with another kernel does not. The check is `ok`, with the endpoint, for a local daemon, and a `warning` that says why, with the way back to a local daemon, otherwise.

A daemon elsewhere is a warning, not a failure. The detection is a heuristic from the client's side: a unix socket can be forwarded from a host that runs the same kernel release, and a `tcp://` endpoint can be this machine's own daemon, so a failure would block setups that work. And the required checks hold for a daemon elsewhere too, as they ask the daemon or read its kernel; what does not hold is what sind does on this machine: `--data` paths, which the daemon resolves on its own host, host DNS and the inotify limit. The trade-off is that doctor passes a setup that sind does not support; the warning says so, and `sind create cluster` and `sind create worker` check `nsdelegate` on the daemon's kernel either way.

For a local daemon, `nsdelegate` comes from this machine's `/proc/mounts`, which shows the same kernel, so doctor needs no image and starts no container. For a daemon elsewhere it is read in a throwaway container of the default node image (`doctor.ProbeCgroupInfo`, as `cluster.CheckNsdelegate` does), the only container doctor starts, and only when the daemon has that image: doctor pulls none. Without the image the check is a `warning`, `nsdelegate not checked`, with the `docker pull` that lets doctor check; a probe that fails fails the check. A daemon elsewhere that is not reachable leaves the check a `warning`, next to the failed Docker Engine check.

`-o json` prints the checks as a JSON array in clusterctl's check model: each entry has `name` (`Docker Engine`, `Docker daemon`, `Docker host`, `cgroupv2`, `inotify`, `DNS policy`), `status` (`ok`, `failed`, or `warning` for the advisory Docker host, inotify and DNS policy checks, and for a cgroupv2 check that could not read `nsdelegate`, none of which fails doctor: sind-action runs it as a gate on GitHub's runners, whose daemon is local), `detail`, and, for a check that did not pass and has a fix, `remediation` with the commands.

### Node Access

```bash
sind ssh [SSH_OPTIONS] [USER@]NODE [-- COMMAND]  # SSH into a specific node (passthrough)
sind enter [CLUSTER] [--user USER]               # Interactive shell on submitter/controller
sind exec [CLUSTER] [--user USER] -- <cmd>       # Command on submitter/controller
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
| `--cap-add CAP` | the newest worker's, else none | Add Linux capability (repeatable; e.g. `SYS_ADMIN`); `--cap-add=` for none |
| `--cap-drop CAP` | the newest worker's, else none | Drop Linux capability (repeatable); `--cap-drop=` for none |
| `--device PATH` | the newest worker's, else none | Expose host device (repeatable; e.g. `/dev/fuse`); `--device=` for none |
| `--security-opt OPT` | the newest worker's, else none | Security option (repeatable); `--security-opt=` for none |

New workers take the shape of the cluster's newest worker, the one with the highest index among those managed like the new ones (or among all workers if none is): sind reads its image ID, CPU and memory limits, `/tmp` size, capabilities, devices and security options from `docker inspect`, so a bare `sind create worker` adds nodes like the ones the cluster has, whatever its `defaults` were. Two things are left out because sind decides them for each new worker: the security options every node gets, and the `SYS_NICE` capability where sind added it by itself, on a managed worker of a cluster whose `slurm.conf` enables `task/affinity` (unless the worker's `capDrop` lists `SYS_NICE` or `ALL`; see slurm.conf under Generated Configuration). A `SYS_NICE` from `capAdd` or `--cap-add` is inherited elsewhere, e.g. with the default `TaskPlugin=task/cgroup` or from an unmanaged worker; an unmanaged new worker modelled on a managed one keeps that worker's `SYS_NICE` either way, as sind reads `slurm.conf` for managed workers only. A seccomp profile is not inherited either, as docker reports its content rather than its file (pass it again with `--security-opt`); sind says so in a `Warning:` line. A cluster without workers gets the controller's image and 1 CPU, 512m and 256m. Each flag that is given replaces the inherited value; a repeatable flag replaces the whole inherited list, and given only empty (`--device=`, or `--device ""`) it replaces the list with none. The library tells the two apart by nil: in `cluster.WorkerAddOptions`, a nil `CapAdd`, `CapDrop`, `Devices` or `SecurityOpt` inherits the newest worker's list, and an empty one asks for none. What sind decides for each new worker itself stays either way: its security options, and `SYS_NICE` for `task/affinity`. The cluster-wide settings, the data and CVMFS mounts, the users and the identity mode, come from the controller's labels as `docker inspect` reports them, a map (`docker ps` joins all labels with commas, which cuts a path with a comma short); a `sind.data.hostpath` that is not an absolute path is refused rather than bind-mounted.

Without `--image`, the workers run the newest worker's (or controller's) image by its ID, not by the tag it was created from, which may have moved to another image since; `--pull` therefore needs `--image`. With `--image`, sind pulls the image once when the daemon does not have it, or with `--pull`, then runs `slurmctld -V` in it and refuses one whose Slurm version is not the cluster's (`sind.slurm.version`), as slurmd must not be newer than slurmctld; unmanaged workers and clusters without a recorded version skip this check.

Before it creates a worker container, sind checks that the Docker host mounts cgroup2 with `nsdelegate`, as `sind create cluster` does (`cluster.CheckNsdelegate`; see Container Startup): a host that has lost it since the cluster was created, as by a reboot after a temporary remount, fails here with the commands that enable it. The check runs the controller's image by its ID, which the daemon has, and runs while sind reads the cluster's configuration and checks an explicit `--image`.

`--count` must be at least 1 and `--cpus` must not be negative; `--image`, `--memory` and `--tmp-size` must not be given empty, which would quietly stand for the default. These, `--pull` without `--image`, and `--cap-add`, `--cap-drop`, `--device`, `--security-opt`, `--memory` and `--tmp-size`, which are checked like the config's `capAdd`, `capDrop`, `devices`, `securityOpt`, `memory` and `tmpSize`, are usage errors that sind reports before it takes the realm lock or creates any container. Once it has found the cluster's controller, and before it starts a stopped mesh or creates a worker, sind refuses workers that would not fit on the realm's mesh or the cluster network (see Limits under Networking), with exit status 1.

With `-v`, `sind create cluster` and `sind create worker` log an info-level `extra privileges` notice for each node that gets extra capabilities, devices or security options, or bind-mounts host directories (the data directory, the host's `/cvmfs`). It is not a warning: a mutation writes nothing on success but, on a terminal, what its progress display leaves.

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

Steps 3 to 5 run while the new workers are registered with the mesh DNS and known_hosts. If any step fails or the command is interrupted after the first container exists, sind removes the new containers and their mesh entries again and writes back the `sind-nodes.conf` it read in step 1 (and reconfigures slurmctld), so a retry starts from a clean state. A rollback step that fails, such as that write or the reconfigure, is added to the error after the original one.

Managed nodes require the sind-generated Slurm configuration (see Generated Configuration). If `sind-nodes.conf` is missing (e.g., user replaced the config), the command fails with an error. Use `--unmanaged` to add nodes without modifying Slurm configuration. On an unmanaged cluster (see Unmanaged Cluster) every new worker is unmanaged, with or without `--unmanaged`. A controller that is stopped or frozen fails the command with an error that says so: start it with `sind power on` or `sind power unfreeze` first.

**delete worker** deletes containers entirely. Works with both managed and unmanaged nodes. For managed nodes, sind removes them from `sind-nodes.conf` and reconfigures slurmctld before deleting the container, while it removes their mesh DNS and known_hosts entries. sind needs a running controller for this: with the controller stopped or frozen, deleting a managed worker fails and removes nothing, as the node would otherwise stay in the Slurm configuration without a container. Unmanaged workers need no controller. When none of the managed workers is in `sind-nodes.conf`, sind leaves the file alone and does not reconfigure. When the update fails, e.g. as `scontrol reconfigure` does, sind writes the file back as it was and keeps the containers, so that running the command again reconfigures slurmctld. On an unmanaged cluster, or one without `sind-nodes.conf`, it never edits the Slurm configuration or runs `scontrol`.

With workers of several clusters, `delete worker` handles the clusters one after another, as the power commands do, and a cluster that fails does not stop the others: the command returns every failure, joined, and exits non-zero. It then updates the exported SSH configuration (see User SSH Client Integration) for the workers that are gone, also when a cluster failed.

### Power Control

```bash
sind power shutdown NODES               # graceful shutdown (alias: off)
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

A docker verb in place of the power command, such as `sind power stop`, is an unknown command whose error suggests the power command that runs it (`shutdown`); `restart` suggests `reboot`, and `resume` `unfreeze`. Shell completion offers `off` once a prefix of it is typed.

`docker stop` sends the image's `STOPSIGNAL`. The sind-node images set `SIGRTMIN+3`, which makes systemd (PID 1) shut the node down cleanly. A custom image without it gets SIGTERM, which systemd does not treat as a shutdown request, so the node is killed after Docker's 10-second timeout (see Custom Images).

A power command runs its Docker calls for the nodes of a cluster in parallel (at most 8 at a time), and handles the clusters of its nodes one after another; `reboot` and `cycle` take every node of a cluster down before they start any of them. A failing node does not stop the others: the command returns every failure, joined, and exits non-zero.

The Docker calls of a cluster's nodes run on go-clikit's `fanout.Map` (`forEachContainer`), which a display shows as a step named by what the calls do, `stopping` (shutdown, reboot), `killing` (cut, cycle), `starting` (on, reboot, cycle), `pausing` (freeze) or `unpausing` (unfreeze), with a target for each node, named as a node argument names it (`worker-0` in the default cluster, `worker-0.dev` in another, so that a command that spans clusters tells their nodes apart, in its rows as in its summary), and with its role. Its `Summarize` (`joinFailures`) words each failure as before, `<verb> <container>: <error>`, joined in the order of the nodes, so that the error text and `errors.Is` stay as they were. Once the command is interrupted it starts no further node, and those left out fail with `context canceled`. A panic in the work for one node fails that node, `sind panicked; this is a bug, please report it: ...`, with a line that names the node and the stack on stderr, above a display (the Bus's panic log, or the writer `cluster.WithPanicLog` puts in the context, the command's stderr), and the other nodes finish: the command exits 1, where the panic used to end sind with Go's report and status 2.

`on`, `reboot` and `cycle` start the realm's mesh DNS and SSH relay first if they are stopped, start the nodes, and then point the started nodes' mesh DNS records at their current cluster network addresses: Docker releases a container's address when it stops and can give it another one on start, for example after `sind create worker` took the address of a node that was powered off. They warn about nodes created with a mesh DNS address the DNS container no longer has. Since they rewrite the realm's Corefile, they take the realm lock.

Freeze/unfreeze uses Docker's cgroup freezer to suspend all processes. The container remains "running" but is completely unresponsive, simulating a hung or unreachable node.

slurmctld learns that a frozen, cut or shut down worker is gone only from failed pings, as slurmd does not sign off, and marks it `DOWN` after `SlurmdTimeout`. sind keeps Slurm's default of 300 seconds: unlike `SlurmctldTimeout`, which sind lowers only for a backup controller pair, no feature asks for a shorter one, and a short timeout marks slow but healthy nodes `DOWN` on a loaded host. Failure tests set it in `slurm.main`, e.g. `SlurmdTimeout=30`.

### Logs

```bash
sind logs NODE [--follow|-f] [--tail|-n N]          # container logs (stdout/stderr)
sind logs NODE SERVICE [--follow|-f] [--tail|-n N]  # journalctl for specific service
```

Examples:
```bash
sind logs controller --follow          # tail container logs
sind logs controller slurmctld         # slurmctld journal logs
sind logs worker-0 slurmd --follow    # follow slurmd logs
sind logs db slurmdbd                  # slurmdbd journal logs on the db node
sind logs db mariadb                   # MariaDB's own messages: the image logs them to the journal
sind logs api slurmrestd               # slurmrestd journal logs on the api node
```

### Web UI

```bash
sind ui [--port PORT] [--no-open] [--monitoring-interval DURATION] [--demo]   # serve the web UI on 127.0.0.1 and open it in a browser
```

`sind ui` serves the web UI of the realm on `127.0.0.1` and prints its URL, with a token that is new each run. `--port` names the port (default: a free one; a port in use is an error that names the flag), and `--no-open` prints the URL without starting a browser. The URL is the only way in: a second `sind ui` of the same user prints the URL of the running one and opens it, then exits 0. It runs until Ctrl-C, which stops the server and interrupts the commands it started (see Web UI). To use it from another machine, forward the port with `ssh -L` and pass `--no-open`. `--monitoring-interval` is how often it reads the monitoring stacks (default `30s`, from `5s` to `1h`; another value is a usage error); the Monitoring page changes it while sind ui runs. `--demo` serves made-up clusters and plays the commands of the dialogs in memory: it uses no Docker, starts no child process, writes nothing and records no running instance, so it can run next to a real `sind ui`. The hidden `--still` (only with `--demo`) stops the demo's clock at a fixed time and plays nothing, so that its pages are the same on every run; `tools/screenshots` uses it.

### Utilities

```bash
sind version [--json]                  # print version information (sind --version prints the same line)
sind doctor [-o json]                  # check host prerequisites
sind get realms                        # list active realms
sind get auth-key [CLUSTER] [--type T] # output a Slurm authentication key (base64)
sind get ssh-config                    # show SSH config path for Include
sind get mesh                          # show mesh infrastructure info
sind get coredns-config                # output a CoreDNS server block for the realm's zone
sind get dns                           # list mesh DNS records
sind get ssh-private-key               # output SSH private key
sind get ssh-public-key                # output SSH public key
sind get ssh-known-hosts               # output SSH known_hosts
sind help [COMMAND | TOPIC]            # help for a command, or for a word, how-to or action of sind ui
sind help topics                       # list the topics of sind help
```

`sind help TOPIC` prints the text of a topic from `internal/help` (the YAML that sind ui shows too): a word (`sind help realm`), a how-to (`sind help first-cluster`) or an action of sind ui (`sind help create-cluster`). `sind help topics` lists them. A name that is a command prints that command's help, and an unknown name is a usage error, as in the rule above. A topic links to its docs page; in sind ui the link carries `?via=ui`, so that the page shows its steps for the web UI (see `docs/content/contributing/conventions.md`).

`sind version` prints the version and commit; `--json` adds the Go version and platform (`version`, `commit`, `goVersion`, `platform`). For release builds the output is `sind <version> (<commit>)`. For dev builds `git describe --tags --always --dirty` is used as the version, embedding tag distance and commit hash directly: `sind 0.5.0-3-gabc1234-dirty`. A binary built without a version, such as one from `go install github.com/GSI-HPC/sind/cmd/sind@vX.Y.Z` (releases after v0.9.0), reports the module version the Go toolchain recorded (`sind X.Y.Z`, read with `cobratree.ReadBuildInfo`); a plain `go build` from a checkout reports `sind dev` with its commit. The `--json` flag outputs all fields as JSON.

`sind get auth-key` outputs a key that authenticates the cluster's Slurm traffic, encoded as base64, suitable for injection into external management tooling. `--type` names the Slurm authentication plugin whose key it prints: `munge` (the munge key), `slurm` (`slurm.key`, identity `clientIds`) or `jwt` (`jwt_hs256.key`, which signs the REST API tokens of a cluster with a managed api node; see API Node). Without `--type` it prints the key of the cluster's `AuthType`: `slurm.key` with identity `clientIds`, the munge key otherwise. A type the cluster does not use fails with an error that says why, e.g. `cluster "dev" has no JWT key: sind sets up JWT only for a cluster with a managed api node`; any other value is a usage error. `-o json` returns `{"type": "munge"|"slurm"|"jwt", "key": "<base64>"}`. It replaces `sind get munge-key`, which v0.11.0 removed: `sind get auth-key --type munge` prints the same key. The library takes the type as `cluster.GetAuthKey`'s last argument, empty for the `AuthType`'s key.

`sind get ssh-config` outputs the path to the SSH config file for the current realm. Add it as an `Include` in `~/.ssh/config` to enable direct SSH access to nodes. The path is printed before the file exists (the first `sind create cluster` of the realm writes it), with a `Note:` on stderr; `-o json` is `{"path": "...", "exists": true|false}`.

`sind get mesh` shows mesh infrastructure info: network name, DNS container/IP/zone/image, SSH container/volume/image. The DNS IP is the DNS container's address while it runs, which stays the same for the life of a mesh that pins it (see Host Reboot and Docker Daemon Restart). The images are the ones the mesh containers run, which can be older than the ones a new mesh gets. Useful for external consumers that need to connect to sind networks.

`sind get coredns-config` outputs a CoreDNS server block that forwards the realm's zone (`<realm>.sind`) to the mesh DNS server, for a DNS server outside sind such as a kind cluster's CoreDNS:

```
sind.sind:53 {
    errors
    cache 30
    forward . 172.18.255.254
}
```

`-o json` returns `{"zone": ..., "server": ..., "corefile": ...}`. It fails while the DNS container is stopped, which then has no address.

`sind get ssh-private-key`, `sind get ssh-public-key`, and `sind get ssh-known-hosts` dump SSH credentials to stdout. This replaces the need to extract files from Docker volumes.

### MCP Server

```bash
sind mcp start                          # MCP server on stdio
sind mcp stream [--host H] [--port P]   # MCP server over HTTP (default 127.0.0.1:8080), bearer token required
sind mcp tools                          # export the tool definitions to mcp-tools.json
sind mcp {claude,vscode,cursor} {enable,disable,list}  # register sind with an editor
```

The MCP server is built with ophis and configured in `cmd/sind/mcp.go`. Each tool runs the sind binary with the command's arguments and flags, in a process group of its own (`mcpExecute`, which a middleware puts in place of ophis's run function, and `cmdexec.WithProcessGroup`).

The editor commands are sind's own (`cmd/sind/mcpeditor.go`, `internal/mcpconfig`), not ophis's, which rewrite the whole file from the fields they know (dropping the editor's other settings and the other servers' fields) and fail on a JSONC file:

- They edit the editor's configuration in place, as JSON with comments and trailing commas (`github.com/tailscale/hujson`): only the entry of sind's server changes, and the editor's other settings, the other servers, comments and indentation stay. The servers are the members of `mcpServers` (Claude Desktop, Cursor) or `servers` (VS Code). A file that does not exist is created, readable by its owner alone, as an entry's `env` can hold secrets; one that exists keeps its permissions, and a symbolic link is followed to the file it points to. A file that names a server twice, or has the server list twice, is an error that names it, and is left alone: an editor reads the last of them, while sind would change the first. The lines sind adds have the file's line breaks, CRLF or LF.
- `enable` adds or replaces the entry `--server-name` (default `sind`): `command` is the absolute path of the running sind binary (`os.Executable`), `args` is `["mcp", "start"]` plus `--log-level`, `env` is `--env`, and VS Code and Cursor get `"type": "stdio"`. A replaced entry keeps its `env`, with each variable `--env` or the realm names set to the new value, so a re-run after sind moved keeps the realm the server serves; it also keeps the members sind does not write (`disabled`, `autoApprove`, `cwd` …) in their place. `type`, `command`, `args` and `url` are sind's: they are written from the flags alone. An editor starts the server with its own environment, so the realm in effect, of the root `--realm` or else an exported `SIND_REALM`, becomes `SIND_REALM` in `env`, as every other command takes the realm; an `--env SIND_REALM` names the realm itself, and one that names another realm than `--realm` is a usage error. The values `enable` writes are checked as the commands that read them check them: `--env SIND_REALM` is a realm name, as `--realm` is, and `--log-level` one that `mcp start` takes; an empty `--server-name` (`enable`, `disable`) or `--config-path` (all three) is a usage error too, since an unset variable in `--config-path "$FILE"` would otherwise edit the editor's own file. `disable` removes the entry and fails (exit 1) when there is none. `list` prints a `NAME`/`COMMAND` table (the command and its arguments, or the URL of an HTTP server), without the servers' environment.
- Before it changes a file that exists, `enable` or `disable` copies it to `<file>.<YYYYMMDD-HHMMSS>.bak` next to it (`-1`, `-2` … appended if that exists), so no backup is ever overwritten; `-v` logs its path. An unchanged file is not written and gets no backup. The new file replaces the old one through a temporary file and a rename.
- The default files are `claude_desktop_config.json` in `Claude/` of the user configuration directory (`os.UserConfigDir`: `$XDG_CONFIG_HOME` or `~/.config` on Linux), `Code/User/mcp.json` there for VS Code, and `~/.cursor/mcp.json`; `--workspace` (VS Code, Cursor) selects `.vscode/mcp.json` or `.cursor/mcp.json` in the working directory, and `--config-path` any file.

- The server reports itself as `sind` with the version `sind version` prints.
- `--realm` on `sind mcp start` or `sind mcp stream` applies to every tool call: the server sets `SIND_REALM` for the sind processes it runs, which take the call's flags and the server's environment; on `sind mcp <editor> enable` it is written to the entry's `env`, as is an exported `SIND_REALM`. The `mcp` commands take no arguments: an extra one is a usage error.
- ophis passes no flag for an empty array, so a middleware turns it into `[""]`, which ophis passes as the flag given empty (`--device ""`): for `sind_create_worker`, `"device": []` gives the new workers no devices, as `--device=` does, while leaving the flag out inherits the newest worker's.

- `sind mcp stream` listens on `127.0.0.1` by default; `--host 0.0.0.0` opts in to all interfaces. An empty `--host`, which `net.Listen` reads as all interfaces, is a usage error, so an unset variable in `--host "$HOST"` cannot open the stream to the network; so is a `--port` outside 0 to 65535. On a wildcard address the listen line gives the `localhost` URL a client uses and adds `(all interfaces)`. `--log-level` of `sind mcp start` and `sind mcp stream` takes `debug`, `info`, `warn` or `error` (any case, as ophis reads it); another value is a usage error, where ophis would use `info`. Every request needs `Authorization: Bearer <token>` (go-sdk's `auth.RequireBearerToken`, constant-time comparison), else `401`: loopback keeps out other hosts but not the other users of this one, and every tool runs sind with the Docker access of the user who started the stream. The token is `SIND_MCP_TOKEN` (printable ASCII without spaces, at least 16 characters) or, when that is unset, a new `crypto/rand` token written at every start to `$XDG_STATE_HOME/sind/mcp-token` (`~/.local/state/sind/mcp-token`), mode `0600`, through a temporary file and a rename. The stream prints the listen URL, where the token is (never the token) and a client configuration to stderr. ophis's own stream has no hook for authentication, so `runMCPStream` (`cmd/sind/mcpstream.go`) replaces its run function and keeps its flags: it runs `sind mcp start`'s ophis server on an in-memory transport, lists its tools through an MCP client session, and serves them from a second MCP server whose handlers forward each call through that session, so ophis still builds every tool and runs every call with sind's selectors and middlewares. Stopped by SIGINT or SIGTERM, it shuts down and exits 0. `sind mcp start` (stdio) is unchanged and needs no token.
- Every runnable leaf command is a tool, except `enter` and `ssh` (interactive) and `get ssh-private-key` and `get auth-key` (secrets). Command groups, the root among them, are not tools: they only print help. Neither are `help`, `completion` and the `mcp` commands, which ophis leaves out.
- Every tool carries MCP hints from `mcpEffects`: read-only (`readOnlyHint`: `get`, `logs`, `doctor`, `version`), additive (`destructiveHint: false`: `power on`, `power unfreeze`, `connect container`) or destructive (`destructiveHint: true`: `create`, `delete`, `disconnect container`, `exec`, and the other `power` actions). `create cluster` and `create worker` count as destructive because their flags choose the image a node runs as root, capabilities, devices, security options, a config file and a host directory mounted read-write: one call can run code with host-root power. A new command has to be classified there; a unit test fails otherwise.
- Tools for commands with `-o` (every `get` subcommand and `doctor`) always run with `-o json`, set by an ophis middleware; `-o` is not in their input schema.
- Tool input schemas leave out `-v` (a count flag, which ophis would pass as `--verbose 2`, a stray positional argument), `logs --follow` (it never ends, and a tool returns its output only when the command exits), and `--progress` and `--progress-log` (a tool's output is no terminal, and holds no progress and no events; the events of a call that reports progress go to the activity log). ophis takes a flag's schema `default` from its default value; the flags of `mcpNoDefault`, `logs --tail`, have none, since leaving them out means something else than that value (`--tail 0` shows no line, no `--tail` every line), which a client that fills in schema defaults would send. The flags of `mcpFlagUsage` get a description without the forms a tool call cannot use: a tool's sind process reads no stdin, so `create cluster`'s `config` describes a file path, without `-` for stdin. `sind mcp start` and `sind mcp tools` change these flags in their own process before ophis builds the tools (`withMCPFlagSchemas`), so the commands' help keeps them. ophis copies a command's usage line into the description of `args`, so a usage line names arguments only (`create cluster [NAME]`), never flags.
- `sind mcp start` and `sind mcp stream` set `SIND_PROGRESS=none` and unset `SIND_PROGRESS_LOG` for the sind processes they run (`asMCPServer`), which inherit the server's environment: a `SIND_PROGRESS=plain` set for the user's shell would add its lines to every tool's output, a `SIND_PROGRESS_LOG` of `/dev/stderr` its events, and one that cannot be opened a note. With the variable unset, a tool call writes the activity log like any command. They also set `SIND_LOG_TIMESTAMPS=false`, which leaves the time stamp off log lines: a tool's stderr reads `ERRO <message>`, as the time a line was written means nothing to the client.
- A tool call whose positional `args` hold a flag (an argument starting with `-`) is refused, because ophis appends the arguments after the flags and they would otherwise bring back `-v`, `--follow` or a `-o` that overrides `-o json`. For `exec` only the arguments before its `--` are checked. The refusal reads as a usage error of the command: `exitCode` 2, `isError: true` and the `ERRO` line in `stderr`. A flag that is not in the tool's input schema fails go-sdk's validation of the call's input before sind sees the call, with an error result that has no `exitCode`.
- A call that comes with a progress token gets the progress of its process as progress notifications (`withToolProgress`, `cmd/sind/mcpprogress.go`). The server hands the process a `TRACEPARENT` of its own and reads the default activity log back every 100 ms with `eventlog.Tail`, leaving out what the log held before and every line of another trace; `eventlog.Forward` gives the events of the process to an `mcpprogress.Notifier`, which sends at most one notification every 500 ms, and at once as a step ends. The Notifier is closed, and so sends what is left, before the call answers. `sind mcp stream` forwards the token to the MCP server it runs, under a token of its own, and sends what that server reports back to the stream's client under the client's token (`toolProgressRelay`). A host with no state directory sends none.
- A cancelled call (`notifications/cancelled`) or a client that disconnects ends the call's context, and the server then kills the tool's process group with SIGKILL: the sind process and the `docker exec` or `docker logs` it runs, so the call answers at once. ophis's own run function kills only the sind process, and the docker command would go on, holding the call's output open until it ended. The kill would skip the rollback of a failed `create` and leave partial resources and the realm lock behind, so the tools that change state (`create`, `delete`, `connect`, `disconnect` and `power`) run to completion on a context that ignores the cancellation (`mcpRunsToCompletion`): a cancelled create finishes, or rolls back, on its own, bounded by `--wait`, and the server sends its result when it ends. Read-only tools and `exec` stay cancellable. The command `exec` started in the node runs on until it ends: Docker has no way to signal a process that `docker exec` started, and killing the docker CLI does not stop it. `sind mcp stream` waits at most `mcpShutdownTimeout` (5 s) for the MCP server to stop, so a call still running does not hold up its shutdown; the sind process it started goes on to finish or roll back.
- A tool's result holds the command's stdout, stderr and exit status (`exitCode`): a usage error reads `2`, and `sind_exec` returns the status of the command it ran. A call whose command exits non-zero is also marked as an error (`isError: true`).

## Web UI

`sind ui` (`cmd/sind/ui.go` for the flags, `internal/ui` for the logic) is a local web UI for sind. Its pages show the realms, clusters and nodes, the networks and the runs of the realm, and it offers the actions that change something (new cluster, add workers, delete, power, drain and resume, connect and disconnect, delete realm). It has no Slurm page and no terminal: the Connect tab of a cluster shows the commands to run in a terminal.

### Architecture

- Pages are server-rendered with `html/template`. [htmx](https://htmx.org/) and Server-Sent Events update them live, one stream per open tab: `localweb/sse` of go-clikit sends every fragment when the stream opens, then each fragment that depends on a topic that changed and whose HTML differs from what the stream sent last; a tab that falls behind loses no topic. The JavaScript and CSS are embedded in the binary, with no build step, and nothing loads from the network. htmx, its SSE extension and idiomorph are the files of go-clikit's `localweb/htmx`, served under `/htmx/`; a newer htmx comes with a newer go-clikit.
- The styles are `web/static/app.css`, with the tokens and the blocks that every page shares, and a file per group of pages in `web/static/css/`. Every colour is a token, with a light and a dark value; text has a contrast of 4.5:1 or more on its background in both themes, and `--deco` is for marks that are not text. The icons of the rail are the symbols of `web/static/icons.svg`, one per entry, named by the key of the entry. Every font size is one of the seven of the type scale, `--t-xs` to `--t-2xl` (11, 12, 13, 14, 16, 20 and 26 px). Monospace is for what the user types or copies: commands, names of nodes and clusters, addresses, images and IDs; titles, labels and sentences use Inter.
- The shell is `layout.tmpl`, `rail.tmpl` and `app.js`. A skip link is the first stop of Tab. The rail is labelled lists, one per group, and no headings. On a phone, and in a window less than 500 px high, the rail is a bar at the bottom of the screen with the entries that `NavItem.Main` marks (Overview, Clusters, Nodes, Doctor) and More, which opens a sheet with the others and stays open across live updates; More shows the most urgent count of the sheet, a failure before work that goes on. On a touch screen (`pointer: coarse`) every control is at least 44 px high. The tab strip of a cluster stays at the top of a phone's screen, and a strip that scrolls sideways shows its current tab. The theme button is a toggle of the dark theme (`aria-pressed`). A cluster page names its tab in its title.
- When the event stream fails for more than 1.5 s, the banner `#conn` says since when the page is not live, the page dims, and its requests and forms wait. On each failed reconnect it asks for the page with `HEAD`: a 403 means that sind ui started again, so the banner offers to sign in again. `#say`, the polite live region of the page, tells screen readers when the page is live again.
- A live update notes the focus and the sentences of the elements with `data-say` (but not `data-say-state`, which are `run.js`'s) before its morph. A changed sentence goes to `#say`, and a focused control that the morph removed or turned into another one gives the focus to the page's `h1`, so that a key press cannot hit a control the user never chose. A control is known by its tag, `id`, `name`, `href`, `type`, `value`, form and htmx targets and the names of its `data-*` attributes, and only by its text when it has none of these, so that a link whose count or state changes keeps the focus. The morph keeps the `tabindex` of the focused element.
- Reads run in the process. Collectors read Docker labels and `docker` output through `cmdexec.Executor` into immutable snapshots, and a page renders from the snapshot. A stream opens with every fragment of its page, as the page may predate a change, and then sends a fragment again only when its HTML changes, 30 ms after the first change it gathers. The page morphs a fragment into its element (idiomorph's htmx extension) instead of replacing it, so the elements that stay keep their animations, focus and the open state of their details, and a bar that changes eases to its new size. A stream that falls behind the store's notices gets one notice for all topics in their place and renders every fragment.
- Changes run in a child process. An action builds an argv from the form fields, validates it against the current snapshot and shows it in its dialog as the exact command. Running the action rebuilds the argv and refuses to start when it differs from the one the dialog showed. The child is the sind binary, in a process group of its own, so it takes the realm lock, rolls back on failure and stops on SIGINT as it does in a terminal. Stop sends SIGINT to that group.
- Progress comes from the activity log, which `internal/ui/activity` reads with go-clikit's `eventlog` package (`Tail` and `Reducer`) and converts to `store.Run` values. The child writes the go-clikit event log (`--progress-log`, `SIND_PROGRESS_LOG`, or the default `activity.jsonl` in the state directory). sind ui tails the log, every 50 ms while a run goes on and every 250 ms otherwise, and reduces its events to runs, steps, targets and the wait limit, and draws them on the run page and the task cards. sind writes each line of the log within 25 ms, and the advances of a step at most four times a second. A command started from a terminal shows the same way when it writes that log, which is the default; it is marked as started from a terminal, and sind ui cannot stop it. sind ui hands its children `TRACESTATE=sind=ui` with their `TRACEPARENT`, and the log keeps it on the run's first line, so a run of a sind ui says "from sind ui" also when its task is gone, as after a restart. A run that continues another caller's trace without that tracestate, such as a CI job's or an MCP client's, says "from another program". The page of a cluster has one banner for all the runs that work on it, with how many wait for the realm lock. sind ui follows the rotated copy `activity.jsonl.1` too, as a command that had the log open when another rotated it goes on writing there. A run whose end the log lacks, as its process was killed, ends with the task that started it, with the task's outcome, or, after an hour without an event, at its last event as "no end recorded".
- The task dock (`dock.go`) draws a card for each task and for each run without a task that goes on, and shows one of them: the commands that work first, oldest first, then those that wait for the realm lock, then those that ended, newest first; `run.js` shows all of them while the user has opened the dock, a choice that `localStorage` keeps. Above two or more cards a bar counts them by state, with Clear done (`POST /tasks/clear`, which removes every task that has ended). A card of a task that ended ok is left out 8 s after its end (`run.js` hides it at `data-hide-at` in between); one that failed or was stopped stays until it is cleared. The page of a run or a task leaves out its card, which the bar still counts. A card's title is the run's title with the flags of the task's command line but `--realm` and `--config`, its realm a chip, and its tip the whole command; the run page and the page of a task name the command the same way.
- Every page has one live region, `#say`. The cards of the dock, the run page and the page of a task mark each command with `data-say-key` (the task, or the run without one), its state and one sentence; `run.js` puts the sentence in the region once (through `app.js`, which tells the sentences of one moment together), when the state of a key changes: started, stopping, done, failed with the first error, or stopped. It keeps the states it has seen for the tab in `sessionStorage`, so the next page announces a command that ended meanwhile, and a page that opens announces only a command that started in the last 10 s. The clocks, the progress lines and the cards are outside any live region. The bar of a run's steps has the `progressbar` role, with the part done as its value and "2 of 4 steps done" as its text.
- The monitoring stacks are read only while a page is open, as every page shows their count in the rail: every `--monitoring-interval`, and at once when the first page opens. The picker on the Monitoring page offers 10s, 30s, 1m and 5m, plus the interval in use when it is none of them; a new interval resets the timer and reads at once. It lasts until sind ui stops.
- The realm a page shows is in its address, `?realm=`, on the pages that show one realm or all of them (Overview, Clusters, Nodes, Mesh and DNS, Monitoring, Topology). These pages are routes (`pgRealmRoute`), as a Page cannot see cookies: an explicit `?realm=` of a realm that exists, or an empty one for every realm, sets or removes the cookie `sind_realm` (`HttpOnly`, `SameSite=Strict`) when a page of sind ui sent the request (`Sec-Fetch-Site: same-origin`), not a typed address nor a link of another site, and a list page without `?realm=` redirects to the address of the remembered realm, so the address always says what the page shows; the Overview shows every realm without `?realm=`. A remembered realm that Docker no longer has is forgotten. Each of these pages answers a `?realm=` that Docker has no trace of with the Not found page and the realms of a close name (`pgRealmMiss`); while Docker is unknown or down, it shows that realm empty, and its live updates keep showing a realm whose last cluster goes. The realm picker in the top bar is a `GET` form to the page itself (`pgRealmPages`, keyed by the registered page names), or to the list of clusters or nodes from any other page; `pg.js` applies a pick of the mouse at once and a pick of the arrow keys only on Enter or blur, with a status line for screen readers; on Apple systems, where the arrow keys open the list, a pick from it applies at once. A filtered page says **Showing realm R · Show all**, and its title names the realm.
- The Logs tab of the cluster page keeps its node, source, unit, line count, filter and level in the address (`?tab=logs&node=worker-0&source=journal&unit=slurmd&lines=1000&q=job&level=warn`; an empty `unit=` is every unit). `View.LogsTab` reads them, so the page draws the choices of the address and the command of the terminal (`logsCommand`), and `logs.js` writes them back with `history.replaceState`. The cluster page's fragments take these keys too (`logsTabParams`), so a live update keeps them. The node page links into the tab (`pgLogsURL`): its Logs button to the journal of the node's Slurm service while the node runs, else to the container output; Issues to the service that is down; and each service row to its unit. The stream (`GET /logs/{realm}/{cluster}`) takes `lines=` from `logsLineCounts` (200, 1000, 5000; another value is a bad request), passes it to `docker logs --tail` or `journalctl -n`, and sends those first lines without the rate limit, waiting for the client, so a slow client still gets the whole tail; later lines keep the limit of 1,000 a second. A log with fewer lines ends the tail early: a pause of the input (`logsTailPause`, 500 ms; 5 s for the first line) marks the lines after it as live. The filter input takes at most 256 characters, and `logs.js` leaves out of the address a filter longer than 256 bytes (`maxParamLen`), which the server would refuse. The tab keeps the last 10,000 lines (`logsKeep`). The filter and errors-and-warnings-only (`level=warn`, the lines the tab colours) only hide lines in the page; Copy lines and Download (a `Blob`, `NODE[.CLUSTER]-SOURCE[-UNIT].log`) take the lines shown.
- An error is a page in the layout. The Not found page (404) names the address and offers the closest match first: a Load or route returns a `ui.Miss`, an `ErrNotFound` that says what is missing and lists the links, such as a cluster of the same name in another realm, a cluster, node or realm whose name is a few edits away (`missClosest`), the tab of the cluster page that an unknown `?tab=` most likely means, or the run of a task's ID. An address that no route serves offers what it names below `/clusters/` and `/nodes/`, such as the cluster of `/clusters/sind/dev/` or the node of `/clusters/sind/dev/nodes/controller` (`pgPathLinks`), then the rail page whose path is closest. `/clusters/NAME` opens the cluster when one realm has it, and offers the clusters of realm NAME when NAME is a realm. A bad request (400) that a browser navigated to (`GET`, `Accept: text/html`, no `HX-Request`) shows its message in the layout too; htmx and scripts get the message as plain text.
- An action whose form lacks a value that the command needs, such as an empty container name or no node picked, returns `action.Missing`: the preview shows it as a hint in place of the command and keeps the button off, as nothing is wrong yet; a run of that form shows it as an error.
- A dialog is modal. While it is open, the script of the dialogs (`act.js`) makes the rest of the page `inert`, but `#layer`, which also holds the help pane and the jump box, and `#act-notes`. It puts the focus on the first field, or on a touch screen (`pointer: coarse`) on the dialog itself, so that no keyboard covers it, and gives the focus back to the element that opened the dialog when it closes. Esc, Cancel and a click on the dim edge close it. Esc in the help pane over a dialog closes the pane and gives the focus back to the dialog. Closing a notice moves the focus that was on it into the open dialog, to the next notice, or back to the opener, without a scroll of the page. On a small screen the footer sticks to the bottom of the dialog, and the dialog's `scroll-padding` keeps a field with the focus above it. Enter in a field does not run the command of a dialog with more than one field; Ctrl+Enter, or Cmd+Enter on macOS, runs it in every dialog, or, while it cannot run, goes to the first problem, and the button shows that key where there is a keyboard. Once the user has changed a field, but the typed name of a delete, Esc, Cancel and the dim edge first have the footer ask to keep editing or to discard the changes, a question that names the new cluster, else the cluster or the realm of the dialog; and in a dialog that keeps no draft, the browser asks before the page goes (`beforeunload`), as the jump box, the g keys and links would lose the changes.
- A command that a dialog starts closes the dialog and leaves the page as it was: `/actions/ID/run` answers with a notice of the command and a link to its run, which htmx adds to `#act-notes` of the layout, and with the event `act-started` (`HX-Trigger-After-Settle`) that closes the dialog. New cluster follows its run instead (`HX-Redirect` to `/tasks/ID`, with `act-started` as `HX-Trigger`), as do a form posted without script, a dialog opened as a page of its own, and Delete cluster or Delete realm sent from a page of what it removes (`HX-Current-URL` is the cluster page or a node page of it), which would then only say that it is gone.
- The Power dialog groups its actions: Drain and Resume (Slurm), the container actions (on, shut down, reboot, freeze, unfreeze), and cycle and cut, which can lose data. Drain and Resume run `sind exec CLUSTER -- scontrol update NodeName=... State=DRAIN Reason=...` (or `State=RESUME`) for workers only, in the same child process as other actions; Drain needs a reason of one line and at most 200 characters, which `sinfo -R` shows. `exec` writes no event log, so such a task has no run page. Before an action that stops nodes, the dialog warns when they include the controller in control (a backup takes over, or no job starts), the standby controller of a pair (the cluster has no failover), the db node (`sacct` fails) or every running worker of a cluster. Its button names the action and the nodes (`Command.OK`: `Shut down 3 nodes`).
- A delete dialog lists what goes and what stays (`Command.Goes`, `Command.Stays`): the containers, network, volumes and Slurm state of a cluster go (its volumes by their exact names, as `sind delete cluster` finds them, not the volumes of cluster `dev-gpu` for `dev`), and the mesh with the realm's last cluster; the host directories it mounts and the other clusters stay. Delete realm keeps the host directories of all its clusters, and the containers that sind did not make where the mesh has any; a list says "if any" where sind ui has not read the status or the mesh it needs. Its button names what it deletes (`Command.OK`: `Delete worker-5`, `Delete cluster dev`). The name to type is the worker's when one worker goes, else the cluster's or the realm's; sind ui trims it and compares it with its case, and a hint below the field says why the button stays off. Delete realm is also on the realm's row of the Overview, and its command always carries `--realm`, so that a copy of it does not delete the realm that `SIND_REALM` names in another shell.
- Add workers shows the size and the names of the new workers: the CPUs and memory of the newest worker, which sind copies, from `docker inspect` of its container (`action.Context.Worker`, kept for 3 s like the image facts), or sind's defaults for a cluster without workers. Like `sind create worker`, `action.NewestWorker` takes the newest worker managed like the controller, as the cluster's status says, and a limit that the worker has none of shows as sind's default. While a preview is on its way, the footer of a dialog shows Checking….
- The New cluster dialog shows a plan beside its form: the nodes, the command and the config. `action.Build` reports a wrong field as a `Problem`, not an error, and builds the plan with that field's default, so the plan never goes blank; a command with problems does not run. Only no config directory is an error. The realm is a field of the New cluster and Connect dialogs (`actSpec.RealmField`), filled in with the realm of the link and suggesting the realms of the snapshot; the other dialogs keep it hidden. In Connect it comes after Container, the field to fill, which gets the focus. A dialog with `actSpec.Fields` shows an error of `Build` that starts with one of them, such as `realm: …`, at that field (`actPreview.ProblemOf`, `ErrAtField`) instead of in the preview; Connect warns of a realm that the snapshot does not have, as `cluster.Connect` needs the mesh of a realm with a cluster. The plan checks the config with `config.ValidateAll`, which returns every error that `Validate` would find, in the order of its checks, where `Validate` returns only the first.
- The footer of the New cluster dialog shows the status of the preview: Ready, with the number of warnings (`Ready · 2 warnings`), or a button with the number of problems and the field of the first, which opens the section of that field and gives it the focus, or without a field shows the list of problems in the plan. Beside it are the containers, CPUs and memory that the nodes use together (`Command.Containers`, `CPUs`, `MemoryMB`), with the memory of the Docker host. A hidden live region says the first problem, or Ready, when the number of problems or the field of the first changes, not at each key.
- The New cluster dialog keeps a draft of its fields in the browser's `localStorage` (`sind-ui-draft-create-cluster`) until its command starts or the user discards it, and a click beside it does not close it. When it opens again, the script posts the draft with the link (`POST /dialogs/create-cluster`, the form encoded as the field `draft`). The server fills the fields in with it, without the defaults, when the draft names the realm of the link and the link fills in nothing else (`actDraftOf`); the dialog then says that it shows the draft and offers Start over.
- The New cluster dialog writes the data storage into the config (`storage.dataStorage`), not as `--data`, so the saved file alone makes the same cluster again. A host directory that does not exist is created by sind ui before the command runs (`Command.Dirs`, part of the fingerprint), as Docker's bind mount needs it; it stays if the create fails. The preview looks for it with `os.Stat`.
- In the New cluster dialog, `--pull` follows the image (`action.PullByDefault`) while the hidden field `pull_auto` is there: on for an image of a registry with a tag that can move (none, `latest`, or a major or major.minor version), off for a fixed tag, a digest or an image without a registry. Ticking or clearing the box removes the field. Copy gives `Command.Script`, a script that writes the config through a quoted here-document (`SIND_CONFIG_<hash>`), creates the directories and runs the command with `--realm`.
- The New cluster dialog posts the node defaults as `size` (`s`, `m`, `l` from `action.Sizes`, or `x` with `cpus`, `memory` and `tmp`), and the fields of each node as `KEY-ATTR`, KEY a role or a worker group: `db-cpus`, `w0-count`. The worker groups are the values of `wg`, in order, named `workers` or, with more than one, `workers 1`, `workers 2` (`action.GroupLabel`, also in the names of their size, −, + and remove buttons; the script renumbers them as groups come and go); the script of the dialog makes the key of a new group (`w` and six letters or digits), and only keys of that form get ids in the page. A form has at most 16 worker groups, 1024 workers in a group and 1024 CPUs in a node, so that a preview never builds a huge plan; sind has no such limit.
- The users, groups and accounts of the New cluster dialog are rows too (`action.UserRows`, `GroupRows`, `AccountRows`): the keys are the values of `ur`, `gr` and `ar` (`u`, `g` or `a` and up to eight letters or digits, never a node role such as `api`), the fields `KEY-name`, `KEY-uid` and so on, and the limits of an account the pairs of `KEY-lk` and `KEY-lv`. A row without a name is left out. The dialog checks names and IDs at their fields; what needs the whole config, such as a group that no row declares, comes from `config.ValidateAll`, whose errors `action.configProblem` puts at the field of the row that causes them, in the words of the form, where it knows the error. The problems of the name say what to change, with a name that would do (`action.nameError`). Without a managed db node the plan leaves the accounts out and says why at the db checkbox. A link's `users=a,b` becomes rows, and a link may fill in the fields of nodes, worker groups and rows too, within the limit of 64 values of a link.
- The New cluster dialog posts the lines of each Slurm config file as `slurm-ID` (`action.SlurmFiles`), a field of text that may have 64 KiB where other fields have 1 KiB; the dialog makes the line ends Unix ones and writes each as the string form of its section, which `config.Section.MarshalYAML` keeps. `identity` (`local`, the default, which the config leaves out, `nssSlurm` or `clientIds`), `ctlusers` (only with `clientIds`) and `cvmfs` set the rest.
- The New cluster dialog knows the Slurm release of an image from its label `sind.slurm.version` (`docker.Client.InspectImage`, which sind ui keeps for 3 s, so that the previews of typing run `docker image inspect` rarely), or from a check that the user starts with Check image (`POST /actions/create-cluster/check`), which runs `slurm.DiscoverVersion` and so pulls an image that is not there; sind ui keeps the result by image ID. `action.Context.Image` hands these facts to `Build`, which lists the images in `Command.Images`. Monitoring needs `slurm.MonitoringMinVersion` in the controller's and the managed workers' images (`slurm.VersionAtLeast`, as sind checks): a known older release is a problem, unless `--pull` is on and the tag can move (`PullByDefault`), which makes it a warning. An api node's image older than that release, or workers newer than the controller, are warnings only, as sind checks the api image for slurmrestd, not its release.
- Edit YAML in the New cluster dialog is one way: the script of the dialog copies the config of the plan into the field `yaml` and enables it with `yaml_on`; Start from YAML enables it empty, for a paste, a file that Open a file reads in the browser, or a file dropped on it. The input and change events of the file field stop at the document (capture), so htmx does not preview it and its out-of-band `act-err-yaml` does not replace the message of a file that is too large or cannot be read; a file that loads previews as a paste does. The form is then hidden and not read, but for the run options (`pull`, `wait`), and Name and Realm, which stay visible: the script puts the `name` and `realm` of the YAML into them and a change of either into the YAML, in place of its line or as a new line after `kind` (`realm` after `name`; an empty realm adds none), so the YAML alone says what `action.Build` reads. `action.Build` parses the YAML, checks it with `config.ValidateAll` and the name as for the form, and saves the text as it is (with Unix line ends). Every problem of the YAML links to its field; those of the name and the realm go to their fields. Copy YAML copies `Command.Config.Content`. A host directory of the YAML that is missing is created first. Discard YAML edits disables both fields again and puts back the Name and Realm that the form had, so the form takes over as it was.
- The Sind collector runs `docker info` once Docker answers, until it has worked once, for the memory and CPUs of the Docker host (`DockerState.MemTotal`, `NCPU`). The plan of a new cluster shows its memory against the host's and warns above 80% (`action.HostMemoryWarn`): memory limits are not reservations, so the nodes can together ask for more than the host has. Above all of it, the plan has the problem `overcommit` (`Command.OverHost`) until the checkbox Create anyway in the plan, the field `overcommit`, accepts it. The box has `data-act-hold`: the script keeps what the user chose across the previews that replace it, and sets the field of each request (`htmx:configRequest`) to it, so that a preview on its way when the user ticks the box does not undo the tick.
- The plan of a new cluster checks what would fail only during the create, for the form and the YAML alike: image references against Docker's grammar (`action.ImageRef`, lowercase repository path, tag, `sha256:` digest of 64 hex digits, at most 255 characters before the tag), and account limits `QOS`, `DefaultQOS` and `QosLevel` that name a QOS other than `normal`, the only one of a new cluster. It warns, without blocking, when `slurm.main` sets one of `slurm.FixedParameters`, which `GenerateSlurmConf` writes itself and Slurm then takes from the main section (`SlurmctldHost` and `StateSaveLocation` with a backup controller are the config's errors), and of a line of a Slurm config section other than `plugstack` that is neither a comment, an `include` nor a `Key=Value` pair. The status pill counts the warnings: `Ready · N warnings`.
- Once the config of a new cluster is valid, the plan shows what sind runs and writes besides the containers (`Command.After`, `Command.SlurmConf`): the users and groups on the nodes that get them (`cluster.UsersScript`, on the nodes of `cluster.NodeRunConfigs` with `AddUsers`), the `sacctmgr -i` commands of the accounts (`slurm.AccountCommands`), and `slurm.conf` with `sind-nodes.conf` (`cluster.SlurmConfFiles`, which `WriteClusterConfig` writes too), so that the preview is the create's own.
- The config of a new cluster is saved with mode `0600` in `$XDG_STATE_HOME/sind/ui/configs/` (`~/.local/state/sind/ui/configs/`), so the command in the dialog can run again from a terminal.
- Help comes in layers that the user opens: a hint card on a word that a page marks with `data-word` (every role and service the pages show is a word of `internal/help`), the help pane that `?` opens (a non-modal dialog that takes the focus and gives it back; it stays below an action dialog that opens later, and lifts above the dialog, as the modal one, when the dialog's own `?` opens the action's entry), and `/help`, which help.js asks for with `HX-Request` and which a browser opens as a page in the layout. The pane also shows the glossary of all words (`topic=words`), every how-to (`topic=index`) and the keys (`topic=keys`, from `helpKeys` in `help.go`, which help.js's `goto` map must match). Single-key shortcuts (`/`, `?`, `g` chords) can be turned off; the setting lives in the browser's `localStorage`, and `Ctrl+K` and `Esc` always work. While they are off, the root has the class `keys-off`, which hides the key caps of class `k1-on`, and the top bar's buttons drop those keys from `title` and `aria-keyshortcuts`.
- The jump box (`GET /jump`) matches every word of the query against the label, kind and text of each entry, and orders by how the label matches (equal, prefix, contains, other), then by kind (clusters, pages, actions, help, nodes, runs, words); it lists `jumpMax` entries and says how many it leaves out. An action that needs a cluster (`actSpec.NeedsCluster`), or a realm when there is more than one, is a step: `GET /jump?step=ACTION&from=PATH` lists the clusters or realms, the one of the page first, as links to the dialog with `realm` and `cluster`, which help.js opens over the page. Without a query, it lists the first `jumpFirstClusters` clusters, the actions of `jumpDefaults` and the pages, without the limit of `jumpMax`. The realm and cluster of the page, or the realm of the top bar, count only when the snapshot has them. It is a combobox in a modal dialog: the options are not in the tab order, `aria-activedescendant` names the active one, and a status line says how many there are; in a step, the question of the step names the field and starts the status line.
- The demo (`internal/ui/demo`) replaces the Docker collectors and the runner: its commands write real go-clikit event logs in memory, which the same reducer reads, so its run pages show what a real command shows. `tools/screenshots`, a Go module of its own so that sind does not depend on a browser driver, starts `sind ui --demo --still`, drives Chromium through the shots of `shots.yaml`, and writes one image per shot, theme and size. The images are not committed: the docs workflow takes them into `docs/static/ui-shots/` before Hugo runs, and the shortcode `uishot` shows one by its name, with the alt text of `shots.yaml`.

### Single instance

The running instance is recorded in `$XDG_RUNTIME_DIR/sind/ui.json` (`{"url": ..., "pid": ...}`, mode `0600`), written once the server listens. A lock file next to it, `ui.lock`, serializes starts, so two starts cannot both find nothing running. The directory must be private: when it is there already, it must be a directory of the user with mode `0700`, not a symbolic link, else `sind ui` fails; the lock file is opened with `O_NOFOLLOW`. The record and the lock are those of go-clikit's `localweb/launch`. A record counts as running when its process exists and a socket of the user listens at its URL (the owner of the listener, from `/proc/net/tcp`); otherwise a new instance replaces it, and the instance that exits removes it when it still names its own process. Without `XDG_RUNTIME_DIR`, and with `--demo`, nothing is recorded, and every `sind ui` starts its own server. The browser opens a small `0600` HTML file, `open-PORT.html`, that redirects to the URL, so the token is never in a command line that other users can read: in the runtime directory, or else in a new `0700` directory below `$TMPDIR`, removed when `sind ui` stops. The opener starts in a session of its own. When that file cannot be written, no browser opens. No browser opens either in an SSH session (`SSH_CONNECTION` is set) or, on Linux, without `DISPLAY` and `WAYLAND_DISPLAY`; the URL is printed anyway.

### Security model

Only the user who started `sind ui` may use it, and it can do what that user's Docker access can do. The checks are those of go-clikit's `localweb` package (`Listen`, `PeerListener`, `Hosts`, `SameOrigin`, `Headers`, `Serve`) and `localweb/session`. A request passes them in this order:

1. **Peer user.** The kernel's table of TCP sockets (`/proc/net/tcp`) names the owner of each connection; only a row in state `ESTABLISHED` with an inode counts. A connection of a process of another user is closed before HTTP is read. This works on Linux only, as sind does. The server sets a timeout for the headers, the request, the answer and an idle connection; the event stream and the log streams lift the one for the answer.
2. **Host.** The `Host` header must be `127.0.0.1:PORT`, which stops DNS rebinding. `localhost` is refused: a browser may resolve it to `::1`, where a process of another user can listen on the same port and would get the token.
3. **Session.** The first `GET` with `?token=` must carry the token of this run (32 random bytes). It sets an `HttpOnly`, `SameSite=Strict` cookie, named for the port, and redirects to the same page without the token. Any other request without a session gets `403`: a browser that opens a page gets the page `signin`, which says why and how to get the address again (run `sind ui`), but never the token; any other request gets a line of text. The static files under `/static/` need no session, as they hold no data and the sign-in page uses them; htmx under `/htmx/` does. The token stays valid while `sind ui` runs, so the printed URL opens it in more than one browser; it ends with the process. Browsers do not keep cookies apart by port, so the browser also sends the cookie to a server of another user on another port of `127.0.0.1`; that user still cannot use it, as check 1 closes their connections. A page that a program of the same user serves on another port of `127.0.0.1` is the same site, so `SameSite` does not keep it out: a request that the browser marks `Sec-Fetch-Site` other than `same-origin` gets `403`, unless it is a `GET` or `HEAD` navigation of the whole page (`Sec-Fetch-Mode: navigate`, `Sec-Fetch-Dest: document`), as when the user types the address or follows a link. The event stream and the log streams, which start work, take only `same-origin`. A request that carries an `Origin` other than its own gets `403`, a `GET` too. No `GET` changes anything but the realm that the browser remembers for the lists (cookie `sind_realm`), and only one that a page of sind ui sent (`Sec-Fetch-Site: same-origin`) changes that.
4. **Writes.** Requests that change something (every method but `GET` and `HEAD`) need an `Origin` of `http://` followed by the `Host` header, and the session's CSRF token in the `X-Sind-CSRF` header or the `csrf` form field. Forms are capped at 64 KiB. Every path and query value is checked again on the server.
5. **Responses.** `Content-Security-Policy` allows only files of the same origin (`default-src 'none'`, `frame-ancestors 'none'`), with `X-Frame-Options: DENY`, `Cache-Control: no-store` and `Referrer-Policy: same-origin` (a form POST then carries its origin). Text from containers and the event log is escaped.

No secret has a page: the munge key, the JWT key and the SSH private key (`sind get auth-key`, `sind get ssh-private-key`) are not shown in the UI. Long-running commands are stopped with SIGINT only. When `sind ui` stops, it interrupts its commands so that they roll back, and kills any that have not ended after 60 seconds.

## Node Arguments

Commands accepting node arguments use DNS-style names, written as a node set expression to name several nodes.

### Format

```
<role>.<cluster>
<role>-<N>.<cluster>
```

The cluster suffix defaults to `.default` if omitted. A name is split at its last `.`, and what follows it must be a valid cluster name; what precedes it must be a node's short name, a lowercase DNS label, so a name with more dots is a usage error. The FQDN that `get nodes` prints (`NODE.CLUSTER.REALM.sind`) is one of them, and every node command (`get node`, `power`, `delete worker`, `logs`, `ssh`) rejects it with the same usage error, `use NODE[.CLUSTER], not the FQDN`, since `parseNodeName` checks every node name. Its errors quote the term of the argument that named the node, as typed (`typedTerm`): `invalid node name "Worker-[0-1]": invalid short name "Worker-0": it has uppercase letters`. `ssh`, `logs` and `get node` act on one node and parse their argument the same way (`parseOneNode`): a node set that names another number of nodes is the usage error `<command> requires exactly one node, got N`.

### Node Sets

Node arguments are node set expressions in ClusterShell's syntax, which Slurm and pdsh users know too, parsed by [go-nodeset](https://github.com/GSI-HPC/go-nodeset), whose [language reference](https://github.com/GSI-HPC/go-nodeset/blob/v1.0.0/doc/language.md) has the full rules:

| Expression | Nodes |
|------------|-------|
| `worker-[0-3]` | worker-0, worker-1, worker-2, worker-3 |
| `worker-[0,2,4]` | worker-0, worker-2, worker-4 |
| `worker-[0-2,5]` | worker-0, worker-1, worker-2, worker-5 |
| `worker-[0-6/2]` | worker-0, worker-2, worker-4, worker-6 |
| `worker-[0-1].dev` | worker-0.dev, worker-1.dev |
| `worker-[0-1].dev[1-2]` | worker-0.dev1, worker-0.dev2, worker-1.dev1, worker-1.dev2 |
| `controller,worker-0` or `controller worker-0` | controller, worker-0 (union) |
| `worker-[0-5]!worker-[2-3]` | worker-0, worker-1, worker-4, worker-5 (difference) |
| `worker-[0-5]&worker-[4-9]` | worker-4, worker-5 (intersection) |
| `worker-[0-3]^worker-[2-5]` | worker-0, worker-1, worker-4, worker-5 (symmetric difference) |

Rules:
- Every run of digits in a name is a number, the cluster's included; a bracket may stand for any of them, and the padding and order rules below apply to each.
- The operators have no precedence: an expression is evaluated left to right, so `worker-[0-9]!worker-[0-4]&worker-[3-6]` is worker-5 and worker-6. `!`, `&` and `^` need an operand on each side; an empty operand of a union is nothing, so `worker-0,` is worker-0.
- A command joins its node arguments with a space, which is a union, so `sind power on 'worker-[0-3]' '!worker-2'` is one expression. Quote an expression with `[`, `!`, `&` or `^` from the shell: the shell reads `!` itself, and expands `worker-[0-3]` to the files in the working directory that match it, such as a job's output file `worker-1`.
- Zero padding is not part of a node's identity: `worker-1,worker-01` is one node, and `worker-[1-3]!worker-02` is worker-1 and worker-3. A node keeps the spelling it was first given, and sind looks it up by that spelling. sind names its workers without padding, so `worker-[00-03]` names none of them.
- A node named with and without its cluster suffix, as in `controller,controller.default`, is one node too: `power` and `delete worker` act on it once.
- The padding of the cluster suffix's numbers is no part of the identity either, but clusters `dev1` and `dev01` are two: an expression whose names differ only in the padding of their cluster (`worker-0.dev1,worker-0.dev01`), which would act on one of the two nodes, is a usage error (`paddingClash` expands each term on its own to see every spelling).
- The operators compare names as written, cluster suffix included: `worker-[0-3]!worker-2.default` removes nothing. Write the suffix the same way throughout an expression.
- A group (`@name`) is a usage error: sind has no node groups.
- An expression that names no node is a usage error, and so is one that names more than 2^20 nodes, which go-nodeset refuses before it allocates the names, or a name that begins with `-`, which a command it is passed to could read as an option.

sind acts on the nodes in go-nodeset's order: sorted by the name with each number taken as a placeholder that sorts before letters, `-` and `.`, then by number, the last number varying fastest. `worker-10 worker-2.dev controller worker-1` is controller, worker-1, worker-10, worker-2.dev. `power` and `delete worker` take the nodes cluster by cluster, the clusters in the order of their first node.

### Examples

```bash
sind power shutdown controller                       # controller.default
sind power cycle worker-0                            # worker-0.default
sind power freeze 'worker-[0-3].dev'                 # 4 nodes in dev cluster
sind power reboot 'controller,worker-[0-1]'          # multiple nodes in default
sind power on 'worker-[0-3]' '!worker-2'             # 3 nodes in default
sind delete worker 'worker-[0-1].dev' worker-3.prod  # nodes in two clusters
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
  image: ghcr.io/gsi-hpc/sind-node:vX.Y.Z  # the sind release; latest for other builds
nodes:
  - role: controller
  - role: worker
```

When `defaults.image` is omitted, sind uses the generic image published for its own release, `ghcr.io/gsi-hpc/sind-node:vX.Y.Z` for sind vX.Y.Z, or `ghcr.io/gsi-hpc/sind-node:latest` when it is not a release build (see Generic Image).

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
  image: ghcr.io/gsi-hpc/sind-node:26.05 # default: sind-node:<sind release>
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

monitoring: true                         # collect metrics and logs in the realm's monitoring stack (default: false; Slurm 26.05+)

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

  - role: api                            # optional, at most one: slurmrestd (Slurm 26.05 and later)

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
├── plugstack.conf          # always: include /etc/slurm/plugstack.conf.d/*
└── plugstack.conf.d/
```

`plugstack.conf` is always created with an `include /etc/slurm/plugstack.conf.d/*` directive, and `PlugStackConfig` is always set in `slurm.conf`. This allows SPANK plugins to be dropped in without additional configuration.

Standalone sections (`gres`, `topology`) are only created when configured. They require enabling in `slurm.conf` (e.g., `GresTypes=gpu`, `TopologyPlugin=topology/tree`) via the `main` section.

Validation rules:
- Fragment names must be plain filenames (no path separators)
- Fragment names and content must not be empty

### Node Roles

| Role | Count | Required | Slurm Daemons | Description |
|------|-------|----------|---------------|-------------|
| `controller` | exactly 1 | yes | slurmctld | Cluster controller |
| `db` | 0-1 | no | mariadb, slurmdbd | Accounting database (see Database Node) |
| `api` | 0-1 | no | slurmrestd | REST API (see API Node) |
| `submitter` | 0-1 | no | none (clients only; `sackd` with identity `clientIds`) | Job submission node |
| `worker` | 1+ | yes | slurmd | Worker nodes |

### Node Parameters

| Parameter | Scope | Default | Description |
|-----------|-------|---------|-------------|
| `image` | global + per-node | `ghcr.io/gsi-hpc/sind-node:vX.Y.Z` (the sind release; `latest` for other builds) | Container image |
| `tmpSize` | global + per-node | `256m` | tmpfs size for /tmp, in the kernel's tmpfs syntax: a whole number with an optional `k`, `m`, `g`, `t`, `p` or `e`, or a percentage of the memory; files there count against `memory` |
| `cpus` | global + per-node | `1` | CPU limit; a managed worker's Slurm `CPUs` |
| `memory` | global + per-node | `512m` | Memory limit, without swap, in Docker's size syntax (`512m`, `2g`, `2gb`, `1.5GiB`, plain bytes); a managed worker's Slurm `RealMemory`, in MiB; `/dev/shm` gets half of it |
| `capAdd` | global + per-node | none | Extra Linux capabilities (e.g. `SYS_ADMIN`) |
| `capDrop` | global + per-node | none | Dropped Linux capabilities |
| `devices` | global + per-node | none | Host devices to expose (e.g. `/dev/fuse`) |
| `securityOpt` | global + per-node | none | Extra security options |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | controller + db + api + worker | `true` | Worker: start slurmd and add to slurm.conf. Db: run MariaDB and slurmdbd and configure accounting (see Database Node). Api: run slurmrestd and set up JWT (see API Node). Controller: `false` makes the whole cluster unmanaged (see Unmanaged Cluster) |
| `backupController` | controller only | `false` | Add a backup controller, `controller-backup` |

Per-node scalar values override the `defaults` section. List fields (`capAdd`, `capDrop`, `devices`, `securityOpt`) are **merged** with defaults rather than replacing them.

`memory` limits everything in the node: the jobs, the node's own services (systemd, journald, munge, sshd and the Slurm daemon; mariadb and slurmdbd on the db node) and the files in its tmpfs mounts (`/tmp`, `/run`, `/dev/shm`). Slurm's `RealMemory` is the whole limit, so a job that uses all the memory it may allocate, or files left in `/tmp`, can push a node past it; the kernel then OOM-kills the largest process in the node, usually the job. Raise `memory` for jobs that need much memory or `/tmp`. Nodes get no swap (`--memory-swap` equal to `--memory`), so they behave the same on hosts with and without swap.

### Validation Rules

- `nodes` - optional; if omitted, creates 1 controller + 1 worker
- `role: controller` - exactly one (auto-created if nodes omitted)
- `role: db` - at most one
- `role: api` - at most one
- `role: submitter` - at most one
- `role: worker` - at least one (auto-created if nodes omitted)
- `count` - only valid for worker role; must not be negative, and `0` means the default, 1
- `cpus` - must not be negative, and `0` means the default; `memory` - Docker's size syntax: a number, optionally with a fraction, and an optional unit `b`, `k`, `m`, `g`, `t` or `p` (powers of 1024, either case, the last five optionally followed by `b` or `ib`), at least `6m`, Docker's minimum; `tmpSize` - a whole number with an optional unit `k`, `m`, `g`, `t`, `p` or `e`, or a percentage. These apply to `defaults` too
- `managed` - only valid for controller, db, api and worker roles; with `managed: false` on the controller, no worker, db or api node may set `managed: true` and no `slurm` section may be set
- `backupController` - only valid for controller role; with it, `slurm.main` must not set `SlurmctldHost` (or `ControlMachine`, `BackupController`, `BackupAddr`) or `StateSaveLocation`
- `slurm.slurmdbd` - requires a managed `db` node; must not set `DbdHost`, `SlurmUser`, `StorageType`, `StorageHost`, `StorageLoc` or `StorageUser`, which the accounting database sind sets up on the db node depends on (Slurm would take the section's value); an `AuthType` it sets is the cluster's plugin, `auth/slurm` with identity `clientIds` and `auth/munge` otherwise (with or without the `auth/` prefix), and with `clientIds` an `AuthInfo` it sets lists `use_client_ids`
- `accounts`, and the users' `accounts`, `coordinator` and `adminLevel` - require a managed `db` node; account names hold only lowercase letters, digits, `_` and `-`, do not start with `-`, have at most 64 characters, are unique and are not `root`; a `parent` is `root` or an account declared before; `limits` keys are among the options listed in Slurm Accounts, in any case, with non-empty values; every account a user names is declared, at most once per list; `adminLevel` is `operator` or `admin`; `coordinator` and `adminLevel` need `accounts`
- `capAdd`, `capDrop` - recognized Linux capability names (e.g. `SYS_ADMIN`, `ALL`)
- `devices` - absolute paths
- `securityOpt` - options Docker knows by name: `label=`, `apparmor=`, `seccomp=`, `no-new-privileges`, `writable-cgroups=` or `systempaths=`, each with a value (`no-new-privileges` may go without); Docker checks the values
- `storage.dataStorage` - `type` is `volume` or `hostPath`; `hostPath` requires a `hostPath`; `mountPath` is absolute and contains no comma (`sind enter` and `sind exec` read it back from `docker ps`, which joins labels with commas), and is none of the paths where the nodes already mount something, as Docker refuses two mounts at one path: `/etc/slurm`, `/etc/munge` (except with identity `clientIds`), `/tmp`, `/run`, `/run/lock`, `/home` with `users`, `/cvmfs` with `storage.cvmfs`, and `/var/spool/slurmctld` with `backupController`
- `users`, `groups` - a name or an object; user and group names start with a lowercase letter or `_`, hold only lowercase letters, digits, `_` and `-`, and have at most 32 characters; `uid` and `gid` are between 1000 and 2147483647 and not 65534 (`nobody` in the image) or 65535 (the 16-bit -1); no two users share a name or `uid`, no two groups (private ones included) a name or `gid`; a user's `group` and `groups` are declared in `groups`, and `groups` repeats neither an entry nor `group`
- `identity` - `local`, `nssSlurm` or `clientIds`, or an object with `mode` and `controllerUsers`; `nssSlurm` and `clientIds` require a managed cluster; `controllerUsers` only with `clientIds`; a `LaunchParameters`, `AuthType`, `CredType` or `AuthInfo` that `slurm.main` sets for the mode lists sind's value (see Identity Modes)
- with a managed api node, an `AuthAltTypes` that `slurm.main` or `slurm.slurmdbd` sets lists `auth/jwt` (or `jwt`); `AuthAltParameters` there is not checked (see API Node)
- `monitoring` - requires a managed controller; `slurm.main` must not set a `MetricsType` other than `metrics/openmetrics`, a `CommunicationParameters` that lists `disable_http`, or `MetricsAuthUsers` (the collector has no token), and with `PrivateData` it must set `MetricsParameters=ignore_private_data` (see Monitoring)
- `name`, `realm` - valid cluster and realm names, see [Cluster and Realm Names](#cluster-and-realm-names)
- unknown keys are rejected
- the file is YAML 1.2: a text field takes any scalar as written (`name: 123` is `123`, `realm: no` is `no`), and a `true`/`false` field also takes `yes`, `no`, `on` and `off`, unquoted; anchors, aliases and merge keys (`<<: *worker`) are followed, so an object, a node's `role` included, can take its keys from another

An error names the config it is about, `config FILE:` or, read from stdin, `config <stdin>:`, and a mistake in its form (YAML syntax, an unknown or repeated key, a value of the wrong type, or a role, identity mode, admin level or storage type that is not one) also the line and key path: `config dev.yaml: line 7: nodes[1].Cpus: unknown key (valid: role, count, …); did you mean "cpus"? keys are case-sensitive`. A config without content, only comments or `null` is `config FILE: empty configuration`.

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
- a cluster is not named `ssh`: its config volume, `<realm>-ssh-config`, would be the realm's SSH volume, which `sind delete cluster ssh` would then try to remove. Nor does its name end in `-ssh`: the config volume of cluster `x-ssh` in realm `a`, `a-x-ssh-config`, would be the SSH volume of realm `a-x`. The mesh also refuses an SSH volume whose labels name another realm or another volume, such as the config volume of a cluster `x-ssh` that an earlier sind created, rather than mount it into the relay as `/root/.ssh`
- with a managed db node, the cluster name has at most 40 characters: slurmdbd names the cluster's MariaDB tables `<cluster>_<table>`, MariaDB allows 64 characters, and Slurm documents the limit of 40. A longer name would keep slurmdbd from registering the cluster

sind checks every place a name enters: the `name` and `realm` config fields, the `[CLUSTER]` argument, the cluster part of node arguments (`worker-0.dev`), `exec`'s cluster argument, `--realm` and `SIND_REALM`. The root command checks `--realm` before any command runs (`checkRealmFlag`), so it is a usage error even for a command that uses no realm, such as `version` or `get realms`. `SIND_REALM` is only checked when it is the realm in effect. The defaults `default` and `sind` are valid.

The realm in effect is `--realm`, then `SIND_REALM`, then, for `sind create cluster` only, the config's `realm`, then `sind` (`resolveRealm` in `cmd/sind/context.go`). Every other command resolves `--realm` > `SIND_REALM` > `sind`, so ranking `SIND_REALM` above the config keeps every command of a session, sind-action's among them, in the realm the environment names. A realm set only in the config does not carry over to later commands; when a command does not find a cluster in its realm, the error names the clusters the realm holds and the other realms that hold a cluster of that name (`cluster "dev" not found in realm "sind" (existing clusters: default; it exists in realm "ci-42")`). Every command that takes a cluster or a node reports a missing cluster this way (`ErrClusterNotFound`, `clusterNotFound`), `get nodes CLUSTER` included, which fails with exit status 1 instead of listing no nodes, and `logs` and `ssh` check the node first (`cluster.CheckNode`), as `get node` does when docker finds no container, so docker's error with an internal container name never reaches the user. A node missing from an existing cluster is `node "x" not found in cluster "dev"` (`ErrNodeNotFound`).

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
- Identity `nssSlurm` and `clientIds` are rejected, and so are `accounts` and the users' `accounts`, `coordinator` and `adminLevel`, which need a managed db node (see Identity Modes and Slurm Accounts), and `monitoring`, which needs the slurm.conf sind writes (`monitoring requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster`, from the key and from `--monitoring` alike). `users` and `groups` work as with identity `local`: every node gets them and, with `users`, mounts the home volume.
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
- `sind get cluster` and `sind get node` report mariadb and slurmdbd for the db node, and `sind get clusters` counts it in `NODES (S/C/D/A/W)`.
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
- A user or group name that already exists in the image (`root`, `slurm`, `munge`, `wheel`, ...), or a uid or gid an image account already has (Ubuntu's `ubuntu` at 1000), fails `sind create cluster` with the `groupadd` or `useradd` error.

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

- Accounts are created in list order, so a parent is declared before its children. `limits` are passed as `key=value`, in key order. Numbers are passed in plain decimal form, as the YAML parser reads them before sind sees them: `MaxJobs: 1` becomes `MaxJobs=1`, but `010` is octal and becomes `8`, `0x10` becomes `16`, `1e3` `1000` and `1.10` `1.1`, also for options that take text, such as `Description: 1.0`. A quoted value is passed as written.
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
| Linux accounts on | every node | every node but the managed workers: controllers, db, api, submitter | login node: the submitter, or the controllers without one; the controllers too with `controllerUsers` |
| Secret sind distributes | munge key | munge key | `slurm.key` |
| munge | on every node | on every node | masked on every node; `sackd` on the submitter |
| Image needs | nothing new | `libnss_slurm.so.2` on managed workers | `libnss_slurm.so.2` on managed workers, `auth/slurm` and `serializer/json` (Slurm built `--with-jwt --with-json`) everywhere, `sackd` on the submitter |
| SSH as a user to | every node | every node but the managed workers | login node, and the controllers with `controllerUsers` |
| Mirrors a site where | every node has LDAP/SSSD | compute nodes have no directory | only login nodes have a directory |

- **Controller:** with `local` and `nssSlurm`, slurmctld resolves each job's user and groups, and the uids of Slurm users, from its own passwd and group files; for an unknown user it retries only once an hour, which is why the controller gets the users before sind creates the Slurm accounts. With `clientIds` it takes the identity from the user's token: `sackd` on the login node puts the caller's passwd and group entries into it, and slurmctld sets the uid on the user's associations before it processes the request.
- **db:** slurmdbd resolves user names itself only for its own checks (admin levels, coordinators, a user changing their default account); with `clientIds` it learns the uids from tokens. sind gives the db node the accounts in `local` and `nssSlurm`.
- **Workers:** with `nssSlurm` and `clientIds`, a managed worker has no Linux accounts. Before it creates any node, sind refuses a local sind-node image from before identity modes, one whose labels lack `sind.libjwt.version`: such an image has neither nss_slurm nor auth/slurm, and is usually a cached official tag that docker reuses without `--pull`, so the error says to pull a current one with `--pull` (or to rebuild a local build). `sind create cluster` checks the images of the managed workers, and with `clientIds` of every managed node, unless it pulls them; `sind create worker` checks the new workers' image. On each managed worker, sind then checks that the image has nss_slurm (`ldconfig -p`, or `/usr/lib64/libnss_slurm.so.2`) and fails `sind create cluster` or `sind create worker` with the image name if not, then puts `slurm` first on the `passwd` and `group` lines of `/etc/nsswitch.conf`. nss_slurm answers only for the job's user and groups, from the job credential, and only to processes inside a job step; sshd, prolog, epilog and health checks run outside one, so SSH as a user to a worker fails by design. No `/etc/nss_slurm.conf` is needed: the hostname is the Slurm node name and the spool directory is the default.
- **clientIds:** sind writes `slurm.key` (1024 random bytes, `slurm`-owned, mode `0600`) to the config volume, creates no munge volume or key, masks `munge.service` from the node entrypoint (`ln -sf /dev/null /etc/systemd/system/munge.service` before systemd starts) and waits for no munge, and enables `sackd` on the submitter like a Slurm daemon. `slurmdbd.conf` gets `AuthType=auth/slurm` and `AuthInfo=use_client_ids`. Root's client commands on the other nodes get their tokens from the SACK service of slurmctld, slurmdbd or slurmd.
- **Every node, every mode:** root and SlurmUser stay local; the `slurm` user needs the same uid on every node, which the image ensures. Unmanaged nodes (unmanaged workers, db and api nodes) get the Linux accounts as with `local`, as sind does not manage their Slurm. `nssSlurm` and `clientIds` need a managed cluster.
- The `slurm.conf` parameters are each set unless `slurm.main` sets it. A value `slurm.main` sets must list sind's, or validation fails, as the mode would not work without it: `LaunchParameters` must list `enable_nss_slurm`, and with `clientIds` `AuthInfo` must list `use_client_ids`, `AuthType` be `auth/slurm` and `CredType` `cred/slurm`.
- The mode is stored as the `sind.identity` label, so `sind create worker` sets up new workers the same way.
- slurmctld checks a partition's `AllowGroups` with local lookups only: with `clientIds`, `controllerUsers: true` gives the controllers the users and groups for it. sind does not parse `slurm.main` for `AllowGroups`.

### API Node

An `api` node runs slurmrestd, Slurm's REST API daemon, with JWT authentication:

```yaml
nodes: [controller, db, api, worker: 2]
```

- The container is `<realm>-<cluster>-api` with hostname `api`; it gets the defaults and per-node parameters like any node, but not `count` or `backupController`. Its mounts are the submitter's: `/etc/slurm` and `/etc/munge` read-only (no munge with identity `clientIds`), the data, `/home` with `users` and `/cvmfs` with `storage.cvmfs`. It gets the cluster users like the nodes that are neither worker nor controller: in `local` and `nssSlurm`, not in `clientIds`; slurmrestd itself needs none.
- The API is at `http://api.<cluster>.<realm>.sind:6820`, for the SSH relay, the host (with host DNS, see Host DNS Resolution) and containers joined to the cluster network; `sind get node api -o json` has its address. sind publishes no host port.
- sind enables the unit Slurm ships, `slurmrestd.service`, in Phase 4, alongside slurmctld and slurmd: it runs slurmrestd as the user `slurmrestd` (slurmrestd refuses root and SlurmUser) with `SLURM_JWT=daemon`, listening on `:6820`. sind waits until the unit is active and the port accepts connections. slurmrestd loads the `/slurmdb/` endpoints only when `slurm.conf` sends accounting to slurmdbd, i.e. with a managed db node.
- Before that, on each managed api node, sind checks that the image has `slurmrestd` and its unit, and fails `sind create cluster` with the image's name if not. The sind-node images ship it from Slurm 26.05 on (see Generic Image); the 25.11 images fail this check.
- slurmrestd unshares its System V IPC namespace and its file descriptor table when it starts, and exits when it cannot: a container denies `unshare`, as Docker's default seccomp profile allows it only with `CAP_SYS_ADMIN`. sind therefore adds the drop-in `/etc/systemd/system/slurmrestd.service.d/sind.conf` with `Environment=SLURMRESTD_SECURITY=disable_unshare_sysv,disable_unshare_files` and reloads systemd, in the setup step after the check. This isolates no less: the container has an IPC namespace of its own, and systemd starts slurmrestd with a file descriptor table of its own.
- **JWT:** sind writes `jwt_hs256.key`, 32 random bytes as Slurm's JWT guide creates it, to the config volume, owned by `slurm` with mode `0600` (slurmctld refuses a key that others can read). `slurm.conf`, and `slurmdbd.conf` with a managed db node, get `AuthAltTypes=auth/jwt` and `AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key`, with identity `clientIds` followed by `,use_jwt_client_ids`, each unless `slurm.main` (or `slurm.slurmdbd`) sets it. An `AuthAltTypes` there must list `auth/jwt`; an `AuthAltParameters` there is taken as written, e.g. to test a JWKS setup (`jwks=…`), and then sind's key is unused. slurmdbd has no default key path, so a `slurm.slurmdbd` that sets `AuthAltParameters` without `jwt_key=` or `jwks=` keeps slurmdbd from starting.
- **Two checks per request:** slurmrestd holds no key and looks no user up; it passes the caller's token (`X-SLURM-USER-NAME`, `X-SLURM-USER-TOKEN`) on with each RPC. slurmctld (for `/slurm/`) or slurmdbd (for `/slurmdb/`) checks the signature and expiry with the key, the same in every identity mode, and then resolves the user: from the token's identity claims when `use_jwt_client_ids` is set and they are complete, otherwise by looking the name up in its own node's `/etc/passwd`.
- **Tokens:** `scontrol token username=USER [lifespan=SECONDS]`, run as root (`sind exec`), prints `SLURM_JWT=<token>`; such a token carries only the user name (`sun`). `sind get auth-key --type jwt` exports the key, so that tooling can sign tokens of its own (HS256), with any lifespan, also while slurmctld is down. Such a token can also carry the identity: `uid`, `gid` and `id` with `name` (equal to `sun`), a non-empty `gecos`, `dir`, `shell` and `gids` or `groups`. Whoever holds the key can sign a token for any user, root included.
- **Identity modes:** in `local` and `nssSlurm` the controller and the db node have the users, so tokens from `scontrol token` work for every user. In `clientIds` they don't: root's tokens work everywhere, a user's only with identity claims, or with `controllerUsers: true` for the `/slurm/` endpoints. This is why sind sets `use_jwt_client_ids` there, the JWT counterpart of sackd's tokens.
- `sind get cluster` and `sind get node` list slurmrestd on a managed api node, and `sind get clusters` counts it in `NODES (S/C/D/A/W)`.
- `managed: false` on the api node of a managed cluster makes it a bare node, labelled `sind.managed=false`, for testing your own slurmrestd provisioning: sind starts no slurmrestd and sets up no JWT. In an unmanaged cluster the api node is a bare node like the others.

## Docker Resources

### Per-Cluster Resources

| Type | Name Pattern | Example (`sind create cluster dev`) |
|------|--------------|------------------------|
| Network | `<realm>-<cluster>-net` | `sind-dev-net` |
| Controller | `<realm>-<cluster>-controller` | `sind-dev-controller` |
| Backup controller | `<realm>-<cluster>-controller-backup` | `sind-dev-controller-backup` |
| Db | `<realm>-<cluster>-db` | `sind-dev-db` |
| Api | `<realm>-<cluster>-api` | `sind-dev-api` |
| Submitter | `<realm>-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `<realm>-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `<realm>-<cluster>-config` | `sind-dev-config` |
| Munge volume (not with identity `clientIds`) | `<realm>-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `<realm>-<cluster>-data` | `sind-dev-data` |
| State volume (backup controller only) | `<realm>-<cluster>-state` | `sind-dev-state` |
| Home volume (`users` only) | `<realm>-<cluster>-home` | `sind-dev-home` |
| Config helper (temporary, managed clusters only) | `<realm>-<cluster>-config-helper` | `sind-dev-config-helper` |
| Munge helper (temporary, not with identity `clientIds`) | `<realm>-<cluster>-munge-helper` | `sind-dev-munge-helper` |
| Journal helper (temporary, `monitoring` only) | `<realm>-<cluster>-journal-helper` | `sind-dev-journal-helper` |

The helpers mount the config and munge volumes while `sind create cluster` writes the Slurm configuration and the munge key into them, and the realm's journal volume while it makes the nodes' journal directories, using the controller's image, and are removed once they are done. They carry the `sind.realm` and `sind.cluster` labels, so `sind delete cluster` removes one that an interrupted create left behind (`docker ps -a`).

### Global Resources (Mesh)

| Type | Name Pattern | Example | Image |
|------|-------------|---------|-------|
| Mesh network | `<realm>-mesh` | `sind-mesh` | — |
| DNS container | `<realm>-dns` | `sind-dns` | `coredns/coredns:1.15.0` |
| SSH container | `<realm>-ssh` | `sind-ssh` | sind's default node image (runs `sleep infinity`) |
| SSH volume | `<realm>-ssh-config` | `sind-ssh-config` | — (keys copied in through `<realm>-ssh` before it first starts) |

The mesh images do not follow `defaults.image`: the relay needs the `ssh` client and `bash` of sind's own node image, which custom images need not have. CoreDNS is pinned to a release; bump `DNSImage` in `pkg/mesh` by hand. `--pull` pulls the mesh images when `sind create cluster` creates the mesh containers; an existing mesh keeps its containers, and their images, until the realm's last cluster is deleted. `CleanupMesh` also removes a `<realm>-ssh-keygen` container, the key helper of earlier sind versions, if one is left over.

### Monitoring Stack

A realm with a monitored cluster has one monitoring stack (see Monitoring):

| Type | Name Pattern | Example | Image |
|------|-------------|---------|-------|
| Grafana container | `<realm>-grafana` | `sind-grafana` | `grafana/grafana:13.2.4` |
| Prometheus container | `<realm>-prometheus` | `sind-prometheus` | `prom/prometheus:v3.15.1` |
| Loki container | `<realm>-loki` | `sind-loki` | `grafana/loki:3.7.9` |
| Alloy container | `<realm>-alloy` | `sind-alloy` | `grafana/alloy:v1.21.1` |
| Data volumes | `<realm>-<service>` | `sind-grafana`, `sind-prometheus`, `sind-loki`, `sind-alloy` | — |
| Journal volume | `<realm>-journal` | `sind-journal` | — (a directory `<cluster>/<node>` per monitored node) |

The names have two parts, so they never equal a cluster's `<realm>-<cluster>-<type>` resources, and no cluster name has to be reserved. A service's container and its data volume share a name, so sind inspects containers with `docker inspect --type container` (`docker.Client.InspectContainers`), which never returns the volume of a missing container. The images are pinned constants in `pkg/observability` (`GrafanaImage`, `PrometheusImage`, `LokiImage`, `AlloyImage`), each bumped on its own by hand; they do not follow `defaults.image`. Grafana must be the standard image: its Prometheus and Loki datasources ship only in the standard image's bundled plugins, which the `-slim` image would download at start, and it is the only one of the four with `curl`, which sind runs there to reach the others. A create that makes the stack pulls its images in Phase 2 with the node images; an existing stack keeps its containers, and their images, until it is removed or recreated (see Monitoring).

### Realm Lock

| Type | Name Pattern | Example | Image |
|------|-------------|---------|-------|
| Configuration-only network | `<realm>-lock` | `sind-lock` | — |

The network exists only while a sind command changes the realm: it is the realm lock's part on the Docker daemon (see Realm Advisory Locking). It is not part of the mesh, `sind get networks` leaves it out, and it has no `sind.cluster` label, so cluster discovery and `sind delete cluster --all` do not see it.

### Defaults

The default realm is `sind` and the default cluster name is `default`, resulting in prefixes like `sind-default-*`.

## Volume Mounts

| Volume | Mount Point | Controller | Db | Api | Worker | Submitter |
|--------|-------------|------------|----|-----|---------|-----------|
| `<realm>-<cluster>-config` | `/etc/slurm` | rw | ro | ro | ro | ro |
| `<realm>-<cluster>-munge` | `/etc/munge` | ro | ro | ro | ro | ro (not with identity `clientIds`) |
| `<realm>-<cluster>-data` | `/data` | rw | rw | rw | rw | rw |
| `<realm>-<cluster>-state` | `/var/spool/slurmctld` | rw (backup controller pairs only) | — | — | — | — |
| `<realm>-<cluster>-home` | `/home` | rw | rw | rw | rw | rw (`users` only) |
| `cvmfs` plugin volume or host `/cvmfs` | `/cvmfs` | ro | ro | ro | ro | ro (`storage.cvmfs` only) |
| `<realm>-journal`, subpath `<cluster>/<node>` | `/var/log/journal` | rw | rw | rw | rw | rw (`monitoring` only) |
| tmpfs | `/tmp` | per-node | per-node | per-node | per-node | per-node |

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
--mount type=volume,source=<realm>-journal,target=/var/log/journal,volume-subpath=<cluster>/<node>  # monitoring
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
| `sind.managed` | `true` | Whether sind manages Slurm on the node: `false` for unmanaged workers, db and api nodes and for every node of an unmanaged cluster. Nodes created before this label existed count as managed. |
| `sind.slurm.version` | `25.11.8` | Slurm version; empty for an unmanaged cluster |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path; empty with the data volume |
| `sind.data.mountpath` | `/shared` | Data mount point, `/data` by default |
| `sind.cvmfs` | `hostPath` | How the node mounts CVMFS: `volume` (plugin) or `hostPath` (host `/cvmfs`); empty without `storage.cvmfs` |
| `sind.users` | `alice:1000:1000 bob:2001:3000` | The cluster users, space-separated `name:uid:gid` entries (gid of the primary group); empty without `users` |
| `sind.groups` | `alice:1000 hpc:3000:carol` | The cluster groups, private groups included, space-separated `name:gid` entries with `:member+member...` for supplementary members; empty without `users` and `groups` |
| `sind.identity` | `clientIds` | The identity mode: `local`, `nssSlurm` or `clientIds` |
| `sind.monitoring` | `true` | Whether the node's cluster is monitored: `true` or `false`. For listings and filters only: what decides is the node's mount of the realm's journal volume, which an image cannot forge (see Monitoring) |

Every node container gets each of these labels, with an empty value where there is nothing to
record. Docker merges the image's labels into the container's, so a label sind left out could come
from the image: an image labelled `sind.data.hostpath=/` would otherwise make `sind create worker`
bind-mount the host's root directory on a cluster that uses the data volume. Nodes created by
earlier sind versions lack some of the labels; sind reads a missing label like an empty one.

Every node container, the mesh's DNS and SSH containers, and every network and volume but the realm lock also carry Docker Compose labels (`com.docker.compose.*`), so Compose-aware tools group them. The project is `<realm>-<cluster>` (`<realm>-mesh` for the mesh). A container's service is its role (`dns` or `ssh` in the mesh) and its container number is 1, N+1 for `worker-N` and 2 for `controller-backup`; networks and volumes name themselves `net`, `mesh`, the volume type (`config`, `munge`, `data`, `state`, `home`) or `ssh-config`.

The monitoring stack's containers carry only the Compose labels of the mesh's project, `<realm>-mesh`, with their service (`grafana`, `prometheus`, `loki`, `alloy`) and container number 1, like the DNS and SSH containers, and `sind.observability.version`, the fingerprint of the stack sind created them with (see Monitoring). They have no `sind.realm`: a container with it and without `sind.cluster` counts as another cluster to `HasOtherClusters`, so the stack would keep the mesh, and itself, after the last cluster is deleted, and `sind get realms` would list a realm that has only a stack. The stack's volumes and the journal volume carry `sind.realm` and the Compose labels of `<realm>-mesh`, naming themselves after their service or `journal`.

### Enter and Exec

`sind enter` and `sind exec` run commands directly inside the target container via `docker exec`
with the working directory set to the data mount point (`/data` by default). This means commands operate on the shared data mount.
With `--user USER` they run as that cluster user (`docker exec -u USER`) in its home directory,
`/home/USER`, which is shared too (see Users).

`docker exec` always gets `-i`. `enter` adds `-t` when stdin is a terminal, for the login shell. `exec` adds `-t` only when stdin and stdout are both terminals, the rule of `sind ssh` (see sind ssh Implementation): `sind exec -- top` and `sind exec -- srun --pty bash` then get a pseudo-terminal, while output that a script captures or redirects keeps its line endings and its separate stderr.

`sind ssh` continues to use the SSH relay container, for a login through the node's sshd. Its ssh client runs in the relay container, so port forwarding (`-L`, `-R`, `-D`) and the files that options name (`-i`, `-F`) are the relay's; forwarding to the host goes through the exported `ssh_config` (see User SSH Client Integration).

## Networking

### Cluster Network

Each cluster has an isolated Docker bridge network:
- Name: `<realm>-<cluster>-net`
- Nodes can reach each other by container hostname

Nodes join it with gateway priority 1 (`--network name=<realm>-<cluster>-net,gw-priority=1`, Docker 28+), ahead of the mesh network's 0. Docker makes a container's hostname a DNS name on every user-defined network it joins, so the mesh holds `controller`, `db` and `worker-N` once per cluster of the realm. The embedded DNS answers a name from the first of the container's networks that knows it, ordered by gateway priority and then by network name. With the priority, the short names Slurm uses resolve to the node's own cluster whatever the cluster is called; without it, a cluster whose network name sorts after `<realm>-mesh` (`test`, `prod`) would resolve `controller` to the controllers of every cluster in the realm. A name the cluster lacks, such as `db` without a db node, still falls through to the mesh and resolves to another cluster's node. The cluster network is also the nodes' default gateway. `docker create` attaches a node to both networks, the cluster network first, as its network mode (`--network name=<realm>-<cluster>-net,gw-priority=1 --network <realm>-mesh`); Docker joins both when the container starts, as it would a network connected between create and start, so no `docker network connect` is needed.

### Mesh Network

All nodes of a realm also join its shared mesh network, which carries the realm's DNS server and SSH relay:

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-mesh` network, starts `sind-dns` and the SSH relay `sind-ssh` |
| Subsequent clusters | Connects cluster nodes to `sind-mesh`, updates DNS |
| Cluster deleted | Disconnects cluster nodes, updates DNS (unless it is the last cluster) |
| Last cluster deleted | Removes the monitoring stack's containers, if any, then `sind-dns` and `sind-ssh`, detaches any other container from `sind-mesh` and removes it, then the stack's volumes |

The mesh does not route traffic between clusters by DNS name: the mesh DNS records point at cluster network addresses, which the SSH relay (on every cluster network) and the host reach, but the nodes of other clusters do not, as Docker isolates bridge networks from each other. Nodes of different clusters reach each other on the mesh by container name (`sind-dev-controller`).

### External Containers

Containers outside sind, such as kind nodes, reach a realm's nodes by joining its networks: the mesh, for the DNS server, and every cluster network, for the addresses DNS answers with. `sind connect container CONTAINER...` attaches them to all of them, the mesh first, and skips the networks a container already joins; `sind disconnect container CONTAINER...` detaches them from every network of the realm, the mesh last. Both take the realm lock, so that no cluster network appears meanwhile, print nothing on success, and fail for a container that does not exist or is sind's own (it has the `sind.realm` label, or is in the mesh's Compose project, as the monitoring stack's containers are).

sind keeps no list of them: an external container is any container on the mesh network that is not sind's own (`cluster.ExternalContainers`), whether `sind connect container` or `docker network connect` put it there. `sind create cluster` connects them to the new cluster network right after the SSH relay (`cluster.ConnectExternal`); a container that cannot be connected is a warning, not a failed create. Deleting a cluster network or the mesh detaches whatever is still attached, stopped containers included, so external containers never block a delete and still start afterwards.

So external containers are connected to the clusters created while the realm has a cluster, that is, while its mesh exists. Deleting the realm's last cluster with `sind delete cluster` takes the mesh with it, and with it what made them external containers: sind then writes a `Warning:` to stderr that names the detached containers and the `sind connect container` command that attaches them to a cluster created later. `sind delete cluster --all` and `sind delete realm`, which delete everything on purpose, detach them without a warning. sind keeps no state to connect them again on its own; a new mesh may also have another DNS address, which a CoreDNS forward to the old one (`sind get coredns-config`) then misses.

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

The DNS container's address is pinned for the life of the mesh (see Host Reboot and Docker Daemon Restart). `sind get mesh` reports it, so DNS servers outside sind, such as the CoreDNS of a kind cluster on the same host, can forward the `<realm>.sind` zone to it.

The DNS container is lightweight and does not run systemd/sshd.

A realm with a monitoring stack also has `grafana.<realm>.sind`, `prometheus.<realm>.sind` and `loki.<realm>.sind`, at the services' mesh addresses: the stack runs on the mesh only (see Monitoring).

sind keeps the records in the Corefile's `hosts` block and writes them when it creates or deletes nodes, and when `sind power on`, `reboot` and `cycle` start nodes, as a node can get another address on each start. The server block for the zone has the `file` plugin after `hosts`, with a zone file `/zone.db` that holds only an SOA record (TTL and minimum 0, so the answer is not cached): CoreDNS's `hosts` plugin has no SOA and answers `SERVFAIL` for a name it lacks, which resolvers retry and report as a temporary failure, while `file` answers `NXDOMAIN`. A name that exists keeps `hosts`' answer, also for a type it has no record of (`AAAA`). Names with no record of their own, the zone's apex and `<cluster>.<realm>.sind`, answer `NXDOMAIN` too, as the zone file knows no names below the apex; nothing looks them up. Every write of the Corefile writes the zone file too, so a DNS container of an earlier sind gets it on the next update. A running CoreDNS reloads the Corefile on `SIGUSR1` (`docker kill -s USR1`), without dropping queries; a stopped DNS container is started instead. The write and the reload ignore Ctrl+C, and so does the rewrite of `known_hosts`, which replaces the file in one step (`docker.Client.ReplaceFile`: a temporary file next to it, renamed over it once complete), so that a write cut short, even by a Ctrl+C that reaches the docker CLI directly, leaves the old file rather than an empty one. Each realm-wide file has one writer at a time (the realm lock); the Corefile and `known_hosts` updates run in parallel, as they live in different containers.

#### Host DNS Resolution

When systemd-resolved runs on the host and polkit lets the user change its per-link settings (`org.freedesktop.resolve1.set-dns-servers`, `set-domains` and `revert`), `sind create cluster` makes the mesh bridge (`br-` and the first 12 characters of the mesh network's ID) a resolver link:

```
resolvectl dns <bridge> <sind-dns-ip>
resolvectl domain <bridge> ~<realm>.sind default.<realm>.sind
```

`~<realm>.sind` is a routing domain: the host sends queries for `*.<realm>.sind` to the realm's CoreDNS, and the link does not become a default route for other queries. `default.<realm>.sind` is a search domain, which systemd-resolved tries for single-label lookups from any process on the host, so a bare `controller` resolves to the `default` cluster's controller; each realm with a mesh adds its own. The setup is best-effort: without systemd-resolved, the polkit authorization or the bridge interface, sind skips it with a debug log line. `sind doctor` checks the polkit policy. Deleting the realm's last cluster runs `resolvectl revert <bridge>` before it removes the mesh network. The CLI turns it on with `mesh.Manager.HostDNS`, which is off by default for library callers.

### Host Reboot and Docker Daemon Restart

sind sets no restart policy: nodes keep the power state `sind power` gave them, and after a host reboot or a Docker daemon restart the mesh containers and the nodes stay stopped. `sind get cluster` shows a stopped DNS container or relay with ✗. `sind power on` starts the realm's DNS container, then the relay, then the nodes, re-registers the nodes' DNS records, and reapplies host DNS; `sind create cluster` and `sind create worker` also start a stopped mesh, DNS first, and so do `sind delete cluster` and `sind delete worker` before they remove nodes from a mesh that stays: `known_hosts` is read and written with `docker exec` in the relay.

The monitoring stack has no restart policy either. `sind power on`, `reboot` and `cycle` start its stopped containers after the nodes, wait until it is ready and register its DNS names again; Alloy's configuration stays, as the nodes keep their container IDs. A monitored `sind create cluster` and `sind create worker` on a monitored cluster start it too, and so do `sind delete cluster` and `sind delete worker` of a monitored cluster before they sync Alloy. `sind get cluster` shows a stopped service with ✗, and `sind get monitoring` with its state.

The DNS container's address is fixed into every node's and the relay's `--dns` when Docker creates them, and Docker does not keep a stopped container's address: a container that starts first can take it. sind therefore pins the address. A new mesh network gets a user-configured subnet, as Docker accepts `--ip` only on such a network, with the subnet's lower half as its `--ip-range`, the range Docker takes the addresses of the nodes and the relay from. The DNS container is created with `--ip` at the subnet's last address before the broadcast address (`172.18.255.254` in `172.18.0.0/16`), which no other container gets, so it has that address again in whatever order the containers start. The mesh network records it in the label `sind.dns.ip`; a DNS container removed by hand is created again at that address.

sind lets Docker choose the subnet: it creates the mesh network without one, so that Docker takes a subnet from its default address pools that no other network and no route of the host uses, reads the subnet and gateway with `docker network inspect`, removes the network and creates it again with them and the range. A network created in between can take the subnet. Docker then refuses the mesh's, with `Pool overlaps with other one on this address space` or, since Docker's address allocator does not check a subnet that comes with an `--ip-range` ([moby#46756](https://github.com/moby/moby/issues/46756)), with `failed to allocate gateway (...): Address already in use` when it shares the other network's subnet and gateway, or with the bridge driver's `networks have overlapping IPv4`, and sind starts over with the subnet Docker picks next, five times at most. Picking a free subnet itself would mean repeating Docker's checks against the other networks and the host's routes, and `docker info` lists only the pools configured in `daemon.json`, not Docker's built-in ones. The price is three more Docker calls when a realm's mesh is created, and a moment in which the mesh network exists without the subnet pinned: an interrupted `sind create cluster` can leave it like that, and it then works like a mesh of earlier sind versions.

The range is half the subnet, and it has to hold every container but the DNS container that a bridge network can take (1,022, see Limits), besides the subnet's first address and the gateway: sind pins the address only in a subnet of `/21` or larger. Docker's stock pools always give one (`/16` and `/20`). With smaller pools (`default-address-pools` with a `size` above 21), and when the subnet was taken five times, the mesh network keeps the subnet Docker gave it, without a pinned address.

A mesh without a pinned address, such as one created by an earlier sind version, works as before; sind does not create it again. Started before anything else on the mesh, its DNS container gets its old address back: it was the first container on the mesh, and Docker hands out the lowest free address. When it gets another one, sind warns (`Warning:` on stderr) and names the containers that still use the old address; they resolve neither `*.<realm>.sind` names nor external names until their clusters are created again (the relay with the realm's mesh, after the last cluster is deleted, when the realm's next mesh pins the address).

### Limits

- Each cluster network and each mesh takes one network from Docker's default address pools, which a stock daemon fills at about 30 networks, shared with every other network on the host. `default-address-pools` in `daemon.json` gives hosts that run many realms or clusters smaller pools; with a `size` of 21 or less, the networks are still large enough for a mesh to pin its DNS address (see Host Reboot and Docker Daemon Restart). When the pools are used up, Docker refuses the network (`all predefined address pools have been fully subnetted`, or `could not find an available, non-overlapping IPv4 address pool` in older versions); sind returns that error wrapping `docker.ErrAddressPoolsExhausted`, with a pointer to `default-address-pools`.
- A Linux bridge has 1,024 ports and the kernel reserves port 0, so one Docker bridge network holds at most 1,023 containers; past that, Docker fails to connect one with `exchange full`. The mesh holds the DNS container, the relay and every node of the realm, a cluster network its nodes and the relay. `sind create cluster`, in its preflight check before it creates the cluster's network, volumes and nodes, and `sind create worker`, before it starts a stopped mesh or creates a worker, list the containers connected to the realm's mesh and to the cluster network in one `docker ps -a --filter network=<realm>-mesh --filter network=<realm>-<cluster>-net`, stopped ones too, as they take a port again when they start. They refuse nodes that would not fit, counting the DNS container and the relay where the network lacks them, with an error that names the network, the containers it has, the number the command adds and the limit (`ErrNetworkFull`). Host limits such as inotify instances and memory bind long before.
- The realm lock serializes the clients that share a daemon (several users of a host, CI jobs that share the host's Docker socket, other `XDG_STATE_HOME`s) through its part on the daemon, `<realm>-lock` (see Realm Advisory Locking). Go programs that call `state.LockRealm` without `LockOptions.Client` take only the file lock and are not serialized against other clients; a mesh network that such a client created between the check and the create counts as existing, so that the other client's failure handling never removes it. A lock left by a process that was killed on another host or in another container stays until it is removed by hand, as sind cannot tell whether that process still runs. Waiting clients take a freed lock in no particular order. Sharing a realm is safe, not isolated: `sind delete cluster --all` deletes every cluster of the realm, another user's or job's too, cluster names must differ, and each user's exported SSH configuration follows only that user's own commands. Independent jobs still use a realm each.

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

When sind creates a node, it waits for sshd to start, then appends the public key from `sind-ssh-config` to root's `authorized_keys` and collects the host key that sshd serves, in the last step of the node's single setup `docker exec` (see Container Startup), as the cluster's setup waits for its slowest node. The node image ships no host keys, so each container generates its own on first boot and the pinned key identifies that node. The step runs these commands under `sh -e`, and it is the only step that writes to the setup's stdout:

```bash
docker exec <node> sh -c '... mkdir -p /root/.ssh
printf "%s\n" "$1" >> /root/.ssh/authorized_keys
ssh-keyscan -t ed25519 localhost' sh "$pubkey"
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
| `lock` | The realm lock's file part (see Realm Advisory Locking) |

The generated `ssh_config` (for default realm `sind`; sind writes the state directory's absolute path, in double quotes with `%` doubled and `\` and `"` escaped, as ssh would otherwise split a path with a space and expand its `%` tokens; a line it cannot parse makes ssh reject the file, and with it every connection that includes it):

```
Host controller controller.* controller-backup controller-backup.* db db.* api api.* submitter submitter.* worker-*
    CanonicalizeHostname yes
    CanonicalDomains default.sind.sind sind.sind

Host *.sind.sind
    IdentitiesOnly yes
    IdentityFile "/home/user/.local/state/sind/sind/id_ed25519"
    UserKnownHostsFile "/home/user/.local/state/sind/sind/known_hosts"
    User root
    StrictHostKeyChecking yes

Host controller.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/controller.default.sind.sind/22; cat <&3 & cat >&3; kill $!'

Host worker-0.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/worker-0.default.sind.sind/22; cat <&3 & cat >&3; kill $!'
```

The first block enables short-name resolution for the default realm: `ssh controller` expands to `controller.default.sind.sind`, and `ssh controller.dev` expands to `controller.dev.sind.sind`. ssh looks the candidates up in the host's DNS, which knows them where sind has pointed systemd-resolved at the mesh DNS for `*.<realm>.sind`, and reads the config again for the name it found. The block applies only to host names shaped like a node's, the node names alone or followed by `.<cluster>`, so ssh does not look up every other host it connects to under the sind domains first; `CanonicalizeMaxDots` keeps its default, 1, as these names have at most one dot. Other realms get no such block; use full names such as `controller.dev.ci.sind` there. While a node of that name exists, its short name reaches it rather than a host of the same name on the user's network. Short names need that host DNS (systemd-resolved with the polkit authorization `sind doctor` checks): without it, ssh falls back to the bare name, which matches no `Host` block (`Could not resolve hostname controller`); full names still work.

The realm's block authenticates with sind's key only (`IdentitiesOnly yes`). ssh would otherwise offer the keys of the user's agent first, and the nodes' sshd ends the connection after `MaxAuthTries` (6) failed keys, so six agent keys would lock the user out; the nodes need not see those keys either.

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

sind updates these files automatically when clusters or nodes are created/deleted, also after a `delete cluster` or `delete worker` that fails partway, reading the private key and `known_hosts` from the relay container in one `docker exec`; a relay container that no longer exists means the realm has no cluster. When the last cluster in a realm is deleted, `ssh_config`, `id_ed25519` and `known_hosts` are removed; the realm directory stays, as it holds the realm's `lock` file. The files are the invoking user's, so only that user's commands update them: in a realm shared with other clients of the daemon, their creates and deletes do not reach them.

## Monitoring

`monitoring: true` in the cluster config, or `sind create cluster --monitoring`, collects a managed cluster's Slurm metrics, its nodes' resource use and their journals into a monitoring stack that belongs to the realm: Grafana Alloy collects, Prometheus stores the metrics, Loki the logs, and Grafana shows them, as four containers on the realm's mesh network (`pkg/observability`). The first monitored cluster creates the stack, the realm's last cluster takes it along with the mesh, and the monitored clusters of a realm share it. Clusters without the setting get no extra mounts, an unchanged `slurm.conf`, and in a realm without a stack no extra docker calls; in a realm with one they are not collected.

The setting is fixed for the cluster's life, like `managed`: the journal mounts and `slurm.conf` are made when the nodes are created. `sind create worker` takes it from the cluster, which is monitored when its controller mounts the realm's journal volume, and has no flag of its own. A config with `monitoring` fails to parse in sind versions before the key, as unknown keys are errors.

### Stack

| Service | Container | Port | Address | Memory | Data volume |
|---------|-----------|------|---------|--------|-------------|
| Grafana | `<realm>-grafana` | 3000 | pinned, DNS address - 1; `grafana.<realm>.sind` | `1g` | `<realm>-grafana` at `/var/lib/grafana` |
| Prometheus | `<realm>-prometheus` | 9090 | pinned, DNS address - 2; `prometheus.<realm>.sind` | `1g` | `<realm>-prometheus` at `/prometheus` |
| Loki | `<realm>-loki` | 3100 | pinned, DNS address - 3; `loki.<realm>.sind` | `1g` | `<realm>-loki` at `/loki` |
| Alloy | `<realm>-alloy` | 12345 | dynamic, no DNS name | `512m` | `<realm>-alloy` at `/var/lib/alloy/data` |

Images and names are listed under Docker Resources. Grafana, Prometheus and Loki take the addresses just below the mesh DNS container's (`mesh.ServiceIP`): like it, they lie in the upper half of the mesh subnet, outside the `--ip-range`, so no other container takes them while the stack is stopped. A mesh without a pinned DNS address (see Host Reboot and Docker Daemon Restart) gives them dynamic addresses, which sind registers again whenever it starts the stack. No port is published, so realms and CI jobs never compete for port 3000.

- **Alloy** runs `run /etc/alloy/sind.alloy --server.http.listen-addr=0.0.0.0:12345 --storage.path=/var/lib/alloy/data --disable-reporting`: it listens on the mesh, as its default `127.0.0.1` would hide `/-/ready` and `/-/reload`, and reports no usage. It runs as root (`--user 0:0`), in the host's cgroup namespace (`--cgroupns host`) with the host's `/sys/fs/cgroup` mounted read-only, so that its embedded cAdvisor reads the nodes' cgroups, and mounts the journal volume at `/var/log/sind`, whose journal files are `root:systemd-journal 0640` and whose directories it clears and removes; `--security-opt label=disable` exempts it from SELinux labeling, which would deny both. It needs no Docker socket.
- **Prometheus** runs with `--config.file=/etc/prometheus/sind.yml --storage.tsdb.path=/prometheus --storage.tsdb.retention.time=7d --storage.tsdb.retention.size=2GB --web.enable-remote-write-receiver`. It scrapes nothing itself: Alloy writes every series through the remote-write receiver.
- **Loki** runs `-config.file=/etc/loki/sind.yaml`, the image's single-process configuration with filesystem storage on its volume, plus the compactor's retention of 168 hours (the stock configuration keeps logs forever), `log_level: warn` and no usage reports.
- **Grafana** runs the standard image with its provisioning (`/etc/grafana/provisioning`): Prometheus and Loki as datasources with the fixed UIDs `sind-prometheus` and `sind-loki`, which the dashboards refer to, not editable; and a dashboard provider for the sind dashboards in the folder `sind`, with `allowUiUpdates: false` and `disableDeletion: true`, so they can be copied but not changed in place. The Slurm cluster dashboard is the home dashboard (`GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH`). Dashboards refresh at most every 15 s, the shortest scrape interval (`GF_DASHBOARDS_MIN_REFRESH_INTERVAL=15s`): a faster refresh shows nothing new and only loads Grafana.

sind copies each service's configuration into its container with `docker cp` before the first start, as it does the Corefile. The dashboards are part of the sind binary, so a stack created by a newer sind gets that version's dashboards.

Grafana is open to whoever reaches it: there is no data or access to protect. Its environment makes every visitor an anonymous org Admin, with no login at all, and keeps it from fetching anything:

```
GF_AUTH_ANONYMOUS_ENABLED=true
GF_AUTH_ANONYMOUS_ORG_ROLE=Admin
GF_AUTH_DISABLE_LOGIN_FORM=true
GF_AUTH_DISABLE_LOGIN=true                     # no password login: admin/admin is unusable
GF_AUTH_BASIC_ENABLED=false
GF_AUTH_DISABLE_SIGNOUT_MENU=true
GF_PLUGINS_PREINSTALL_DISABLED=true            # no plugin downloads at start: offline and pinned
GF_PLUGINS_PLUGIN_ADMIN_ENABLED=false
GF_PLUGINS_PUBLIC_KEY_RETRIEVAL_DISABLED=true
GF_ANALYTICS_REPORTING_ENABLED=false
GF_ANALYTICS_CHECK_FOR_UPDATES=false
GF_ANALYTICS_CHECK_FOR_PLUGIN_UPDATES=false
GF_ANALYTICS_FEEDBACK_LINKS_ENABLED=false
GF_NEWS_NEWS_FEED_ENABLED=false
GF_SECURITY_DISABLE_GRAVATAR=true
GF_SNAPSHOTS_EXTERNAL_ENABLED=false            # no publishing to an external snapshot service
```

An org Admin can use Explore, edit dashboards and datasources and create service account tokens, but not install plugins or change server settings other than SMTP. Anyone who reaches the mesh, local users of the host and jobs on the nodes alike, acts as that Admin, and can point a new datasource at any address the Grafana container reaches. Prometheus and Loki are unauthenticated on the mesh as well. With plugin preinstallation off, the Logs and Metrics Drilldown apps are absent; Explore remains.

From the host, Grafana is at `http://grafana.<realm>.sind:3000` when host DNS works (see Host DNS Resolution), otherwise at the address `sind get monitoring -o json` prints. For a Docker host reached over SSH, `ssh -L 3000:grafana.sind.sind:3000 devbox` (or the address) forwards it.

### Collection

sind generates Alloy's whole configuration from Docker state: the realm's node containers (with `sind.role`) that mount its journal volume, and the daemon's cgroup driver (`docker info`). It writes `/etc/alloy/sind.alloy` with `docker cp` and asks Alloy to reload it with `POST /-/reload`, through `curl` in the Grafana container, as Alloy's image has no `curl`; the endpoint returns Alloy's error where `SIGHUP` would only log it. Every sync rewrites the file whole, so no state is kept and nothing is left to repair after an interrupted command. Alloy reaches the nodes by container name (`sind-dev-controller`), which Docker's DNS resolves to their mesh addresses; the `*.<realm>.sind` names point at cluster network addresses that the mesh does not reach.

| Source | Alloy component | Target | Interval |
|--------|-----------------|--------|----------|
| Slurm metrics | `prometheus.scrape` per endpoint | `/metrics/jobs`, `/metrics/nodes`, `/metrics/partitions` and `/metrics/scheduler` on port 6817 of each managed controller | 30 s |
| Slurm users and accounts | `prometheus.scrape` | `/metrics/jobs-users-accts` on the same | 60 s |
| Slurm daemon health | `prometheus.exporter.blackbox` | slurmctld `/livez` and `/readyz` (6817) on managed controllers, slurmd `/readyz` (6818) on managed workers, a TCP connect to slurmdbd (6819) on a managed db node | 15 s |
| Containers and units | `prometheus.exporter.cadvisor` | each monitored node's cgroup and the cgroups below it | 15 s |
| Logs | `loki.source.journal`, one per node | `/var/log/sind/<cluster>/<node>` | as written |

- The metrics scrapes do not follow redirects: a standby slurmctld answers `/metrics/*` with 303, so its `up` is 0, and `up == 1` marks the controller in control, steadily through reconfigures, unlike `/readyz`. Following the redirect would be wrong, as its `controller` resolves on the mesh to any cluster's controller.
- slurmdbd serves no HTTP, hence the TCP probe. sackd, slurmrestd, munge, sshd and MariaDB get no probe: each unit runs in a cgroup of its own, whose use cAdvisor reports.
- cAdvisor runs with `docker_only = false` and `raw_cgroup_prefix_allowlist` listing each node's cgroup, `/system.slice/docker-<id>.scope` under the systemd cgroup driver and `/docker/<id>` under cgroupfs, from the container IDs. A relabeling generated with it maps the cgroup path to `cluster`, `node` and `role`, the rest of the path to `cgroup`, a `job_<id>` in it to `job_id`, and a `<unit>.service` at its end to `unit`; series of other cgroups are dropped.
- Each journal source sets `unit` from `_SYSTEMD_UNIT`, or `UNIT` for PID 1's messages about a unit ("Started", "Failed with result"), as `journalctl -u` and so `sind logs` show them, and `level` from the journal priority. journald files every stderr line of a service at priority info, so for the Slurm daemons' units (slurmctld, slurmd, slurmdbd, sackd, slurmrestd) the level is parsed from the message (`fatal:` becomes `crit`, `error:` `err`, `warning:` and `debug...:` stay), and for `mariadb.service` from `[ERROR]`, `[Warning]` and `[Note]` (`err`, `warning`, `info`).
- Every series and log line carries `realm`, `cluster`, `node` and `role` (the probes also `service` and `check`), so a dashboard can link a panel to the matching logs.
- Component names are `n_<cluster>__<node>` with `-` turned into `_`, as Alloy labels must be identifiers and cluster names may start with a digit. They stay the same across sind versions: Alloy keys a journal's read position by them.

What a node gets depends only on its role and its `sind.managed` label (`observability.Collect`):

| Node | Journal | cgroups | Slurm metrics | Health probe | Image probe |
|------|---------|---------|---------------|--------------|-------------|
| controller, controller-backup | yes | yes | 5 endpoints; a standby's `up` is 0 | slurmctld `/livez`, `/readyz` | yes, with slurmctld (replaces `slurmctld -V`) |
| managed worker | yes | yes | no (slurmd has none) | slurmd `/readyz` | its image, with slurmd |
| managed db | yes, slurmdbd and MariaDB included | yes | no | slurmdbd TCP | MariaDB's error log setting only |
| api, submitter | yes | yes | no | no | no |
| unmanaged worker, db or api node | yes | yes | no | no | no |
| any node of an unmanaged cluster | rejected by validation | | | | |

Unmanaged nodes in a managed cluster exist to test someone's provisioning, so their logs and resource use are worth collecting; Slurm probes there would only show sind's own daemons as down.

### Nodes of a monitored cluster

- `slurm.conf` gets `MetricsType=metrics/openmetrics`, unless `slurm.main` sets it (see slurm.conf).
- Every node mounts its own directory of the realm's journal volume at `/var/log/journal` (`--mount type=volume,source=<realm>-journal,target=/var/log/journal,volume-subpath=<cluster>/<node>`; `-v` cannot mount a subpath). With the directory present, journald's default `Storage=auto` writes a persistent journal there, into a `<machine-id>/` it creates. A directory per node, not per cluster, is needed: the nodes of an image very likely share the machine ID baked into it, and in one directory they would all write the same `<machine-id>/system.journal`. The subpath must exist when the container is created: `sind create cluster` makes it with the journal helper (see Container Startup), `sind create worker` with `docker exec` in Alloy.
- The node entrypoint writes `/etc/systemd/journald.conf.d/zz-sind-monitoring.conf` with `SystemMaxUse=64M` before it execs `/sbin/init`. journald sorts the drop-ins of `/etc`, `/run` and `/usr/lib` by name and the last wins, so the `zz-` name also replaces a cap that a custom image, or the provisioning under test on an unmanaged node, sets; without it, an image without a cap could write up to 4 GB per node to the shared volume.
- The journal moves from the `/run` tmpfs, which counts against the node's memory, to the volume on the host's disk, up to 64 MB per node until the stack goes. It survives `sind power reboot` and `cycle`, so `sind logs` on a monitored node also shows its earlier runs.
- A node name can come back, as a cluster deleted and created again or a worker removed and added again. The journal helper empties `<cluster>/` at `sind create cluster`, and `sind create worker` empties `<cluster>/<node>/`, so neither `sind logs` nor Loki shows the earlier node's journal again.
- Every node gets the label `sind.monitoring`, `true` or `false`, for listings and filters. Docker merges an image's labels into a container's, so an image could carry it; what decides whether a node is collected, and whether a cluster is monitored (its controller), is the node's mount of `<realm>-journal`, which an image cannot forge.
- Nothing else runs in the node: no agent, and no extra docker call per node.

### Checks

A monitored cluster is checked at three points: its config before any docker call, its images before any node exists, and slurmctld's HTTP endpoint once Slurm runs.

The config checks are in `config.Validate` (see Validation Rules), so library callers of `cluster.Create` get them too: monitoring needs a managed controller, and `slurm.main` must not keep the collector from slurmctld's metrics with another `MetricsType`, `CommunicationParameters=disable_http`, `MetricsAuthUsers` (the collector has no JWT) or `PrivateData` without `MetricsParameters=ignore_private_data`. The CLI ORs `--monitoring` into the config before `Validate`, so a monitored unmanaged config fails before the realm lock is taken.

Slurm 26.05 and later serve HTTP on slurmctld's and slurmd's RPC ports, telling it apart from RPC by a connection's first bytes, once an HTTP parser, a URL parser and a TLS plugin load; with HTTP on, they also load an HTTP authentication and a data parser plugin, and abort without them. The HTTP parser is built only when configure finds its library, and without one Slurm logs only at debug level that HTTP is disabled. The image probe (`slurm.ProbeImage`) therefore runs one throwaway container per distinct image (`--network none --entrypoint /bin/sh`) in Phase 2, after the pull: the controllers' image with slurmctld, each managed worker group's image with slurmd (both in one run when they share an image), and a managed db node's image for MariaDB's error log setting. The run reports each daemon's version, the plugin directories (`PluginDir` from `slurm.main`, else the directory of the `libslurmfull` the first daemon links, which is Slurm's plugin directory whatever `--libdir` an image used, else the usual ones that exist), the plugin files of the needed types in them and whether their libraries resolve (`ldd`), and with a db node `my_print_defaults --mysqld`'s last log-error option. The image is untrusted: every line of the output must have one of the expected forms, with versions, paths and file names of the expected shapes, or the probe fails. The run of the controllers' image also gives the Slurm version, replacing `slurmctld -V` (see Slurm Version Discovery).

`ImageInfo.MonitoringProblems` lists what keeps an image from serving the endpoints, and the create fails with `cluster.ErrImageUnsupported` naming every problem, before any node container exists:

| Check | Passes when | Otherwise |
|-------|-------------|-----------|
| Daemon | the image has the daemon | `has no slurmd` |
| Version | `slurm X.Y.Z` with X.Y at least 26.05; 26.05 pre-releases count | `image ghcr.io/gsi-hpc/sind-node:25.11 cannot serve monitoring: Slurm 25.11.8 is older than 26.05, which monitoring needs` |
| Plugin directory | `PluginDir` is set, `ldd` finds `libslurmfull`, or a usual directory exists | `cannot find Slurm's plugin directory; set PluginDir in slurm.main` |
| HTTP parser, URL parser, TLS, HTTP authentication, data parser | a plugin of each type whose libraries resolve | `Slurm has no HTTP parser plugin in /usr/lib64/slurm; build it with --with-llhttp-parser`, or `Slurm's <plugin> in <dir> cannot load: a library it needs is missing` |
| OpenMetrics (controllers' image only) | `metrics_openmetrics.so`, loadable | the same forms |
| MariaDB error log (managed db image) | `--skip-log-error` is the last log-error option, or there is none | a warning only: `MariaDB writes its error log to a file, which monitoring does not collect; add skip-log-error to a my.cnf.d file` |

`sind create worker` on a monitored cluster probes the image of new managed workers the same way, with slurmd, whether it comes from `--image` or from the newest worker; unmanaged workers are not probed.

The probe cannot see everything: a plugin can fail at start, and edited configuration shapes the result. Readiness therefore adds the `slurmctld HTTP` check on monitored clusters, after slurmctld is ready (see Readiness Checks): `GET /metrics/nodes` on port 6817 must answer 2xx on `controller`, and `GET /livez` on `controller-backup`, whose standby slurmctld redirects the metrics.

### Lifecycle

Every command below holds the realm lock, so the stack needs no locking of its own.

| Command | Effect on monitoring |
|---------|----------------------|
| `create cluster`, monitored | Validation; in Phase 2 the stack's images pulled with the node images, the image probe, the journal volume and the nodes' directories; in Phase 3 the nodes mount them; in Phase 4 the stack created, or started or upgraded, waited for and synced with the new nodes, beside the Slurm daemons, and its DNS names registered after the nodes' (see Container Startup) |
| `create cluster`, not monitored | Nothing; a stack of the realm is left as it is, stopped or not |
| `create worker` on a monitored cluster | The image probe for managed workers; before the workers exist, the stack started, or created again if it was removed, and its DNS names registered, and the workers' journal directories emptied or made with `docker exec` in Alloy; after Slurm, a sync. A failed create worker takes the workers out of the stack again |
| `delete worker` of monitored workers | After the containers are gone: the stack started if stopped, a sync, then the workers' directories removed |
| `delete cluster`, monitored, not the realm's last | After the containers are gone: the stack started if stopped, a sync, then the cluster's directory removed. The collected metrics and logs stay until retention or the stack's removal; the stack stays while the realm has a cluster, monitored or not |
| `delete cluster` of the realm's last cluster, and `--all` | The stack's containers removed, then the mesh, whose network must have no endpoints left, then the stack's volumes and the journal volume, which Docker removes only once no container refers to them. `--all` deletes clusters in parallel without a sync each; when a cluster fails to delete and the mesh stays, one sync takes the deleted ones out of the stack |
| `power on`, `reboot`, `cycle` | The stack's stopped containers started after the nodes, waited for, and their DNS names registered again; container IDs do not change, so Alloy's configuration stays |
| `power shutdown`, `cut`, `freeze`, `unfreeze` | Nothing: probes and `up` show the node down, a frozen one timing out |

The failures that come after the work is done, in `delete worker`, `delete cluster` and the rollback of `create worker`, are warnings: Alloy reads what is gone until the next sync, and a directory left behind is emptied when a node of its name comes back, or goes with the stack. A failed `create cluster` removes the stack when the mesh goes too, or when the create made the stack and no other cluster of the realm is monitored; its containers before the mesh, its volumes after it.

The stack is ready when Grafana's `/api/health`, Prometheus's `/-/ready`, Loki's `/ready` and Alloy's `/-/ready` answer, which sind checks with `curl` in the Grafana container, Grafana first. sind waits for it within `--wait`, and at most two minutes (`observability.DefaultReadyTimeout`), so tests and CI jobs can query the stack once the create returns.

Each stack container carries `sind.observability.version`, a fingerprint of the stack sind creates: the first 16 hex digits of a SHA-256 over every service's container arguments (images, environment, mounts, memory caps, commands) and the files sind provisions it with (Prometheus's and Loki's configuration, Grafana's provisioning and dashboards), but not Alloy's configuration, which each sync rewrites. When `sind create cluster` or `sind create worker` finds a container with another fingerprint, as after a sind upgrade that changed an image, a configuration or a dashboard, it removes the container and creates it again, keeping its volume, so the stack never runs one sind version's configuration with another's images.

### Dashboards

The dashboards are generated in `pkg/observability` for sind's labels and versioned with sind, provisioned into the folder `sind`. Each has a realm and a cluster variable; the logs dashboard filters further.

| Dashboard | Built from | Shows |
|-----------|------------|-------|
| Slurm cluster (home) | OpenMetrics | The controller in control; nodes, idle and down or draining nodes, running and pending jobs; jobs and nodes by state; CPUs and memory allocated against allocatable; pending, running and allocated CPUs per partition; scheduler and backfill cycles, queues and slurmctld threads |
| Nodes | cAdvisor and OpenMetrics | Per node: the container's CPU, memory, processes and disk I/O next to what Slurm has allocated on it; CPU and memory per unit. Slurm reports allocation per node and states per cluster only |
| Service health | probes, metrics scrape and Loki | The Slurm daemons' probes as a node by service matrix and their durations; which controller is in control, from the scrape's `up`; errors and warnings per unit, and the latest ones, Slurm, MariaDB and systemd alike |
| Users and accounts | OpenMetrics and cAdvisor | Running and pending jobs and allocated CPUs per user and per account; CPU and memory per running job, from its cgroup on the workers |
| Logs | Loki | The nodes' journals, filtered by node, unit, level and a search text, with the log volume by level. The other dashboards link their node panels into it |

Slurm reports memory in MiB, as `slurm.conf` states `RealMemory`, whatever the `_bytes` in its metric names says; the panels say so. Job history, efficiency and timelines as `sacct` shows them need accounting data, which no metric carries.

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

Runs a command on the same target as `sind enter`, through `docker exec` in `/data` rather than the SSH relay, with a pseudo-terminal when stdin and stdout are terminals (see Enter and Exec).

## Container Images

### Generic Image

sind provides a generic multi-role image that works for all node types, built for each supported Slurm release line:

```
ghcr.io/gsi-hpc/sind-node:latest                # newest release line
ghcr.io/gsi-hpc/sind-node:<YY>.<MM>             # newest patch release of a line, e.g. 25.11
ghcr.io/gsi-hpc/sind-node:<YY>.<MM>.<patch>     # a patch release, e.g. 25.11.8
ghcr.io/gsi-hpc/sind-node:vX.Y.Z                # newest release line at sind release vX.Y.Z
ghcr.io/gsi-hpc/sind-node:vX.Y.Z-<YY>.<MM>      # a release line at sind release vX.Y.Z
```

The default image when `defaults.image` is not specified is `config.DefaultImage`. Release builds set it with `-ldflags -X` (`.goreleaser.yaml`) to `vX.Y.Z`, the image published for their own tag, so a sind upgrade switches to the image it was released with, which Docker then pulls, and a pinned sind version keeps its Slurm version. Builds from source (`make build`, `go install`) keep `latest`. The image workflow (`.github/workflows/image.yml`) publishes `latest`, `<YY>.<MM>` and `<YY>.<MM>.<patch>` from `main` when the image build changes, and `vX.Y.Z` and `vX.Y.Z-<YY>.<MM>` for each release tag; it publishes from no other ref. Every week, and when run by hand on `main`, it rebuilds the images of `main` and of the newest release with the current Rocky Linux packages: the runtime stage, which runs `dnf -y upgrade`, is built without the cache (`REFRESH_PACKAGES=true` sets the bake target's `no-cache-filter`), while the compiled components come from the cache. Each published multi-platform image gets a build provenance attestation (`actions/attest`, stored in the registry), which `gh attestation verify oci://ghcr.io/gsi-hpc/sind-node:<tag> --repo GSI-HPC/sind --signer-workflow GSI-HPC/sind/.github/workflows/image-build.yml` checks. The maintainer publishes a release once the image workflow of its tag has finished, since its binaries refer to these tags.

The generic image:
- Published for linux/amd64 and linux/arm64
- Based on Rocky Linux 10
- Builds Slurm, OpenMPI, PMIx, PRRTE, UCX and libjwt from source
- Contains the Slurm daemons (slurmctld, slurmdbd, slurmd, and from Slurm 26.05 on slurmrestd), munge, sshd, MariaDB and a full MPI stack
- MariaDB's data directory is initialised at build time, so the first `systemctl enable --now mariadb` on a db node skips `mariadb-install-db`
- MariaDB writes its error log to the journal, like the Slurm daemons: `/etc/my.cnf.d/mariadb-sind.cnf` sets `skip-log-error`, which unsets the `log-error=/var/log/mariadb/mariadb.log` of Rocky Linux's `mariadb-server.cnf`, so `sind logs db mariadb` shows MariaDB's own messages and `/var/log/mariadb/mariadb.log` stays empty
- Slurm is built with `--with-pmix` for native PMIx job launch support
- Ships what the identity modes need (see Identity Modes): nss_slurm as `/usr/lib64/libnss_slurm.so.2`, built from Slurm's `contribs/nss_slurm`; Slurm's `auth/slurm` and `cred/slurm` plugins, built with libjwt 1.x (`--with-jwt`), and the `serializer/json` plugin they load (`--with-json`); and `sackd`. The build fails without them
- Ships slurmrestd from Slurm 26.05 on, built with Rocky Linux's llhttp (`llhttp-devel` from CRB at build time, `llhttp` at run time), with its JWT plugin (`rest_auth_jwt.so`), its systemd unit and a `slurmrestd` system user, created after `slurm` so that `slurm` keeps its uid. Slurm 25.11's configure knows only the nodejs http-parser, which upstream archived and Rocky Linux 10 does not package, so the 25.11 images have no slurmrestd; one Dockerfile builds both, and its build check requires slurmrestd unless `slurmctld -V` reports 25.x
- Ships no SSH host keys: each container generates its ed25519 key on first boot
- Caps the systemd journal at 32 MB in `/run`, which counts against the node's memory limit, and 64 MB on disk
- sind enables the appropriate services based on node role

The `Dockerfile` uses a multi-stage build with a shared `builder-base` stage. UCX, PMIx and libjwt build in parallel; PRRTE depends on PMIx, Slurm on PMIx and libjwt, and OpenMPI on UCX, PMIx and PRRTE. UCX, PMIx, PRRTE, OpenMPI and libjwt are pinned only in the Dockerfile: their versions are `ARG` defaults, the tarballs are checked with `ADD --checksum`, and libjwt, which publishes no 1.x release tarballs, is cloned by tag and checked against `LIBJWT_COMMIT`. The Slurm version and tarball checksum are build arguments without defaults: `SLURM_RELEASES` in `docker-bake.hcl` lists one Slurm release per supported release line, newest first, and each becomes a bake target `slurm-<YY>-<MM>` tagged `<version>` and `<YY>.<MM>`, the first one also `latest`. Targets build for linux/amd64 and linux/arm64; CI builds each platform on a native runner and merges them into one multi-platform image per tag, while `make image` builds for the host platform only. The image workflow builds all targets of a platform in one build, so the release lines share every layer up to the Slurm one: the Dockerfile copies Slurm last and declares the version `ARG`s right before the `LABEL`.

### Custom Images

Custom images must provide:

**All roles:**
- systemd as init at `/sbin/init`, and `/bin/sh`: sind starts each node with its own `/bin/sh` entrypoint, which execs `/sbin/init` (see Container Startup), so node containers do not use the image's `ENTRYPOINT` and `CMD`. sind's helper containers run commands in the image directly, so it should not set an `ENTRYPOINT` that wraps them
- `STOPSIGNAL SIGRTMIN+3`, systemd's shutdown request, so that `sind power shutdown` and `sind power reboot` shut the node down cleanly
- sshd service (enabled, sind injects authorized_keys at runtime), with no host keys in the image: each container generates its ed25519 host key on first boot (on RHEL-family images `sshd-keygen@.service` does), so nodes do not share the key sind pins
- `/etc/shadow` readable by root without `CAP_DAC_OVERRIDE` (e.g. `0400 root:root`; Rocky's default `0000` is not). On nodes with `apparmor=unconfined`, the host's `unix-chkpwd` AppArmor profile (e.g. Ubuntu 24.04) denies that capability, and sshd's `pam_unix` account check would refuse root
- munge service (enabled)
- Slurm client tools (srun, sbatch, squeue, etc.)
- Slurm's `mpi/pmix` plugin (`mpi_pmix.so`, built when Slurm finds PMIx, `--with-pmix`): the generated `slurm.conf` sets `MpiDefault=pmix`, and without the plugin every `srun` fails with "Invalid MPI type 'pmix'". An image without it needs `MpiDefault=none` in `slurm.main`
- `munge` and `slurm` users with the same uids as in the controller's image: sind sets the owner of the munge key, `slurm.key` and `slurmdbd.conf` from there, and munged, slurmctld, slurmdbd and, with identity `clientIds`, sackd read them as these users

**Per-role requirements:**

| Role | Additional Requirements |
|------|------------------------|
| controller | slurmctld (installed, not enabled) |
| db | mariadb-server (MariaDB 10.4 or later, for the built-in `unix_socket` authentication) with the `mysql` client, root access to MariaDB over its local socket without a password, slurmdbd (installed, not enabled) with a unit that creates `/run/slurmdbd` for the `slurm` user, and `/var/log/slurm` writable by it |
| worker | slurmd (installed, not enabled) |
| api | slurmrestd (Slurm built with an HTTP parser, llhttp from 26.05 on, and `--with-jwt`) with its systemd unit, installed but not enabled, running it as a user other than root and SlurmUser (Slurm's unit: `slurmrestd`) and listening on port 6820; sind adds a drop-in that sets `SLURMRESTD_SECURITY` (see API Node) |
| submitter | Slurm client tools only |

sind enables Slurm services based on the node's role once every node is ready (`systemctl enable --now`). Services should be installed but not enabled in the image.

The identity modes `nssSlurm` and `clientIds` need more from the image: see the "Image needs" row under Identity Modes.

The Slurm requirements apply to managed clusters only. sind neither runs nor queries Slurm on an unmanaged cluster, so its image may leave Slurm for the provisioning under test to install.

**With monitoring** (see Monitoring): the controllers' image and the image of each managed worker group need Slurm 26.05 or later with the plugins its daemons serve HTTP with, in the plugin directory (`PluginDir`, else the directory of the `libslurmfull` the daemon links): an HTTP parser (`http_parser_*.so`, built with `--with-llhttp-parser` or `--with-libhttp-parser`), a URL parser, a TLS plugin, an HTTP authentication plugin and a data parser, and on the controllers `metrics_openmetrics.so`, each with its libraries resolving; the probe needs `/bin/sh`, `ldd`, `awk` and `grep` there. Every node's journald must keep the persistent journal under `/var/log/journal` (`Storage=auto` or `persistent`), and the Slurm daemons must log to the journal (stderr or syslog, as Slurm's own units do), not only to a file. On a managed db node's image, MariaDB's error log must go to the journal too (`skip-log-error`; Debian-family packages leave it unset): `my_print_defaults` must report `--skip-log-error` as the last log-error option, or none; otherwise `sind create cluster` warns that MariaDB's log is not collected. sind writes a journald drop-in that caps the persistent journal at 64 MB, `zz-sind-monitoring.conf`, whose name sorts after the drop-ins images usually ship, so it also replaces a cap the image sets.

**With users** (managed or unmanaged): the nodes that get the users need `groupadd`/`useradd` and `/bin/bash`, the controller `/etc/skel`; the image must have no user or group with a declared name or ID, and IDs from 1000 up, which sind assigns, must be free unless the configuration sets them.

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
├── slurm.key               # auth/slurm key (identity clientIds only)
└── jwt_hs256.key           # auth/jwt key (managed api node only)
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

<JWT parameters: a managed api node (see API Node)>

<accounting parameters: a managed db node (see Database Node)>

<metrics parameters: monitoring, MetricsType=metrics/openmetrics (see Monitoring)>

<slurm.main: its content, or an include line per fragment>
include /etc/slurm/sind-nodes.conf
```

- The first block is fixed, as sind's volumes, images and readiness checks depend on it; a backup controller adds `SlurmctldHost=controller-backup` to it, and `SlurmctldTimeout=20` unless `slurm.main` sets it (see Backup Controller).
- Every other parameter is set unless `slurm.main` sets it, so that a value there replaces sind's instead of following it (Slurm would take the later one and log an error for the duplicate).
- `sind-nodes.conf` is included after `slurm.main`, so that the `NodeName=DEFAULT` and `PartitionName=DEFAULT` lines of `slurm.main`, which apply only to the lines after them, reach sind's nodes and partition `all`, e.g. `PartitionName=DEFAULT DefaultTime=00:30:00` for a default time limit. slurmctld reads every node before any partition, so a partition in `slurm.main` may name sind's nodes (`Nodes=worker-[0-1]` or `ALL`).
- `TaskPlugin=task/cgroup` leaves out `task/affinity`. A node's CPUs are a CPU quota (`--cpus`), not a cpuset, so every worker sees all host CPUs, and `task/affinity` would bind the tasks of every worker, of every cluster on the host, to the same first host CPUs. To test CPU binding (`--cpu-bind`), set `TaskPlugin=task/cgroup,task/affinity` in `slurm.main`. Managed workers then get `--cap-add SYS_NICE`, unless their `capDrop` lists `SYS_NICE` or `ALL`: slurmstepd sets each task's CPU affinity after the task has switched to the job's user, and root needs `CAP_SYS_NICE`, which Docker drops by default, to change the affinity of another user's process; without it, every job of a user other than root fails with `task_g_set_affinity` ("Slurmd could not execve job"). `sind create worker` reads the `TaskPlugin` from `slurm.conf` and the files it includes on the config volume, so workers added later match. Nothing else adds `SYS_NICE`.
- `MpiDefault=pmix` makes `srun` launch through PMIx, which needs Slurm's `mpi/pmix` plugin (see Custom Images); set `MpiDefault=none` in `slurm.main` for an image without it.
- `ReturnToService=2` returns a `DOWN` worker to service when its slurmd registers with a valid configuration, e.g. after `sind power cut` and `sind power on`.
- `DefMemPerCPU` is the smallest `RealMemory`/`CPUs` over the managed workers, in MB, rounded down, and is left out when `slurm.main` sets `DefMemPerCPU` or `DefMemPerNode`. Slurm's defaults, `SelectType=select/cons_tres` with `SelectTypeParameters=CR_Core_Memory`, make memory a consumable resource, and without a default a job that does not ask for memory gets all of a node's memory: a worker would run one such job at a time, whatever its `cpus`. With it, each CPU of every worker can run such a job. sind writes `slurm.conf` once, so a worker added later with less memory per CPU runs fewer jobs without `--mem` at a time than it has CPUs.

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
- Partition `all` holds every managed worker and is the default partition. It sets nothing else, so Slurm's defaults apply (`MaxTime=UNLIMITED`, `State=UP`, no `DefaultTime`) unless a `PartitionName=DEFAULT` line in `slurm.main` changes them. When `slurm.main` declares a default partition of its own (a `PartitionName` line other than `DEFAULT` with `Default=YES`), sind writes `Default=NO` on `all`: slurmctld takes the last default partition it reads, and `sind-nodes.conf` comes last. `sind create worker` decides the same way, from `slurm.conf` and the files it includes on the config volume, when it writes the line again: after `sind delete worker` took it along with the last managed worker, or on a cluster created without managed workers.
- `sind create cluster` generates the file; `sind create worker` adds new nodes (unless `--unmanaged`), replacing a definition of the same name, and removes them again when it fails; `sind delete worker` removes them (for managed nodes). Both rewrite only the `Nodes=` list of the partition line and keep everything else, so edits to the other lines survive; the line goes with the last managed worker, and `sind delete worker` never adds one. Both replace the file in one step: a temporary file next to it takes its place (`mv -f`) only once it holds all of the new content, and the write ignores Ctrl+C and the `--wait` deadline, so an interrupted command never leaves the file cut short, which would take every worker out of Slurm. To change sind's nodes and partition, prefer `NodeName=DEFAULT` and `PartitionName=DEFAULT` lines in `slurm.main`; their `CPUs`, `RealMemory` and `Default` stay sind's.
- Nodes with `managed: false` in the cluster config are excluded from `sind-nodes.conf`. To add custom node definitions, put them in `slurm.main` or in a separate file included from `slurm.conf`.

#### cgroup.conf

sind generates a minimal `cgroup.conf`, `CgroupPlugin=autodetect`, which selects cgroup v2. With `ProctrackType=proctrack/cgroup`, `TaskPlugin=task/cgroup` and `JobAcctGatherType=jobacct_gather/cgroup`, Slurm puts every job and step into a cgroup to track its processes and gather its usage, but it constrains nothing. Constraints are opt-in through the `slurm.cgroup` section:

- `ConstrainRAMSpace=yes` limits each job to the memory it was allocated: `--mem`, or `DefMemPerCPU` per CPU (see slurm.conf). A worker's `RealMemory` is its whole container memory limit, with no reserve for slurmd, the other daemons and `/tmp`, so jobs that together use all of it can make the container's OOM killer hit the daemons. Leave room with a smaller `DefMemPerCPU` in `slurm.main`.
- `ConstrainCores=yes` confines each job to a cpuset of its allocated CPUs. sind limits workers with a CPU quota, not a cpuset, so every worker sees all host CPUs, and Slurm maps a worker's CPU N to host CPU N: the jobs of every worker, of every cluster on the host, run on the same low-numbered host CPUs. Use it to test the setting, not for throughput. `task/affinity` binds the same way (see slurm.conf).

#### slurmdbd.conf

Only generated for a cluster with a managed db node (not with `managed: false`), together with the accounting parameters in `slurm.conf`; see Database Node. It authenticates with `AuthType=auth/munge`, or with identity `clientIds` with `AuthType=auth/slurm` and `AuthInfo=use_client_ids`; `CredType` is a `slurm.conf` parameter only. With a managed api node it also gets the JWT parameters (see API Node).

#### User Customization

sind delivers a working starter configuration. The `slurm` config key allows extending it declaratively at creation time (see Slurm Configuration Sections above). For post-creation changes, the `/etc/slurm` volume is writable on the controller node.

Users may:
- Use `slurm.main`, `slurm.cgroup`, etc. to extend config at creation time
- Edit config files directly after creation (sind does not modify them after creation)
- Add additional include files for custom configuration
- Replace the entire configuration (but `sind create worker` will then fail for managed nodes)

## Slurm Version Discovery

sind does not manage Slurm versions directly—the version is implicit in the chosen container images. sind discovers the Slurm version of the controller's image to show it in CLI output (the `SLURM` column, `slurm_version` in JSON) and to label the node containers with it. The generated configuration does not depend on the version.

sind skips the discovery for unmanaged clusters: the Slurm the user provisions may differ from the one in the image. On a monitored cluster, the image probe replaces it (see Monitoring): its run of the controllers' image reports the version too, so a cluster whose nodes share one image starts no extra container.

### Discovery Method

While it creates the cluster network and volumes, sind runs an ephemeral container of the controller's image:

```bash
docker run --rm <controller image> slurmctld -V
# Output: "slurm 26.05.5"
```

The version must have the form `X.Y.Z`, optionally with a pre-release suffix such as `-0rc1` (`[0-9A-Za-z.]`); any other output, such as a version followed by a terminal control sequence from an untrusted image, fails the create.

The discovered version is stored as a label on each node container:

```
--label sind.slurm.version=26.05.5
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

A realm's monitoring stack takes the names `grafana.<realm>.sind`, `prometheus.<realm>.sind` and `loki.<realm>.sind` (see Monitoring). They have no cluster part, so they cannot clash with a node's name.

## Realm Advisory Locking

Mutating operations acquire a per-realm lock to prevent concurrent modifications to shared realm state. It has two parts, taken in this order and released in reverse:

1. A `flock(2)` on a file in the invoking user's state directory, which orders the commands that share that directory, and the goroutines of one process, without a call to the daemon:

   ```
   $XDG_STATE_HOME/sind/<realm>/lock    # default: ~/.local/state/sind/<realm>/lock
   ```

2. The daemon lock: the network `<realm>-lock` on the Docker daemon, which exists while a command holds the lock. The realm's resources live on the daemon, which several clients can share: users of one host, CI jobs that share the host's Docker socket, containers that mount it, shells with another `XDG_STATE_HOME`. Each of them has a file lock of its own; they meet at the daemon.

The file lock stays as the fast path: commands of one user wait for each other in the kernel, and only one of them at a time polls the daemon.

### Protected operations

- `sind create cluster`
- `sind delete cluster` (single and `--all`) and `sind delete realm`
- `sind create worker`
- `sind delete worker`
- `sind connect container` and `sind disconnect container`
- `sind power on`, `sind power reboot` and `sind power cycle` (they start a stopped mesh and rewrite DNS records)

Read-only operations (`get`, `logs`, `ssh`, etc.) and the other `power` commands do not acquire the lock.

### Library callers

The lock is `state.LockRealm` in `pkg/state`, which also resolves the state directory (`state.Dir`, `state.RealmDir`). `cluster.Create`, `cluster.Delete`, `cluster.DeleteAll`, `cluster.WorkerAdd`, `cluster.WorkerRemove`, `cluster.PowerOn`, `cluster.PowerReboot` and `cluster.PowerCycle` take no lock themselves: their caller holds the realm lock for the whole operation, for `Create` from before `mesh.Manager.EnsureMesh`, as the CLI does. Without it, concurrent calls in one realm lose each other's DNS records, `known_hosts` entries and `sind-nodes.conf` lines, or remove each other's resources. `LockOptions.Client` is the daemon to take the daemon lock on; without it, `LockRealm` takes the file lock only, which does not serialize other clients of the daemon (see Limits). `LockOptions.OnWait` receives the holder of a lock it waits for (`state.LockHolder`, nil for the file lock), `OnWarning` the warnings below, and `Command` overrides the command line the lock records (the process's arguments). `state.LockNetworkName` names the daemon lock.

`state.Holder(ctx, client, realm)` tells who holds the lock without taking it, for a caller that only shows it: the `LockHolder` (command, host, PID and since when) read from the lock network's labels, or nil with no error when the network does not exist, as nobody holds the lock then. It reads the daemon part alone: a command that waits for the file lock of its state directory holds nothing yet, and a network of the lock's name that sind did not make is a holder with no command and no host.

### Daemon lock

The daemon lock is a configuration-only network (`docker network create --config-only`) named `<realm>-lock`. dockerd refuses a network name that another network has: since Docker 25 the check is unconditional, and libnetwork checks and creates under a lock per name, so of the clients that create it at once exactly one succeeds; the others get `network with name <realm>-lock already exists`. A configuration-only network has the driver `null`: Docker takes no subnet from its address pools for it, creates no bridge device, and attaches no container to it, and creating it needs no image. Its labels describe the holder:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.realm` | `sind` | Realm namespace |
| `sind.lock.command` | `sind create cluster dev` | The holder's command line, with the program's base name |
| `sind.lock.host` | `build-07` | Host name of the machine or container the holder runs on |
| `sind.lock.pid` | `4242` | The holder's process ID |
| `sind.lock.boot-id` | `6b1f3a52-…` | `/proc/sys/kernel/random/boot_id` of the holder's kernel |
| `sind.lock.pid-ns` | `pid:[4026531836]` | The holder's PID namespace (`/proc/self/ns/pid`) |
| `sind.lock.token` | random | One acquisition: a create that failed after the daemon made the network removes only its own lock |

The network carries no Docker Compose labels, as it belongs to no Compose project. `sind get networks` leaves it out; as it has no `sind.cluster` label, cluster discovery and `sind delete cluster --all` do not see it, and completion lists containers only.

Alternatives that were rejected:

- A never-started container `<realm>-lock`. Container names are reserved atomically as well, but a container needs an image. The pinned CoreDNS image is on the daemon wherever the realm has a mesh, but not before the realm's first create, and not after a sind upgrade that bumps it: taking the lock would pull, and fail on an offline host, even for `sind delete cluster`. sind's container listings by `sind.realm` (`get realms`, `get nodes`) would need to filter it too.
- A bridge network `<realm>-lock`. It takes one of the about 30 networks of the default address pools and a bridge device while it is held, and fails when the pools are exhausted, which is when the user deletes clusters to free them.
- A volume: `docker volume create` of an existing name succeeds, so it cannot be a mutex.

### Behavior

- Each part is attempted non-blocking first; if both are free, the operation proceeds immediately, without a notice
- If another operation holds the file lock, sind prints `Warning: waiting for another sind command in realm "<realm>" to finish` to stderr, at every verbosity, and blocks until the lock is released: waiting is a state the user may have to act on, so it is not left to `-v`
- If another client holds the daemon lock, the warning names it from the lock's labels, e.g. `Warning: waiting for another sind command in realm "sind" to finish: sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)`, with the time the daemon created the lock in local time. sind tries again at intervals that double from 100 ms to 2 s; waiting clients take a freed lock in no particular order. What the warnings quote of the labels is escaped, as another client wrote it
- The waits have no timeout; context cancellation (e.g., Ctrl+C, SIGTERM) ends them
- The wait is also the progress wait `realm lock`, from the first wait until sind takes the lock or gives up, whose message names the holder of the daemon lock once it is known: on a terminal, the progress display shows it below the `Warning:` line, which stays
- Lock is released when the operation completes, success or failure, after the rollback of a failed `create cluster`, and after Ctrl+C or SIGTERM: sind removes the daemon lock by its network ID, so that it never removes another client's lock, with a context that the interrupt does not cancel, for at most 30 s, and then releases the file lock. If the removal fails, sind warns `Warning: releasing the realm lock: <error>; remove it with: docker network rm <realm>-lock`

### Stale daemon locks

A process killed with SIGKILL, or by a second Ctrl+C, cannot release the daemon lock; the kernel releases its file lock. sind takes over a daemon lock whose holder ran on the same host name, kernel boot (boot ID) and PID namespace as itself, so that the holder's PID means the same process to both, and that no longer runs (`kill(pid, 0)` fails with `ESRCH`; `EPERM` means it runs as another user). It warns `Warning: removing the realm lock of <holder>, which no longer runs`, removes the lock by ID and takes it. The boot ID tells a reboot, after which PIDs start over; a PID namespace's inode number can pass to a new namespace only after the old one has ended with all its processes, and a PID taken in the new one only makes the holder count as running.

A holder on another host, in another container (PID namespace) or since another boot cannot be checked from here, and sind never removes its lock, as that process may be running. After waiting a minute for such a lock, sind warns once: `Warning: the realm lock is still held by <holder>; if that command no longer runs, remove the lock with: docker network rm <realm>-lock`.

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
