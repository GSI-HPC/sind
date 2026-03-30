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
docker exec -it sind-ssh ssh <node>.sind.local
```

## enter

```bash
sind enter [CLUSTER]
```

Opens an interactive shell on the cluster's submitter node. If no submitter is configured, it connects to the controller instead.

```bash
sind enter          # default cluster
sind enter dev      # dev cluster
```

This is a convenience wrapper — equivalent to `sind ssh submitter` or `sind ssh controller`.

## exec

```bash
sind exec [CLUSTER] -- COMMAND [ARGS...]
```

Runs a one-shot command on the submitter (or controller). The `--` separator is required.

```bash
sind exec -- sinfo
sind exec -- srun hostname
sind exec dev -- sbatch job.sh
```

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

Add to your `~/.ssh/config` for a single realm:

```bash
Include ~/.local/state/sind/sind/ssh_config
```

Or include all realms at once using a wildcard:

```bash
Include ~/.local/state/sind/*/ssh_config
```

Then use standard SSH tools directly:

```bash
ssh controller.default.sind.local
scp file.txt worker-0.dev.sind.local:/tmp/
```
