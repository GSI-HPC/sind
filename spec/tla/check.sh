#!/usr/bin/env bash
# Runs the TLA+ models of this directory with the TLC model checker and
# compares the result of each configuration with the one it expects.
#
# A configuration <Module>.<name>.cfg belongs to <Module>.tla. Its line
# "\* expect: pass" or "\* expect: <Invariant> is violated" is the result
# that TLC must give: the code configurations pass, and a configuration of
# a design that the model rules out must fail, which shows that the model
# still finds the defect.
#
# Needs Java 11 or later. TLA2TOOLS names a tla2tools.jar to use; without
# it the script downloads the pinned release into the user's cache.
set -euo pipefail
cd "$(dirname "$0")"

VERSION=1.7.4
SHA256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88

jar=${TLA2TOOLS:-}
if [[ -z "$jar" ]]; then
  jar=${XDG_CACHE_HOME:-$HOME/.cache}/sind/tla2tools-$VERSION.jar
  if [[ ! -f "$jar" ]]; then
    mkdir -p "$(dirname "$jar")"
    curl -fsSL -o "$jar.part" "https://github.com/tlaplus/tlaplus/releases/download/v$VERSION/tla2tools.jar"
    echo "$SHA256  $jar.part" | sha256sum -c --quiet -
    mv "$jar.part" "$jar"
  fi
fi

# TLC writes its states and a trace file here.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failed=0
count=0
for cfg in *.cfg; do
  count=$((count + 1))
  module=${cfg%%.*}
  expect=$(sed -n 's/^\\\* expect: //p' "$cfg")
  if [[ -z "$expect" ]]; then
    echo "FAIL $cfg: no \"\\* expect:\" line"
    failed=1
    continue
  fi

  # TLC ends with a status other than 0 when it finds a violation.
  out=$(java -XX:+UseParallelGC -cp "$jar" tlc2.TLC -workers auto -deadlock \
    -metadir "$work/$cfg" -config "$cfg" "$module.tla" 2>&1) || true
  if grep -q 'Model checking completed. No error has been found.' <<<"$out"; then
    got=pass
  else
    got=$(sed -n 's/^Error: Invariant \(.*\) is violated\.$/\1 is violated/p' <<<"$out" | head -n 1)
  fi
  states=$(sed -n 's/^\([0-9]* states generated, [0-9]* distinct states found\).*/\1/p' <<<"$out" | tail -n 1)

  if [[ "$got" == "$expect" ]]; then
    echo "ok   $cfg: $expect ($states)"
  else
    echo "FAIL $cfg: expected \"$expect\", got \"${got:-an error}\""
    printf '    %s\n' "${out//$'\n'/$'\n'    }"
    failed=1
  fi
done

if [[ $count -eq 0 ]]; then
  echo "No configuration found" >&2
  exit 1
fi
exit $failed
