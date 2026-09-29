# Troubleshooting

## The installer refuses the pool

An existing unencrypted pool is refused because ZFS cannot encrypt existing data in place.
Migrate it instead: install fresh onto a new disk, then copy datasets across with `zfs send`
and `zfs receive` into the new encrypted pool.

## The machine does not boot the installed system

The installed node boots through the UEFI removable-media path, so it does not depend on a
firmware boot entry. If it still does not boot, confirm UEFI mode
([river#check-the-firmware-mode]) and that the firmware boots from the disk the installer
used.

## No guide model

`river-guide` runs in degraded mode when the machine has too little memory for the guide
model, or when the medium was built without it. Answers are then the matching guide
sections, quoted directly. Install steps are unaffected.

## Remote observation and questions

The remote channel lets an operator elsewhere follow this install through comments on a
GitHub issue. It is off unless you turn it on at this console (`remote on`), and it needs a
GitHub sign-in by device flow: `river-guide` shows a code on this console that you enter at
github.com/login/device from any device. No credential is stored on the medium.

Use a **private** repository dedicated to the installs of one site; `river-guide` warns when
the repository is public. Collaborators comment
`@river_install status`, `@river_install hw`, `@river_install plan`, or a question. Comments
carry only generic progress and a redacted hardware summary: no hostnames, addresses,
serial numbers, MAC addresses or keys.

A remote collaborator can approve a read-only check step only after you proposed it here and
switched on `remote approvals on`. Destructive steps are never run from a comment.

## GitHub is unreachable on an IPv6-only network

GitHub publishes no IPv6 address. Provide NAT64/DNS64 on the network, or an HTTPS proxy with
`river.guide.proxy=http://[proxy-address]:port` on the kernel command line. Without either,
the remote channel and platform bundles are unavailable and everything else works offline.

## The network check says lan-only or offline

Both are valid outcomes: the install needs no network. **lan-only** means the machine has an
address or its gateway answers, but the internet check did not get through: the network may
be closed, DNS may not resolve, or IPv4-only destinations may need NAT64/DNS64. **offline**
means no link has a usable address. Run `sudo river-netsetup` on the second console to set
up wired, Wi-Fi or static addressing ([river#set-up-the-network-first]), or continue offline.
