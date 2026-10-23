#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Prepare an Ubuntu GitHub runner to record the overview video (record.py):
# sind from this checkout, mise, host DNS for *.sind, and the Docker Hub
# images of the scenario. Run it from the repository root.
set -euo pipefail

bin="$HOME/.local/bin"
mkdir -p "$bin"
[ -n "${GITHUB_PATH:-}" ] && echo "$bin" >>"$GITHUB_PATH"

# sind from this checkout, with its version from git describe.
make build
mv "sind-$(go env GOOS)-$(go env GOARCH)" "$bin/sind"

# mise, for the install shot: it installs the latest sind release with
# its own shims, which only that shot puts first on PATH.
curl -fsSL https://mise.run | MISE_INSTALL_PATH="$bin/mise" sh

# Host DNS: sind points systemd-resolved at the mesh DNS when polkit lets
# it. The rules that sind doctor prints need an active session, which a
# runner's job has not; allow the runner's user instead.
if ! dpkg -s polkitd >/dev/null 2>&1; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq --no-install-recommends polkitd
fi
sudo mkdir -p /etc/polkit-1/rules.d
sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules >/dev/null <<RULES
polkit.addRule(function(action, subject) {
    if (["org.freedesktop.resolve1.set-dns-servers",
         "org.freedesktop.resolve1.set-domains",
         "org.freedesktop.resolve1.revert"].indexOf(action.id) >= 0 &&
        subject.user == "$(id -un)") {
        return polkit.Result.YES;
    }
});
RULES
sudo systemctl restart polkit

# Docker Hub rate-limits anonymous pulls per IP, which runners share: pull
# the Docker Hub images of the scenario (the mesh's CoreDNS, the monitoring
# stack and alpine for the connect shot) from Google's mirror, and tag them
# with their Docker Hub names, as the integration tests do.
dns=$(sed -n 's/^const DNSImage = "\(.*\)"$/\1/p' pkg/mesh/mesh.go)
stack=$(sed -n 's/^\t[A-Za-z]*Image *= "\(.*\)"$/\1/p' pkg/observability/observability.go)
if [ -z "$dns" ] || [ "$(echo "$stack" | wc -w)" -ne 4 ]; then
  echo "::error::image constants not found in pkg/mesh/mesh.go or pkg/observability/observability.go"
  exit 1
fi
# shellcheck disable=SC2086 # one word per image
for image in "$dns" $stack alpine:3; do
  case "$image" in
    */*) path=$image ;;
    *) path=library/$image ;;
  esac
  docker pull -q "mirror.gcr.io/$path"
  docker tag "mirror.gcr.io/$path" "$image"
done
docker pull -q ghcr.io/gsi-hpc/sind-node:latest

"$bin/sind" doctor
