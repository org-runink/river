# Tier 1 CI image: the tools the lint scripts need, nothing else. Built by
# scripts/ci-tier1.sh (with network), then RUN with --network=none. Base pinned by digest.
# go: the test of build/tools/bpfdoc (standard library only, so it needs no network).
# curl: `river test models-fetch` drives build/models-fetch.sh against a loopback server.
FROM docker.io/library/alpine:3@sha256:79ff19e9084a00eece421b2523fb93e22d730e2c0e525905de047e848e56d95f
RUN apk add --no-cache shellcheck coreutils findutils grep diffutils git go curl
