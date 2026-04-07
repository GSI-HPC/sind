---
weight: 440
title: "Slurm Configuration"
icon: "tune"
description: "Generated configuration files and version discovery"
toc: true
---

## Generated files

sind generates a multi-file Slurm configuration and writes it to the `sind-<cluster>-config` volume:

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
└── topology.conf           # network topology (if slurm.topology is set)
```

## slurm.conf

The main configuration file includes cluster name, controller host, process tracking settings, and always contains:

```
include /etc/slurm/sind-nodes.conf
PlugStackConfig=/etc/slurm/plugstack.conf
```

sind does not modify `slurm.conf` after initial creation.

## sind-nodes.conf

This file contains node and partition definitions for sind-managed nodes. sind owns this file exclusively:

- `sind create cluster` generates initial node definitions
- `sind create worker` appends new managed nodes
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

sind discovers the Slurm version before creating any cluster resources by running an ephemeral container:

```bash
docker run --rm <image> scontrol --version
# Output: "slurm 25.11.4"
```

This happens once per unique image. The version is stored as a label on cluster resources (`sind.slurm.version`) and used for:

- Generating version-appropriate configuration
- Displaying version information in CLI output

### Mixed versions

When different images report different Slurm versions, sind logs a warning but continues. The controller image's version is used for configuration generation.

## Post-creation customization

sind provides a working starter configuration. For additional customization after creation, users can:

- Edit config files on the controller (the config volume is writable)
- Add include files for custom configuration
- Replace the entire configuration (but `sind create worker` will fail for managed nodes without `sind-nodes.conf`)
