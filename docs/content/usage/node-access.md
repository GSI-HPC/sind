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

sind can export SSH configuration for use with your own SSH client. The files are stored in `~/.sind/`:

| File | Description |
|------|-------------|
| `~/.sind/ssh_config` | SSH config snippet |
| `~/.sind/id_ed25519` | Private key |
| `~/.sind/known_hosts` | Host keys |

Add to your `~/.ssh/config`:

```
Include ~/.sind/ssh_config
```

Then use standard SSH tools directly:

```bash
ssh controller.default.sind.local
scp file.txt worker-0.dev.sind.local:/tmp/
```
