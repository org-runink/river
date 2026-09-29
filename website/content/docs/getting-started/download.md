---
title: Download & verify
weight: 1
description: "Download the Runink River ISO and verify it: the one-line install.sh, every flag, the manual download from GitHub Releases, and the gpg and sha256sum checks against the pinned release key."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

{{< callout type="warning" >}}
**No signed Runink River release has been published yet, so there is nothing to download.**
The release-signing key is published and its fingerprint is pinned in {{< repo "KEYS" >}}
and in `install.sh`. The newest release on the GitHub releases page predates that key and
carries no signed `SHA256SUMS`, so `install.sh` stops at its first request with
`install.sh: download failed: .../SHA256SUMS` and downloads no image. That is the script
failing closed, as designed. It starts working, unchanged, with the first signed release.
Until then, [build the ISO yourself]({{< relref "/docs/build" >}}).
{{< /callout >}}

## The image

Each release is one installer image for 64-bit x86 PCs:

```text
runink-river-<YYYYMMDD>-x86_64.iso        volume label RIVER
```

It contains the complete system (the kernel, the desktop, the tools) and the installer.
Nothing is downloaded during an install. A release is a GitHub release tagged
`runink-os-YYYY.MM` on [github.com/org-runink/river/releases](https://github.com/org-runink/river/releases),
and it carries, next to the ISO:

| File | What it is |
|---|---|
| `SHA256SUMS` | the sha256 of the ISO (and of its parts, if any) |
| `SHA256SUMS.asc` | the detached OpenPGP signature over `SHA256SUMS`, made with the release key |
| `<iso>.part-00`, `-01`, ... | the ISO split into numbered parts, when it is larger than 2 GiB (GitHub's limit for one release file) |
| SBOMs, scans, provenance | the evidence of the release gate ([Verify a release]({{< relref "/docs/security/verify#what-the-evidence-tells-you" >}})) |

## Before you download

The installer refuses a machine below these minimums (the workstation profile of the
hardware planner, {{< repo "installer/internal/planner/plan.go" >}}):

| What | Minimum |
|---|---|
| Firmware | UEFI; legacy BIOS boot is refused |
| CPU | x86-64-v3 (AVX2, FMA, F16C, BMI1/2, MOVBE) |
| Physical cores | 2 |
| RAM | 7168 MiB as reported by the kernel (8 GB installed) |
| Disk | one internal, non-USB, non-removable, unused disk of 64 GiB or more |

You also need a **USB stick of 8 GB or more** (everything on it is erased) and a way to keep
the **recovery key** the installer shows you once: pen and paper, or a second USB stick. The
details, including how several disks are combined, are under
[System requirements]({{< relref "/docs/getting-started#system-requirements" >}}).

## The one-line install

On any Linux machine with `curl`, `gpg` and `sha256sum`:

```bash
curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh
```

With no flags it downloads the latest release's ISO into the current directory and verifies
it. To pass flags through the pipe, put them after `sh -s --`:

```bash
curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh -s -- --dry-run
```

### What it does

1. **Downloads three small files**: `SHA256SUMS` and `SHA256SUMS.asc` from the latest release,
   and the release public key from `https://runink.org/.well-known/gpg-key.txt`. Every
   request is HTTPS only, TLS 1.2 or newer.
2. **Checks the key.** It imports the key into a temporary, empty GnuPG home (your own
   keyring is not touched) and refuses unless the key's fingerprint is exactly the pinned
   `95C0A7B97D547413E42660DDB06FE75626F15BF3`.
3. **Checks the signature.** `SHA256SUMS.asc` must be a valid signature over `SHA256SUMS` whose
   primary key is that fingerprint. Anything else stops the script.
4. **Picks the image** named `runink-river-<YYYYMMDD>-x86_64.iso` in the signed `SHA256SUMS`.
5. **Downloads and checks the image.** If the release has parts, each part is checked against
   its own signed sha256 before it is appended, and the joined file is checked against the
   ISO's sha256. The download goes to `<iso>.part` and is renamed only after its sha256
   matches; a mismatch deletes the file. An ISO already in the output directory with the
   right sha256 is not downloaded again.
6. **Optionally writes a USB stick** (`--write`, below).

It **fails closed**: a missing file, a key with the wrong fingerprint, a bad signature or a
wrong checksum ends the script with an error and no image. It **never escalates privileges**:
it does not run `sudo`, and a step that needs root prints the exact command for you to run.
It **sends nothing anywhere**: its only requests are the release files and the public key.

### Flags

| Flag | Does |
|---|---|
| `--dry-run` | Downloads and verifies the key, `SHA256SUMS` and its signature, prints what it would download and run, and changes nothing. |
| `--out DIR` | Puts the ISO in `DIR` (created if missing) instead of the current directory. |
| `--write /dev/sdX` | After verifying, writes the ISO to the USB stick `/dev/sdX`, erasing it. See below. |
| `--yes-i-have-checked-serial=SERIAL` | Confirms the stick's serial without a prompt, for a scripted `--write`. |
| `--lab` | Only inside the Runink River live session: plans the install below the documented minimums, for VMs and test rigs. It has no effect on a download. |

`--write` refuses anything but a whole-disk block device that is USB or removable, is not
mounted, and reports a serial number. It then shows the stick's model, size and serial and
asks you to **type the serial**; a mismatch writes nothing. Run as a normal user, it stops
there and prints the command to run as root:

```text
sudo dd if=./runink-river-<YYYYMMDD>-x86_64.iso of=/dev/sdX bs=4M conv=fsync oflag=direct status=progress
```

Inside the Runink River live session, `install.sh` does not download anything: it probes the
hardware, prints the install plan, and hands over to the text installer (`sudo runink-install`),
which is where `--lab` applies. The live session also opens the graphical installer by itself
([Install]({{< relref "install" >}})).

Three environment variables exist for mirrors and testing: `RIVER_RELEASE_URL` (the directory
to fetch the release files from, default the latest GitHub release), `RIVER_KEY_URL` (where to
fetch the public key) and `RIVER_KEY_FPR` (overrides the pinned fingerprint). Setting
`RIVER_KEY_FPR` means you are choosing what to trust; leave it unset.

### Read it before you run it

```bash
curl -fsSLO https://raw.githubusercontent.com/org-runink/river/main/install.sh
less install.sh
sh install.sh --dry-run
```

The same file is {{< repo "install.sh" >}} in the repository.

## Download and verify by hand

### 1. Download

From the release on [GitHub Releases](https://github.com/org-runink/river/releases), download
the ISO (or all of its parts), `SHA256SUMS` and `SHA256SUMS.asc` into one directory. With
`curl`, for the release tag `runink-os-YYYY.MM`:

```bash
base=https://github.com/org-runink/river/releases/download/runink-os-YYYY.MM
curl -fL -O "$base/SHA256SUMS" -O "$base/SHA256SUMS.asc" -O "$base/runink-river-<YYYYMMDD>-x86_64.iso"
```

### 2. Get the key and check its fingerprint

```bash
curl -fsSLO https://runink.org/.well-known/gpg-key.txt
gpg --show-keys --with-fingerprint gpg-key.txt
```

The output must show this fingerprint, the same as in {{< repo "KEYS" >}}:

```text
pub   nistp384 2026-09-26 [SC]
      95C0 A7B9 7D54 7413 E426  60DD B06F E756 26F1 5BF3
```

Compare the **full fingerprint**, never a short key ID or a name: the user IDs on the key may
change, the fingerprint does not. Then import it:

```bash
gpg --import gpg-key.txt
gpg --fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3   # must be listed
```

### 3. Check the signature, then the checksum

```bash
gpg --verify SHA256SUMS.asc SHA256SUMS                     # must say "Good signature"
sha256sum -c --ignore-missing SHA256SUMS                   # the ISO must say "OK"
```

`gpg --verify` must print **Good signature** and a primary key fingerprint of
`95C0 A7B9 7D54 7413 E426 60DD B06F E756 26F1 5BF3`. It also warns that the key is not
certified with a trusted signature unless you have signed it yourself; that is expected.

If you downloaded parts, join them first and check again:

```bash
cat runink-river-<YYYYMMDD>-x86_64.iso.part-* > runink-river-<YYYYMMDD>-x86_64.iso
sha256sum -c --ignore-missing SHA256SUMS
```

Build provenance, the Sigstore signature, the signed tag and what each evidence file tells you
are on [Verify a release]({{< relref "/docs/security/verify" >}}).

## Next: write the stick

Write the verified image to a USB stick, with `install.sh --write` or by hand with `dd`:
[Create a USB stick]({{< relref "usb" >}}).
