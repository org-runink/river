# DEsk replaces KDE Plasma: the plan

Status: **phase A was research; phase B built it** (2026-10-30). The plan below is kept as
written. Where phase B decided otherwise, this section says so and wins.

## Phase B: what was decided and built

- **wlroots 0.18.3 is built from source** in `[runink]` (`runink-wlroots0.18`), pinned by
  sha256, with muon. Artix's `wlroots0.18` is the documented fallback only (§0.2 proposed the
  opposite).
- **The DEsk fork's repository waits on the owner.** Until it exists, its four changes ship as
  patches next to `build/pkgbuilds/runink-desk/PKGBUILD`, applied to upstream's v0.1.4 tag:
  no Python in the build (C helpers built `native: true`, a Go word check), elogind instead of
  systemd, `-Dweather_default` (built `false`), and the fork's credit. `runink-desk` builds
  the same way once `source` names the fork's tag.
- **greetd comes from Artix's repositories** for now (debt). Artix has no greetd-s6, so the
  service comes from a new `[runink]` package, **`runink-greetd-s6`**, built like Artix's
  `-s6` packages: a plain longrun named `greetd` (execline `run`), not the
  `greetd-srv`/bundle pair §3 sketched. A package rather than an overlay directory because an
  s6 `run` file in the tree is a new shell script (`river lint shell-ratchet`). 80-enable-s6,
  the GPU seat watcher in `rc.local` and `river-live-fallback` handle both service forms.
- **`python` is NOT on forbidden.closure.** `base-devel` (a developer workstation's toolchain,
  invariant 5) pulls in gdb, which embeds the interpreter, so the closure has always contained
  it. The ban stays on Python *we* ship (none of DEsk's helpers that are Python are listed).
  forbidden.closure gains `plasma-workspace kwin sddm`.
- **The live medium's first path is the weston kiosk**, as greetd's `initial_session` for the
  live user (`live-overlay/etc/greetd/live.toml`); the ordinary login screen follows it.
  `20-clone-rootfs` deletes that file and fails the install if any `initial_session` survives.
- **The VM harnesses are not yet ported**: `build/qemu-test.sh`, `build/qemu-gui-test.sh` and
  `river test medium-unaided` still look for Plasma and SDDM. They are rewritten against the
  first DEsk ISO, not guessed at before it exists.
- **Owner-run:** the builder image, the four packages, the closure before and after, and the
  ISO are built by the owner's script (`desk-owner-run.sh`, outside this repository); the
  release gate is unchanged, a hardware boot to a working desktop.

Decision (owner, 2026-10-30): DEsk ([fyzugx/DEsk](https://github.com/fyzugx/DEsk), MIT, beta)
becomes the one desktop of Runink River. KDE Plasma, KWin, SDDM and Breeze leave the image
entirely. The reasons: a slimmer image, and branding that is easier to keep within the Runink
trademark.

Studied at DEsk commit `bcc088701775b84d35e617f1c8ada8a0c026b1c3` (2026-10-28, `meson.build`
version 0.1.4). Package facts come from the Artix package index (packages.artixlinux.org),
read on 2026-10-30. The image installs from the mirror snapshot `PIN_DATE=2026-07-01`
(`pacman/mirrorlist.pin`), so every package named below has to be re-checked against that
snapshot on the builder (PR 1 does this). Each claim is marked **verified** (read in source or
the package index) or **to verify** (needs the builder or a booted image).

## 0. Findings that change the brief

1. **Meson is Python, and the builder bans Python.** DEsk and wlroots build with meson. Meson
   is a Python program (Artix `meson` depends on `python`). `builder/Containerfile` removes
   every Python package and fails if an interpreter is left on PATH. AGENTS.md says "No Python
   anywhere in the build path or the image". DEsk's own `meson.build` also calls Python three
   times: `scripts/room-key.py` (always), `scripts/glsl2c.py` (GPU builds) and
   `scripts/check-words.py` (`meson test`). See §2.4 for the fix: build with **muon**, a meson
   implementation written in C, and have the fork replace the Python helpers.
2. **wlroots 0.18.3 does not need a source build.** Artix `world` has `wlroots0.18` 0.18.3
   (built 2025-10-23, before the pin date; verified). DEsk's installer builds the same 0.18.3
   only because other distributions ship a different version. The plan uses the pinned binary
   package. A from-source `runink-wlroots0.18` (with muon) stays the fallback if the snapshot
   lacks it.
3. **The live medium does not run only a kiosk.** Its first path is SDDM autologin into Plasma
   (`live-overlay/etc/sddm.conf.d/50-live-autologin.conf`). The Plasma session then opens
   `river-installer-desktop` (`etc/xdg/autostart/river-installer.desktop`,
   `OnlyShowIn=KDE`). The weston kiosk (`river-kiosk`) is only the fallback, chosen by
   `river-live-fallback`, which looks for `plasmashell` and stops the `sddm` bundle. The live
   medium therefore changes as well (§7).
4. **Several programs DEsk's helper prefers are written in Python**, so they cannot ship:
   `udiskie` (automounting media), `blueman` (Bluetooth), `system-config-printer` (printers),
   and `ibus` setup. `nwg-hello` (a greeter) is Python too. And `mpv` depends on `vapoursynth`,
   which needs Python. §6 lists a replacement for each, or names the gap.
5. **DEsk phones home by default.** `weather = yes` asks `api.open-meteo.com` every half hour
   (`src/occasion.c`). Invariant 5 forbids that ("no telemetry or phone-home"). The image ships
   `/etc/xdg/desk/shell.conf` with `weather = no`, and the fork makes the default a build
   option.
6. **`slurp` cannot run under DEsk.** It needs layer-shell, which DEsk does not provide yet
   (ARCHITECTURE.md §4.7: "Not yet provided: layer-shell, session-lock, …"). DEsk passes the
   window geometry to `desk-screenshot` itself, so `slurp` is not added.
7. **`greetd` has no `-s6` package in Artix.** There are `greetd-openrc`, `-runit` and
   `-dinit`, but no `-s6` (verified). Runink River supplies the s6 service itself, the way it
   already supplies `river-perms` and `zfs-mount`.

## 1. DEsk, as read

- **Programs.** `desk` is the whole desktop: the wlroots 0.18 compositor, the ray-traced room,
  and the PAM "front door" lock. `desk-session` is the sh entry named in
  `/usr/share/wayland-sessions/desk.desktop`. The sh helpers are `desk-session-helper`,
  `desk-ctl`, `desk-window`, `desk-screenshot`, `desk-report` and `desk-screen-chooser`. There
  are also `desk-settings`, `desk-index` and `desk-preview`.
- **Required build set** (MODDING §2): wlroots, wayland, xkbcommon, cairo, pixman, fontconfig,
  libdrm and PAM. Optional, each behind an `#ifdef`: libsystemd (`-Dpost`), libpulse
  (`-Dsound`), libinput, EGL/GLES/gbm (the GPU tracer) and Xwayland/xcb (`-Dxwayland`).
  Everything except PAM and wlroots is already in the Runink River closure (Plasma pulled it
  in).
- **Run-time tools DEsk calls** (verified in `src/`): `wpctl` (volume), `rsvg-convert`
  (icons), `curl` (weather only), `notify-send`, `xdg-user-dir`, `grim`, `wl-copy`, and
  `zenity` (screen-share chooser; optional).
- **Configuration.** Machine defaults go in `/etc/xdg/desk/shell.conf`; the user's own files
  go in `$XDG_CONFIG_HOME/desk/`. DEsk reads the system keyboard from
  `/etc/X11/xorg.conf.d/00-keyboard.conf`, the file `32-locale-keyboard` already writes.
- **Look.** The room, paper, frames and lock are drawn by `src/draw.c` (about 35k lines) and
  `src/room.glsl` (a line-for-line GPU port of the tracer). They must change together. The
  icon theme `themes/icons/DEsk` is SVG committed to the repository, generated by
  `themes/make-icons.py`. The GTK theme is `themes/gtk/DEsk` (CSS on top of Adwaita). DEsk has
  no plugin API, so branding is a source change (MODDING.md).

## 2. s6: logind, sd-bus and `--systemd`

### 2.1 `-Dpost` works on elogind (verified)

The image already ships `elogind` and `elogind-s6`. Artix splits the library into
`libelogind` (system, 257.16), which installs `libelogind.pc` and **also**
`/usr/include/elogind/systemd/sd-bus.h` (and the rest of the `sd-*` headers). With the usual
`-I${includedir}/elogind` from `libelogind.pc`, DEsk's `#include <systemd/sd-bus.h>` compiles
unchanged. The only change is in `meson.build`:

```meson
sdbus = dependency('libsystemd', 'libelogind', required: get_option('post'))
```

Meson has accepted a list of fallback names since 0.60. This is how sway and wlroots pick
`libsystemd`, `libelogind` or `basu`. It can go upstream.
*To verify on the builder:* `pkg-config --cflags libelogind` prints `-I/usr/include/elogind`.

What then works through elogind (it implements `org.freedesktop.login1`; all verified in
source):

- `src/power.c`: `Manager.PowerOff` and `Manager.Reboot`, and `Session.SetBrightness` for the
  backlight.
- `src/lock.c`: the `Lock`, `Unlock` and `PrepareForSleep` signals (lock on suspend).
- `post.c`, `tray.c`, `media.c` and `ime.c` use only the **session** bus (notifications,
  StatusNotifierItem, MPRIS). None of them needs systemd.
- wlroots opens the seat through libseat. Artix `seatd` depends on `libelogind`, so libseat
  talks to elogind and no seatd daemon is needed (*to verify*: `seatd`'s logind backend is
  built in).

**Without `-Dpost`** the desktop would lose a lot: the post box (it would no longer be the
notification daemon), the tray (so `nm-applet --indicator` has nowhere to show), MPRIS media
keys, the IME indicator, and lock-on-suspend. Power-off would fall back to `systemctl
poweroff`, which **fails** on s6. So `-Dpost` stays **enabled** (`-Dpost=enabled`, so a missing
library fails the build instead of quietly turning the feature off).

### 2.2 `dbus-update-activation-environment --systemd` (`src/session.c`)

DEsk runs this once the display exists. The tool first updates the dbus-daemon's activation
environment, which is the part Runink River needs. Then, because of `--systemd`, it tries
`org.freedesktop.systemd1.SetEnvironment`, which fails here. DEsk uses the exit status only
for a log line ("(it said no)") and then runs autostart either way. The next step,
`systemctl --user unset-environment`, is skipped when `systemctl` is not on PATH.
`desk-session-helper end` also exits at once without `systemctl`. **So it works unpatched.**
The patch (upstreamable): pass `--systemd`, and run the `systemctl --user` steps, only when
`/run/systemd/system` exists (the `sd_booted()` test). The session log then stops reporting a
failure that is expected on every s6 boot.
*To verify on a booted image:* the exit status of
`dbus-update-activation-environment --systemd` with no systemd1 on the bus.

### 2.3 Power without `-Dpost`

`power.c`'s non-post fallback runs `systemctl`. The fork changes it to try `loginctl
poweroff|reboot` (which elogind ships) before `systemctl`. This can go upstream. Runink River
builds with post, so this only matters for a minimal build.

### 2.4 Building without Python

- **Build tool: muon** (C99, about 1 MB, bootstraps with only a C compiler; GPL-3.0 for the
  build tool only, which never ships in the image). It is not in Artix, so it is built from a
  pinned release tarball (sha256) in `builder/Containerfile`, like the rest of the
  toolchain. It is used only as a `makedepends` of the `[runink]` desktop packages. `samurai`
  (Artix `world`, the ninja implementation in C) or muon's built-in backend runs the build.
  *To verify:* `muon setup` accepts DEsk's, cage's and gtkgreet's `meson.build` (PR 1 is
  exactly that spike).
- **DEsk's Python helpers, replaced in the fork:**
  - `scripts/glsl2c.py` and `scripts/room-key.py` become two small C programs built
    `native: true` by the same build (`tools/glsl2c.c` wraps the shader as a C string;
    `tools/room-key.c` writes the FNV-1a hash header). C, not Go, because they run inside a C
    project's build, need no second toolchain, and can go upstream. Upstream's reason for
    Python ("the Python meson itself runs on") no longer holds under muon.
  - `scripts/check-words.py` (the translation-table test) becomes a Go test in the fork's
    `tools/checkwords`, run in the fork's CI. It is not part of the package build.
  - `themes/make-icons.py` and `themes/contact-sheet.py`: see §5.3.
- **Fallback, if muon cannot build one of the three:** the fork carries a plain `Makefile`
  for its own build. This would not be upstreamed.

## 3. Login manager

Requirements: graphical, reads `wayland-sessions`, s6-run, packaged in the pinned repos or
`[runink]`, no Python, and the smallest closure.

| Option | Pieces | Added (installed) | s6 | Notes |
|---|---|---|---|---|
| **greetd + gtkgreet on cage** (chosen) | `greetd` 0.6 MiB + `greetd-agreety` 0.4 MiB (world); `cage` and `gtkgreet` built in `[runink]` | ≈ 1.5 MiB | Runink River's own longrun | gtkgreet is GTK3, already in the closure through Firefox; cage reuses the same `wlroots0.18` DEsk needs |
| greetd + regreet on weston | `greetd-regreet` (galaxy, Rust) + `weston` + gtk4 + accountsservice | ≈ 75 MiB | own longrun | needs gtk4 (57 MiB, new to the closure) and weston on the installed system; no build |
| lightdm-s6 + lightdm-gtk-greeter | world packages | ≈ 1.5 MiB plus xorg-server if not already there | packaged (`lightdm-s6`) | the greeter is an X11 client: an Xorg server at every boot, only for the login screen |
| greetd + tuigreet, ly, lemurs | | ≈ 3.5 MiB | | text on a VT: **not graphical**, so ruled out |
| nwg-hello | | | | Python: ruled out |

**Chosen: greetd + gtkgreet running in cage.** It is the slimmest graphical option, needs no
X server and no new toolkit, and its parts are tiny C or Rust programs. greetd is a ~0.6 MiB
Rust daemon that depends only on `pam` (verified), and its IPC is a stable JSON protocol.

- **s6 service** (root-overlay, the same pattern as `river-perms` and `zfs-mount`):
  `etc/s6/sv/greetd-srv/` holds `type` (longrun), `run` (execline:
  `/usr/bin/greetd --config` with `/etc/greetd/live.toml` if present, else
  `/etc/greetd/config.toml`), and `dependencies.d/{zfs-mount,plymouth-quit,elogind,dbus}`.
  There is also a `greetd` bundle, and a `greetd-log` pipeline if wanted to match the
  `-srv`/`-log` convention. Name: the `greetd` bundle and the `greetd-srv` longrun, so the
  `<name>-srv` checks in `80-enable-s6.sh` keep working unchanged.
- **`/etc/greetd/config.toml`**: `vt = 1`; `command = "cage -s -- gtkgreet -l -s
  /usr/share/runink/greeter.css"`; `user = "greeter"`. gtkgreet lists sessions from
  `/usr/share/wayland-sessions` (desk.desktop).
- **PAM**: Artix `greetd` ships `/etc/pam.d/greetd`. Runink River adds `pam_gnome_keyring`
  (`auth optional`, `session optional auto_start`) so the keyring unlocks at login. This
  replaces `kwallet-pam`. *To verify:* the shipped file includes `system-login` →
  `pam_elogind`, so the user's session gets a seat and `XDG_RUNTIME_DIR`.
- **The session bus**: today the session bus and PipeWire come from the profile's
  `user-services` (s6). *To verify* that they reach a greetd session the way they reach
  SDDM's. If they do not, the session command becomes `dbus-run-session desk-session`.
- **VT 1**: the existing guard stays. `80-enable-s6` disables the `tty1` getty when a display
  manager takes VT 1, and `river lint profile-manifest` checks the live `tty1.conf`. Both are
  generalised from `sddm` to the display manager the profile enables (PR 2).
- **Later, optional**: DEsk's own front door as the greetd greeter (a `desk --greeter` mode
  that speaks greetd IPC instead of unlocking). That would remove cage and gtkgreet, and the
  login would be the same door as the lock. This is fork work, after the cut-over.

## 4. Packages and files

### 4.1 Add

From the pinned repos (Artix; sizes are installed MiB from the index, 2026-10-30):

| Package | Repo | MiB | For |
|---|---|---:|---|
| `wlroots0.18` (+ `libliftoff`, `xcb-util-errors`, `libdisplay-info`) | world | 1.9 | DEsk, cage |
| `libelogind` | system | 2.0 | `-Dpost` (likely already pulled in by elogind) |
| `greetd`, `greetd-agreety` | world | 1.0 | login |
| `xorg-xwayland` | world | 2.4 | X11 programs (likely already present through artools `packages-xorg`) |
| `xdg-desktop-portal-wlr`, `xdg-desktop-portal-gtk` | world | 0.5 | replace `xdg-desktop-portal-kde` |
| `mate-polkit` | galaxy | 0.3 | polkit agent (first in `desk-session-helper`'s list) |
| `network-manager-applet`, `libnma`, `nm-connection-editor` | world | 5.6 | network tray applet + editor |
| `gnome-keyring` | world | 3.3 | Secret Service (replaces KWallet) |
| `grim`, `wl-clipboard`, `libnotify` | world | 0.3 | Print / Alt+Print (`desk-screenshot`) |
| `foot` | galaxy | 0.8 | terminal (in DEsk's `STD_TERMINAL`) |
| `thunar`, `exo`, `libxfce4ui`, `libxfce4util`, `xfconf`, `tumbler`, `gvfs`, `udisks2` | galaxy/world | 30 | file manager, mounts, thumbnails |
| `mousepad` | galaxy | 2.2 | text editor |
| `ristretto` | galaxy | 1.1 | images |
| `atril` | galaxy | 16.2 | PDF |
| `engrampa` | galaxy | 13.1 | archives |
| `parole` | galaxy | ≈ 3 | audio and video (GStreamer, already present) |
| `wdisplays` | galaxy | 0.1 | displays (Shell Settings row) |
| `gsfonts` | world | 3.1 | URW base 35, DEsk's own faces |
| `xdg-user-dirs`, `xdg-utils` | world | 0.5 | Pictures/Screenshots, opening links |

Built in `[runink]` (new `build/pkgbuilds/*`, sources pinned by sha256):

| Package | Source | For |
|---|---|---|
| `runink-desk` | the org-runink fork's release tarball (§5), built with muon, `-Dpost=enabled -Dsound=enabled -Dxwayland=enabled` | the desktop |
| `runink-cage` | cage 0.2.x release (wlroots 0.18), muon | greeter compositor |
| `runink-gtkgreet` | gtkgreet release, muon, `-Dlayershell=disabled` | greeter |

Build-only (never in the image): `muon` (builder Containerfile), `samurai`,
`wayland-protocols`.

### 4.2 Remove

`Packages-Root` and `profile.yaml` (both lists): the whole Plasma list. That is `aurorae
bluedevil breeze breeze-gtk kactivitymanagerd kde-cli-tools kde-gtk-config kdecoration
kglobalacceld kinfocenter kmenuedit knighttime kpipewire kscreen kscreenlocker ksshaskpass
ksystemstats kwallet-pam kwayland kwin kwrited layer-shell-qt libkscreen libksysguard libplasma
milou ocean-sound-theme plasma-activities plasma-activities-stats plasma-browser-integration
plasma-desktop plasma-disks plasma-integration plasma-keyboard plasma-nm plasma-pa
plasma-systemmonitor plasma-thunderbolt plasma-workspace polkit-kde-agent powerdevil
print-manager systemsettings xdg-desktop-portal-kde sddm-kcm sddm konsole dolphin kate`. Also
the KDE applications `dragon elisa gwenview okular ark spectacle kcalc partitionmanager`, and
`sddm-s6` from `packages-init.s6`. The services list swaps `sddm` for `greetd`.

`forbidden.closure` gains guards so none of it returns transitively: `plasma-workspace kwin
sddm`, and **`python`** (the image's no-Python rule as a closure check, which also stops
`udiskie`, `blueman`, `system-config-printer` and `vapoursynth`). The `python` entry lands in
the cut-over PR only, once the builder has shown the closure without Plasma has no Python in
it.

Files (68 under the Plasma, SDDM and splash trees, plus single files):

- `root-overlay/usr/share/plasma/**`: the look-and-feel `org.runink.river.desktop` (layout
  JS, defaults, splash QML, metadata) and the wallpaper plugin `org.runink.river.desk` (river
  #50, #57).
- `root-overlay/usr/share/sddm/themes/runink-river/**`, `root-overlay/etc/sddm.conf.d/10-river.conf`.
- `root-overlay/etc/xdg/{kwinrc,kdeglobals,ksplashrc}`.
- `root-overlay/etc/s6/sv/sddm-srv/` → `etc/s6/sv/greetd-srv/` (new, §3).
- `live-overlay/etc/sddm.conf.d/50-live-autologin.conf`,
  `live-overlay/etc/xdg/{kscreenlockerrc,powermanagementprofilesrc}`. The live medium gets
  `/etc/greetd/live.toml` and `/etc/xdg/desk/shell.conf` with `screen_off = never` instead
  (§7).
- `branding/sddm/**`, `branding/src/sddm/**`, `branding/splash/**`, `branding/src/splash/**`,
  `branding/src/qml/RiverLockup.qml`, and their entries in `branding/render.sh`,
  `cli/internal/lint/brandingsync.go` (+ test) and `REUSE.toml`.
- `tests/sddm/Harness.qml` and `cli/internal/testcmd/sddmtheme.go`, replaced by a greeter
  check.
- References that need editing rather than deleting: `root-overlay/etc/s6/rc.local` (the GPU
  seat watcher restarts `sddm-srv` and looks for `kwin_wayland`; it becomes `greetd-srv` and
  `cage|desk`), `root-overlay/etc/s6/adminsv/plymouth-quit/up` (comment),
  `usr/local/bin/runink-plymouth-quit`, `etc/firefox/policies/policies.json`,
  `etc/sysctl.d/10-runink-hardening.conf` (comments), `scripts/patch-artools.go` (the
  `sddm-srv`/artix-live dependency), `build/qemu-gui-test.sh` and `build/qemu-test.sh`
  (SDDM login, `plasma-login` checks), `cli/internal/testcmd/mediumunaided.go`,
  `cli/internal/lint/{profilemanifest,closure}.go`, `installer/livefallback/*`,
  `installer/internal/wizard/{sys_linux,validate}.go`, `installer/internal/hw/probe_test.go`,
  `installer/netsetup/river-netsetup`, the installer steps (§7), and the docs and website
  pages listed by `grep -ril 'plasma\|sddm\|kwin\|kde'` (about 25).

### 4.3 Closure size

These are estimates. The measured figure comes from the builder (`pactree`/`river lint
closure` against the pinned sync databases) in PR 1 and replaces this table.

| | Installed MiB | Basis |
|---|---:|---|
| Plasma + KDE apps, the 59 explicit packages | ≈ 384 | sum of Artix `installed_size` |
| Their Qt 6 / KF6 / VLC dependencies, a 51-package sample (qt6-declarative 137, qt6-base 68, breeze-icons 57, vlc 42, kio 20, …) | ≈ 500 | same; the full KF6 set is larger |
| **Removed** | **≈ 0.9 – 1.1 GiB** | Qt 6 leaves completely: no Qt program remains |
| **Added** (§4.1, upper bound; several are already present) | **≈ 90** | |
| **Net** | **≈ −0.8 to −1.0 GiB** installed | |

Today's absolute closure size is not computable on this workstation (no compute here). PR 1
records "before" and "after" from the builder.

## 5. Branding: a maintained fork

### 5.1 Fork, not patches

A fork, **`org-runink/desk`** (public, MIT), with these rules:

- `LICENSE` keeps "Copyright (c) 2026 The DEsk authors" and adds "Copyright (c) 2026 The
  RIVER Authors" for our changes. `NOTICE`/`README` credit DEsk and fyzugx and link upstream.
  MODDING.md itself asks forks to "say in your README what you changed".
- Branch `upstream` mirrors fyzugx/DEsk. Branch `runink` carries our commits as a short,
  ordered series, rebased onto each upstream release tag. Releases are tagged
  `v<upstream>-runink.<n>`, and `build/pkgbuilds/runink-desk` pins the tarball by sha256
  (invariant 9).
- Each upstreamable commit goes upstream as a PR, so the delta shrinks: the `libelogind`
  fallback, `--systemd` only on systemd, `loginctl` power fallback, C generators instead of
  Python, a `-Dweather_default` option, and theme hooks (below).

Why not a patch series in the PKGBUILD: themes change `draw.c` (35k lines) and `room.glsl`
(2.4k) **in step** (MODDING §7: "Change both, the same way"). A patch set that size breaks on
every upstream release and cannot be tested on its own (`desk-gputest --room`,
`desk-preview`). A fork can.

### 5.2 How the themes are built

- **The fork (MIT, neutral):** a refactor that can go upstream. Every colour, material and
  paper constant moves out of `draw.c` and `gtk.css` into one theme description that both the
  C tracer and the GLSL preamble read. `room_glsl_preamble` already passes every named `RM_*`
  constant, so this extends an existing mechanism. A build option `-Dtheme_dir=<path>` selects
  a theme directory (`theme.h`, `gtk.css`, icon set, door artwork). The default stays DEsk's
  walnut desk.
- **Runink River (trademark assets):** `branding/desk/<theme>/`, generated from
  `branding/palette.md` (the only colour source; `river lint branding-sync` extended to it).
  The Runink marks stay `LicenseRef-Runink-Trademark` in Runink River, never in the MIT fork.
  `runink-desk` builds with `-Dtheme_dir` pointing at it. A community build swaps that
  directory, like every other mark (docs/LICENSING.md §5).
- **Two themes, in order:** a ledger/paper "bookproof" theme (ruled ledger paper, ink, a
  clerk's lamp, the bookcase as bound ledgers), then an alchemy "sorcerer" theme (brass,
  parchment, an alembic on the desk). The first changes mostly materials and paper. The second
  changes room geometry, so it is a larger job in both tracers.
- **Greeter:** gtkgreet's CSS (`/usr/share/runink/greeter.css`) and background come from the
  same theme directory.

### 5.3 `themes/make-icons.py`

**Decision: ship the pre-generated SVGs now, and port the generator to Go when the icons are
restyled.** The 136 SVGs under `themes/icons/DEsk/` are committed upstream, so the build never
runs the script. The fork deletes `make-icons.py` and `contact-sheet.py`, so no Python remains
in our tree. When a Runink theme needs its own icons, `tools/deskicons` (Go, standard library:
SVG is text) is ported from the script's 1,530 lines. The contact sheet becomes a `-sheet`
flag. Their output is committed. Nothing about icons runs in the package build.

## 6. What Plasma provided, and what replaces it

`desk-session-helper` jobs come from DEsk's own autostart entries (`OnlyShowIn=DEsk`). Each
row below names what it will find on the image.

| Need | Plasma had | DEsk path | On the image |
|---|---|---|---|
| Notifications | plasmashell | **DEsk itself** (`post.c`, needs `-Dpost`) | built in |
| Tray (SNI) | plasmashell | **DEsk itself** (`tray.c`) | built in |
| Lock screen | kscreenlocker | **DEsk's front door** (PAM service `/etc/pam.d/desk` → `login`) | built in |
| Volume, battery and network readings | plasma-pa, powerdevil | DEsk's Control Panel (`panel.c`: `/sys`, `/proc`, `wpctl`) | wireplumber (present) |
| Network applet | plasma-nm | helper `network` → `nm-applet --indicator` | `network-manager-applet`, `nm-connection-editor` |
| Polkit agent | polkit-kde-agent | helper `polkit` → first in its list | `mate-polkit` |
| Keyring | kwallet + kwallet-pam | helper `keyring` → `gnome-keyring-daemon` | `gnome-keyring` + `pam_gnome_keyring` in greetd's PAM |
| Screenshot | spectacle | `desk-screenshot` (Print, Alt+Print) | `grim`, `wl-clipboard`, `libnotify` |
| Terminal | konsole | `STD_TERMINAL` | `foot` |
| File manager | dolphin | `STD_FILES` | `thunar` (+ `gvfs`, `udisks2`, `tumbler`) |
| Editor | kate | `STD_EDITOR` | `mousepad` |
| Images, PDF, archives, media | gwenview, okular, ark, dragon/elisa | `desk-mimeapps.list` | `ristretto`, `atril`, `engrampa`, `parole` |
| Displays | kscreen | Shell Settings → `wdisplays` (wlr-output-management) | `wdisplays` |
| Portals | xdg-desktop-portal-kde | `desk-portals.conf` (wlr, gtk) | `-wlr`, `-gtk` |
| Input methods | plasma-keyboard / none | helper `ime`, only for CJK/VI/TH locales | **none shipped** (ibus is 152 MiB and its setup is Python). `fcitx5` is documented as an opt-in |
| Media automount | plasma (device notifier) | helper `media` → `udiskie` | **gap**: udiskie is Python. Thunar mounts on click (gvfs/udisks2) |
| Low-battery letter | powerdevil | helper `power` → `poweralertd`/`batsignal` | **gap**: neither is in Artix. Later, a small Go notifier (`notify-send` on a `/sys` threshold) |
| Bluetooth UI | bluedevil | Shell Settings → `blueman-manager` | **gap**: blueman is Python. `bluetoothctl` (bluez-utils, present) for now |
| Printers UI | print-manager | Shell Settings → `system-config-printer` | **gap**: Python. CUPS's own web UI on `localhost:631` |
| Sound devices UI | plasma-pa | Shell Settings → `pavucontrol` | not shipped (needs gtk4). `wpctl` and DEsk's volume control |
| Partitions | partitionmanager | none | not shipped. `parted`/`gptfdisk` (present) |
| Screen-share chooser | kde portal | `desk-screen-chooser` → `zenity` | not shipped (gtk4). xdpw shares the only/first output |

## 7. Installer and live medium

### Installed system (installer/lib steps and their byte-identical profile copies)

- `80-enable-s6.sh`: `for _svc in sddm bluetoothd cupsd` becomes `greetd bluetoothd cupsd`.
  The VT 1 guard keys on `greetd` (and its messages say "the display manager"). The boot
  database check then requires `greetd-srv`.
- `20-clone-rootfs.sh`: delete `/etc/greetd/live.toml` (instead of the SDDM autologin
  drop-in) and the live `/etc/xdg/desk/shell.conf`. Its autologin check becomes "no
  `initial_session` survived in `/etc/greetd/*.toml`". The `sddm.conf.d` lines go.
- `32-locale-keyboard.sh`: trigger on `/usr/share/wayland-sessions/desk.desktop` instead of
  `/usr/share/{sddm,plasma}`. Keep `00-keyboard.conf`, which DEsk reads. Drop `kxkbrc`. Write
  `XKB_DEFAULT_LAYOUT`/`VARIANT` into the greeter's environment (the greetd `command` line) so
  the login screen types in the chosen layout.
- `50-runink-user.sh`: comment only (SDDM → greetd).
- Optional: write the measured Graphics level to `/etc/xdg/desk/shell.conf` with `desk-index
  --suggest` (DEsk's own installer does this), so a machine with a weak GPU starts at the
  right level. It must stay inside a timeout. A step for the graphical wizard, not a blocker.
- `installer/internal/wizard/*`, `installer/livefallback/*`: replace Plasma/SDDM process and
  bundle names (`plasmashell` → the live kiosk, `sddm` → `greetd`).

### Live medium

Today the live medium autologs into Plasma, and the installer opens in it. After the cut-over:

- `greetd` runs on the live medium too, with `/etc/greetd/live.toml` carrying
  `[initial_session] user = "runink"`, `command = "weston --shell=kiosk-shell.so --
  /usr/local/bin/river-installer-desktop"`. weston is already in `Packages-Live`.
- That makes the **weston kiosk the primary live path**, under a real elogind session for the
  live user. That is better than today's fallback, where weston runs as root. It is the path
  that already draws on machines where Plasma failed (`Packages-Live` comment, the XPS 13 9300
  case). `river-installer-desktop` already handles the non-root case.
- `river-live-fallback` waits for that weston instead of `plasmashell`, and stops `greetd`
  instead of `sddm` before its own kiosk and text-installer tiers.
- The live medium does **not** start DEsk. Its first-login room trace (6–9 s on a GPU,
  minutes on a CPU) has no place in an installer. Showing DEsk on the live medium is a later,
  separate choice.

## 8. Implementation order (each PR green on its own)

Fork work happens in `org-runink/desk` first. Runink River's PKGBUILD can only pin a fork tag
that exists.

| # | Repo | PR | Green means |
|---|---|---|---|
| F1 | desk | Fork, credit and NOTICE; delete all `.py`; `tools/glsl2c.c` and `tools/room-key.c` (`native: true`); `tools/checkwords` (Go test); `libelogind` fallback; `--systemd`/`systemctl` only on systemd; `loginctl` power fallback; `-Dweather_default`. Tag `v0.1.4-runink.1` | fork CI: muon build of the four configurations MODDING §10 lists, `go test ./tools/...` |
| 1 | river | **Packaging spike.** muon in `builder/Containerfile` (pinned tarball + sha256); `build/pkgbuilds/runink-{desk,cage,gtkgreet}`; build-all builds them. **Not** in Packages-Root yet. Record the measured closure before and after (pactree on the pinned DBs), replacing §4.3's estimates. Re-check every §4.1 package against `PIN_DATE` | Tier 1 (`python-purge` check 5 reads the new PKGBUILDs; `reuse`); a box run of the component stage producing the three packages |
| 2 | river | **Generalise, no behaviour change.** The display manager becomes a named value instead of the literal `sddm`: `80-enable-s6.sh` (+ copy), `rc.local` watcher, `river lint profile-manifest`'s VT 1 rule, `livefallback`'s bundle name, `patch-artools.go`. Still SDDM | Tier 1, `installer-go`, the existing Go tests still pass, with new cases for `greetd` |
| 3 | river | **Cut-over** (one coordinated change, no shims). Packages-Root/profile.yaml swap (§4); `forbidden.closure` gains `plasma-workspace kwin sddm python`; root-overlay `greetd-srv`, greetd config + PAM, `/etc/xdg/desk/shell.conf` (`weather = no`); Plasma/SDDM/KDE overlay files and their branding sources removed (`branding-sync` updated in the same PR); live-overlay `live.toml`; installer steps 20/32/50/80 + profile copies; `sddmtheme` → a greeter check; `qemu-gui-test.sh` logs in at gtkgreet and checks for `desk` instead of `plasmashell` | Tier 1, `installer-go`, the box component build; then **the owner's ISO build and a hardware boot to a working DEsk desktop** (release gate) |
| 4 | river | Docs: README, AGENTS.md §1 and invariant 1's examples (SDDM → greetd; the s6 invariant itself is unchanged), `website/` pages, CHANGELOG (installer-step contract change: downstream profiles notice at their next pin bump) | Tier 1, `docs.yml` |
| F2 | desk | Theme hooks: constants → one theme description read by C and GLSL; `-Dtheme_dir`. Default look unchanged | fork CI + `desk-gputest --room` (no pixel difference on the default theme) |
| 5 | river | `branding/desk/<bookproof>/` from `palette.md`; greeter CSS; `runink-desk` built with it; `branding-sync` rules | Tier 1; screenshots from `desk-preview` (box) attached |
| 6 | desk + river | Optional: `tools/deskicons` (Go) and the theme's icons; the second theme; DEsk's front door as the greetd greeter | per PR |

PRs 1 and 2 can be reviewed in parallel. PR 3 depends on both and on F1's tag. Nothing in
1–2 changes what an image ships, so a release can be cut between them.

## 9. Not verified here

No build or boot was run for this plan (phase A is research). Each item below is checked in
the PR named:

- the pinned snapshot has every package in §4.1 at `PIN_DATE` (PR 1);
- `libelogind.pc` Cflags, and the seatd/libseat logind backend (PR 1);
- muon builds DEsk, cage and gtkgreet unchanged apart from the fork's Python removal (PR 1);
- the measured closure delta (PR 1);
- `dbus-update-activation-environment --systemd`'s exit status without systemd1, the greetd
  PAM stack, and the session bus and PipeWire reaching a greetd session (PR 3, on a booted
  image);
- the DEsk session on the reference hardware: GPU trace, 2D fallback, lock, power-off through
  elogind (PR 3, hardware boot).
