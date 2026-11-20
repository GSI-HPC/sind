#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Compares the public Go API of sind, the packages under pkg/, and the API
# of the go-clikit packages it is part of, with that of a base commit, and
# fails on an incompatible change (apidiff -incompatible).
#
# usage: api-check.sh <base-commit>
#
# Run it from the repository root, with apidiff (golang.org/x/exp/cmd/apidiff)
# on PATH. It prints each incompatible change under the package it is in, a
# package that the base has and the head lacks as removed, and exits 1 when
# there is one. With SIND_API_BREAK=true, as for a pull request labelled
# breaking-api, it prints them and exits 0.
#
# go-clikit's progress package is the progress contract of library callers
# (progress.WithBus and its spans, see DESIGN.md), so that a go-clikit bump
# that removes from it is a break of sind's API too.
set -euo pipefail

base=${1:?usage: api-check.sh <base-commit>}
deps=(github.com/GSI-HPC/go-clikit/progress)

tmp=$(mktemp -d)
# shellcheck disable=SC2329 # the trap runs it
cleanup() {
  git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
git worktree add --quiet --detach "$tmp/base" "$base"

# public lists the import paths of the packages under pkg/ of the checkout
# in $1, but those of package main.
public() {
  (cd "$1" && go list -f '{{if ne .Name "main"}}{{.ImportPath}}{{end}}' ./pkg/... | sed '/^$/d' | sort)
}

# exports writes the export data of each of the packages to $2/<n>, in the
# checkout $1.
exports() {
  local dir=$1 out=$2 pkg
  mkdir -p "$out"
  shift 2
  for pkg in "$@"; do
    (cd "$dir" && apidiff -w "$out/${pkg//\//_}" "$pkg")
  done
}

mapfile -t old < <(public "$tmp/base")
mapfile -t new < <(public .)
exports "$tmp/base" "$tmp/old" "${old[@]}" "${deps[@]}"
exports . "$tmp/new" "${new[@]}" "${deps[@]}"

broken=0
for pkg in "${old[@]}" "${deps[@]}"; do
  if [[ ! -e "$tmp/new/${pkg//\//_}" ]]; then
    echo "$pkg: removed"
    broken=1
    continue
  fi
  report=$(apidiff -incompatible "$tmp/old/${pkg//\//_}" "$tmp/new/${pkg//\//_}")
  if [[ -n "$report" ]]; then
    echo "$pkg:"
    while IFS= read -r line; do echo "  $line"; done <<<"$report"
    broken=1
  fi
done

if [[ "$broken" == 0 ]]; then
  echo "No incompatible API change against $base."
  exit 0
fi
if [[ "${SIND_API_BREAK:-}" == true ]]; then
  echo "Incompatible API changes, allowed by the breaking-api label."
  exit 0
fi
echo "::error::Incompatible API changes against $base. A pull request that means them is labelled breaking-api, and its commits say BREAKING CHANGE (see Library API in DESIGN.md)."
exit 1
