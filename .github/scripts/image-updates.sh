#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Lists the pinned versions of the node image and of the mesh and monitoring
# images that lag behind upstream.
#
# usage: image-updates.sh [--verify-download] [repository-root]
#
# Prints one tab-separated line per finding and nothing when everything is
# current:
#
#   update    <component>  <pinned>  <latest>  newer release in the pinned line
#   new-line  <component>  <pinned>  <latest>  newest release of a newer line
#
# A line is the Slurm release line (25.11), the Open MPI series (5.0) or the
# major version of the other components. Slurm pins one version per supported
# line, so it can have several update rows; a new Slurm line is any line newer
# than the newest supported one. libjwt stays on 1.x on purpose (Slurm's
# auth/slurm uses its 1.x API), so it never reports a new line. Adding a line is
# a maintainer decision (see .claude/skills/update-image-deps).
#
# Pins are read from docker-bake.hcl (Slurm), the Dockerfile ARGs (UCX, PMIx,
# PRRTE, Open MPI, libjwt) and the image constants in pkg/mesh/mesh.go (CoreDNS)
# and pkg/observability/observability.go (Alloy, Prometheus, Loki, Grafana).
# Releases come from the final release tags (v1.2.3, or slurm-25-11-8-1 for
# Slurm) of each project's GitHub repository; release candidates are ignored.
# GITHUB_BASE replaces https://github.com, for tests. With --verify-download a
# version counts only once its tarball, or for an image its Docker Hub tag,
# is downloadable, since a tag can precede the upload.
set -euo pipefail

verify=false
if [[ "${1:-}" == --verify-download ]]; then
  verify=true
  shift
fi
root=${1:-.}
github=${GITHUB_BASE:-https://github.com}
hub=${DOCKERHUB_API:-https://hub.docker.com/v2/repositories}

# older A B: A < B as versions.
older() { [[ "$1" != "$2" && "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" == "$1" ]]; }

# downloadable COMPONENT VERSION: the artifact of a release exists upstream.
downloadable() {
  $verify || return 0
  local url
  case "$1" in
    slurm) url="https://download.schedmd.com/slurm/slurm-$2.tar.bz2" ;;
    ucx) url="https://github.com/openucx/ucx/releases/download/v$2/ucx-$2.tar.gz" ;;
    pmix) url="https://github.com/openpmix/openpmix/releases/download/v$2/pmix-$2.tar.bz2" ;;
    prrte) url="https://github.com/openpmix/prrte/releases/download/v$2/prrte-$2.tar.bz2" ;;
    ompi) url="https://download.open-mpi.org/release/open-mpi/v$(cut -d. -f1-2 <<<"$2")/openmpi-$2.tar.bz2" ;;
    libjwt) return 0 ;; # a git tag only
    coredns) url="$hub/coredns/coredns/tags/$2" ;;
    loki) url="$hub/grafana/loki/tags/$2" ;;
    grafana) url="$hub/grafana/grafana/tags/$2" ;;
    alloy) url="$hub/grafana/alloy/tags/v$2" ;;
    prometheus) url="$hub/prom/prometheus/tags/v$2" ;;
    *) echo "image-updates: no download check for $1" >&2; return 1 ;;
  esac
  curl -fsIL -m 30 -o /dev/null "$url" || {
    echo "image-updates: skipping $1 $2, $url is not downloadable" >&2
    return 1
  }
}

# check COMPONENT REPO TAG-STYLE DEPTH NEW-LINES PINNED...
# REPO is the upstream's owner/name, TAG-STYLE slurm or v, DEPTH the number of
# version fields that name a line, NEW-LINES yes or no. A pinned version may
# carry the leading v of its image tag; the output keeps it.
check() {
  local component=$1 repo=$2 style=$3 depth=$4 newlines=$5
  shift 5
  local tags released pinned bare prefix line latest newest="" newest_pin="" v
  tags=$(git ls-remote --tags "$github/$repo" '*') || {
    echo "image-updates: cannot list the tags of $github/$repo" >&2
    return 1
  }
  if [[ "$style" == slurm ]]; then
    released=$(sed -nE 's|.*refs/tags/slurm-([0-9]+)-([0-9]+)-([0-9]+)-[0-9]+$|\1.\2.\3|p' <<<"$tags")
  else
    released=$(sed -nE 's|.*refs/tags/v([0-9]+\.[0-9]+\.[0-9]+)$|\1|p' <<<"$tags")
  fi
  released=$(sort -uV <<<"$released")

  # latest_in LINE [NEWER-THAN]: newest downloadable release of the line.
  latest_in() {
    while read -r v; do
      [[ -z "${2:-}" ]] || older "$2" "$v" || return 0
      if downloadable "$component" "$v"; then
        echo "$v"
        return
      fi
    done < <(grep -E "^${1//./\\.}\.[0-9.]+$" <<<"$released" | sort -rV)
  }

  for pinned in "$@"; do
    prefix=""
    bare=$pinned
    if [[ "$pinned" == v* ]]; then
      prefix=v
      bare=${pinned#v}
    fi
    line=$(cut -d. -f1-"$depth" <<<"$bare")
    latest=$(latest_in "$line" "$bare")
    [[ -z "$latest" ]] || printf 'update\t%s\t%s\t%s\n' "$component" "$pinned" "$prefix$latest"
    if [[ -z "$newest" ]] || older "$newest" "$line"; then
      newest=$line
      newest_pin=$pinned
    fi
  done
  [[ "$newlines" == yes ]] || return 0

  # The newest release of every line newer than the newest pinned one.
  while read -r line; do
    older "$newest" "$line" || continue
    latest=$(latest_in "$line")
    [[ -z "$latest" ]] || printf 'new-line\t%s\t%s\t%s\n' "$component" "$newest_pin" "$prefix$latest"
  done < <(cut -d. -f1-"$depth" <<<"$released" | sort -uV)
}

# pins FILE SED-EXPRESSION: the captured group of every matching line.
pins() {
  local out
  out=$(sed -nE "$2" "$root/$1")
  if [[ -z "$out" ]]; then
    echo "image-updates: no pin found in $1 by $2" >&2
    return 1
  fi
  echo "$out"
}

# The Slurm versions are the version lines inside the SLURM_RELEASES block.
mapfile -t slurm < <(
  pins docker-bake.hcl '/^variable "SLURM_RELEASES"/,/^}/ s/^ *version *= *"([0-9.]+)".*/\1/p'
)
check slurm SchedMD/slurm slurm 2 yes "${slurm[@]}"

arg() { pins Dockerfile "s/^ARG $1_VERSION=([0-9.]+)\$/\\1/p" | head -n1; }
check ucx openucx/ucx v 1 yes "$(arg UCX)"
check pmix openpmix/openpmix v 1 yes "$(arg PMIX)"
check prrte openpmix/prrte v 1 yes "$(arg PRRTE)"
check ompi open-mpi/ompi v 2 yes "$(arg OMPI)"
check libjwt benmcollins/libjwt v 1 no "$(arg LIBJWT)"

image() { pins "$1" "s|^[[:space:]]*$2 += \"$3:([^\"]+)\"\$|\\1|p"; }
check coredns coredns/coredns v 1 yes "$(image pkg/mesh/mesh.go 'const DNSImage' coredns/coredns)"
check alloy grafana/alloy v 1 yes "$(image pkg/observability/observability.go AlloyImage grafana/alloy)"
check prometheus prometheus/prometheus v 1 yes "$(image pkg/observability/observability.go PrometheusImage prom/prometheus)"
check loki grafana/loki v 1 yes "$(image pkg/observability/observability.go LokiImage grafana/loki)"
check grafana grafana/grafana v 1 yes "$(image pkg/observability/observability.go GrafanaImage grafana/grafana)"
