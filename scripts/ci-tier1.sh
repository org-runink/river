#!/bin/sh
# ci-tier1.sh — Tier 1 CI (lint + hermetic checks) in an EPHEMERAL ROOTLESS PODMAN container.
#
# Runs the same way on a laptop and on a GitHub-hosted runner:
#   sh scripts/ci-tier1.sh
# Outside the container it re-executes itself inside one: rootless podman, no network,
# the source tree mounted READ-ONLY, a tmpfs /tmp, all capabilities dropped, removed on
# exit. Nothing the checks run can reach the network or write to the checkout, so the
# same job is safe to point at untrusted pull-request code. See docs/governance/CI.md.
#
# RIVER_TIER1_NO_CONTAINER=1 runs the checks directly on the host (needs shellcheck).
set -eu

# Built from ci/tier1.Containerfile (alpine pinned by digest + the lint tools).
IMAGE="localhost/river-tier1:latest"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ "${RIVER_TIER1_IN_CONTAINER:-0}" != 1 ] && [ "${RIVER_TIER1_NO_CONTAINER:-0}" != 1 ]; then
	command -v podman >/dev/null 2>&1 || {
		echo "ci-tier1: podman not found (or set RIVER_TIER1_NO_CONTAINER=1)" >&2; exit 1; }
	[ "$(id -u)" != 0 ] || echo "ci-tier1: WARNING: running podman as root; Tier 1 is meant to be rootless" >&2
	podman build -q -t "$IMAGE" -f "$ROOT/ci/tier1.Containerfile" "$ROOT/ci" >/dev/null
	# A git worktree keeps its metadata outside the checkout; mount it read-only at the same
	# path so `git ls-files` works inside (`river lint python-purge` uses it).
	gitmounts=""
	for d in "$(git -C "$ROOT" rev-parse --path-format=absolute --git-dir 2>/dev/null)" \
	         "$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"; do
		case "$d" in ""|"$ROOT"/*) ;; *) gitmounts="$gitmounts -v $d:$d:ro" ;; esac
	done
	# shellcheck disable=SC2086
	exec podman run --rm --network=none --read-only --tmpfs /tmp \
		--cap-drop=ALL --security-opt=no-new-privileges \
		-v "$ROOT:/src:ro" $gitmounts -w /src -e RIVER_TIER1_IN_CONTAINER=1 \
		-e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0="*" \
		"$IMAGE" sh scripts/ci-tier1.sh
fi

cd "$ROOT"
rc=0
run() { echo "== $*"; "$@" || { echo "ci-tier1: FAILED: $*" >&2; rc=1; }; }

# 0. The `river` CLI (docs/GO-CLI.md), built first: the lints ported from scripts/lint-*.sh
#    and the host-side tests (were tests/*.sh) below are its subcommands, all run from this one
#    build. The checks of its own code (gofmt, vet, tests) are step 10b.
RIVER="${TMPDIR:-/tmp}/river"
export RIVER
river_build() {
	command -v go >/dev/null 2>&1 || { echo "go missing" >&2; return 1; }
	rm -f "$RIVER"
	(
		export GOCACHE="${TMPDIR:-/tmp}/river-cli-gocache" GOPATH="${TMPDIR:-/tmp}/river-cli-gopath" GOTOOLCHAIN=local \
			GOFLAGS="-buildvcs=false -mod=vendor" CGO_ENABLED=0 GOENV=off
		cd cli && go build -o "$RIVER" ./cmd/river
	)
}
run river_build
# 1. shellcheck every shell script still in the tree, warnings and up (fails when shellcheck is
#    missing or finds nothing: --strict).
run "$RIVER" lint shell --repo . --severity=warning --strict
# 2. installer steps and their baked copies agree.
run "$RIVER" lint installer-sync --repo .
#    ... and every step the image ships is run by a step list (an edition or runink-install).
run "$RIVER" lint edition-steps --repo .
# 3. branding copies in sync and within the size budget.
run "$RIVER" lint branding-sync --repo .
# 4. no python in the build path; go provisioned where build-iso-box.sh needs it.
run "$RIVER" lint python-purge --repo .
# 5. the k0s pin agrees across build/config.env and the PKGBUILD.
run "$RIVER" lint k0s-pin --repo .
# 6. the AUR packaging (packaging/aur/) pins the same kernel, configs and OpenZFS as the tree.
run "$RIVER" lint aur-sync --repo .
run "$RIVER" lint profile-manifest --repo .
# 7. the host firewall: default-deny input, no fixed port, forward default-drop, the pairing
#    ports link-local and live-only (loaded into a namespace where nft and userns allow).
run "$RIVER" lint firewall --repo .
# 10. the documentation website builds offline from exactly the pinned Hextra theme and FlexSearch.
run "$RIVER" lint docs-vendor --repo .
# 10b. The `river` CLI and its pipeline package (docs/GO-CLI.md): gofmt, vet and tests of pkg/
#      and cli/ (vendored, so no network), then the lints ported to it: mistral.rs is the only
#      inference engine, and no new shell script (scripts/shell-ratchet.txt only shrinks).
river_cli() {
	command -v go >/dev/null 2>&1 || { echo "go missing" >&2; return 1; }
	(
		export GOCACHE="${TMPDIR:-/tmp}/river-cli-gocache" GOPATH="${TMPDIR:-/tmp}/river-cli-gopath" GOTOOLCHAIN=local \
			GOFLAGS="-buildvcs=false -mod=vendor" CGO_ENABLED=0 GOENV=off
		unformatted="$(gofmt -l pkg cli/cmd cli/internal)"
		[ -z "$unformatted" ] || { echo "gofmt: $unformatted" >&2; exit 1; }
		(cd pkg && GOFLAGS=-buildvcs=false go vet ./... && GOFLAGS=-buildvcs=false go test -count=1 ./...) || exit 1
		# pkg/ is also vendored into cli/vendor (a replaced local module): the copy must match.
		for f in $(cd pkg && find . -name "*.go" ! -name "*_test.go"); do
			cmp -s "pkg/$f" "cli/vendor/github.com/org-runink/river/pkg/$f" || { echo "cli/vendor is stale for pkg/$f: run go mod vendor in cli/" >&2; exit 1; }
		done
		cd cli && go vet ./... && go test -count=1 ./...
	)
}
run river_cli
run "$RIVER" lint one-engine --repo .
run "$RIVER" lint shell-ratchet --repo .
run "$RIVER" lint reuse --repo .
# 10c. the public tree names no downstream product, private repository, internal address, runner
#      label or personal mailbox (AGENTS.md, "Public repository"; scripts/public-leak.allow).
run "$RIVER" lint public-leak --repo .
# The host-side contract tests (were tests/*.sh), run by the binary step 0 built. Each keeps its
# own run line, so a failure names the test.
# 8. the first-boot hook contract (done / registered / deferred / failed), in a scratch root.
run "$RIVER" test firstboot-hooks --repo .
# 8b. the install plan's ZFS ARC and zram sizes on the target (installer/lib/memtune.sh, runink-zram.sh).
run "$RIVER" test memtune --repo .
# 9. a profile outside this repository (a downstream distribution): described, staged, linted.
run "$RIVER" test external-profile --repo .
# 9b. a profile's installed-system checks (tests/installed.d) under qemu-gui-test: the contract,
#     against fake hooks in a scratch directory (build/qemu-hooks.sh).
run "$RIVER" test installed-hooks --repo .

# 10. bpfdoc (build/tools/bpfdoc), the Go port of the kernel's scripts/bpf_doc.py that lets
#     linux-runink build without python: output byte-identical to upstream bpf_doc.py's for the
#     pinned kernel's bpf.h, and each of the script's consistency checks still rejecting.
bpfdoc_test() {
	command -v go >/dev/null 2>&1 || { echo "go missing" >&2; return 1; }
	(
		cd build/tools/bpfdoc || exit 1
		export GOCACHE="${TMPDIR:-/tmp}/bpfdoc-gocache" GOPATH="${TMPDIR:-/tmp}/bpfdoc-gopath" GOTOOLCHAIN=local \
			GOFLAGS=-buildvcs=false CGO_ENABLED=0 GOENV=off
		unformatted="$(gofmt -l .)"
		[ -z "$unformatted" ] || { echo "gofmt: $unformatted" >&2; exit 1; }
		go vet . && go test -count=1 .
	)
}
run bpfdoc_test

# 11. archaudit (build/tools/archaudit), the release gate's vulnerability check for the image's
#     distribution packages: libalpm's version comparison (pacman's own test vectors), the
#     block/report/waiver rules, and the files-the-authors-changed listing.
archaudit_test() {
	command -v go >/dev/null 2>&1 || { echo "go missing" >&2; return 1; }
	(
		cd build/tools/archaudit || exit 1
		export GOCACHE="${TMPDIR:-/tmp}/archaudit-gocache" GOPATH="${TMPDIR:-/tmp}/archaudit-gopath" GOTOOLCHAIN=local \
			GOFLAGS=-buildvcs=false CGO_ENABLED=0 GOENV=off
		unformatted="$(gofmt -l .)"
		[ -z "$unformatted" ] || { echo "gofmt: $unformatted" >&2; exit 1; }
		go vet . && go test -count=1 .
	)
}
run archaudit_test

[ "$rc" -eq 0 ] && echo "ci-tier1: OK" || echo "ci-tier1: FAILED" >&2
exit "$rc"
