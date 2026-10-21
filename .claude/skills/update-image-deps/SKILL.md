---
name: update-image-deps
description: Check every pinned version of the sind images against upstream (Slurm, UCX, PMIx, PRRTE, Open MPI, libjwt, CoreDNS, Alloy, Prometheus, Loki, Grafana) and apply the pending updates in one PR to next. Use when a maintainer asks to check for or apply version updates, or when the weekly "Image updates" issue is open.
---

# Update the image dependencies

`.github/scripts/image-updates.sh` compares every version that sind pins with the newest
upstream release and prints what lags behind. `bump-image-deps` holds the procedure to
bump one component (checksum, cross-check, edits, checks); this skill adds the discovery,
the order of work and the PR. The weekly workflow `.github/workflows/image-updates.yml`
runs the same script and keeps one open issue labelled `image-updates`.

```
image-updates.sh ──► rows? ──no──► report "up to date", stop
                       │
                      yes
                       ▼
  per component (and per Slurm line):  bump-image-deps ──► one commit
                       ▼
          make test, lint-docs ──► PR to next (Closes the issue)
```

## What is checked

| Component | Pinned in | Line (a new one is only reported) |
|-----------|-----------|-----------------------------------|
| Slurm | `SLURM_RELEASES`, `docker-bake.hcl` | release line `YY.MM`, one pin per supported line |
| UCX, PMIx, PRRTE | `ARG <NAME>_VERSION`, `Dockerfile` | major version |
| Open MPI | `ARG OMPI_VERSION`, `Dockerfile` | series `X.Y` (5.0) |
| libjwt | `ARG LIBJWT_VERSION`, `Dockerfile` | major, never reported: stays on 1.x |
| CoreDNS | `DNSImage`, `pkg/mesh/mesh.go` | major version |
| Alloy, Prometheus, Loki, Grafana | `<Name>Image`, `pkg/observability/observability.go` | major version |

Not checked: the Rocky Linux base image (a floating major tag, rebuilt weekly by the
image workflow), Go modules and GitHub Actions (Dependabot).

## Steps

1. **Check.** From the repository root:

   ```bash
   .github/scripts/image-updates.sh [--verify-download]
   ```

   It prints tab-separated rows, nothing when current. `--verify-download` ignores a
   release whose tarball or Docker Hub tag is not published yet.

   | Row | Meaning | Action |
   |-----|---------|--------|
   | `update <component> <pinned> <latest>` | newer release in the pinned line | bump it |
   | `new-line <component> <pinned> <latest>` | newest release of a newer line | report it, never add it unasked |

   A `new-line` is a maintainer decision: a Slurm line becomes `latest` and needs the
   review in `bump-image-deps` step 1; a new PMIx, PRRTE or Open MPI line must stay
   compatible with the other two and with Slurm's `--mpi=pmix`.

2. **Find the open issue:** `gh issue list --label image-updates --state open`. Its
   number goes into the commit bodies as `Closes #N`. No issue is not an error.

3. **Branch** from `next`:
   `git fetch origin next && git checkout -B claude/image-updates origin/next`.

4. **Bump each `update` row** by `bump-image-deps`, in a commit of its own: for Slurm one
   commit per release line (`build(image): bump slurm to X.Y.Z`), for another component
   `build(image): bump <component> to <version>`. Slurm first, then the rest in table
   order. Read the release notes of every bump that changes the minor or major
   version, and of every monitoring image or CoreDNS bump, and say in the commit body
   what you checked.
   - A host that the session cannot reach (cloud sessions may block
     `download.schedmd.com` and `download.open-mpi.org`) stops that component only:
     name the host in the final report, never guess a checksum, and go on with the rest.
   - If no cross-check source matches a checksum, skip that component and report it.

5. **Verify.** `go mod download && make test`, `make lint-docs`, and
   `docker buildx bake --print` if the node image changed. Run the script again: it
   must print only the skipped components and `new-line` rows.

6. **Open the PR to `next`**, as `steward` describes. The title is the commit subject
   of a single bump, or `build(image): update image dependencies` for several. The body
   lists each component with old and new version, a link to the release notes, the
   cross-check source, the skipped components with the reason, and the `new-line` rows.
   The CI Integration Test, which builds every Slurm release line on both platforms and
   runs the monitoring stack, is the real verification.
