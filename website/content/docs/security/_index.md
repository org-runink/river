---
title: Security
weight: 10
sidebar:
  open: true
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

{{< cards >}}
  {{< card link="reporting" title="Report a vulnerability" icon="shield-check" subtitle="Privately, never in a public issue." >}}
  {{< card link="verify" title="Verify a release" icon="badge-check" subtitle="Signature, checksum, provenance and the release gate's evidence." >}}
  {{< card link="release-signing" title="Release signing" icon="key" subtitle="The release key and how releases are signed." >}}
  {{< card link="secure-boot" title="Secure Boot" icon="lock-closed" subtitle="Off today; the plan to support it." >}}
{{< /cards >}}

## Security design in one list

- **Every file on disk is encrypted** (ZFS native encryption, aes-256-gcm), and the key is
  never stored on the machine.
- **Secrets are handled like `/etc/shadow`** and their modes are re-asserted at every boot.
- **No secret, key or token is ever baked into an image.**
- **Inbound traffic is denied by default**, from the first boot and on the live medium.
- **Untrusted code runs under `river-sandbox`**, which cannot be pointed at the machine's
  secrets.
- **Every upstream is pinned** by version and checksum, and by signature where it signs:
  the kernel, OpenZFS, container base images and every GitHub Action (by full commit SHA).
- **Releases are signed offline** with a dedicated project key; CI never holds it.
- **No telemetry, no phone-home**, no third-party hosted AI service in the image.

## Hardening on an installed machine

Settings that are on by default, from the source:

| Where | What |
|---|---|
| Kernel command line (`/etc/default/grub.d/10-runink-zfs.cfg`) | `slab_nomerge init_on_alloc=1 init_on_free=1 randomize_kstack_offset=1` |
| sysctl (`/etc/sysctl.d/10-runink-hardening.conf`) | `kernel.kptr_restrict=2`, `kernel.dmesg_restrict=1` (the kernel log needs `sudo`), `kernel.kexec_load_disabled=1`, `kernel.yama.ptrace_scope=1`, `net.core.bpf_jit_harden=2`, `kernel.perf_event_paranoid=2`, the `fs.protected_*` links, FIFOs and regular files, `fs.suid_dumpable=0`, no ICMP redirects or source routing, `log_martians` |
| Kernel build | CPU mitigations compiled in and on; hibernation, kexec, `/dev/mem` and `/proc/kcore` disabled ([The kernel]({{< relref "/docs/features/kernel#security-settings-are-not-traded-away" >}})) |
| Accounts | root's password is locked; your account uses `sudo` with its own password (`%wheel ALL=(ALL:ALL) ALL`) |
| Firefox | telemetry off by policy (`/etc/firefox/policies/policies.json`) |
| Installed image | the live medium's autologin, passwordless `sudo`, installer and pairing tools are removed by `20-clone-rootfs` |

## Known gaps

The assurance case names them, and so does this site: Secure Boot is off, the pool
passphrase is typed at every boot, unprivileged user namespaces stay enabled for
`river-sandbox`, the boot and encryption tests are not in CI yet, the ISO is not
reproducible, and there is one maintainer and no external security review. The full
assurance case is in {{< repo "docs/SECURITY-ASSURANCE.md" >}}.
