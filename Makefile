# Runink River image build orchestration (the one in-tree profile, iso-profiles/river; a
# downstream profile builds with build/local-iso.sh and RIVER_PROFILE_DIR)
#
# Targets are ordered: repo -> components -> localrepo -> iso -> vmtest.
# `iso` and `vmtest` require Artix + artools (+ qemu); the rest run anywhere.

SHELL        := /bin/bash
PROFILE      := river
PROFILE_DIR  := iso-profiles/$(PROFILE)
LOCALREPO    := localrepo
VERSION      := $(shell cat VERSION 2>/dev/null || echo dev)
WORKSPACE    ?= $(HOME)/artools-workspace

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: repo
repo: ## Scaffold checks + pin the iso-profiles fork base
	@scripts/fetch-iso-profiles.sh
	@echo "repo ready (VERSION=$(VERSION))"

.PHONY: components
components: ## Build the components (+ the downstream payload if RIVER_PAYLOAD_DIR is set)
	@build/build-all.sh

.PHONY: localrepo
localrepo: ## repo-add the built .pkg.tar.zst into localrepo/
	@scripts/make-localrepo.sh $(LOCALREPO)

.PHONY: builder
builder: ## Build the Artix build-environment container (BUILD_FLUTTER=1 adds Flutter for payloads)
	@podman build -t runink-os-builder --build-arg BUILD_FLUTTER=$(BUILD_FLUTTER) builder
BUILD_FLUTTER ?= 0

.PHONY: iso
iso: ## buildiso -p river  (Artix + artools; use `make iso-in-builder` off-Artix)
	@command -v buildiso >/dev/null || { echo "buildiso not found — run on Artix, or: make iso-in-builder"; exit 1; }
	# Gate the ISO on the k0s pin, not just `make lint`. buildiso bakes whatever is in
	# localrepo/, so this is the last point at which a stale package can be caught — and it
	# is the one that failed: an ISO built from a localrepo still holding runink-k0s-1.31.2
	# shipped kube-router v2.2.1 (hard-legacy) to the live server while this repo had pinned
	# 1.33.13 for two weeks. `make lint` would have caught it; nothing on the build path ran it.
	# iso-in-builder runs `make components && make localrepo && make iso`, so gating here
	# covers the containerised path too.
	@cd cli && LOCALREPO=$(LOCALREPO) go run -buildvcs=false ./cmd/river lint k0s-pin --repo ..
	@cd cli && go run -buildvcs=false ./cmd/river lint python-purge --repo ..
	@scripts/build-iso.sh $(PROFILE) $(WORKSPACE)

.PHONY: iso-in-builder
iso-in-builder: builder ## Build the ISO inside the Artix builder container (any podman host)
	# --userns=keep-id: the container's `builder` user must map to the SAME uid as the
	# host caller, or every write into the bind-mounted $(CURDIR) (build/artifacts/,
	# localrepo/) fails with "Permission denied" — rootless podman does not do this by
	# default even with --privileged (that only grants capabilities within the
	# container's own user namespace, it doesn't change the mapping).
	# PROFILE MUST BE PASSED EXPLICITLY. The inner `make` re-reads this Makefile inside
	# the container, where `PROFILE := river` applies again — so an outer
	# `make iso-in-builder PROFILE=<other>` once built the default profile and said nothing.
	# A command-line assignment overrides a `:=` default, which is why it is repeated here
	# rather than exported.
	# A downstream payload (RIVER_PAYLOAD_DIR) is bind-mounted at /payload when set.
	@podman run --rm --privileged --userns=keep-id -v $(CURDIR):/os:Z -w /os \
		$(if $(RIVER_PAYLOAD_DIR),-v $(RIVER_PAYLOAD_DIR):/payload:Z -e RIVER_PAYLOAD_DIR=/payload) \
		runink-os-builder \
		bash -c 'make components && make localrepo && make iso PROFILE=$(PROFILE)'

.PHONY: vmtest
vmtest: ## Boot the ISO in qemu, install to a virtual ZFS disk, run asserts
	@tests/vm-boot-test.sh

.PHONY: lint
lint: ## shellcheck scripts + closure-lint the manifest + installer/branding sync + k0s pin/floor
	@cd cli && go run ./cmd/river lint shell --repo ..
	@cd cli && go run ./cmd/river lint closure $(abspath $(PROFILE_DIR))/Packages-Root
	@cd cli && go run ./cmd/river lint installer-sync --repo ..
	@cd cli && go run ./cmd/river lint branding-sync --repo ..
	@cd cli && LOCALREPO=$(LOCALREPO) go run ./cmd/river lint k0s-pin --repo ..
	@cd cli && go run ./cmd/river lint python-purge --repo ..
	@cd cli && go run ./cmd/river lint aur-sync --repo ..
	@cd cli && go run ./cmd/river lint profile-manifest --repo ..
	@cd cli && go run ./cmd/river lint docs-vendor --repo ..
	@cd cli && go run ./cmd/river lint one-engine --repo ..
	@cd cli && go run ./cmd/river lint shell-ratchet --repo ..
	@cd cli && go run ./cmd/river lint reuse --repo ..

.PHONY: test
test: ## go vet + go test the installer's hardware probe and planner (installer/)
	@cd installer && gofmt -l . | (! grep .) && go vet ./... && go test -count=1 ./...

.PHONY: test-guide
test-guide: ## gofmt + vet + test river-guide, the install guide agent (guide/)
	@cd guide && gofmt -l . | (! grep .) && go vet ./... && go test -count=1 ./...

.PHONY: test-bench
test-bench: ## gofmt + vet + test riverbench, the analytics benchmark harness (bench/analytics/)
	@cd bench/analytics && gofmt -l . | (! grep .) && go vet ./... && go test -count=1 ./...

.PHONY: test-bpfdoc
test-bpfdoc: ## gofmt + vet + test bpfdoc, the Go port of the kernel's scripts/bpf_doc.py (build/tools/bpfdoc)
	@cd build/tools/bpfdoc && gofmt -l . | (! grep .) && go vet ./... && go test -count=1 ./...

.PHONY: lock
lock: ## Regenerate Pkglist.lock from the pinned repos
	@scripts/gen-pkglist-lock.sh $(PROFILE_DIR)

.PHONY: clean
clean: ## Remove build artifacts (keeps sources)
	@rm -rf $(LOCALREPO)/*.pkg.tar.zst $(LOCALREPO)/*.db* $(LOCALREPO)/*.files* build/.out
	@echo "cleaned"

# ── Documentation website (website/, Hugo + the vendored Hextra theme) ────────────────────
# Builds outside the tree and outside /tmp (a tmpfs that Hugo builds can fill). Offline: the
# theme and FlexSearch are vendored. CI pins Hugo 0.147.3 (.github/workflows/docs.yml);
# any newer Hugo builds the same site.
HUGO         ?= hugo
DOCS_OUT     ?= $(HOME)/.cache/river-docs-build
DOCS_FLAGS   := --source website --cacheDir $(DOCS_OUT)/cache --noBuildLock

.PHONY: docs
docs: docs-check ## Build the documentation website into ~/.cache/river-docs-build/public
	@HUGO_RESOURCEDIR=$(DOCS_OUT)/resources $(HUGO) $(DOCS_FLAGS) --minify --gc --panicOnWarning --destination $(DOCS_OUT)/public
	@echo "docs: $(DOCS_OUT)/public/index.html"

.PHONY: docs-serve
docs-serve: docs-check ## Preview the documentation website on http://localhost:1313/river/
	@HUGO_RESOURCEDIR=$(DOCS_OUT)/resources $(HUGO) server $(DOCS_FLAGS) --renderToMemory

.PHONY: docs-check
docs-check: ## Verify the vendored docs dependencies (Hextra, FlexSearch) are the pinned ones
	@cd cli && go run ./cmd/river lint docs-vendor --repo ..
