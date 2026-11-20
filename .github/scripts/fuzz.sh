#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Runs every fuzz target of the module, one after the other, each for
# FUZZTIME (default 30s): go test -fuzz takes one target of one package.
# A target that fails does not stop the others. go test writes the input
# that failed to testdata/fuzz/<target>/ in the directory of the package
# and prints the command that runs it again; the script lists those files
# at the end and fails.
#
# Run it from the repository root: make fuzz, or FUZZTIME=5m make fuzz.
set -euo pipefail

fuzztime=${FUZZTIME:-30s}
failed=()

mapfile -t dirs < <(git grep --untracked -l '^func Fuzz' -- '*_test.go' | xargs -r -n1 dirname | sort -u)
if [[ ${#dirs[@]} -eq 0 ]]; then
  echo "no fuzz target found" >&2
  exit 1
fi
for dir in "${dirs[@]}"; do
  mapfile -t targets < <(go test -list '^Fuzz' "./$dir" | grep '^Fuzz')
  for target in "${targets[@]}"; do
    echo "== ./$dir $target"
    if ! go test -run '^$' -fuzz "^$target\$" -fuzztime "$fuzztime" "./$dir"; then
      failed+=("./$dir $target")
    fi
  done
done

if [[ ${#failed[@]} -gt 0 ]]; then
  echo
  echo "Fuzz targets that failed:"
  printf '  %s\n' "${failed[@]}"
  echo "Inputs that failed:"
  git ls-files --others --exclude-standard -- '*/testdata/fuzz/*' | sed 's/^/  /'
  exit 1
fi
echo
echo "All fuzz targets passed."
