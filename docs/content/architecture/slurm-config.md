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
├── slurm.conf           # main config, includes sind-nodes.conf
├── sind-nodes.conf      # sind-managed node definitions
└── cgroup.conf          # cgroupv2 configuration
```

## slurm.conf

The main configuration file includes cluster name, controller host, and process tracking settings. It contains an include directive:

```
include /etc/slurm/sind-nodes.conf
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

## User customization

sind provides a working starter configuration. For additional customization, users can:

- Edit `slurm.conf` on the controller (the config volume is writable)
- Add include files for custom configuration
- Replace the entire configuration (but `sind create worker` will fail for managed nodes without `sind-nodes.conf`)
