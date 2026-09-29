---
title: Networking
weight: 6
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River uses **NetworkManager**, supervised by s6. In Plasma, the network applet in the
panel (plasma-nm) handles wired, Wi-Fi and VPN connections; `nmcli` and `nmtui` do the same
from a terminal.

## During the install: network first

Setting up the network is the first thing the live session does, and **every outcome is
valid**: `online`, `lan-only` or `offline` all continue, because the install copies the live
system and downloads nothing. The live session's tool is `river-netsetup` (automatic wired
setup, Wi-Fi, static addresses, `nmtui` for anything else, or offline), with a read-only
check:

```bash
river-netsetup --check     # links, addresses, routes, DNS, gateway, internet over IPv4 and IPv6
```

The internet check is a plain TCP connection to port 443 of a well-known host; nothing is
downloaded. On a machine without a screen, the live medium also accepts the network setup on
the kernel command line (`river.net=dhcp`, `river.net=dhcp,v6only`,
`river.net=static:ADDR/PREFIX,gw=...,dns=...`, `river.net=off`). Wi-Fi passphrases are
never accepted there, because every local user can read `/proc/cmdline`; use a config file
(`sudo river-netsetup --config FILE`). The syntax is in {{< repo "docs/INSTALL.md" >}}
("Network first").

## On the installed system

If the installed system is not connected yet, connect
from the panel applet, or:

```bash
nmcli device status                                  # interfaces and their state
nmcli device wifi list                               # Wi-Fi networks in range
nmcli --ask device wifi connect "Example Lab"        # asks for the passphrase
```

### Static addresses

```bash
nmcli connection modify "Wired connection 1" \
  ipv4.method manual ipv4.addresses 192.0.2.10/24 ipv4.gateway 192.0.2.1 ipv4.dns 192.0.2.53 \
  ipv6.method manual ipv6.addresses 2001:db8::10/64 ipv6.gateway fe80::1
nmcli connection up "Wired connection 1"
```

### IPv6-only networks

The default is dual-stack: IPv4 by DHCP and IPv6 by SLAAC or DHCPv6. On an IPv6-only
network, turn IPv4 off for the connection so the machine does not wait for a DHCPv4 answer:

```bash
nmcli connection modify "Wired connection 1" ipv4.method disabled
```

## Firewall

The host firewall and how to open a port are described in
[Firewall]({{< relref "/docs/features/firewall" >}}).
