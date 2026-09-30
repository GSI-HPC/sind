---
weight: 320
title: "Node Access"
icon: "vpn_key"
description: "SSH, interactive shells, and command execution"
toc: true
---

## ssh

```bash
sind ssh [SSH_OPTIONS] [USER@]NODE [-- COMMAND [ARGS...]]
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

# As a cluster user
sind ssh alice@worker-0

# Port forwarding
sind ssh -L 8080:localhost:80 controller
```

Internally, `sind ssh` executes SSH via the mesh SSH container:

```bash
docker exec -i -t sind-ssh ssh [SSH_OPTIONS] <node>.<cluster>.<realm>.sind [COMMAND...]
```

The relay container is `<realm>-ssh`, and `-t` is only passed when stdin is a terminal.

Shell completion is available for both `sind ssh` and `sind exec` — press Tab to complete node and cluster names. Load it with `source <(sind completion bash)`, or the `zsh`, `fish` or `powershell` variant; `sind completion <shell> --help` shows how to install it permanently.

## enter

```bash
sind enter [CLUSTER] [--user USER]
```

Opens an interactive shell on the cluster's submitter node. If no submitter is configured, it connects to the controller instead. The working directory inside the container is the data mount, `/data` unless the cluster config sets another `mountPath`.

```bash
sind enter          # default cluster
sind enter dev      # dev cluster
```

## exec

```bash
sind exec [CLUSTER] [--user USER] -- COMMAND [ARGS...]
```

Runs a one-shot command on the submitter (or controller). The `--` separator is required. The working directory inside the container is the data mount, as for `enter`.

```bash
sind exec -- sinfo
sind exec -- srun hostname
sind exec dev -- sbatch job.sh
```

## Cluster users

A cluster with [users]({{< relref "/configuration/cluster-config#users-section" >}}) can be used as any of them instead of root:

```bash
sind enter --user alice               # shell as alice, in /home/alice
sind exec -u alice -- sbatch job.sh   # command as alice, in /home/alice
sind ssh alice@worker-0               # SSH as alice (ssh -l alice)
ssh -l alice controller               # with the exported ssh_config
```

`--user`, or `-u`, runs `docker exec -u USER` in the user's home directory, `/home/USER`, which every node shares. `USER@NODE` passes `-l USER` to SSH. `--user root` is the default. A user name that is not valid exits `2`; one that the cluster does not have fails in docker.

## Exit status

`sind ssh`, `sind enter` and `sind exec` exit with the status of what they run: the remote command, the shell, or the command. They print no error of their own when it fails, since it has written its own.

```bash
sind exec -- sh -c 'exit 3'; echo $?   # 3
sind ssh worker-0 -- false; echo $?    # 1
```

`ssh` exits `255` when it cannot connect. A command line that sind rejects, such as `sind exec` without `--`, exits `2`. See [Exit Status]({{< relref "/usage/exit-status" >}}).

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
`sind ssh` connects via SSH and starts in the user's home directory, not `/data`. The data mount is still accessible — just `cd /data`. Use `sind enter` or `sind exec` to land in `/data` directly. With `--user`, they start in the user's home directory instead.
{{< /hint >}}

## Command routing

| Command | Target node |
|---------|-------------|
| `sind ssh <node>` | Explicit node |
| `sind enter [cluster]` | Submitter if exists, otherwise the controller in control |
| `sind exec [cluster]` | Submitter if exists, otherwise the controller in control |

On an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}), sind does not know which controller is in control: without a submitter, `enter` and `exec` target `controller` if it runs, otherwise `controller-backup`.

## User SSH client integration

sind automatically exports SSH configuration per realm to `$XDG_STATE_HOME/sind/<realm>/` (defaulting to `~/.local/state/sind/<realm>/`, which is also used when `XDG_STATE_HOME` is a relative path):

| File | Description |
|------|-------------|
| `ssh_config` | SSH config snippet |
| `id_ed25519` | Private key |
| `known_hosts` | Host keys |
| `lock` | Advisory lock that serializes creating and deleting clusters and workers in the realm |

The SSH files are updated on every create/delete operation and removed when the last cluster in a realm is deleted; the directory stays, as it holds the `lock` file.

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

For the default realm `sind`, the generated SSH config includes `CanonicalizeHostname` directives that expand short names automatically. Other realms get no such directives; use full names such as `controller.dev.ci.sind` there.

```bash
ssh controller                        # → controller.default.sind.sind
ssh controller.dev                    # → controller.dev.sind.sind
ssh controller.default.sind.sind      # full FQDN
scp file.txt worker-0.dev.sind.sind:/tmp/
```

{{< hint info >}}
Short-name canonicalization (`ssh controller`, `ssh controller.dev`) requires the `Include` line to appear **before** any `Host` or `Match` blocks in your `~/.ssh/config`. OpenSSH processes `CanonicalizeHostname` directives in order — if a `Host *` block appears first, canonicalization is skipped.
{{< /hint >}}
