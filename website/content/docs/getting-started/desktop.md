---
title: Using the desktop
linkTitle: The desktop
weight: 5
---
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
| Text | Mousepad |
| Web | Firefox (telemetry and studies off by system policy) |
| Pictures, PDF, archives, music and video | Ristretto, Atril, Engrampa, Parole |
| Displays | wdisplays |
| Screenshots | **Print** and **Alt+Print** (`desk-screenshot`, with `grim`); a letter says where the picture went |

## Common tasks

### Connect to a network

The network applet in the tray (`nm-applet`) handles wired, Wi-Fi and VPN connections, and
`nm-connection-editor` edits them. From a terminal, `nmcli` and `nmtui` do the same:
[Networking]({{< relref "/docs/configuration/networking" >}}).

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
[firewall]({{< relref "/docs/features/firewall" >}}) lets in.

### Keyboard layout and language

See [Keyboard & language]({{< relref "/docs/configuration/keyboard-locale" >}}).

## Under the hood

The session runs on s6 through elogind and dbus, not systemd:
[s6 init]({{< relref "/docs/features/s6#the-desktop-on-s6" >}}). The helpers are one Go
program, `river-desk` ({{< repo "installer/desk/" >}}, standard library only), shipped in
`runink-installer`; it starts only the programs it names (foot, `xdg-open`, `udisksctl`,
`gdbus`, `notify-send`). The design record for the move to DEsk is
{{< repo "docs/DESKTOP-DESK-PLAN.md" >}}.
