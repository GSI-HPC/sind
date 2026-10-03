---
weight: 440
title: "Slurm Configuration"
icon: "tune"
description: "Generated configuration files and version discovery"
toc: true
---

## Generated files

sind generates a multi-file Slurm configuration and writes it to the `<realm>-<cluster>-config` volume. For an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}) it writes nothing: the volume stays empty for your own configuration.

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
└── slurm.key               # auth/slurm key (identity clientIds only)
```

## slurm.conf

The main configuration file includes cluster name, controller host, process tracking settings, and always contains:

```
include /etc/slurm/sind-nodes.conf
PlugStackConfig=/etc/slurm/plugstack.conf
```

With a managed [db node]({{< relref "/configuration/node-definitions#database-node" >}}) it also contains these accounting parameters, each unless the `main` section sets it:

```
AccountingStorageType=accounting_storage/slurmdbd
AccountingStorageHost=db
JobAcctGatherType=jobacct_gather/cgroup
```

With [identity]({{< relref "/configuration/cluster-config#identity-section" >}}) `nssSlurm` or `clientIds` it contains the identity parameters, `clientIds` only for the first three, each unless the `main` section sets it:

```
AuthType=auth/slurm
CredType=cred/slurm
AuthInfo=use_client_ids
LaunchParameters=enable_nss_slurm
```

sind does not modify `slurm.conf` after initial creation.

## slurmdbd.conf

Only generated for a cluster with a managed db node. It points slurmdbd at the local MariaDB (`StorageHost=localhost`, `StorageLoc=slurm_acct_db`, `StorageUser=slurm`, which MariaDB authenticates by `unix_socket` as the OS user `slurm` slurmdbd runs as, or by the `StoragePass` the `slurmdbd` section sets), keeps the pid file in `/run/slurmdbd` and the log in `/var/log/slurm/slurmdbd.log`, and is owned by `slurm` with mode `0600`, as slurmdbd requires. It authenticates with `AuthType=auth/munge`, or with identity `clientIds` with `AuthType=auth/slurm` and `AuthInfo=use_client_ids`. The `slurmdbd` section extends it like the other sections. The fragments of its map form, in `slurmdbd.conf.d/`, get the same protection, as they can hold secrets such as a `StoragePass`: owned by `slurm` with mode `0600`, in a directory only `slurm` can read.

## slurm.key

Only generated with identity `clientIds`: 1024 random bytes, owned by `slurm` with mode `0600`, the key of `auth/slurm` and `cred/slurm`. Every node reads it from the config volume; there is no munge key. `sind get auth-key` prints it.

## sind-nodes.conf

This file contains node and partition definitions for sind-managed nodes. sind owns this file exclusively:

- `sind create cluster` generates initial node definitions
- `sind create worker` adds new managed nodes, replacing a definition of the same name, and removes them again when it fails
- `sind delete worker` removes managed nodes

Nodes with `managed: false` are excluded from this file.

Do not edit `sind-nodes.conf` directly. To add custom node definitions, create a separate file and add an include directive to `slurm.conf`.

## cgroup.conf

sind generates a `cgroup.conf` for cgroupv2 support, enabling resource isolation and accounting for Slurm jobs.

## plugstack.conf

Always created with an `include plugstack.conf.d/*` directive and an empty `.conf.d/` directory. This allows SPANK plugins to be dropped in as fragment files without additional configuration.

## Extending configuration via slurm sections

The `slurm` key in the cluster config allows extending any Slurm config file declaratively at creation time. Each section supports two forms:

**String form** — content appended directly to the config file:

```yaml
slurm:
  main: |
    SelectType=select/cons_tres
  cgroup: |
    ConstrainCores=yes
```

**Map form** — named fragments in a `.conf.d/` directory, included explicitly:

```yaml
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
    resources: |
      SelectType=select/cons_tres
```

This creates `slurm.conf.d/scheduling.conf` and `slurm.conf.d/resources.conf`, with explicit include directives appended to `slurm.conf` for each fragment file. Glob includes are not used because Slurm's main config parser does not support them (the SPANK `plugstack.conf` parser does).

Standalone sections (`gres`, `topology`) are only created when configured and require enabling in `slurm.conf` via the `main` section (e.g., `GresTypes=gpu`).

See [Cluster Configuration]({{< relref "/configuration/cluster-config" >}}) for the full schema.

## Version discovery

While it creates the cluster network and volumes, sind discovers the Slurm version of the controller's image by running an ephemeral container:

```bash
docker run --rm <controller image> slurmctld -V
# Output: "slurm 26.05.4"
```

The version is stored as the `sind.slurm.version` label on each node container and shown in the `SLURM` column of `sind get clusters` and `sind get cluster`. It does not change the generated configuration. Workers added with `sind create worker` copy the controller's label, and nodes with a different `image` carry the controller's version too: sind does not compare versions across images.

sind skips the discovery for unmanaged clusters, where the Slurm you provision may differ from the one in the image. Their `SLURM` column shows `-`.

## Post-creation customization

sind provides a working starter configuration. For additional customization after creation, users can:

- Edit config files on the controller (the config volume is writable)
- Add include files for custom configuration
- Replace the entire configuration (but `sind create worker` will fail for managed nodes without `sind-nodes.conf`)
