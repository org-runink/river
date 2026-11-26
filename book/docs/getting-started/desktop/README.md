# Using the desktop

<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The desktop is **DEsk** on Wayland: one wlroots compositor that is the whole desktop. Windows
are sheets of paper on a desk, programs sit in a bookcase, notifications arrive as letters in a
post box, and the screen locks at a front door. It is built in the `[runink]` repository from
DEsk's release plus the Runink fork's patches (`runink-desk`). Nothing in the session is
Python; the few jobs DEsk would hand to a Python program are done by small Go helpers instead.

## Signing in

greetd shows the login screen, gtkgreet in the cage compositor, on the dark Runink River
wallpaper. Sign in with the account the installer created. **The same password unlocks your
keyring**: the login screen's PAM stack (`/etc/pam.d/greetd`) includes `pam_gnome_keyring`, so
programs that keep passwords in the GNOME keyring (Wi-Fi passphrases from the network applet,
for example) do not ask again.

## The programs

| Job | Program |
|---|---|
| Terminal | foot |
| Files | Thunar (with `gvfs` and `udisks2`) |
| Text | DEsk Notepad (the drawer's notebook, `desk-notepad`), and Mousepad |
| Web | Firefox (telemetry and studies off by system policy) |
| Pictures, PDF, archives, music and video | Ristretto, Atril, Engrampa, Parole |
| Displays | wdisplays |
| Screenshots and films | **Print** puts up DEsk's camera (area, window or screen, with a timer); **Shift+Print** films (`desk-record`, with `ffmpeg`); `grim` takes the picture. A letter says where it went |
| Calculator, clipboard | The calculator in the drawer; **Super+V** opens the clipboard |

## Common tasks

### Connect to a network

The network applet in the tray (`nm-applet`) handles wired, Wi-Fi and VPN connections, and
`nm-connection-editor` edits them. From a terminal, `nmcli` and `nmtui` do the same:
[Networking](../../configuration/networking/README.md).

### Memory sticks and external disks

A memory stick or disk that udisks2 marks for automounting is mounted for you as you put it
in, and any that are already in when you sign in. Each mount arrives as a letter in the post
box. This is `river-desk automount`, started at login from
`/etc/xdg/autostart/river-automount.desktop`.

### Battery

On a laptop, `river-desk battery` sends a letter when a discharging battery falls to 15 %, and
again at 5 %.

### Bluetooth

Shell Settings' Bluetooth row (and DEsk's Bluetooth lever) opens **Bluetooth**,
`river-bluetooth`: [bluetuith](https://github.com/darkhz/bluetuith), a terminal program for
finding, pairing and connecting devices, in a foot window. `bluetoothctl` works too. The
`bluetoothd` service runs under s6.

### Printers

Shell Settings' Printers row opens **Printers**, `river-printers`: CUPS's own pages at
`http://localhost:631/` in your browser, where you add and manage printers. `cupsd` runs under
s6. Printers on your LAN are found over mDNS, which the
[firewall](../../features/firewall/README.md) lets in.

### Keyboard layout and language

See [Keyboard & language](../../configuration/keyboard-locale/README.md).

### The weather at the front door

Upstream DEsk asks open-meteo.com for the weather every half hour. Runink River builds it with
`-Dweather_default=false`, so **nothing is asked until you tick "The real weather at the front
door" in Shell Settings**. Then one request goes out every 30 minutes, carrying only the
latitude and longitude of the city your time zone is named after, made by DEsk itself. Off, the
door still shows the hour and the porch lamp, and the weather is always fine.

### Starting the desk over

DEsk keeps your settings, its traced room and its logs under `~/.config/desk`,
`~/.local/share/desk`, `~/.local/state/desk` and `~/.cache/desk`. Log out and remove them to
start with a fresh desk; your files are elsewhere. The first login after that traces the room
again (a few seconds on a GPU, longer on a screen drawn by the CPU). `runink-desk` is part of
the image and greetd starts it, so it is not removed with `pacman -R`.

## What differs from upstream DEsk

The desk, drawer, bookcase, post box, Control Panel, camera, calculator, notebook, front door and
the keys are upstream DEsk v0.2.1's code. What Runink River changes:

- **No Python.** DEsk's own build runs none, and its Python helpers are replaced: automounting
  and the low-battery letter are `river-desk` (Go) rather than `udiskie` and `poweralertd`;
  Bluetooth is `river-bluetooth` (bluetuith) rather than `blueman`; printers are CUPS's own
  pages rather than `system-config-printer`. `desk-look bake` (which needs Blender) is not
  installed.
- **elogind, not systemd**: sd-bus comes from `libelogind`, and the session does not ask a systemd
  user manager anything.
- **The weather is off until you turn it on** (above).
- **One login screen** (gtkgreet in cage, branded), no session chooser, since there is one desktop.
- **A smaller set of programs** than upstream's installer brings: foot, Firefox, Thunar and
  Mousepad. Most of the rows on Shell Settings' System tab (sound, look, default
  programs, disks, date and time, users, passwords, packages, backups, task manager, about)
  open programs this image does not carry, so they show faint with the name of the package.
- **No power-profile switch or auto-rotate** (`power-profiles-daemon`, `iio-sensor-proxy`: no
  s6 service yet) and **no input-method framework** for typing Japanese, Chinese, Korean,
  Vietnamese or Thai into DEsk's own fields (IBus needs Python), and no **Klee One** (the memo
  pad falls back to another handwriting face). The screen-share chooser shares the first screen
  without asking (no `yad`).
- **Not yet proven on a booted image**: the lamp-lit login, the front door's lock, the Control
  Panel's volume, brightness and radio levers, the post box and the camera under greetd and s6.
  Nobody has booted this build since DEsk replaced Plasma; the checklist is in
  [`docs/DESKTOP-DESK-PLAN.md`](https://github.com/org-runink/river/blob/main/docs/DESKTOP-DESK-PLAN.md).

## Under the hood

The session runs on s6 through elogind and dbus, not systemd:
[s6 init](../../features/s6/README.md#the-desktop-on-s6). The helpers are one Go
program, `river-desk` ([`installer/desk/`](https://github.com/org-runink/river/blob/main/installer/desk/), standard library only), shipped in
`runink-installer`; it starts only the programs it names (foot, `xdg-open`, `udisksctl`,
`gdbus`, `notify-send`). The design record for the move to DEsk is
[`docs/DESKTOP-DESK-PLAN.md`](https://github.com/org-runink/river/blob/main/docs/DESKTOP-DESK-PLAN.md).
