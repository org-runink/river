---
title: Install with the graphical installer
linkTitle: Install
weight: 3
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The installer is built for people who have never installed an operating system: click
**Next**, answer a handful of questions in plain language, and confirm once which disks
will be erased. It needs no network, because it copies the running live system onto your
disk. It takes about ten minutes.

{{< callout type="info" >}}
**Status.** The graphical installer, `river-installer`, is on the main branch (September
2026) and not yet in a release. The live session also has the text installer,
`sudo runink-install`, which runs the same install steps with the same safety checks. Both
are described below.
{{< /callout >}}

## Before you start

- **Back up** anything on the disks you will install to. The disks you confirm are erased
  completely; other disks are not touched.
- Have **pen and paper or a second USB stick** ready for the recovery key.
- On a laptop, plug in the **power cable**.
- Turn **Secure Boot off** in the firmware settings
  ([why]({{< relref "/docs/security/secure-boot" >}})).

## Start the live system

1. Plug in the stick and open the firmware's boot menu (often F12, F11, F8 or Esc at
   power-on). Pick the stick's **UEFI** entry.
2. The GRUB menu of the stick starts the live system after a short timeout.
3. The live Plasma desktop logs in by itself (user `runink`) and opens the installer
   full-screen in Firefox. If you close it, the **Install Runink River** icon on the desktop
   opens it again.

The live session is a normal desktop: you can open Konsole, browse the web or check the
hardware before you install. A text console, also logged in automatically, is on
**Ctrl+Alt+F2**.

{{< callout type="warning" >}}
The live medium's account is `runink` with the password `runink`, with autologin and
passwordless `sudo`, so do not boot it on a network you do not trust. None of this is carried
to the installed system.
{{< /callout >}}

## The graphical installer, screen by screen

{{% steps %}}

### Welcome

Pick the language of the installer and of the installed system (English, Español, Français
or Português) and the keyboard layout: English (US or UK), Spanish (Spain or Latin America),
French (France, Belgium, Switzerland or Canada), German, Italian, Portuguese (Portugal or
Brazil). Type in the test field to check the layout. The layout is applied to the installed
system too, including the console where you type the disk passphrase at boot.

### What to install

The image the stick started is already selected. Click **Next**.

### Network

The installer connects a wired network automatically, or lets you pick a Wi-Fi network and
type its password. It reports one of *Connected*, *Connected to the local network, but not to
the internet* or *No network connection*. **Every outcome continues**: "Continue without
internet" is a normal choice, because the install needs nothing from the internet. The
network you set up here is kept on the installed system.

### Your computer

The installer has already measured the machine (`river-hwprobe`) and made an install plan
(`river-plan`): the processor, the memory, which disks it will use and how they are combined
(for two disks: "The 2 disks will keep identical copies: if one fails, nothing is lost").
Each disk that will be erased is shown with its model, size and **serial number**, and the
disks it will not use are listed with the reason.

A machine below the [minimums]({{< relref "/docs/getting-started#system-requirements" >}})
stops here with "This computer is not supported" and the reason. On a test machine or a VM,
**Details** → **Try anyway (lab)** waives the minimums.

### Erase and install

The one safety question. **Erase and install** opens a confirmation: *This deletes everything
on …* and a field where you type a word: `ERASE`, or the word in the screen's language
(`BORRAR`, `EFFACER`, `APAGAR`). It confirms exactly the disks shown, by serial. The USB stick
you started from is never offered.

### Name and administrator

- **Computer name**: how the machine appears on the network; lowercase letters, numbers and
  dashes. The default is `runink`.
- **Administrator user name** and **password**, typed twice. The password needs at least 8
  characters, all of which can be typed at the console.
- **Add an SSH key** (optional): paste a public key (`ssh-ed25519 ...` or `ssh-rsa ...`) to
  sign in over the network later. SSH also needs port 22 opened in the firewall
  ([Firewall]({{< relref "/docs/features/firewall" >}})).

Your account is the administrator: it uses `sudo` with its own password, and its login
shell is fish. The root account stays locked.

### Recovery key

The disk is encrypted, and the installer has generated the key in memory. It is shown
**once**, large and as a QR code for a phone, and **Save to "…"** writes it as a text file to
a second USB drive if one is plugged in (never to the installation stick). **Start
installing** is enabled only once you tick *I have written it down or saved it*.

**This key is what the computer asks for every time it starts.** It is never stored on the
computer, and without it nothing on the disk can be read. If the installer restarts before
the install begins, it shows a new key; the old one was never used.

### Installing

A progress bar, one line per step in plain words, and the time left. If a step fails, the
screen names it, with **Show details** (the log) and **Retry**.

### Finished

Remove the USB stick, then click **Restart**. Continue with [First boot]({{< relref "first-boot" >}}).

{{% /steps %}}

Everything secret (the password, the recovery key) stays in the installer's memory: the
password is hashed there, the key is handed to the disk step on its standard input, and
neither is written to a file or a log. The installer's interface is a local web page served
on the IPv6 loopback address only (`[::1]:47660`), answering only the live desktop user and
root. If its window reloads or its service restarts, it comes back on the same screen.

## The text installer

In the live session, open Konsole (or the console on Ctrl+Alt+F2) and run:

```bash
sudo runink-install
```

It runs the same sequence as a text dialog:

1. **Network first**, with `river-netsetup`: keep what the live session set up, or set it up
   again. Any outcome continues.
2. **The hardware probe and plan**, shown in full. A refused plan stops here;
   `sudo runink-install --lab` waives the minimums on a VM or test rig.
3. **Every disk the plan erases, confirmed by typing its serial number.** A mistyped serial
   asks again; three misses abort, and nothing is written.
4. **A few questions**:

   | Prompt | Default | Notes |
   |---|---|---|
   | ZFS pool name | `zriver`, or the name of an importable pool it found | an existing pool is **imported**, not erased; an unencrypted one is refused |
   | Hostname | `runink`, or the one chosen in the network step | |
   | Boot-environment name | `runink-<version>` | the dataset `<pool>/ROOT/<name>` |
   | Enrollment file | blank | used only by downstream images; leave it blank |
   | ZFS encryption key | `generated` | `generated` shows a random key once as the recovery key; `own` asks for a passphrase of at least 12 characters, typed twice |

5. **`YES`** to start. With a generated key, step `10-disk-zfs` prints the recovery key in
   eight groups of eight characters and waits for Enter: record it before you continue.

Type the recovery key at boot **without the spaces**.

## What the install does

The install steps are plain shell scripts, run from a fixed list (the directory is never
globbed, so an extra script never runs) and stopped at the first failure. For the graphical
installer on the Runink River image:

| Step | Does |
|---|---|
| `00-preflight` | Checks UEFI, CPU features and the target disks. |
| `05-hwplan-verify` | Probes the machine again and refuses if a target disk changed since you confirmed it. |
| `10-disk-zfs` | Partitions every disk of the plan (a 1 GiB EFI partition plus ZFS), creates the **encrypted** pool (aes-256-gcm at the pool root) and the boot environment `<pool>/ROOT/runink`, creates `<pool>/home`, and mounts the EFI partition at `/boot`. |
| `20-clone-rootfs` | Copies the live system onto the pool with `rsync`, then removes live-only state: the autologin, the live `sudo` rule, the installer, the machine-id and the SSH host keys. |
| `30-target-config` | Hostname, `/etc/runink-os-version`, the `/boot` line in `/etc/fstab`, the ZFS cache limit and zram size from the plan, root-only modes on `/etc/runink`. It also removes the `curl` and `wget` programs from the installed system (their libraries stay). |
| `32-locale-keyboard` | Your language and keyboard, for the console, the login screen and Plasma. |
| `35-pacman-keyring` | Sets up the package keyring: the distribution's packager keys and the Runink River release key, so `pacman` can verify what it installs, and the `[runink]` repository in `/etc/pacman.conf`. Fails the install if the keyring stays empty. |
| `40-boot-grub-zfs` | `/etc/hostid` from the pool, the kernel and initramfs on the EFI partition, GRUB on the removable UEFI path, and the kernel command line. |
| `50-runink-user` | Your administrator account; root locked. |
| `75-install-plan` | Saves the install plan to `/etc/runink/install-plan.json` (root only). |
| `80-enable-s6` | Builds the s6-rc boot database with the firewall, `zfs-mount`, `river-perms`, the network, SSH, the login screen, Bluetooth and printing, and disables the live-only services. |
| `90-export` | Unmounts everything and exports the pool, which also unloads the key. |

The text installer runs the same steps (without `32-locale-keyboard`) plus two that only do
something on downstream images. The full reference, including the network set-up syntax for a
machine without a screen, is {{< repo "docs/INSTALL.md" >}}.

## Installing on several disks

With two or more eligible internal disks, the plan combines them into one pool with
redundancy: a mirror for two, RAID-Z for more. The rules are in
[the hardware planner]({{< relref "/docs/features/installer#how-the-disks-are-laid-out" >}}),
and the caveats (this path is the least tested, and only the boot disk's EFI partition is
kept up to date) are on [Hardware support]({{< relref "/docs/hardware#more-than-one-disk" >}}).
