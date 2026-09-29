#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# ci-runner-prep.sh — make a GitHub-hosted ubuntu-24.04 runner fit an image build
# (.github/workflows/images.yml).
#
#   sh build/ci-runner-prep.sh [NEED_GB]
#
# A standard hosted runner leaves ~14 GB free on /; the kernel build alone needs ~30 GB and
# an ISO build ~20 GB. /mnt is on the same root disk (it used to be a separate ~70 GB
# volume), so the room has to come from the root disk itself:
#   1. remove the preinstalled toolchains this build never uses (.NET, Android SDK, GHC,
#      Swift, PowerShell, Boost, the hosted tool cache, /opt/google, /opt/microsoft, global
#      node modules) and every preinstalled docker image. Common practice; on 2026-09-05 a
#      subset of this list took kernel-build.yml's runner from 14 GB to 38 GB free;
#   2. put the build cache (XDG_CACHE_HOME=/mnt/cache) and BOTH podman stores, rootless and
#      root (buildiso's privileged container), under /mnt, and fail now, not hours in with
#      "No space left on device", unless /mnt has NEED_GB free (default 30);
#   3. install the host tools local-iso.sh needs that Ubuntu lacks (repo-add, bsdtar).
# Runs ONLY on a GitHub-hosted runner: it deletes system directories and rewrites
# /etc/containers/storage.conf, which is fine on a throwaway VM and nowhere else. This
# repository runs no job on a self-hosted runner (docs/governance/CI.md, "Runners").
set -eu

NEED_GB="${1:-30}"
case "$NEED_GB" in *[!0-9]*|"") echo "ci-runner-prep: NEED_GB must be a number" >&2; exit 1 ;; esac
[ "${GITHUB_ACTIONS:-}" = true ] && [ "${RUNNER_ENVIRONMENT:-}" = github-hosted ] || {
	echo "ci-runner-prep: only on a GitHub-hosted runner (RUNNER_ENVIRONMENT=github-hosted)" >&2
	exit 1
}
echo "== before"; df -h / /mnt
sudo rm -rf /usr/share/dotnet /opt/ghc /usr/local/.ghcup /usr/local/lib/android /opt/hostedtoolcache \
	/usr/local/share/boost /usr/share/swift /usr/local/share/powershell /opt/microsoft /opt/google \
	/usr/local/lib/node_modules "${AGENT_TOOLSDIRECTORY:-/nonexistent}"
sudo docker system prune -af >/dev/null 2>&1 || true
# actions/setup-* install into the tool cache it just emptied: give it back, empty and ours.
sudo mkdir -p "${RUNNER_TOOL_CACHE:-/opt/hostedtoolcache}"
sudo chown "$(id -u):$(id -g)" "${RUNNER_TOOL_CACHE:-/opt/hostedtoolcache}"

sudo mkdir -p /mnt/cache /mnt/containers/user /mnt/containers/root
free_kb=$(df -Pk /mnt | awk 'NR == 2 { print $4 }')
[ "$free_kb" -ge $((NEED_GB * 1024 * 1024)) ] || {
	echo "ci-runner-prep: /mnt has $((free_kb / 1024 / 1024)) GB free; this build needs about $NEED_GB GB" >&2
	exit 1
}
sudo chown -R "$(id -u):$(id -g)" /mnt/cache /mnt/containers/user
mkdir -p "$HOME/.config/containers"
printf '[storage]\ndriver = "overlay"\ngraphroot = "/mnt/containers/user"\n' > "$HOME/.config/containers/storage.conf"
printf '[storage]\ndriver = "overlay"\nrunroot = "/run/containers/storage"\ngraphroot = "/mnt/containers/root"\n' \
	| sudo tee /etc/containers/storage.conf >/dev/null

sudo apt-get update -q
sudo apt-get install -y -q zstd libarchive-tools pacman-package-manager
for t in podman repo-add bsdtar zstd openssl go; do
	command -v "$t" >/dev/null 2>&1 || echo "ci-runner-prep: note: $t is not on PATH yet" >&2
done
echo "== after"; df -h / /mnt
