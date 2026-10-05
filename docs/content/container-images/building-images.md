---
weight: 510
title: "Building Images"
icon: "build"
description: "Official images and custom image requirements"
toc: true
---

{{< video "building-images" >}}

## Official images

sind publishes a multi-role node image for each supported Slurm release line to `ghcr.io/gsi-hpc/sind-node`:

| Slurm release line | Image tags |
|--------------------|------------|
| 26.05 | `latest`, `26.05`, `26.05.4` |
| 25.11 | `25.11`, `25.11.8` |

- `latest` is the newest release line. Builds of sind from source (`make build`, `go install`) use it when `defaults.image` is not specified.
- `<YY>.<MM>` (e.g. `25.11`) follows the newest patch release of that line. Use it to stay on one Slurm release line.
- `<YY>.<MM>.<patch>` (e.g. `25.11.8`) pins a patch release.

Each sind release also publishes the images it was released with:

- `vX.Y.Z` (e.g. `v0.11.0`) is the newest release line's image of sind vX.Y.Z. The release binaries of vX.Y.Z use it when `defaults.image` is not specified, so upgrading sind also brings the node image it was released with, and a pinned sind version keeps its Slurm version.
- `vX.Y.Z-<YY>.<MM>` (e.g. `v0.11.0-25.11`) is the image of each supported release line at sind vX.Y.Z.

Every tag is a multi-platform image for linux/amd64 and linux/arm64; Docker pulls the variant that matches the host. The images of all release lines share their layers up to Slurm, so pulling a second release line downloads little more than its Slurm.

The images are rebuilt when the image build changes, so the tags above pick up image fixes. They are also rebuilt every week with the current Rocky Linux packages, together with the tags of the newest sind release, so they pick up security updates. Tags of superseded patch releases, of older sind releases and of release lines that are no longer supported stay available but are not updated.

To run a cluster on a specific release line, set its image in the [cluster configuration](../../configuration/cluster-config/):

```yaml
defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11
```

Every tag can move: a rebuild publishes a new image under it. To run exactly one build, for reproducible CI for example, refer to the image by its digest, which `docker buildx imagetools inspect ghcr.io/gsi-hpc/sind-node:25.11` prints:

```yaml
defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11@sha256:<digest>
```

Each published image carries a build provenance attestation. Check that sind's image workflow built an image with the [GitHub CLI](https://cli.github.com/):

```bash
gh attestation verify oci://ghcr.io/gsi-hpc/sind-node:25.11 --repo GSI-HPC/sind \
  --signer-workflow GSI-HPC/sind/.github/workflows/image-build.yml
```

Every official image:

- Is based on Rocky Linux 10
- Builds Slurm, OpenMPI, PMIx, PRRTE, UCX and libjwt from source
- Contains the Slurm daemons (slurmctld, slurmdbd, slurmd, and from Slurm 26.05 on slurmrestd), munge, sshd, MariaDB and a full MPI stack
- Ships MariaDB's data directory initialised, so the first start of MariaDB on a db node skips `mariadb-install-db`
- Slurm is built with PMIx support (`--with-pmix`) for native PMIx job launch
- Includes nss_slurm, Slurm's NSS module for job users, as `/usr/lib64/libnss_slurm.so.2`; Slurm's `auth/slurm` and `cred/slurm` plugins, built with libjwt 1.x, and the `serializer/json` plugin they need, built with json-c; and `sackd`, Slurm's authentication daemon for login nodes
- From Slurm 26.05 on, includes slurmrestd, Slurm's REST API daemon, built with Rocky Linux's llhttp, with its JWT plugin and a `slurmrestd` user to run it as. Slurm 25.11 builds slurmrestd only with the archived nodejs http-parser, which Rocky Linux 10 does not package, so the 25.11 images have none
- OpenMPI is built with external PMIx, PRRTE, UCX, hwloc, and libevent
- Uses systemd as init (PID 1), with the journal capped at 32 MB in `/run`, which counts against the node's memory limit, and 64 MB on disk

sind enables the appropriate Slurm services based on node role once every node is ready.

### Building locally

Pre-built images are published to GHCR, so building locally is only needed when modifying the Dockerfile or developing sind itself. The `Dockerfile` and `docker-bake.hcl` are in the repository root. `SLURM_RELEASES` in `docker-bake.hcl` lists the Slurm release and tarball checksum of each image, and each entry becomes a bake target named `slurm-<YY>-<MM>`:

```bash
make image                                                # all release lines
docker buildx bake --set '*.platform=local' slurm-25-11   # a single release line
```

Both build for the host platform only. Without `--set '*.platform=local'`, bake builds every platform in `docker-bake.hcl`, which needs a builder that supports multi-platform builds, and compiles the whole stack under QEMU emulation for the other architecture, which takes hours. CI builds each platform on a native runner instead.

The Dockerfile has no default Slurm version. To build it without bake, pass `--build-arg SLURM_VERSION=<version>` and `--build-arg SLURM_SHA256=<checksum>`.

## Custom image requirements

Custom images must provide the following:

### All roles

- **systemd** as init at `/sbin/init`, and `/bin/sh`: sind starts each node with a short `/bin/sh` entrypoint that sets up the cgroup controllers and execs `/sbin/init`, so node containers do not use the image's `ENTRYPOINT` and `CMD`. sind's helper containers run commands in the image directly, so it should not set an `ENTRYPOINT` that wraps them
- `STOPSIGNAL SIGRTMIN+3`, systemd's shutdown request, so that `sind power shutdown` and `sind power reboot` shut the node down cleanly (see [Container settings](#container-settings))
- **sshd** service (enabled) — sind injects authorized_keys at runtime. The image contains no SSH host keys: each container generates its ed25519 host key on its first boot (see [SSH setup](#ssh-setup))
- `/etc/shadow` readable by root without `CAP_DAC_OVERRIDE` (see [Shadow file permissions](#shadow-file-permissions))
- **munge** service (enabled)
- Slurm client tools (srun, sbatch, squeue, etc.)
- Slurm's **mpi/pmix** plugin (`mpi_pmix.so`, which Slurm builds only when it finds PMIx, `--with-pmix`): the generated `slurm.conf` sets `MpiDefault=pmix`, and without the plugin every `srun` fails with "Invalid MPI type 'pmix'". For an image without it, set `MpiDefault=none` in the [`main` section]({{< relref "/configuration/cluster-config#slurm-section" >}})
- The `munge` and `slurm` users with the same uids as in the controller's image: sind sets the owner of the munge key, `slurm.key` and `slurmdbd.conf` (modes 0400 and 0600) from there, and munged, slurmctld, slurmdbd and, with identity `clientIds`, sackd read them as these users. Images built from the same distribution's packages agree; a cluster that mixes images of different origins may not

### Per-role requirements

| Role | Additional requirements |
|------|------------------------|
| controller | slurmctld installed, **not enabled** |
| db | mariadb-server (MariaDB 10.4 or later, whose `unix_socket` authentication sind uses for slurmdbd's `slurm` account) with the `mysql` client and slurmdbd installed, **not enabled**; root reaches MariaDB over its local socket without a password; slurmdbd runs as the OS user `slurm`; slurmdbd's unit creates `/run/slurmdbd` for the `slurm` user, which can write `/var/log/slurm` |
| worker | slurmd installed, **not enabled** |
| api | slurmrestd with its systemd unit, installed, **not enabled**: Slurm built with an HTTP parser (llhttp from Slurm 26.05 on) and `--with-jwt`; the unit runs slurmrestd as a user other than root and SlurmUser (Slurm's unit: `slurmrestd`) and listens on port 6820. sind checks for slurmrestd and the unit before it starts it, and adds a drop-in to the unit that sets `SLURMRESTD_SECURITY=disable_unshare_sysv,disable_unshare_files`, as the container denies slurmrestd's `unshare` |
| submitter | Slurm client tools only |

sind enables Slurm services based on the node's role (`systemctl enable --now`) once every node is ready. Services must be installed but **not** enabled in the image.

With [identity]({{< relref "/configuration/cluster-config#identity-section" >}}) `nssSlurm` or `clientIds`, the image of managed workers needs nss_slurm: `libnss_slurm.so.2` where glibc finds it (in the `ldconfig` cache or `/usr/lib64`), built from Slurm's `contribs/nss_slurm`, which `make install` skips. sind checks for it before it changes `/etc/nsswitch.conf`. With `clientIds` every node needs Slurm's `auth/slurm` plugin (`auth_slurm.so`), which Slurm builds only with libjwt 1.x (`--with-jwt`), and the `serializer/json` plugin it loads (`serializer_json.so`, built only with json-c, `--with-json`), the submitter also needs `sackd` with its systemd unit, installed but not enabled, and since sind masks `munge.service` on every node, no other unit may require it.

The Slurm requirements apply to managed clusters only. sind neither runs nor queries Slurm on an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}), so its image may leave Slurm for the provisioning under test to install.

### With users

With [`users`]({{< relref "/configuration/cluster-config#users-section" >}}), on managed and unmanaged clusters alike:

- The nodes that get the users need `groupadd` and `useradd` (shadow-utils), and `/bin/bash`, the users' login shell, which `sind ssh USER@NODE` and `sind enter --user` log in with
- The controller needs `/etc/skel`, from which sind creates the home directories
- No user or group in the image may have a declared name or ID. sind assigns IDs from 1000 up, so an image with an account at 1000 or above, such as Ubuntu's `ubuntu` user and group at 1000, needs explicit `uid` and `gid` values it does not use

### Container settings

The image must use `SIGRTMIN+3` as the stop signal (systemd's graceful shutdown signal) and declare the shared volumes. sind replaces the entrypoint of node containers, so `CMD` only matters when the image runs outside sind:

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

Root logs in with the public key sind injects. sind collects each node's ed25519 host key once sshd is up and pins it in the realm's `known_hosts`, so the image must not contain SSH host keys: keys baked into an image are shared by every node of every cluster, and by anyone who pulls the image. Each container generates its own on its first boot instead, before sshd starts. RHEL-family images such as Rocky Linux do that with `sshd-keygen@.service`, which `sshd.service` pulls in. The official image keeps only the ed25519 key:

```dockerfile
RUN rm -f /etc/ssh/ssh_host_* && \
    echo 'HostKey /etc/ssh/ssh_host_ed25519_key' > /etc/ssh/sshd_config.d/40-sind-hostkey.conf && \
    systemctl mask sshd-keygen@rsa.service sshd-keygen@ecdsa.service && \
    sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    mkdir -p /root/.ssh && chmod 700 /root/.ssh
```

Debian and Ubuntu generate the host keys when `openssh-server` is installed and do not create missing ones at boot: delete them in the image and run `ssh-keygen -A` from a oneshot unit ordered before `ssh.service`.

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
    getty.target \
    console-getty.service
```

Leave `sys-fs-fuse-connections.mount` unmasked: FUSE inside a node (`capAdd: [SYS_ADMIN]` and the `/dev/fuse` device) needs it, and without `/dev/fuse` it does nothing.
