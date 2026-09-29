# Arch Linux as the interim base: evaluated and deferred

Status: **EVALUATED, NOT ADOPTED** (2026-09-25). This records a measured alternative to the
Artix transitional base. Nothing here ships. The transitional ISO stays Artix-built
(`artools`/`buildiso`), and the long-term direction stays Runink River's own from-source base
([OWN-BASE.md](OWN-BASE.md)).

## 1. The question

Could the transitional userland and builder move from Artix (artools/buildiso, Artix repos,
`*-s6` service packages, `artix_*` initcpio hooks) to **upstream Arch Linux packages plus our
own `[runink]` repository**, with s6 kept as init? Invariant 1 in [AGENTS.md](../AGENTS.md)
(s6, never systemd) is not negotiable, so the question is how much of Arch is coupled to
systemd and what we would have to rebuild or write.

## 2. Method

- Pinned snapshot: Arch Linux Archive `2026/09/24` (`core`: 299 packages, `extra`: 14 983),
  resolved in rootless podman from `docker.io/library/archlinux@sha256:f3691b4dde62ba4c4b6f0ae2c1fbf28e8c0c8c4b9a35c7e06dc1f70e21aa29f6`.
- Resolver: `pacman --dbpath <empty> -Sp --print-format %n <list>`. The empty local database
  matters: against the image's own database pacman reports only what is missing, which
  undercounts the closure by a factor of three.
- systemd-free closure: the same resolve with `--assume-installed systemd=261.3`, then a
  scan of every `Depends On` in the closure for `systemd`, `systemd-sysvcompat`,
  `dbus-units` and `udev` (hard coupling) and for `systemd-libs`, `libsystemd.so`,
  `libudev.so` (library coupling).
- The server list is `iso-profiles/river/Packages-Root` with Artix names mapped to Arch
  (§3), plus the declared dependencies of the `[runink]` packages. The live extras are the
  install tooling from `profile.yaml`.

## 3. Package mapping (server)

| Artix (today) | Arch equivalent | Note |
|---|---|---|
| `base` | **not usable**: Arch `base` 3-3 hard-depends on `systemd` and `systemd-sysvcompat` | Replaced by its members listed explicitly: filesystem gcc-libs glibc bash coreutils file findutils gawk grep procps-ng sed tar gettext pciutils psmisc shadow util-linux bzip2 gzip xz licenses iputils iproute2 pacman archlinux-keyring |
| `mkinitcpio` | `mkinitcpio` 42-1 | **hard-depends on `systemd`**, so it needs a rebuild |
| `grub`, `efibootmgr`, `linux-firmware-intel` | same names | fine |
| `s6`, `s6-rc`, `s6-linux-init` | **not in Arch** | build: `base/recipes` has s6, skalibs and execline; s6-rc, s6-linux-init and s6-portable-utils are new recipes |
| `s6-scripts`, `*-s6` (elogind, dbus, networkmanager, openssh) | **none** | write our own s6-rc tree (udevd, mounts, ttys, hostname, sysctl, modules, zfs import/mount, sshd, network, rc-local, river-perms, river-guide-model) |
| `elogind` | **not in Arch** (AUR only) | dropped on the server anyway ([OWN-BASE.md](OWN-BASE.md) §8.2) |
| udev (Artix builds it from the systemd tarball) | **only inside the `systemd` package** | repackage `systemd-udevd`, `udevadm`, `libsystemd-shared`, rules and hwdb from the pinned `systemd` package, or build eudev (not in Arch either) |
| esysusers / etmpfiles | **only inside `systemd`** | Arch packages ship `sysusers.d`/`tmpfiles.d` and rely on systemd's alpm hooks; we would need a standalone sysusers/tmpfiles package plus the hooks |
| `networkmanager`, `wpa_supplicant`, `dbus` | available (library coupling only) | the server plan already replaces them with `dhcpcd` 10.5.2 (in Arch; `systemd-libs` only) |
| `openssh` 10.5p1, `nftables`, `iptables` 1.8.13, `rsync`, `sudo`, `vim`, `cpupower` 7.2.7, `bubblewrap` | same names | fine |
| `runink-tayga` | ours (`tayga` is not in Arch) | unchanged |
| `artools-base`, `artix-grub-live` (live) | `arch-install-scripts`, `mkinitcpio-archiso` 73-1, `archiso` 90 (build host) | fine; mkarchiso replaces buildiso |
| `runink-zfs-utils` | ours | declares `depends=libudev` (an Artix name); Arch provides `libudev.so` from `systemd-libs`, so the PKGBUILD dependency must change or a shim is needed |

## 4. Measurements

| | Packages |
|---|---|
| Server closure, Arch packages, naive (systemd allowed in) | **156** |
| Server closure, Arch packages, systemd assumed away | **150** |
| Pulled only by `systemd` | 6: systemd, dbus-units, dbus-broker, dbus-broker-units, cryptsetup, kbd |
| Live medium extras | +6: arch-install-scripts, dialog, dosfstools, gptfdisk, mkinitcpio-archiso, parted |
| `[runink]` on top (kernel, headers, ZFS ×2, k0s, core, runtime, installer, tayga, river-guide) | +10 |
| New packages we would own (s6 stack ×6, s6-rc service tree, udev repack, sysusers/tmpfiles repack, init glue) | about +10 |
| **Server image total** | **about 170**, against about 206 in today's Artix closure |

**Hard `systemd` dependents in the server and live closure: 2 real packages**, plus the
meta-package:

| Package | Why | Fix |
|---|---|---|
| `base` | meta-package | list its members explicitly |
| `mkinitcpio` 42-1 | the `systemd` hook and udev binaries | rebuild without the dependency (shell scripts; minutes) |
| `pacman` 7.1.0 | the `alpm` download-sandbox user comes from `systemd-sysusers` | rebuild with the dependency pointed at our sysusers package, or drop pacman from the server as OWN-BASE §4 plans |

**Library-only coupling (`systemd-libs` / `libudev.so`), fine as-is under the policy below:**
dbus (pulled by libpcap via iptables), device-mapper, dhcpcd, libusb, pam, pciutils,
procps-ng, util-linux.

Also pulled in, and worth knowing: `pacman` brings `curl`, `gnupg`, `gpgme`; `gnupg` brings
`tpm2-tss` and `libusb`; `pinentry`/`libsecret` bring `glib2`. None run as services, but they
are closure the own base drops (OWN-BASE §4).

### Policy that would have applied

- **Forbidden in the closure:** `systemd`, `systemd-sysvcompat`, `dbus-units`,
  `dbus-broker-units`, and any package providing a systemd daemon.
- **Allowed:** `systemd-libs` (`libsystemd.so`, `libudev.so`) as a library, because no systemd
  daemon runs and the alternative is rebuilding eight base packages for no runtime gain.
- **Allowed with a note:** the udev daemon and the standalone sysusers/tmpfiles tools,
  repackaged byte-for-byte from the pinned `systemd` package. This is what Artix does today
  (its udev comes from the systemd tarball), and mkinitcpio's own `udev` hook already runs
  `systemd-udevd` without systemd as PID 1.

## 5. Workstation (Plasma 6)

Measured the same way, from `iso-profiles/runink-workstation/Packages-Root` with `base`
expanded and the `*-s6`/s6 entries removed:

| | Packages |
|---|---|
| Workstation closure, Arch packages | **838** (833 with systemd assumed away) |
| Hard `systemd` dependents | **9**: accountsservice, bolt, cups, mdadm, media-player-info, mkinitcpio, pacman, polkit, xdg-desktop-portal (plus `systemd` itself) |
| Library-only (`systemd-libs`/`libudev.so`) users | 56 |

On top of the server work, a systemd-free Plasma 6 needs: those seven extra rebuilds (polkit is
the important one: KDE's authentication agent, udisks2, NetworkManager and PowerDevil need it);
elogind and a logind-compatible stack, which Arch does not carry at all (elogind,
`polkit`-on-elogind, sddm checked against elogind); s6 services for dbus, elogind,
NetworkManager, bluez, cups and sddm; and PipeWire/WirePlumber started as user services
without systemd user units (s6 per-user supervision or the session's autostart). Plasma's
non-systemd `startplasma` path works today on Artix, but every Plasma release can regress it.

Estimate: **3 to 6 weeks** beyond the server, then continuing work on each Plasma and
snapshot bump. Not measured: whether any of the 56 library users call logind or systemd's
D-Bus APIs at run time and degrade without them.

## 6. Effort estimate (not spent)

| Work | Estimate |
|---|---|
| s6-rc, s6-linux-init, s6-portable-utils recipes (next to the existing three) | 1 day |
| Own s6-rc service tree and PID 1 glue replacing `s6-scripts` and every `*-s6` package, including the live medium's autologin and tty1 river-guide hand-off | 3 to 5 days |
| mkinitcpio and pacman rebuilds; udev and sysusers/tmpfiles repackaging with alpm hooks | 1 to 2 days |
| archiso profile with s6 as PID 1 in the airootfs (archiso's live environment assumes systemd), River GRUB theme, volume label, model payload and multiboot USB compatibility | 3 to 5 days |
| Installer: basestrap/artix-chroot → pacstrap/arch-chroot, `/run/artix` → `/run/archiso`, `80-enable-s6` against our tree, keyring step, NetworkManager dispatcher scripts ported to dhcpcd hooks | 2 to 3 days |
| Closure lint on the Arch snapshot, Tier 1, QEMU boot test, then hardware | 2 to 4 days |
| **Server total** | **about 2 to 4 weeks** to a boot-tested ISO |
| Workstation on top | see §5 |
| Ongoing | tracking Arch's rolling snapshot, re-checking systemd coupling on each bump |

## 7. Risks

- **Arch is systemd-first by design.** Coupling can appear on any snapshot bump (pacman
  gained its `systemd` dependency in 7.0); every bump needs the §2 scan, and a hit means
  another rebuild we own.
- **The live medium.** archiso's airootfs, its `customize_airootfs` conventions and its
  initcpio hooks assume systemd after switch_root. Booting s6-linux-init as PID 1 from the
  archiso hooks is unproven.
- **udev from a systemd binary.** A repackaged `systemd-udevd` must track the exact
  `systemd-libs` version; a skew breaks `libudev` consumers (ZFS by-id links, the zfs hook).
- **Network change.** Replacing NetworkManager with dhcpcd changes behaviour on installed
  nodes (MAC stabilisation, the four dispatcher hooks); failures show only on real IPv6 networks.
- **It is still an outside distribution.** See §8.

## 8. Conclusion

Measured, the Arch path is feasible: two real rebuilds (mkinitcpio, pacman), a udev and
sysusers repackage, the s6 stack and service tree we would need in any case, and about 2 to 4
weeks to a boot-tested server ISO. **It is deferred because it would replace one distribution
dependency with another.** The owner's goal is to reduce dependence on outside distributions,
and Arch, like Artix, would still decide our package versions, build flags and release
cadence, while being more systemd-coupled than Artix and so costing a recurring coupling
audit that Artix does for us today. The effort is better spent on the own base: the s6-rc
service tree, the s6-rc/s6-linux-init recipes and the udev decision listed above are needed
there too (OWN-BASE Phase 2), so none of this analysis is wasted. The transitional ISO stays
Artix-built until the own base replaces it.
