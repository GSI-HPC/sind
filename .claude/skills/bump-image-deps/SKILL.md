---
name: bump-image-deps
description: Bump Slurm or another source-built component (UCX, PMIx, PRRTE, Open MPI) in the sind-node images, or add or drop a Slurm release line. Updates SLURM_RELEASES or the version ARGs, sha256 checksums, docker-bake.hcl, and every doc and test reference. Use when a new Slurm point release or release line is out or an issue asks to update a node-image component.
---

# Bump a node-image component

`Dockerfile` builds these from source, each pinned by version and sha256. Slurm's
version and checksum are build args without defaults; `SLURM_RELEASES` in
`docker-bake.hcl` sets them for each image, one entry per supported release line,
newest line first. The first entry is also tagged `latest`, sind's default image.

| Component | ARG / bake variable | Tarball URL |
|-----------|---------------------|-------------|
| Slurm | `SLURM_RELEASES` entry (`version`, `sha256`) | `https://download.schedmd.com/slurm/slurm-${V}.tar.bz2` |
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
   Bump each entry within its release line. Add a release line only when an issue
   asks for it: insert it as the first entry, since that one becomes `latest`, and
   review the generated `slurm.conf` and `cgroup.conf` (`pkg/slurm/config.go`) and the
   Dockerfile's `configure` flags against its `RELEASE_NOTES.md`. Dropping a line
   removes its entry; its published tags stay but are no longer rebuilt. Adding or
   dropping a line also updates the release lines listed in `README.md` and under
   "Supported Versions" in `DESIGN.md`.
2. **Checksum the exact tarball the Dockerfile downloads:** `curl -fsSL <url> | sha256sum`.
   GitHub's auto-generated source archives differ from SchedMD's tarballs, so never use
   them. If the host is unreachable (cloud sessions may block `download.schedmd.com`),
   stop and tell the maintainer. Never guess or drop the checksum.
3. **Edit:**
   - Slurm: the `version` and `sha256` of the release line's `SLURM_RELEASES` entry
     in `docker-bake.hcl`, and the tag table under "Official images" in
     `docs/content/container-images/building-images.md`. The Dockerfile needs no change.
   - Other components: both `ARG <NAME>_VERSION=` lines in `Dockerfile` (the builder
     stage and the final stage that sets the image labels), the component's
     `ADD --checksum=sha256:` line, and the variable default in `docker-bake.hcl`.
   - every other occurrence of the old version: `grep -rn '<old>' --exclude-dir=.git .`
     For Slurm these are `DESIGN.md`, `docs/content/architecture/{docker-resources,slurm-config}.md`,
     `docs/content/usage/diagnostics.md`, `pkg/slurm/version_test.go` and
     `pkg/cluster/status_test.go`.
   - `docker buildx bake --print` shows the resulting targets, build args and tags.
4. **Check:** `go mod download && make test` and `make lint-docs`.
5. **Commit:** `build(image): bump slurm to X.Y.Z` with bullets for the version, the
   checksum, and the docs/test references, plus `Closes #N` if there is an issue.
   Precedent: commit `53347ed` (from before `SLURM_RELEASES`).
6. **PR to `next`.** Link the release notes
   (`https://github.com/SchedMD/slurm/releases/tag/slurm-X-Y-Z-1`). The CI Integration
   Test builds the image of every release line from its tarball, on linux/amd64 and
   linux/arm64, and runs the integration suite against each. That is the real verification, since cloud sessions have no Docker.
