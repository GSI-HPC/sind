---
weight: 356
title: "Using CVMFS"
icon: "folder_shared"
description: "Mount CVMFS repositories on the nodes, or test how your tooling provisions the CVMFS client"
toc: true
---

[CernVM-FS](https://cvmfs.readthedocs.io) (CVMFS) serves software repositories read-only under `/cvmfs`. sind supports two use cases:

- **Consume CVMFS**: jobs on the cluster read software from `/cvmfs`. With `storage.cvmfs: true`, sind mounts CVMFS from the Docker host or from a Docker volume plugin. The nodes need no extra privileges.
- **Provision CVMFS**: test the configuration management (Ansible, Puppet, ...) that installs and configures the CVMFS client on a node. The client runs inside the nodes, which needs `SYS_ADMIN` and `/dev/fuse`.

## Mount CVMFS on the nodes

```yaml
kind: Cluster
name: dev
storage:
  cvmfs: true
nodes:
  - controller
  - submitter
  - worker: 2
```

```bash
sind create cluster --config cluster.yaml
sind exec dev -- ls /cvmfs/sft.cern.ch
```

Every node mounts CVMFS read-only at `/cvmfs`, and so do workers added later with `sind create worker`. Repositories mount on demand when first accessed, as on a bare-metal client with autofs, so there is no list of repositories to configure.

### Backends

`sind create cluster` picks one of two backends and logs its choice (with `-v`):

| Backend | When | Docker mount |
|---------|------|--------------|
| Volume plugin | an enabled Docker volume plugin named `cvmfs` is installed | `type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly` |
| Host bind mount | otherwise | `type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave` |

If neither works, `sind create cluster` fails before creating any node, with Docker's error and a hint to install CVMFS on the Docker host or the volume plugin.

`sind get cluster` lists the mount under `MOUNTS`:

```
MOUNTS
MOUNT        SOURCE               TYPE       STATUS
/etc/slurm   sind-dev-config      volume     ✓
/etc/munge   sind-dev-munge       volume     ✓
/data        /home/user/project   hostPath   ✓
/cvmfs       /cvmfs               hostPath   ✓
```

### Host bind mount

The Docker host needs the CVMFS client with autofs, set up as usual (`cvmfs_config setup`), so that `ls /cvmfs/sft.cern.ch` works on the host itself.

sind bind-mounts the host's `/cvmfs` as a slave mount. A lookup inside a node reaches the host's autofs, and the repository it mounts appears in every node; autofs idle unmounts propagate the same way. Docker accepts the slave mount only when the host's `/cvmfs` is on a `shared` or `slave` mount, the default on systemd hosts. Check with:

```console
$ findmnt -o TARGET,PROPAGATION /cvmfs
TARGET PROPAGATION
/cvmfs shared
```

Before creating the nodes, sind tries the mount in a throwaway container of the controller image, so a host without `/cvmfs`, or with a `private` one, fails early.

### Volume plugin

A Docker volume plugin runs the CVMFS client on the host as a privileged daemon, and the nodes stay unprivileged. sind uses it when `docker plugin ls` shows an enabled volume plugin named `cvmfs`, which is the name `volume-driver=cvmfs` resolves to. Install a plugin under that name with `docker plugin install --alias cvmfs <plugin>`.

sind mounts the plugin's volume `cvmfs` and leaves repository handling to the plugin. All clusters share that volume; sind does not remove it with a cluster.

## Provision CVMFS inside the nodes

To test how your tooling installs and configures CVMFS, leave `storage.cvmfs` unset and let the client run inside the nodes. Mounting a FUSE file system needs the `SYS_ADMIN` capability and the `/dev/fuse` device on those nodes:

```yaml
kind: Cluster
name: cvmfs
nodes:
  - role: controller
  - role: submitter
    capAdd: [SYS_ADMIN]
    devices: [/dev/fuse]
    securityOpt: [apparmor=unconfined]  # AppArmor hosts only (Ubuntu, Debian)
  - role: worker
    capAdd: [SYS_ADMIN]
    devices: [/dev/fuse]
    securityOpt: [apparmor=unconfined]
```

Then provision the nodes as on bare metal, for example by hand:

```bash
dnf install -y https://cvmrepo.s3.cern.ch/cvmrepo/yum/cvmfs-release-latest.noarch.rpm
dnf install -y cvmfs
cat > /etc/cvmfs/default.local <<EOF
CVMFS_CLIENT_PROFILE=single
CVMFS_HTTP_PROXY=DIRECT
CVMFS_REPOSITORIES=cvmfs-config.cern.ch,sft.cern.ch
CVMFS_NFILES=65536
EOF
cvmfs_config setup
cvmfs_config probe
```

autofs then mounts `/cvmfs/sft.cern.ch` on first access, and Slurm jobs on the worker can read it.

Two things to know:

- **AppArmor hosts**: under Docker's default AppArmor profile, nodes with `SYS_ADMIN` exit with code 255 at boot. `securityOpt: [apparmor=unconfined]` avoids that; hosts without AppArmor don't need it.
- **Open files**: `cvmfs2` refuses to mount ("Failed to set maximum number of open files, insufficient permissions") when `CVMFS_NFILES` exceeds the node's hard limit on open files. The default, 131072, can exceed it: on GitHub's `ubuntu-latest` runners the limit is 65536. Check it with `ulimit -Hn` inside a node and keep `CVMFS_NFILES` at or below it.

See [Capabilities and devices]({{< relref "/configuration/node-definitions#capabilities-and-devices" >}}) for the node fields.
