---
weight: 210
title: "Cluster Configuration"
icon: "description"
description: "YAML configuration schema reference"
toc: true
---

## Minimal configuration

The simplest valid configuration creates a cluster with one controller and one worker using the default image:

```yaml
kind: Cluster
```

This is equivalent to the fully expanded form:

```yaml
kind: Cluster
name: default
defaults:
  image: ghcr.io/gsi-hpc/sind-node:latest
  cpus: 1
  memory: 512m
  tmpSize: 256m
nodes:
  - role: controller
  - role: worker
```

## Full example

```yaml
kind: Cluster
name: test-cluster

defaults:
  image: ghcr.io/gsi-hpc/sind-node:latest
  cpus: 1
  memory: 512m
  tmpSize: 256m

storage:
  dataStorage:
    type: volume
    mountPath: /data

groups:
  - name: hpc
    gid: 3000

users:
  - alice
  - name: bob
    uid: 2001
    group: hpc
    accounts: [physics]

accounts:
  - physics

slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainCores=yes
  slurmdbd: |
    PurgeJobAfter=1month

nodes:
  - role: controller
    cpus: 2
    memory: 1g
    tmpSize: 512m

  - role: db

  - role: submitter

  - role: worker
    count: 3
    cpus: 2
    memory: 1g

  - role: worker
    count: 2
    managed: false
```

## Top-level fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `kind` | yes | — | Must be `"Cluster"` |
| `name` | no | `"default"` | Cluster name, used in resource naming; must be a valid name (see below) |
| `realm` | no | `"sind"` | Realm namespace for resource isolation; must be a valid name (see below). `--realm` and `SIND_REALM` take precedence, and later commands do not read it: see [Realms]({{< relref "/configuration/realms#setting-the-realm" >}}) |
| `defaults` | no | — | Default settings applied to all nodes |
| `storage` | no | — | Shared storage configuration |
| `slurm` | no | — | Slurm configuration extension |
| `users` | no | — | Linux user accounts on every node |
| `groups` | no | — | Linux groups for the users |
| `accounts` | no | — | Slurm accounts, with a managed db node |
| `identity` | no | `local` | Which nodes get the users, and how Slurm authenticates them |
| `nodes` | no | 1 controller + 1 worker | Node definitions |

Cluster and realm names end up in Docker resource names, DNS names and paths, so each must be a single DNS label: lowercase ASCII letters, digits and `-`, 1 to 63 characters, not beginning or ending with `-`. Names such as `Dev`, `my_cluster`, `dev.test` or `../x` are rejected. The same rule applies to cluster names given on the command line and to `--realm` and `SIND_REALM`. A cluster may not be named `ssh`, as its config volume would be the realm's SSH volume, `<realm>-ssh-config`. With a managed [db node]({{< relref "/configuration/node-definitions#database-node" >}}), the cluster name has at most 40 characters, Slurm's limit for clusters with accounting: slurmdbd builds the names of the cluster's database tables from it.

## Defaults section

The `defaults` section sets values inherited by all nodes unless overridden at the node level.

| Field | Default | Description |
|-------|---------|-------------|
| `image` | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `cpus` | `1` | CPU limit per container; a managed worker's Slurm `CPUs` |
| `memory` | `"512m"` | Memory limit per container, without swap, in Docker's size syntax (see below); a managed worker's Slurm `RealMemory`; `/dev/shm` gets half of it |
| `tmpSize` | `"256m"` | tmpfs size for `/tmp`; files there count against `memory` |
| `capAdd` | none | Extra Linux capabilities |
| `capDrop` | none | Dropped Linux capabilities |
| `devices` | none | Host devices to expose |
| `securityOpt` | none | Extra security options |

Scalar fields (`image`, `cpus`, `memory`, `tmpSize`) are overridden by per-node values. List fields (`capAdd`, `capDrop`, `devices`, `securityOpt`) are merged with per-node values.

`memory` takes Docker's size syntax: a number, optionally with a fraction, and an optional unit `b`, `k`, `m`, `g`, `t` or `p`, in either case, which may be followed by `b` or `ib`. All units are powers of 1024 and a number without a unit counts bytes, so `512m`, `2g`, `2GB`, `2GiB`, `1.5g` and `1073741824` are valid. It must be at least `6m`, Docker's minimum. A managed worker's Slurm `RealMemory` is its `memory` in MiB, rounded down. `tmpSize` is passed to the kernel as the tmpfs size: a whole number with an optional unit `k`, `m`, `g`, `t`, `p` or `e`, or a percentage of the memory, such as `50%`. `cpus` must not be negative; `0` means the default.

`memory` covers everything in a node: the jobs, the node's own services (systemd, munge, sshd, the Slurm daemon; also mariadb and slurmdbd on a db node) and files in `/tmp`, `/run` and `/dev/shm`. Slurm is told the whole limit (`RealMemory`), so raise `memory` for jobs that need much memory or `/tmp`.

Workers added later with `sind create worker` do not read the config: they take these settings from the cluster's newest worker (see [Worker Management]({{< relref "/usage/worker-management#defaults-from-the-newest-worker" >}})).

## Storage section

```yaml
storage:
  dataStorage:
    type: hostPath     # "hostPath" or "volume"
    hostPath: ./data   # existing host directory for type: hostPath
    mountPath: /data   # default: /data
  cvmfs: true          # mount CVMFS read-only at /cvmfs (default: false)
```

### Data storage

| Field | Default | Description |
|-------|---------|-------------|
| `type` | set by `--data` | `"hostPath"` bind-mounts `hostPath`; `"volume"` uses the Docker volume `<realm>-<cluster>-data` and ignores `hostPath`. A `hostPath` without `type` means `"hostPath"` |
| `hostPath` | — | Host directory, required with `type: hostPath`. It must exist: `sind create cluster` fails otherwise rather than have Docker create it as root. A relative path is taken relative to the directory `sind create cluster` runs in and stored as an absolute path |
| `mountPath` | `"/data"` | Absolute mount point inside the nodes |

If the config sets neither `type` nor `hostPath`, the `--data` flag of `sind create cluster` decides; its default, `.`, bind-mounts the working directory (see [Data mount]({{< relref "/usage/node-access#data-mount" >}})).

Workers added with `sind create worker` mount the data at the same place, `sind get cluster` reports it there, and `sind enter` and `sind exec` start in it.

### CVMFS

`cvmfs: true` mounts CVMFS read-only at `/cvmfs` on every node, including workers added later with `sind create worker`. Repositories mount on demand when first accessed. The mount comes from the `cvmfs` Docker volume plugin if one is installed and enabled, otherwise from the Docker host's `/cvmfs`; `sind create cluster` fails if neither is available. See [Using CVMFS]({{< relref "/guides/cvmfs" >}}) for the requirements of each.

## Users section

sind configures root on every node. `users` adds Linux user accounts, e.g. to run jobs as someone other than root, and `groups` Linux groups for them:

```yaml
groups:
  - name: hpc
    gid: 3000      # default: lowest free gid from 1000
users:
  - alice          # bare name
  - name: bob
    uid: 2001      # default: lowest free uid from 1000
    group: hpc     # primary group instead of a private one
  - name: carol
    groups: [hpc]  # supplementary groups
```

| User field | Default | Description |
|------------|---------|-------------|
| `name` | — | User name: a lowercase letter or `_`, then lowercase letters, digits, `_` and `-`, at most 32 characters |
| `uid` | lowest free from `1000` | ID of the user, between 1000 and 2147483647, but not 65534 or 65535 |
| `group` | private group | Primary group, from `groups`. Without it, the user gets a private group of its own name with gid = uid |
| `groups` | none | Supplementary groups, from `groups` |
| `accounts` | none | Slurm accounts the user gets associations with, from `accounts`; the first is the default account |
| `coordinator` | none | Slurm accounts the user coordinates, from `accounts` |
| `adminLevel` | none | Slurm admin level: `operator` or `admin`. Needs `accounts` |

| Group field | Default | Description |
|-------------|---------|-------------|
| `name` | — | Group name, with the same rules as user names. It must not be the name of a user with a private group |
| `gid` | lowest free from `1000` | ID of the group, between 1000 and 2147483647, but not 65534 or 65535 |

Users without `uid` get their IDs first, in list order, skipping the explicit `gid`s; then groups without `gid` get the lowest IDs no user or group has. Set the IDs explicitly to keep file ownership stable across re-creates, e.g. on a `hostPath` data directory.

Every node, including workers added later with `sind create worker`, gets each group and user with the same IDs, as munge and Slurm require. Workers also get the `SYS_NICE` capability, which slurmstepd needs to bind the tasks of users other than root (see [Capabilities and devices]({{< relref "/configuration/node-definitions#capabilities-and-devices" >}})). The [identity mode](#identity-section) can keep them off some nodes. The home directories, `/home/<user>`, are on the Docker volume `<realm>-<cluster>-home`, which every node mounts at `/home`, so job output written there is the same on every node. The realm's SSH key may log in as each user, as it may as root. See [Node access]({{< relref "/usage/node-access#cluster-users" >}}) for running commands as a user.

A user or group name that already exists in the image, such as `root`, `slurm` or `wheel`, makes `sind create cluster` fail.

## Accounts section

With a managed [db node]({{< relref "/configuration/node-definitions#database-node" >}}), `accounts` declares Slurm accounts, and the users' `accounts`, `coordinator` and `adminLevel` fields give them associations and roles:

```yaml
nodes: [controller, db, submitter, worker: 2]
accounts:
  - physics          # bare name, parent root
  - name: theory
    parent: physics
    limits:
      MaxJobs: 1
users:
  - name: alice
    accounts: [theory]
  - name: bob
    accounts: [physics, theory]   # the first is the default account
    coordinator: [physics]
```

| Account field | Default | Description |
|---------------|---------|-------------|
| `name` | — | Account name: lowercase letters, digits, `_` and `-`, not starting with `-`, at most 64 characters, not `root` |
| `parent` | `root` | Parent account: `root` or an account declared before this one |
| `limits` | none | Further `sacctmgr add account` options, passed as `key=value`, e.g. `GrpTRES: cpu=4`, `MaxJobs: 1`, `Fairshare: 10`. The keys are among the options listed below |

Once slurmctld has registered the cluster with slurmdbd, sind runs `sacctmgr -i` on the controller: `add account` for each account, in list order; `add user` for each user with accounts, with `defaultaccount` set to the first and `adminlevel` if set; then `add coordinator`. The example runs:

```bash
sacctmgr -i add account physics
sacctmgr -i add account theory parent=physics MaxJobs=1
sacctmgr -i add user alice account=theory defaultaccount=theory
sacctmgr -i add user bob account=physics,theory defaultaccount=physics
sacctmgr -i add coordinator account=physics names=bob
```

`limits` keys are these options, in any case: `Description`, `Organization` and `Flags` of the account, and `Comment`, `DefaultQOS`, `Fairshare` (or `Shares`), `GrpJobs`, `GrpJobsAccrue`, `GrpSubmitJobs`, `GrpTRES`, `GrpTRESMins`, `GrpTRESRunMins`, `GrpWall`, `MaxJobs`, `MaxJobsAccrue`, `MaxSubmitJobs`, `MaxTRES` (or `MaxTRESPerJob`), `MaxTRESMins` (or `MaxTRESMinsPerJob`), `MaxTRESPerNode`, `MaxTRESRunMins`, `MaxWall` (or `MaxWallDurationPerJob`), `MinPrioThresh`, `Priority` and `QOS` (or `QosLevel`) of its association. sacctmgr accepts abbreviations, but sind takes only these full names, so that no key reaches the name, parent or cluster options sind sets.

YAML 1.1 reads unquoted numbers before sind sees them, and sind passes them in plain decimal form: `MaxJobs: 1` becomes `MaxJobs=1`, but `MaxJobs: 010` is octal and becomes `MaxJobs=8`, and `Description: 1.10` becomes `Description=1.1`. Quote a value to pass it as written, e.g. `Description: "1.10"`.

Every account a user names must be declared, so a typo fails validation instead of creating a new account. See [Users and Identity]({{< relref "/guides/users" >}}) for a worked example with limits and roles. The associations are in place when `sind create cluster` returns. sind does not enforce them: set `AccountingStorageEnforce=associations,limits` in the [`main` section](#slurm-section) to reject jobs without an association and apply the limits. Without `limits` (or `safe` or `all`) there, Slurm ignores the `Max*` and `Grp*` limits, and `sind create cluster` prints a warning. Slurm accounts and Linux groups are unrelated, even when they share a name.

## Identity section

`identity` selects where the nodes look up the users, and how Slurm authenticates them. It is a mode, or an object with `mode` and `controllerUsers`:

```yaml
identity: nssSlurm          # local (default) | nssSlurm | clientIds
```

```yaml
identity:
  mode: clientIds
  controllerUsers: true     # also give the controllers the users, for AllowGroups
```

| Mode | Linux accounts on | What sind sets up |
|------|-------------------|-------------------|
| `local` | every node | munge |
| `nssSlurm` | every node but the managed workers | munge; `LaunchParameters=enable_nss_slurm`; `slurm` first for `passwd` and `group` in the workers' `/etc/nsswitch.conf` |
| `clientIds` | the submitter, or the controllers without one; the controllers too with `controllerUsers` | as `nssSlurm`, plus `AuthType=auth/slurm`, `CredType=cred/slurm` and `AuthInfo=use_client_ids` in `slurm.conf` and `slurmdbd.conf`, a `slurm.key` instead of a munge key, munge masked on every node and `sackd` on the submitter |

The `slurm.conf` parameters are set unless the [`main` section](#slurm-section) sets them. A value set there must keep sind's, or validation fails: `LaunchParameters` must list `enable_nss_slurm`, and with `clientIds` `AuthInfo` must list `use_client_ids`, `AuthType` be `auth/slurm` and `CredType` `cred/slurm`. Unmanaged nodes, such as `managed: false` workers, get the Linux accounts in every mode. `nssSlurm` and `clientIds` need a managed cluster, and managed workers whose image has nss_slurm (`libnss_slurm.so.2`), as the official images do. Official images cached before identity modes existed lack it under the same tags; sind refuses such a cached image before it creates anything, and `--pull` fetches a current one. See [Users and Identity]({{< relref "/guides/users" >}}) for how each mode resolves users, and how to choose one.

## Slurm section

The `slurm` section extends the generated Slurm configuration. Each key maps to a config file:

| Key | Config file | sind generates defaults |
|-----|-------------|:----------------------:|
| `main` | `slurm.conf` | yes |
| `cgroup` | `cgroup.conf` | yes |
| `gres` | `gres.conf` | no |
| `topology` | `topology.conf` | no |
| `plugstack` | `plugstack.conf` | yes (always scaffolded) |
| `slurmdbd` | `slurmdbd.conf` | yes (needs a managed [`db` node]({{< relref "/configuration/node-definitions#database-node" >}})) |

Each key supports two forms:

**String form** — content appended to the config file:

```yaml
slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainCores=yes
```

**Map form** — named fragments placed in a `.conf.d/` directory:

```yaml
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
      SchedulerParameters=bf_continue
    resources: |
      SelectType=select/cons_tres
```

Fragment validation:

- Names must be plain filenames (no path separators)
- Names and content must not be empty

See [Slurm Configuration]({{< relref "/architecture/slurm-config" >}}) for details on the generated files.

## Trust

Validation checks a config's form, not its intent. A cluster config, like the `--data` flag, is as trusted as a script run with your Docker access, which amounts to root on the Docker host:

- `image` runs any image, whose init and services run as root.
- `storage.dataStorage.hostPath` bind-mounts any host directory read-write into every node, `/` included.
- `capAdd`, `devices` and `securityOpt` give nodes host privileges, such as `SYS_ADMIN`, raw block devices or `apparmor=unconfined`.
- The `slurm` sections set programs Slurm runs as root, such as `Prolog` or `HealthCheckProgram`.

Use only configs you would run as a script: not a config from an untrusted pull request in a privileged CI workflow, and not one an agent wrote that you have not read.

## Validation rules

- `kind` must be `"Cluster"`
- Exactly one `controller` node is required
- At most one `db` node is allowed
- At most one `submitter` node is allowed
- At least one `worker` node is required
- `count` is only valid for worker nodes
- `managed` is only valid for controller, db and worker nodes
- With `managed: false` on the controller, no worker or db node may set `managed: true` and no `slurm` section may be set
- `backupController` is only valid for controller nodes
- The `slurmdbd` section requires a managed `db` node
- With `backupController`, `slurm.main` must not set `SlurmctldHost` (or its deprecated forms `ControlMachine`, `BackupController`, `BackupAddr`) or `StateSaveLocation`
- `count` must not be negative; `0` means the default, 1
- `cpus` must not be negative; `memory` and `tmpSize` must be valid sizes (see [Defaults section](#defaults-section)), in `defaults` too
- `capAdd`/`capDrop` values must be recognized Linux capability names (e.g. `SYS_ADMIN`, `NET_ADMIN`, `ALL`)
- `devices` paths must be absolute (start with `/`)
- `securityOpt` entries must name an option Docker knows, with a value: `label=`, `apparmor=`, `seccomp=`, `no-new-privileges` (value optional), `writable-cgroups=` or `systempaths=`
- `storage.dataStorage.type` must be `volume` or `hostPath`; `hostPath` requires a `hostPath`, and `mountPath` must be absolute
- User and group names must be valid (see [Users section](#users-section)) and unique; `uid` and `gid` must be between 1000 and 2147483647, not 65534 or 65535, and unique, private groups included; a user's `group` and `groups` must be declared in `groups`, and `groups` must not repeat an entry or the primary `group`
- `identity` must be `local`, `nssSlurm` or `clientIds`; `nssSlurm` and `clientIds` require a managed cluster; `controllerUsers` is only valid with `clientIds`; an identity parameter that `slurm.main` sets must keep sind's value (see [Identity section](#identity-section))
- `accounts`, and the users' `accounts`, `coordinator` and `adminLevel`, require a managed db node; account names must be valid and unique (see [Accounts section](#accounts-section)); a `parent` must be `root` or declared before; `limits` keys must be among the options in the [Accounts section](#accounts-section), with non-empty values; every account a user names must be declared, at most once in each of `accounts` and `coordinator`; `adminLevel` is `operator` or `admin`; `coordinator` and `adminLevel` need `accounts`
- Unknown keys are rejected
