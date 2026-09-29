---
title: Firewall
weight: 4
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River's host firewall, `runink-fw`, is an nftables table, `inet runink_fw`, that
covers **IPv4 and IPv6 in one ruleset** and **denies by default**.

It is loaded by the s6 oneshot `runink-fw` **before NetworkManager, sshd and the rest of
boot**, on the installed workstation and on the live medium, so there is no moment when the
machine is reachable unprotected. It only ever replaces its own table, so other tools'
nftables tables stay intact.

## What it lets in

Input is **dropped** unless it is one of these (the `common_in` chain of
`/usr/local/lib/runink-net/runink-fw-common.nft`):

| Accepted | Rule |
|---|---|
| loopback | `iif "lo"` |
| replies to connections the machine opened | `ct state established,related`; `invalid` is dropped |
| ICMPv6, all of it | neighbour discovery, router advertisements and path MTU need it |
| ICMPv4 essentials | `echo-request`, `echo-reply`, `destination-unreachable`, `time-exceeded`, `parameter-problem` |
| DHCPv4 replies | UDP from port 67 to port 68 |
| DHCPv6 replies | UDP port 546 |
| mDNS | UDP port 5353, so printers and other machines on your LAN are still found |
| ports you opened | the `open_tcp` and `open_udp` sets |
| interfaces you listed for forwarding | the `forward_if` set (see below) |

**SSH is closed by default**, although `sshd` runs. **Outbound traffic is open**: browsing,
package downloads, `git`, Wi-Fi and DHCP work as on any laptop. **Forwarding is dropped**
unless you name an interface the machine may route for.

Every dropped packet is logged to the kernel log with the prefix `runink-fw drop: `
(forwarded ones with `runink-fw fwd drop: `), limited to five a minute:

```bash
sudo dmesg | grep 'runink-fw'
```

## Managing it

```bash
sudo runink-fw list                 # the ruleset, its sets and counters
sudo runink-fw open tcp 8080        # open a port to everyone until the next apply
sudo runink-fw close tcp 8080       # close it again
sudo runink-fw apply                # reload the posture from its files
sudo runink-fw flush                # remove the table: NO firewall until the next apply
```

`open` and `close` change the running sets only. To keep a port open across reboots, list it
in `/etc/runink/fw-open`, one per line, then apply:

```text
# /etc/runink/fw-open
tcp 22       # SSH
udp 51820    # WireGuard
```

```bash
sudo runink-fw apply
```

`#` comments are allowed. `apply` is what the boot runs, so the file is the posture.

## Forwarding for VMs and containers

To let the machine route for a VM network or a container bridge, put the interface name on its
own line in `/etc/runink/fw-forward` and run `sudo runink-fw apply`:

```text
# /etc/runink/fw-forward
virbr0
```

A listed interface is trusted in both directions: forwarding through it is accepted, and so
is **input** arriving from it to this machine. List only interfaces whose other side you
control.

## On the live medium

The live medium loads the same table from the first second. For the LAN-install pairing
tools it also accepts their ports (UDP 47653, TCP 47654 and 47655) from IPv6 link-local sources
(`fe80::/10`) only, from `/usr/local/lib/river-pair/fw-pair`. An installed machine carries
no pairing file and keeps those sets empty.

## How it is checked

`river lint firewall` ({{< repo "cli/internal/lint/firewall.go" >}}) checks the rules in CI: input and forward are
`policy drop` and output `policy accept`; only the essentials above are accepted and no fixed
port is open, SSH included; only `forward_if` opens forwarding; the whole ruleset is never
flushed; `runink-fw` is loaded before NetworkManager and sshd and is enabled on the live
medium; and the pairing ports stay link-local and live-only. Where the host allows it, the
lint also loads the ruleset into a throwaway network namespace. `build/qemu-gui-test.sh` checks that the firewall is loaded on an installed machine. The
design is in {{< repo "docs/ARCHITECTURE.md" >}} (Networking); the command's own help is the
header of `/usr/local/bin/runink-fw`.
