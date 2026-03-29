---
weight: 510
title: "Building Images"
icon: "build"
description: "Generic image and custom image requirements"
toc: true
---

## Generic image

sind provides a generic multi-role image that works for all node types:

```
ghcr.io/gsi-hpc/sind-node:latest
ghcr.io/gsi-hpc/sind-node:<slurm-version>
```

This is the default image when `defaults.image` is not specified.

The generic image:
- Is based on Rocky Linux 10
- Builds Slurm from source
- Contains all daemons (slurmctld, slurmd, munge, sshd)
- Uses systemd as init (PID 1)

sind enables the appropriate Slurm services based on node role at container start. The `Dockerfile` and `docker-bake.hcl` are in the repository root.

### Building the generic image

```bash
docker buildx bake
```

## Custom image requirements

Custom images must provide the following:

### All roles

- **systemd** as init (PID 1)
- **sshd** service (enabled) — sind injects authorized_keys at runtime
- **munge** service (enabled)
- Slurm client tools (srun, sbatch, squeue, etc.)

### Per-role requirements

| Role | Additional requirements |
|------|------------------------|
| controller | slurmctld installed, **not enabled** |
| worker | slurmd installed, **not enabled** |
| submitter | Slurm client tools only |

sind enables Slurm services at container start based on the node's role. Services must be installed but **not** enabled in the image.

### Container settings

The image should use `SIGRTMIN+3` as the stop signal (systemd's graceful shutdown signal) and declare the shared volumes:

```dockerfile
VOLUME ["/etc/slurm", "/etc/munge", "/data"]
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
```

### Munge setup

Munge directories must exist with correct ownership and permissions:

```dockerfile
RUN mkdir -p /etc/munge /var/lib/munge /var/log/munge /run/munge && \
    chown -R munge:munge /etc/munge /var/lib/munge /var/log/munge /run/munge && \
    chmod 700 /etc/munge /var/lib/munge /var/log/munge /run/munge
```

### SSH setup

SSH host keys should be pre-generated and root login configured:

```dockerfile
RUN ssh-keygen -A && \
    sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    mkdir -p /root/.ssh && chmod 700 /root/.ssh
```

### Masked systemd units

For a clean container environment, mask unnecessary systemd units:

```dockerfile
RUN systemctl mask \
    dev-hugepages.mount \
    sys-fs-fuse-connections.mount \
    systemd-logind.service \
    getty.target \
    console-getty.service
```

## Example Dockerfiles

See the `Dockerfile` in the repository root for a complete example.
