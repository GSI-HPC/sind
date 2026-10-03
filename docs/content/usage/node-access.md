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

SSH into a specific node. SSH options, before or after `NODE`, are passed through to the underlying SSH command. A remote command must follow `--`: `NODE` is the only other argument before it, so `sind ssh worker-0 hostname`, which `ssh` would run as a command, is a usage error. sind's own flags, such as `--realm`, go before `ssh`.

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
```

Internally, `sind ssh` executes SSH via the mesh SSH container:

```bash
docker exec -i [-t] sind-ssh ssh [SSH_OPTIONS] <node>.<cluster>.<realm>.sind [COMMAND...]
```

The relay container is `<realm>-ssh`. `-t` is only passed when both stdin and stdout are terminals: through a pseudo-terminal, the output would get CRLF line endings and the remote stderr would arrive on stdout. Output that you capture or redirect, as in `v=$(sind ssh worker-0 -- hostname)` or `sind ssh worker-0 -- cat /etc/hosts > hosts`, therefore comes through unchanged, also in an interactive shell.

Since `ssh` runs in the relay container, the ports and files that SSH options name are the relay's: `-L`, `-R` and `-D` forward ports of the relay container, which your machine cannot reach, and `-i`, `-F` or `-E` name files in it. To forward a port to your machine, run your own `ssh` with the [exported SSH config](#user-ssh-client-integration):

```bash
# localhost:8080 on your machine to port 80 of the controller
ssh -F "$(sind get ssh-config)" -L 8080:localhost:80 controller.default.sind.sind
```

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

With [identity]({{< relref "/configuration/cluster-config#identity-section" >}}) `nssSlurm`, managed workers do not have the users, and with `clientIds` only the submitter (or, without one, the controller) has them, plus the controllers with `controllerUsers`. Unmanaged nodes have them in every mode. SSH as a user works only to the nodes that have the user. `enter` and `exec` run on the submitter or the controller, which have the users in every mode.

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

The directory must exist on the Docker host, which sind expects to be the machine it runs on; `sind create cluster` fails if it does not. The nodes mount it read-write, and files that root writes there from a node belong to root on the host. When the directory is `/` or your home directory, `sind create cluster` prints a warning on stderr: it usually means the command ran there by accident.

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
| `ssh_config` | SSH config snippet, with a `Host` block for each node of the realm |
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

For the default realm `sind`, the generated SSH config also expands the short names of nodes, `<node>` in the `default` cluster and `<node>.<cluster>`, with hostname canonicalization. Other realms get no such directives; use full names such as `controller.dev.ci.sind` there.

```bash
ssh controller                        # → controller.default.sind.sind
ssh controller.dev                    # → controller.dev.sind.sind
ssh controller.default.sind.sind      # full FQDN
scp file.txt worker-0.dev.sind.sind:/tmp/
ssh -L 8080:localhost:80 controller   # port forwarding to your machine
```

The `Include` relies on these OpenSSH behaviours:

- An `Include` after a `Host` or `Match` line belongs to that block and applies only to the hosts it matches, hence its place at the top.
- ssh uses the first value it gets for each option, so the settings for the nodes take precedence over later ones, such as those of a `Host *` block.
- Short names use hostname canonicalization (`CanonicalizeHostname`), only for names shaped like a node's: `controller`, `controller-backup`, `db`, `submitter` and `worker-*`, alone or followed by `.<cluster>`. For them, ssh looks up `<name>.default.sind.sind` and then `<name>.sind.sind` in the host's DNS, which needs [host DNS resolution]({{< relref "/architecture/networking#host-dns-resolution" >}}), and reads the config again for the name it found. Other host names are not looked up. Without host DNS resolution, use full names.
- ssh puts a host name into a `ProxyCommand` as it was given, and runs the command in a shell. The relay's `ProxyCommand` is therefore set only in the `Host` block of each node, with the node's name written into it, so a host name with shell syntax in it, such as one from a git submodule URL, never reaches a shell (the pattern of CVE-2023-51385; OpenSSH 9.6 and later also refuse such names on the command line). sind rewrites the file whenever it creates or deletes a cluster or a worker in the realm.

{{< hint warning >}}
While a node of that name exists, `ssh controller`, `ssh db` or `ssh db.lab` (with a cluster named `lab`) connect to the sind node, as root, even when your network has a host of that name. Reach such a host by its full name, or include the default realm's `ssh_config` only while you need it.
{{< /hint >}}
