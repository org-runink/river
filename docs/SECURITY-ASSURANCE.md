<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River security assurance case

This document argues why Runink River's security requirements are met, and says where they
are not. It is the assurance case the OpenSSF Best Practices *silver* level asks for
(`assurance_case`). It adds no new mechanism: every claim points at the code or the
document that implements it, and every gap is named as a gap. Read it with
[SECURITY.md](/SECURITY.md) (reporting, known weaknesses) and the design documents it
cites.

**Scope.** The Runink River Server image, its installer (`installer/`, `install.sh`), the
live-medium guide agent (`guide/`), the packaging and build (`build/`, `base/`), and the
CI and release process. Not in scope: the unchanged upstream software it packages (Linux,
OpenZFS, k0s, bubblewrap, and so on), the Runink River Workstation desktop stack, and the
workloads and downstream payloads that run on a node. The RIVER runtime (`riverd`) has no
code yet; its confinement design is covered where it reuses existing mechanisms.

## 1. Security requirements

What Runink River promises an operator:

| # | Requirement | Primary mechanism |
| --- | --- | --- |
| R1 | Data at rest on a node is unreadable without the pool key. | ZFS native aes-256-gcm encryption root; every dataset inherits it ([ENCRYPTION.md](ENCRYPTION.md)) |
| R2 | No secret exists in any image; node secrets exist only on the node that uses them, readable only by their one reader. | First-boot enrollment; `river-perms` 0600/0700 modes re-asserted on every boot ([ENCRYPTION.md](ENCRYPTION.md), [GOLDEN-IMAGE.md](GOLDEN-IMAGE.md)) |
| R3 | A node accepts only the network traffic it was enrolled for. | Default-deny `inet runink_fw` nftables table on every node from first boot (base posture), narrowed to a whitelist at enrollment; forward default-drop (`runink-fw`) |
| R4 | Code a node runs on behalf of others cannot reach node secrets, cluster state or other steps. | `river-sandbox` (bubblewrap) with a fixed deny-list ([runtime/README.md](../runtime/README.md)) |
| R5 | What an operator installs is what the project built from reviewed source. | Pinned and verified upstreams; signed release checksums; `install.sh` fails closed ([RELEASE-SIGNING.md](RELEASE-SIGNING.md)) |
| R6 | The installer never destroys data the operator did not choose to destroy. | Serial-typed disk confirmation, boot-medium exclusion, re-probe before writing ([INSTALLER-HARDWARE.md](INSTALLER-HARDWARE.md)) |
| R7 | The install guide agent can neither run a destructive command nor leak site data. | Step machine and redaction in `guide/` ([INSTALL-GUIDE-AGENT.md](INSTALL-GUIDE-AGENT.md)) |
| R8 | Pull-request code, including from forks, cannot reach a machine that holds anything of value. | CI tiers on GitHub-hosted runners only, maintainer approval of fork runs, no secrets in pull-request jobs ([governance/CI.md](governance/CI.md)) |

## 2. Threat model summary

**Assets:** data on the node's pool; the pool key (which is also the recovery key); the
enrollment secrets (SSH keys, identity, cluster role and tokens); the k0s cluster state;
the integrity of released images and packages; the site's identifying facts (addresses,
hostnames, serials) during an install.

**Adversaries considered:**

| Adversary | Can | Cannot (by design) |
| --- | --- | --- |
| Network attacker | Reach the node's interfaces; tamper with downloads in transit | Reach a port the enrollment did not open (R3); make `install.sh` accept an unsigned or altered ISO (R5) |
| Thief of a powered-off disk or node | Read every block of the disk | Read any dataset without the key (R1); the ESP holds only the kernel and initramfs |
| Malicious workload or pipeline step | Run arbitrary code as confined by the sandbox or the container runtime | Read `/etc/runink`, runner credentials, the `runink` user's secret dirs or k0s state; create nested user namespaces (R4) |
| Malicious pull request author | Put arbitrary code in a PR | Run it anywhere but a throwaway GitHub-hosted VM (this repository has no self-hosted runner) or before a maintainer approves the run; reach any secret (R8) |
| Compromised upstream mirror or release | Serve altered source or binaries | Pass a sha256 pin and, where upstream signs, a signature check (R5) |
| Remote commenter on the guide's issue channel | Ask questions; approve a step the console proposed, if the console enabled approvals | Propose or run a destructive step; make the guide execute model output (R7) |
| Hallucinating or prompt-injected model | Produce any text | Have that text executed, or have an invented command shown as guidance (R7) |

**Out of scope:** an attacker with physical or KVM access to a *running, unlocked* node
(console access is trusted, as for any server); the live installer medium on an untrusted
network (documented weakness, see §6); vulnerabilities in unchanged upstream software.

## 3. Trust boundaries

1. **Build host → image.** Everything in an image comes from pinned sources, verified at
   build time; nothing secret crosses this boundary. Checked by the closure lint
   (`river lint closure`) and the image contract (`tests/assert-golden.sh`).
2. **Release → operator.** Crossed by `install.sh` or by hand: HTTPS only (`--proto
   '=https'`, TLS 1.2 or later), the release key's fingerprint pinned in `KEYS` and
   `install.sh`, `gpg --verify` over `SHA256SUMS`, then `sha256sum` over the ISO. It fails
   closed while the fingerprint is unpublished.
3. **Enrollment file → node.** The only path by which secrets reach a node; the file is
   consumed at first boot and removed (`runink-firstboot.sh`, [ARCHITECTURE.md](ARCHITECTURE.md)).
4. **Network → node.** The nftables default-deny table (both families); a dual-stack host with an
   IPv6-only cluster network; pods reach IPv4-only destinations only through NAT64/DNS64.
5. **Node → confined code.** `river-sandbox`: every namespace unshared, no capabilities,
   no network unless requested, cleared environment, read-only `/usr` and minimal `/etc`,
   and a deny-list that refuses (not trims) binds exposing secrets.
6. **Console → guide agent → remote channel.** The console is trusted; issue commenters
   partly; GitHub, the network and model output not at all
   ([INSTALL-GUIDE-AGENT.md](INSTALL-GUIDE-AGENT.md), "Threat model").
7. **Pull request → CI.** Tier 1 runs in an ephemeral rootless container with no network
   and a read-only source mount; every job runs on a throwaway GitHub-hosted VM, never a
   self-hosted runner, and a fork's run waits for a maintainer's approval; Tier 2 never runs
   on a pull request; no workflow uses `pull_request_target`.

## 4. Secure design principles applied

The Saltzer and Schroeder principles, as the badge criteria name them:

| Principle | Where it shows |
| --- | --- |
| Economy of mechanism | A curated ~30-package server allow-list (`Packages-Root`) closure-linted against forbidden packages; one kernel; one init (s6); no desktop, codecs or fetch tools on the server. Installer probe/planner and guide agent are Go standard library only. |
| Fail-safe defaults | Firewall default-deny; sandbox network off unless asked; `install.sh` downloads nothing without a verifiable signature; `river-plan` returns REFUSED below the minimums; the guide agent withholds any answer with an unverifiable command. |
| Complete mediation | The disk list is re-probed and re-confirmed right before anything is written; every sandbox bind is checked against the deny-list on every call; every guide answer is checked, not only the first. |
| Open design | All mechanisms are in this public repository; security rests on keys (the pool key, the release key), not on secrecy of design. |
| Separation of privilege | Release signing is separate from CI (CI never holds a key; a maintainer signs offline). Destructive install steps need the operator's typed serials and `YES`. Remote approvals need both a console proposal and console-enabled approvals. |
| Least privilege | Secrets are 0600 files in 0700 directories owned by their one reader (`river-perms`); the guide model server runs as `nobody`, loopback only; confined code gets no capabilities; CI jobs use `permissions: contents: read` and `persist-credentials: false`. |
| Least common mechanism | Each confined step gets its own namespaces, tmpfs `/tmp` and HOME; the guide's private bundle is excluded from remote-scope retrieval. |
| Psychological acceptability | One-line install with verification built in; the install plan is shown before any question; the recovery key is shown once, grouped for transcription. |

## 5. Countermeasures for common weaknesses

| Weakness (CWE) | Where it could arise | Countermeasure | Verified by |
| --- | --- | --- | --- |
| OS command injection (CWE-78) | Guide agent steps; installer shell | Guide steps run as argv without a shell, from a load-time syntax-restricted bundle; model output is never executed. Shell scripts quote variables and pass shellcheck. | `guide/agent` tests; shellcheck in `tier1`; gosec (`go-security`) |
| Download of code without integrity check (CWE-494) | Build inputs, release download | sha256 and signature pins for every upstream (kernel, OpenZFS, k0s, mistral.rs, models, container bases, Actions by SHA); signed `SHA256SUMS` for releases | `build/models-fetch.sh`, PKGBUILD `sha256sums`/`validpgpkeys`, `river lint k0s-pin`, gatekeeper SHA-pin check |
| Hard-coded credentials (CWE-798), exposure of secrets (CWE-200) | Images, repository, logs, the guide's issue channel | No secret in any image (enrollment only); secret scanning on every change; the guide redacts every comment and keeps its token in memory only | gatekeeper (gitleaks, credential checks); `guide/redact` tests; `tests/assert-golden.sh` |
| Cleartext storage of sensitive data (CWE-312) | Disks, backups | ZFS native encryption on every dataset; raw (still-encrypted) backup streams | [ENCRYPTION.md](ENCRYPTION.md); `tests/assert-golden.sh` |
| Incorrect permission assignment (CWE-732) | Secret and state files | `river-perms` re-asserts owner and 0600/0700 modes on every boot | `tests/assert-golden.sh` |
| Improper certificate validation (CWE-295) | `install.sh`, the guide's GitHub client | curl and Go's default verification; no code disables it (no `InsecureSkipVerify`, no `curl -k`) | gosec; code review |
| Use of a broken or risky algorithm (CWE-327) | Encryption, signing, checksums | aes-256-gcm, Ed25519 or RSA-4096 OpenPGP, sha256; `MODULE_SIG_SHA1` disabled in the kernel config. The one SHA-1 use is the SPDX 2.x SBOM document-reference checksum, which that format mandates; it protects nothing. | kernel `config.delta`; `base/river-build` |
| Insufficient randomness (CWE-330) | The pool key | 32 bytes from `/dev/urandom`; Go's `crypto/rand` | `installer/lib/10-disk-zfs.sh` |
| Integer overflow (CWE-190) | Planner arithmetic | Inputs clamped; conversions annotated with the invariant that makes them safe | gosec; planner table tests |
| Path traversal / link following (CWE-22, CWE-59) | Sandbox binds | Bind targets are canonicalised and checked against the deny-list; protected links and FIFOs sysctls on the node | `river-sandbox`; [GOLDEN-IMAGE.md](GOLDEN-IMAGE.md) sysctl list |
| Improper input validation (CWE-20) | Probe JSON into the planner; guide bundles; kernel command line switches | The planner validates the probe and manifest and refuses what it cannot parse; bundles must verify against an ed25519 trust anchor; nothing on the command line can enable remote approvals | `installer/internal/planner` tests; `guide/bundle` tests |
| Known-vulnerable dependencies (CWE-1395) | Go toolchain and modules; pinned upstreams | govulncheck in CI; the dependency check in every release ([RELEASE.md](../RELEASE.md)); Dependabot for Actions | `go-security` job; `.github/dependabot.yml` |

## 6. Known gaps and residual risk

These are real, and each is tracked:

- **The release key does not exist yet.** Until it does, releases cannot be verified and
  `install.sh` refuses to download. ([RELEASE-SIGNING.md](RELEASE-SIGNING.md))
- **Secure Boot is off.** The boot chain is not verified by firmware; module signing is
  staged but not forced. ([SECURE-BOOT.md](SECURE-BOOT.md))
- **Attended boot.** With no key provider installed, the pool passphrase is typed at the
  console on every boot; TPM2 unattended unlock is planned. ([ENCRYPTION.md](ENCRYPTION.md))
- **The live installer medium** has a default account with autologin and starts `sshd`;
  never boot it on an untrusted network. The installed system does not carry it.
  ([SECURITY.md](/SECURITY.md))
- **Passwordless sudo for the wheel group** on the node. ([SECURITY.md](/SECURITY.md))
- **Unprivileged user namespaces are enabled** on the node because `river-sandbox`
  needs them; nested user namespaces are disabled inside the sandbox, but the host-level
  setting widens the kernel attack surface for local users.
- **Tier 2 (boot, ZFS, encryption tests) does not run in CI yet**; those paths are tested
  by hand. ([governance/CI.md](governance/CI.md))
- **The ISO is not reproducible**, so release provenance proves what was published, not
  how it was built (SLSA Build L1). ([BUILD.md](BUILD.md#reproducibility-status))
- **One maintainer.** No change or security fix gets an independent review until a second
  maintainer joins. ([MAINTAINERS.md](../MAINTAINERS.md))
- **No external security review** has been done.

## 7. How this case is kept true

- A change that weakens a requirement in §1 breaks an [AGENTS.md](../AGENTS.md) invariant
  and needs a TSC vote ([GOVERNANCE.md](../GOVERNANCE.md)).
- A new mechanism that crosses a trust boundary in §3 updates this document in the same
  pull request.
- Every security fix adds a check that would have caught it
  ([CONTRIBUTING.md](../CONTRIBUTING.md#tests), [SECURITY.md](/SECURITY.md)).
