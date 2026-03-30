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

- `sind create cluster` interprets the manifest once to generate the cluster
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
| Slurm daemon ready | Role-specific daemon responding (slurmctld, slurmd) |

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
| `log/slog` (stdlib) | Structured logging |
| `github.com/mattn/go-isatty` | TTY detection for interactive commands |
| `github.com/njayp/ophis` | MCP server framework |
| `github.com/spf13/afero` | Filesystem abstraction for testability |

**Nodeset expansion** (e.g., `worker-[0-2,5]` → individual hostnames) is implemented internally rather than using an external library, keeping the dependency footprint small.

### Docker Interaction

sind interacts with Docker by **shelling out to the `docker` CLI** rather than using the Docker SDK for Go. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- Avoids tight coupling to Docker daemon internals

sind wraps command execution in a thin abstraction layer using Go's `os/exec` package, with proper output handling and error reporting.

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
sind create cluster [--name NAME] [--config FILE] [--pull]
sind delete cluster [NAME]
sind delete cluster --all
sind get clusters
sind get nodes [CLUSTER]
sind get networks
sind get volumes
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
worker-0.dev     workerready
worker-1.dev     workerready
```

### Cluster Diagnostics

```bash
sind status [CLUSTER]
```

Displays detailed health information for a cluster:

```
$ sind status dev
Cluster: dev
Status:  running

NODES
NAME              ROLE        IP            CONTAINER   MUNGE  SSHD   SERVICES
controller.dev    controller  172.18.0.2    running     ✓      ✓      slurmctld ✓
worker-0.dev     worker172.18.0.3    running     ✓      ✓      slurmd ✓
worker-1.dev     worker172.18.0.4    running     ✓      ✓      slurmd ✗

NETWORK
Mesh:     sind-mesh ✓
DNS:      sind-dns ✓
Cluster:  sind-dev-net ✓

VOLUMES
sind-dev-config ✓
sind-dev-munge ✓
sind-dev-data ✓
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
| `--cpus N` | cluster default (2) | CPU limit per node |
| `--memory SIZE` | cluster default (2g) | Memory limit |
| `--tmp-size SIZE` | 1g | /tmp tmpfs size |
| `--unmanaged` | false | Don't start slurmd, don't add to slurm.conf |
| `--pull` | false | Pull images before creating containers |

Examples:

```bash
sind create worker                           # 1 managed node with cluster defaults
sind create worker --count 3                 # 3 managed nodes
sind create worker --count 2 --unmanaged     # 2 unmanaged nodes (slurmd not started)
sind create worker --cpus 4 --memory 8g      # 1 managed node with resource limits
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
```

`sind get munge-key` outputs the cluster's munge key encoded as base64, suitable for injection into external management tooling.

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

defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11.2  # default: sind-node:latest
  tmpSize: 1g                            # per-node /tmp tmpfs size
  cpus: 2                                # container CPU limit
  memory: 4g                             # container memory limit

storage:
  dataStorage:
    type: volume                         # volume | hostPath
    hostPath: ./data                     # only if type=hostPath
    mountPath: /data                     # default: /data

nodes:
  - role: controller
    tmpSize: 2g                          # override default
    cpus: 2
    memory: 4g

  - role: submitter                      # optional, at most one

  - role: worker
    count: 3                             # default: 1
    cpus: 4
    memory: 8g

  - role: worker
    count: 2
    managed: false                       # slurmd not started, not in slurm.conf
```

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
| `tmpSize` | global + per-node | `1g` | tmpfs size for /tmp |
| `cpus` | global + per-node | `2` | CPU limit |
| `memory` | global + per-node | `2g` | Memory limit |
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

| Type | Name Pattern | Example (`--name dev`) |
|------|--------------|------------------------|
| Network | `sind-<cluster>-net` | `sind-dev-net` |
| Controller | `sind-<cluster>-controller` | `sind-dev-controller` |
| Submitter | `sind-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `sind-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `sind-<cluster>-config` | `sind-dev-config` |
| Munge volume | `sind-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `sind-<cluster>-data` | `sind-dev-data` |

### Global Resources (Mesh)

| Type | Name |
|------|------|
| Mesh network | `sind-mesh` |
| DNS container | `sind-dns` |
| SSH container | `sind-ssh` |
| SSH volume | `sind-ssh-config` |

### Default Cluster Name

When `--name` is omitted, the default cluster name is `default`, resulting in prefixes like `sind-default-*`.

## Volume Mounts

| Volume | Mount Point | Controller | Worker | Submitter |
|--------|-------------|------------|---------|-----------|
| `sind-<cluster>-config` | `/etc/slurm` | rw | ro | ro |
| `sind-<cluster>-munge` | `/etc/munge` | ro | ro | ro |
| `sind-<cluster>-data` | `/data` | rw | rw | rw |
| tmpfs | `/tmp` | per-node | per-node | per-node |

### Mount Options

All volume mounts include the `:z` SELinux label for compatibility with SELinux-enabled systems.

Container mount flags:
```
-v sind-<cluster>-config:/etc/slurm:rw,z   # controller
-v sind-<cluster>-config:/etc/slurm:ro,z   # all others
-v sind-<cluster>-munge:/etc/munge:ro,z    # all nodes
-v sind-<cluster>-data:/data:rw,z          # all nodes
--tmpfs /tmp:rw,nosuid,nodev,size=1g       # configurable size
--tmpfs /run:exec,mode=755                 # systemd runtime
--tmpfs /run/lock                          # systemd lock files
```

### Host Path Storage

When `dataStorage.type: hostPath` is specified:
```
-v /path/on/host:/data:rw,z
```

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

The `sind-dns` container (CoreDNS) provides name resolution across meshed clusters:

```
<role>.<cluster>.sind.local → container IP
```

Nodes are configured with:
```
--dns <sind-dns-ip>
--dns-search <cluster>.sind.local
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
controller.dev.sind.local ssh-ed25519 AAAA...
worker-0.dev.sind.local ssh-ed25519 AAAA...
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
docker exec -it sind-ssh ssh [SSH_OPTIONS] <node>.sind.local [COMMAND [ARGS...]]
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

sind exports SSH configuration to `~/.sind/` for integration with the user's SSH client:

| File | Description |
|------|-------------|
| `~/.sind/ssh_config` | SSH config snippet |
| `~/.sind/id_ed25519` | Private key (copy from volume) |
| `~/.sind/known_hosts` | Host keys (copy from volume) |

The generated `~/.sind/ssh_config`:

```
Host *.sind.local
    ProxyCommand docker exec -i sind-ssh nc %h 22
    IdentityFile ~/.sind/id_ed25519
    UserKnownHostsFile ~/.sind/known_hosts
    User root
    StrictHostKeyChecking yes
```

To enable, add to `~/.ssh/config`:

```
Include ~/.sind/ssh_config
```

This allows direct use of standard SSH tools:

```bash
ssh controller.default.sind.local
ssh worker-0.dev.sind.local hostname
scp file.txt controller.dev.sind.local:/tmp/
```

sind updates `~/.sind/` files when clusters or nodes are created/deleted.

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
├── slurm.conf           # main config, includes sind-nodes.conf
├── sind-nodes.conf      # sind-managed node definitions
└── cgroup.conf          # cgroupv2 configuration
```

The main `slurm.conf` contains an include directive:

```
include /etc/slurm/sind-nodes.conf
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

sind avoids providing fully flexible customization of the initial configuration due to the complexity of Slurm configuration. The intent is to deliver a working starter configuration; users are expected to manage additional Slurm configuration with their own tooling after cluster creation. The `/etc/slurm` volume is writable on the controller node for this purpose.

Users may:
- Edit `slurm.conf` directly (sind does not modify it after creation)
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

The mesh DNS uses a hierarchical namespace with cluster names as subdomains under `sind.local`:

```
<role>.<cluster>.sind.local
<role>-<N>.<cluster>.sind.local
```

Examples:
- `controller.default.sind.local`
- `submitter.default.sind.local`
- `worker-0.default.sind.local`
- `worker-1.default.sind.local`
- `controller.dev.sind.local`

This hierarchical scheme keeps hostnames short while ensuring uniqueness across meshed clusters.

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

