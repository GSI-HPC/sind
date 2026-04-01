---
weight: 320
title: "Node Access"
icon: "vpn_key"
description: "SSH, interactive shells, and command execution"
toc: true
---

## ssh

```bash
sind ssh [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
```

SSH into a specific node. All SSH options and arguments are passed through to the underlying SSH command.

```bash
# Interactive shell
sind ssh controller
sind ssh worker-0.dev

# Run a command
sind ssh worker-0 -- hostname
sind ssh -t worker-0 -- top

# Verbose SSH
sind ssh -v worker-0

# Port forwarding
sind ssh -L 8080:localhost:80 controller
```

Internally, `sind ssh` executes SSH via the mesh SSH container:

```bash
docker exec -it sind-ssh ssh <node>.<realm>.sind
```

Shell completion is available for both `sind ssh` and `sind exec` — press Tab to complete node and cluster names.

## enter

```bash
sind enter [CLUSTER]
```

Opens an interactive shell on the cluster's submitter node. If no submitter is configured, it connects to the controller instead. The working directory inside the container is `/data`.

```bash
sind enter          # default cluster
sind enter dev      # dev cluster
```

## exec

```bash
sind exec [CLUSTER] -- COMMAND [ARGS...]
```

Runs a one-shot command on the submitter (or controller). The `--` separator is required. The working directory inside the container is `/data`.

```bash
sind exec -- sinfo
sind exec -- srun hostname
sind exec dev -- sbatch job.sh
```

## Data mount

By default, `sind create cluster` bind-mounts the current working directory into all containers at `/data`. Both `sind enter` and `sind exec` set `/data` as the working directory, so files from the host are immediately accessible.

Use the `--data` flag to change this behavior:

| Flag value | Behavior |
|------------|----------|
| `--data .` (default) | Bind-mount CWD as `/data` |
| `--data /path/to/dir` | Bind-mount the given directory as `/data` |
| `--data volume` | Use a Docker-managed volume instead of a host mount |

```bash
# Mount a specific directory
sind create cluster --data /home/user/shared

# Use a Docker volume instead
sind create cluster --data volume
```

{{< hint info >}}
`sind ssh` connects via SSH and starts in the user's home directory, not `/data`. The data mount is still accessible — just `cd /data`. Use `sind enter` or `sind exec` to land in `/data` directly.
{{< /hint >}}

## Command routing

| Command | Target node |
|---------|-------------|
| `sind ssh <node>` | Explicit node |
| `sind enter [cluster]` | Submitter if exists, otherwise controller |
| `sind exec [cluster]` | Submitter if exists, otherwise controller |

## User SSH client integration

sind automatically exports SSH configuration per realm to `$XDG_STATE_HOME/sind/<realm>/` (defaulting to `~/.local/state/sind/<realm>/`):

| File | Description |
|------|-------------|
| `ssh_config` | SSH config snippet |
| `id_ed25519` | Private key |
| `known_hosts` | Host keys |

These files are updated on every create/delete operation and removed when the last cluster in a realm is deleted.

To find the path for your current realm:

```bash
sind get ssh-config
```

Add to the **top** of your `~/.ssh/config` (before any `Host` or `Match` blocks) for a single realm:

```
Include ~/.local/state/sind/sind/ssh_config
```

Or include all realms at once using a wildcard:

```
Include ~/.local/state/sind/*/ssh_config
```

The generated SSH config includes `CanonicalizeHostname` directives that expand short names automatically:

```bash
ssh controller                        # → controller.default.sind.sind
ssh controller.dev                    # → controller.dev.sind.sind
ssh controller.default.sind.sind      # full FQDN
scp file.txt worker-0.dev.sind.sind:/tmp/
```

{{< hint info >}}
Short-name canonicalization (`ssh controller`, `ssh controller.dev`) requires the `Include` line to appear **before** any `Host` or `Match` blocks in your `~/.ssh/config`. OpenSSH processes `CanonicalizeHostname` directives in order — if a `Host *` block appears first, canonicalization is skipped.
{{< /hint >}}
