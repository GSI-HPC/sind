---
weight: 510
title: "Building Images"
icon: "build"
description: "Official images and custom image requirements"
toc: true
---

## Official images

sind publishes a multi-role node image for each supported Slurm release line to `ghcr.io/gsi-hpc/sind-node`:

| Slurm release line | Image tags |
|--------------------|------------|
| 25.11 | `latest`, `25.11`, `25.11.6` |

- `latest` is the newest release line. sind uses it when `defaults.image` is not specified.
- `<YY>.<MM>` (e.g. `25.11`) follows the newest patch release of that line. Use it to stay on one Slurm release line.
- `<YY>.<MM>.<patch>` (e.g. `25.11.6`) pins a patch release.

The images are rebuilt when the image build changes, so the tags above pick up image fixes. Tags of superseded patch releases and of release lines that are no longer supported stay available but are not updated.

To run a cluster on a specific release line, set its image in the [cluster configuration](../../configuration/cluster-config/):

```yaml
defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11
```

Every official image:

- Is based on Rocky Linux 10
- Builds Slurm, OpenMPI, PMIx, PRRTE, and UCX from source
- Contains all Slurm daemons (slurmctld, slurmd, munge, sshd) and a full MPI stack
- Slurm is built with PMIx support (`--with-pmix`) for native PMIx job launch
- OpenMPI is built with external PMIx, PRRTE, UCX, hwloc, and libevent
- Uses systemd as init (PID 1)

sind enables the appropriate Slurm services based on node role at container start.

### Building locally

Pre-built images are published to GHCR, so building locally is only needed when modifying the Dockerfile or developing sind itself. The `Dockerfile` and `docker-bake.hcl` are in the repository root. `SLURM_RELEASES` in `docker-bake.hcl` lists the Slurm release and tarball checksum of each image, and each entry becomes a bake target named `slurm-<YY>-<MM>`:

```bash
make image                       # all release lines
docker buildx bake slurm-25-11   # a single release line
```

The Dockerfile has no default Slurm version. To build it without bake, pass `--build-arg SLURM_VERSION=<version>` and `--build-arg SLURM_SHA256=<checksum>`.

## Custom image requirements

Custom images must provide the following:

### All roles

- **systemd** as init (PID 1)
- **sshd** service (enabled) — sind injects authorized_keys at runtime
- `/etc/shadow` readable by root without `CAP_DAC_OVERRIDE` (see [Shadow file permissions](#shadow-file-permissions))
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

### Shadow file permissions

Root must be able to read `/etc/shadow` as its owner, without `CAP_DAC_OVERRIDE`: mode `0400 root:root`, or `0640 root:shadow` as on Debian and Ubuntu. RHEL-family images such as Rocky Linux ship it with mode `0000` and need a fix:

```dockerfile
RUN chmod 0400 /etc/shadow /etc/gshadow
```

On login, sshd's PAM account check (`pam_unix`) runs `unix_chkpwd` to read root's shadow entry. On nodes with `securityOpt: [apparmor=unconfined]`, AppArmor attaches the host's `unix-chkpwd` profile (shipped by Ubuntu 24.04, for example, including GitHub's `ubuntu-latest` runners) to that binary. The profile denies `CAP_DAC_OVERRIDE`, so with mode `0000` the check fails and `sind ssh` into those nodes ends with `Connection closed`. Nodes on Docker's default AppArmor profile are not affected.

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
