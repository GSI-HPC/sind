#!/usr/bin/env bash
# SPDX-License-Identifier: LGPL-3.0-or-later
#
# Prepare an Ubuntu GitHub runner to record the overview video (record.py):
# sind from this checkout, mise, host DNS for *.sind, a raised inotify
# instance limit, and the images of the scenario. Run it from the repository
# root.
set -euo pipefail

bin="$HOME/.local/bin"
mkdir -p "$bin"
[ -n "${GITHUB_PATH:-}" ] && echo "$bin" >>"$GITHUB_PATH"

# sind from this checkout, with its version from git describe. Without the
# build's VCS stamp it defaults to the node image latest, which is pulled
# below, also in a clean checkout of a release tag, whose vX.Y.Z image the
# image workflow may still be building.
GOFLAGS=-buildvcs=false make build
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

# The Ubuntu 26.04 runner image allows 128 inotify instances per user,
# which the recorded clusters can use up: their node containers then exit
# with code 255.
sudo sysctl -w fs.inotify.max_user_instances=8192

# Docker Hub rate-limits anonymous pulls per IP, which runners share: pull
# the Docker Hub images of the scenario (the mesh's CoreDNS and the
# monitoring stack, which tools/images prints, and alpine for the connect
# shot) from Google's mirror, and tag them with their Docker Hub names, as
# the integration tests do.
images=$(go run ./tools/images)
# shellcheck disable=SC2086 # one word per image
for image in $images alpine:3; do
  case "$image" in
    */*) path=$image ;;
    *) path=library/$image ;;
  esac
  docker pull -q "mirror.gcr.io/$path"
  docker tag "mirror.gcr.io/$path" "$image"
done
docker pull -q ghcr.io/gsi-hpc/sind-node:latest

"$bin/sind" doctor
