# Security tools (BlackArch)

*The BlackArch repository is configured and signed on every installed system, and the image installs no tool from it: how to list, search and install a tool, keep the keyring current, and turn the repository off.*

<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

[BlackArch](https://blackarch.org) is a pacman repository of several thousand
penetration-testing and security-research tools. Runink River configures it on every installed
system so you can install a tool with one command, and **installs none of them**: a fresh
system has the repository, its signing keys and its mirrors, and no BlackArch tool.

> **Note:** Only use these tools on systems and networks you own or are explicitly authorised to
> test.

## What is configured

The installer adds this at the end of `/etc/pacman.conf`:

```ini
[blackarch]
SigLevel = Required
Include = /etc/pacman.d/blackarch-mirrorlist
```

- `SigLevel = Required`: pacman refuses the repository's database and any package unless it is
  signed by a key BlackArch trusts. The keys are in `/usr/share/pacman/keyrings/blackarch*`;
  Runink River copied them from BlackArch's own signed keyring package and checked them against
  the fingerprints the BlackArch project publishes. No download-and-run setup script is used.
- It is the **last** repository, so a tool never replaces a system package of the same name.
- `/etc/pacman.d/blackarch-mirrorlist` lists seven HTTPS mirrors; pacman moves to the next one
  when a mirror fails.

To see the keys:

```bash
pacman-key --list-keys | grep -i blackarch
```

## List and search tools

Refresh the package databases first (this also updates the rest of the system's database):

```bash
sudo pacman -Syu
```

Then:

```bash
pacman -Sl blackarch                 # every package in the repository
pacman -Sg | grep '^blackarch-'      # the categories (blackarch-scanner, blackarch-wireless, ...)
pacman -Sg blackarch-recon           # the tools in one category
pacman -Ss -q '^nmap'                # search by name (all repositories)
pacman -Si <tool>                    # details, dependencies, which repository it comes from
```

## Install a tool

```bash
sudo pacman -S <tool>
```

Install tools **one at a time, by name**. A category such as `blackarch-recon` can hold
hundreds of packages, and the whole `blackarch` group thousands; installing a group
pulls in every tool in it and their dependencies.

To remove a tool and the dependencies nothing else needs:

```bash
sudo pacman -Rns <tool>
```

## Tools and the pinned system

Runink River's system packages come from a dated snapshot; BlackArch always serves its newest
builds and keeps no older versions. Most tools install and run, but a tool built recently can
need a newer library than your system has:

- pacman stops with `unable to satisfy dependency`: wait for the next Runink River update,
  then try again.
- The tool installs but fails with `error while loading shared libraries`: remove it
  (`sudo pacman -Rns <tool>`) and try again after the next update.
- If `pacman -Syu` offers to **replace** a system package with a package from `blackarch`,
  answer **n** unless you know you want that; the default answer is yes.

## Keep the keyring current

The image carries BlackArch's keyring as of the Runink River release, and pacman does not
update it by itself. If BlackArch starts signing with a key that keyring lacks, `pacman -Syu`
stops with an error about the `blackarch` database until a Runink River update brings the new
key. To have pacman follow BlackArch's keys from now on, install BlackArch's own keyring
package while the repository still verifies (it is signed by a key you already trust):

```bash
sudo pacman -Syu
sudo pacman -S --overwrite '/usr/share/pacman/keyrings/blackarch*' blackarch-keyring
```

`--overwrite` is needed because the image's copy of the same files belongs to no package. From
then on `pacman -Syu` updates the BlackArch keys with the rest of the system.

## Turn the repository off

With the repository configured, `pacman -Syu` needs at least one BlackArch mirror to answer. To
stop using it, comment out the three `[blackarch]` lines in `/etc/pacman.conf`:

```ini
#[blackarch]
#SigLevel = Required
#Include = /etc/pacman.d/blackarch-mirrorlist
```

Tools you installed stay installed and are no longer updated; remove them with
`sudo pacman -Rns <tool>`.

For maintainers: what ships, how the key was verified, and the snapshot interaction in detail
are in [docs/BLACKARCH.md](https://github.com/org-runink/river/blob/main/docs/BLACKARCH.md).
