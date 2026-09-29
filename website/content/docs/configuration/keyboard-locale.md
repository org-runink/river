---
title: Keyboard & language
weight: 5
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The installer applies the language and keyboard layout you picked on its first screen to the
installed system (install step `32-locale-keyboard`), for the console, the SDDM login screen
and Plasma, so the password you typed during the install works from the first boot.

The installer offers English (US), Spanish (Spain), French (France) and Portuguese (Brazil)
as system languages, and a list of common keyboard layouts.

## Where the settings live

| File | Sets |
|---|---|
| `/etc/locale.conf` | the system language (`LANG=`) |
| `/etc/vconsole.conf` | the console keymap (`KEYMAP=`), also used when you type the disk passphrase |
| `/etc/X11/xorg.conf.d/00-keyboard.conf` | the keyboard layout for the login screen |
| `/etc/xdg/kxkbrc` | the default layout for Plasma sessions |

## Change the keyboard layout

- **For your Plasma session:** System Settings → Keyboard → Layouts. This is per user.
- **For the console and the disk-unlock prompt:** edit `KEYMAP=` in `/etc/vconsole.conf`
  (for example `KEYMAP=fr-latin9`), then rebuild the initramfs so the passphrase prompt uses
  it:

  ```bash
  sudo mkinitcpio -P
  ```

- **For the login screen:** edit `XkbLayout` in `/etc/X11/xorg.conf.d/00-keyboard.conf`.

{{< callout type="warning" >}}
Your disk passphrase is typed at boot with the **console** keymap. If you change it, make sure
you can still type your passphrase (or recovery key, which is only hexadecimal characters) with
the new layout before you reboot.
{{< /callout >}}

## Change the language

1. Uncomment the locale in `/etc/locale.gen` (for example `de_DE.UTF-8 UTF-8`) and run
   `sudo locale-gen`.
2. Set `LANG=de_DE.UTF-8` in `/etc/locale.conf`.
3. For your own session, System Settings → Region & Language.

Sign out and in again (or reboot) for the change to apply everywhere.
