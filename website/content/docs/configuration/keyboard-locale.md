---
title: Keyboard & language
weight: 5
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The installer applies the language and keyboard layout you picked on its first screen to the
installed system (install step `32-locale-keyboard`), for the console, the login screen
and DEsk, so the password you typed during the install works from the first boot.

The installer offers English (US), Spanish (Spain), French (France) and Portuguese (Brazil)
as system languages, and a list of common keyboard layouts.

## Where the settings live

| File | Sets |
|---|---|
| `/etc/locale.conf` | the system language (`LANG=`) |
| `/etc/vconsole.conf` | the console keymap (`KEYMAP=`), also used when you type the disk passphrase |
| `/etc/X11/xorg.conf.d/00-keyboard.conf` | the system keyboard layout, which DEsk (its `system` setting) and X11 programs read |
| `/etc/greetd/config.toml` | the login screen's layout (`XKB_DEFAULT_LAYOUT` on its `command` line) |

## Change the keyboard layout

- **For your DEsk session:** Shell Settings (the `keyboard_layout` row), or `keyboard_layout` in `~/.config/desk/shell.conf`. This is per user.
- **For the console and the disk-unlock prompt:** edit `KEYMAP=` in `/etc/vconsole.conf`
  (for example `KEYMAP=fr-latin9`), then rebuild the initramfs so the passphrase prompt uses
  it:

  ```bash
  sudo mkinitcpio -P
  ```

- **For the login screen:** edit `XKB_DEFAULT_LAYOUT` (and `XKB_DEFAULT_VARIANT`) on the
  `command` line in `/etc/greetd/config.toml` (the installer writes them as `env
  XKB_DEFAULT_LAYOUT=… XKB_DEFAULT_VARIANT=… cage …`); it applies from the next login screen. For
  DEsk's `system` setting and X11 programs, edit `XkbLayout` in
  `/etc/X11/xorg.conf.d/00-keyboard.conf` as well.

{{< callout type="warning" >}}
Your disk passphrase is typed at boot with the **console** keymap. If you change it, make sure
you can still type your passphrase (or recovery key, which is only hexadecimal characters) with
the new layout before you reboot.
{{< /callout >}}

## Change the language

1. Uncomment the locale in `/etc/locale.gen` (for example `de_DE.UTF-8 UTF-8`) and run
   `sudo locale-gen`.
2. Set `LANG=de_DE.UTF-8` in `/etc/locale.conf`.

Sign out and in again (or reboot) for the change to apply everywhere.
