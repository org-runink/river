---
title: Services (s6)
weight: 2
description: "Inspect, start and stop services on Runink River, read how a service is defined, and add one of your own, with s6-rc and the s6 front end."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River's services are managed by **s6-rc** on top of the s6 supervision tree, with the
**`s6` front end** (s6-frontend) for the set that starts at boot. There is no `systemctl`;
the commands below replace it. Background: [s6 init]({{< relref "/docs/features/s6" >}}).

## See what is running

```bash
s6-rc -a list                                            # every service that is up now
s6-rc-db -c /etc/s6/rc/compiled contents default         # what the boot database starts
sudo s6 set status                                       # the enabled/disabled working set
s6-svstat /run/service/NetworkManager-srv                # state of one supervised longrun
```

`/run/service` is s6-linux-init's scan directory: every **longrun** that is up has a
directory there, and `s6-svstat` reports whether it is up, its PID and for how long.
A **oneshot** (such as `runink-fw` or `river-perms`) runs once and has no process to show;
`s6-rc -a list` lists it while it is in the "up" state.

On a fresh workstation `s6-rc -a list` includes, among others:

| Service | What it is |
|---|---|
| `runink-fw` | the [firewall]({{< relref "/docs/features/firewall" >}}), loaded before the network |
| `zfs-mount` | mounts the pool's datasets beside the boot environment (`/home`) |
| `river-perms` | re-asserts the modes of secret files |
| `NetworkManager-srv` | the network |
| `sshd-srv` | the SSH server (the firewall keeps port 22 closed until you open it) |
| `sddm-srv` | the login screen |
| `bluetoothd-srv`, `cupsd` | Bluetooth and printing |
| `rc-local` | runs `/etc/s6/rc.local` at the end of boot |

## Start and stop a service now

```bash
sudo s6-rc -d change sddm-srv    # bring a service (and everything that depends on it) down
sudo s6-rc -u change sddm-srv    # bring it (and everything it depends on) up again
```

`-d` and `-u` follow the dependency graph: stopping `runink-fw` would also stop
`NetworkManager-srv` and `sshd-srv`, which depend on it. A change made this way lasts until
the next boot.

## How a service is defined

A service is a directory, not a unit file. Runink River's own services live in two stores:
`/etc/s6/sv/` and `/etc/s6/adminsv/`. The firewall oneshot, for example:

```text
/etc/s6/sv/runink-fw/
├── type                     "oneshot"
├── up                       an execline script: runs `/usr/local/bin/runink-fw apply`, then exits 0
└── dependencies.d/
    └── modules              an empty file: start after the `modules` service
```

- **`type`** is `oneshot` (runs to completion, like `runink-fw`) or `longrun` (a supervised
  daemon, restarted by s6 if it dies).
- A oneshot has an **`up`** script (and optionally `down`); a longrun has a **`run`** script
  that `exec`s the daemon in the foreground.
- **`dependencies.d/`** holds one empty file per service that must be up first.

A dependency can be added to a packaged service the same way. Runink River does that to order
the desktop after its filesystems: `sddm-srv/dependencies.d/` contains `zfs-mount` and
`plymouth-quit`, and `NetworkManager-srv` and `sshd-srv` each get a `runink-fw` file. The
sources are under {{< repo "iso-profiles/river/root-overlay/etc/s6/" >}}.

Two rules the project's own services follow, and yours should too:

- **Never fail the boot over something recoverable.** `runink-fw` and `zfs-mount` report a
  failure and exit 0, so a problem leaves the machine reachable for repair instead of
  stopping NetworkManager or the login screen.
- **Long-running jobs that are not part of the desktop** start from `/etc/s6/rc.local`, which
  runs in init context, so elogind never reaps them when you log out.

## Change what starts at boot

The boot database is compiled from the stores by the `s6` front end. The installer builds it
with these commands (install step `80-enable-s6`, run in a chroot of the new system):

```bash
s6 repository sync                                   # read the stores into /etc/s6/repo
s6 set enable --pull-dependencies NetworkManager sshd rc-local ...   # the enabled set
s6 set commit                                        # compile it
s6 live install --init                               # install it as /etc/s6/rc/compiled
```

On an installed machine the same front end changes the set: `sudo s6 set enable <service>` or
`sudo s6 set disable <service>`, then `sudo s6 set commit`. s6 refuses to commit a set in
which an enabled service depends on a disabled one, which is why the installer passes
`--pull-dependencies`. The options for installing a new database on a running system are in
skarnet's [s6-frontend documentation](https://skarnet.org/software/s6-frontend/); after a
change, `s6-rc-db -c /etc/s6/rc/compiled contents default` shows what the next boot starts.

{{< callout type="warning" >}}
Keep `runink-fw`, `zfs-mount` and `river-perms` enabled. Without `zfs-mount`, `/home` stays
unmounted and no desktop session can start; without `runink-fw` the machine boots with no
firewall. `80-enable-s6` checks that each of them is in the boot database and fails the
install otherwise.
{{< /callout >}}

## Add a service of your own

1. Create its directory in `/etc/s6/sv/<name>/` with a `type` file, a `run` (longrun) or
   `up` (oneshot) script, and a `dependencies.d/` entry for each service it needs, for
   example `NetworkManager-srv` for anything that needs the network.
2. Enable it with the `s6` front end, as above, and commit.
3. Start it now with `sudo s6-rc -u change <name>`.

A longrun's `run` script should send its errors to the log and replace itself with the
daemon, as the graphical installer's service does:

```sh
#!/bin/sh
exec 2>&1
exec /usr/local/bin/my-daemon --foreground
```

Software that ships only a systemd unit needs such a definition: s6 does not read `.service`
files, and Runink River will not add systemd to run one.

## Logs

s6 sends a service's output to its catch-all logger unless the service has a logger of its
own. Runink River's own tools also write where noted on their pages; for example
`river-perms` logs every mode it had to correct to `/var/log/river-perms.log` (root only), and
the firewall logs dropped packets to the kernel log (`sudo dmesg | grep 'runink-fw'`).
