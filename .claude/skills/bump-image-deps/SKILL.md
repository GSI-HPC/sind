---
name: bump-image-deps
description: Bump Slurm or another source-built component (UCX, PMIx, PRRTE, Open MPI) in the sind-node image. Updates version ARGs, sha256 checksums, docker-bake.hcl, and every doc and test reference. Use when a new Slurm point release is out or an issue asks to update a node-image component.
---

# Bump a node-image component

`Dockerfile` builds these from source, each pinned by version and sha256:

| Component | ARG / bake variable | Tarball URL |
|-----------|---------------------|-------------|
| Slurm | `SLURM_VERSION` | `https://download.schedmd.com/slurm/slurm-${V}.tar.bz2` |
| UCX | `UCX_VERSION` | `https://github.com/openucx/ucx/releases/download/v${V}/ucx-${V}.tar.gz` |
| PMIx | `PMIX_VERSION` | `https://github.com/openpmix/openpmix/releases/download/v${V}/pmix-${V}.tar.bz2` |
| PRRTE | `PRRTE_VERSION` | `https://github.com/openpmix/prrte/releases/download/v${V}/prrte-${V}.tar.bz2` |
| Open MPI | `OMPI_VERSION` | `https://download.open-mpi.org/release/open-mpi/v5.0/openmpi-${V}.tar.bz2` |

sind discovers the Slurm version from the image at runtime (`scontrol --version`), so a
bump needs no Go code changes, only the files below.

## Steps

1. **Pick the version.** Slurm tags on GitHub look like `slurm-25-11-8-1`, which means
   version `25.11.8`:
   `git ls-remote --tags https://github.com/SchedMD/slurm 'slurm-25-11-*'`.
   Stay on the current release line unless the issue asks for a new one. A new Slurm
   major (e.g. 26.05) needs its own review of the generated `slurm.conf`.
2. **Checksum the exact tarball the Dockerfile downloads:** `curl -fsSL <url> | sha256sum`.
   GitHub's auto-generated source archives differ from SchedMD's tarballs, so never use
   them. If the host is unreachable (cloud sessions may block `download.schedmd.com`),
   stop and tell the maintainer. Never guess or drop the checksum.
3. **Edit:**
   - both `ARG <NAME>_VERSION=` lines in `Dockerfile` (the builder stage and the final
     stage that sets the image labels)
   - the component's `ADD --checksum=sha256:` line
   - the variable default in `docker-bake.hcl` (the image tag is `sind-node:${SLURM_VERSION}`)
   - every other occurrence of the old version: `grep -rn '<old>' --exclude-dir=.git .`
     For Slurm these are `DESIGN.md`, `docs/content/architecture/{docker-resources,slurm-config}.md`,
     `docs/content/usage/diagnostics.md`, `pkg/slurm/version_test.go` and
     `pkg/cluster/status_test.go`.
4. **Check:** `go mod download && make test` and `make lint-docs`.
5. **Commit:** `build(image): bump slurm to X.Y.Z` with bullets for the ARG defaults,
   the checksum, and the docs/test references, plus `Closes #N` if there is an issue.
   Precedent: commit `53347ed`.
6. **PR to `next`.** Link the release notes
   (`https://github.com/SchedMD/slurm/releases/tag/slurm-X-Y-Z-1`). The CI Integration
   Test builds the image from the new tarball and runs the integration suite. That is the
   real verification, since cloud sessions have no Docker.
