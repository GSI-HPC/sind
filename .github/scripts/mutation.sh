#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Runs mutation tests on the packages of MUTATION_PACKAGES (default: the
# packages that parse and generate text), one after the other, with
# gremlins: it changes one operator of the code at a time (a mutant) and
# runs the tests of the package. A mutant that the tests fail is killed, one
# that they pass lived: no test tells the changed code from the right one.
#
# The script prints, for each package, the mutants killed, lived, timed
# out (a mutant that loops; not counted) and not covered, and the score,
# killed / (killed + lived), and then each mutant that lived as
# <file>:<line>:<column> <kind>. With GITHUB_STEP_SUMMARY set it writes
# the same there as Markdown. A mutant that lives does not fail the script:
# the score is a report, not a gate. It fails when gremlins fails.
#
# gremlins gives the tests of a mutant the time of the first run of the
# tests times a coefficient, 3 if not set. A mutant of a small package
# then times out while it builds, so the script sets 10
# (MUTATION_TIMEOUT_COEFFICIENT).
#
# Run it from the repository root: make mutation, or
# MUTATION_PACKAGES="pkg/mesh" make mutation.
set -euo pipefail

read -r -a packages <<<"${MUTATION_PACKAGES:-internal/natsort internal/mcpconfig pkg/config internal/slurm pkg/mesh}"
gremlins=(go run "github.com/go-gremlins/gremlins/cmd/gremlins@${GREMLINS_VERSION:-v0.6.0}")
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

for pkg in "${packages[@]}"; do
  echo "== ./$pkg"
  "${gremlins[@]}" unleash "./$pkg" --silent --timeout-coefficient "${MUTATION_TIMEOUT_COEFFICIENT:-10}" \
    --output "$out/${pkg//\//-}.json"
done

rows=()
lived=()
for pkg in "${packages[@]}"; do
  json="$out/${pkg//\//-}.json"
  rows+=("$(jq -r --arg pkg "$pkg" '
    [.files[]?.mutations[]? | select(.status == "TIMED OUT")] as $timedOut
    | [$pkg, .mutants_killed, .mutants_lived, ($timedOut | length), .mutants_not_covered,
       (if .mutants_killed + .mutants_lived > 0 then (.test_efficacy * 10 | round / 10 | tostring) + "%" else "-" end)]
    | @tsv' "$json")")
  while IFS= read -r mutant; do
    lived+=("$mutant")
  done < <(jq -r --arg pkg "$pkg" '
    .files[]? | .file_name as $file | .mutations[]? | select(.status == "LIVED")
    | "\($pkg)/\($file | sub(".*/"; "")):\(.line):\(.column) \(.type)"' "$json" | sort -t: -k1,1 -k2,2n -k3,3n)
done

echo
{
  printf 'package\tkilled\tlived\ttimed out\tnot covered\tscore\n'
  printf '%s\n' "${rows[@]}"
} | awk -F '\t' '{ printf "%-26s %7s %6s %10s %12s %7s\n", $1, $2, $3, $4, $5, $6 }'
echo
if [[ ${#lived[@]} -eq 0 ]]; then
  echo "No mutant lived."
else
  echo "Mutants that lived:"
  printf '  %s\n' "${lived[@]}"
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "### Mutation score"
    echo
    echo "| Package | Killed | Lived | Timed out | Not covered | Score |"
    echo "|---|---:|---:|---:|---:|---:|"
    for row in "${rows[@]}"; do
      IFS=$'\t' read -r pkg killed alive timedOut notCovered score <<<"$row"
      echo "| \`$pkg\` | $killed | $alive | $timedOut | $notCovered | $score |"
    done
    echo
    if [[ ${#lived[@]} -eq 0 ]]; then
      echo "No mutant lived."
    else
      echo "<details><summary>${#lived[@]} mutants lived</summary>"
      echo
      echo '```'
      printf '%s\n' "${lived[@]}"
      echo '```'
      echo
      echo "</details>"
    fi
  } >> "$GITHUB_STEP_SUMMARY"
fi
