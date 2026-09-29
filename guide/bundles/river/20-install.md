# Installing

## Set up the network first
<!-- river-guide:step id=network kind=check run="river-netsetup --check" -->

Networking is the first step of every Runink River live session. Before `river-guide`
started, `river-netsetup --auto` applied the `river.net=` kernel parameter, or gave the
automatic wired connection a few seconds, and recorded the outcome. This check shows it:
the links and addresses, the default routes, the DNS servers, and whether the default
gateway, DNS and the internet (IPv4 and IPv6) answer. The internet check is a TCP connect
to port 443 of a well-known host; nothing is downloaded.

The outcome is **online**, **lan-only** or **offline**, and all three are valid: the
install copies the running live system and needs no network. The outcome is recorded in
`/run/river/net-state.json` for the later steps.

To change the setup, switch to the second console (**Alt+F2**) and run
`sudo river-netsetup`. It offers automatic wired setup (dual-stack: IPv4 DHCP plus IPv6
SLAAC/DHCPv6, or IPv6 only), Wi-Fi (scan, pick a network, type its passphrase), static IPv4/IPv6
addresses, `nmtui` for anything else, an optional HTTP proxy, and the hostname. The
installer asks again as its step 0. On a machine with no console, put
`river.net=dhcp` (dual-stack), `river.net=dhcp,v6only`,
`river.net=static:ADDR/PREFIX,gw=ADDR,dns=ADDR` or `river.net=off` on the kernel command
line.

## Confirm the live session
<!-- river-guide:step id=live-session kind=info -->

You booted **Runink River Sovereignty Server** from the install medium and `river-guide` is
on tty1. The live system runs from memory; nothing has been written to any disk yet. A login
shell is on the second console (**Alt+F2**, user `runink`).

## Check the firmware mode
<!-- river-guide:step id=firmware kind=check run="ls /sys/firmware/efi" -->

The directory `/sys/firmware/efi` exists only when the machine booted in UEFI mode. If this
check fails, reboot, enter the firmware setup, disable legacy/CSM boot, and boot the install
medium again in UEFI mode.

## Probe the hardware
<!-- river-guide:step id=hwprobe kind=check run="river-hwprobe" -->

`river-hwprobe` reads `/proc` and `/sys` only. It reports the CPU level (x86-64-v3 is
required), memory, GPUs, disks, network interfaces and TPM. It never opens a block device.
The install medium itself is marked and is never eligible as a target.

Type `hw` at the `river>` prompt for the same summary at any time.

## Review the install plan
<!-- river-guide:step id=plan kind=info -->

Type `plan` at the `river>` prompt. `river-plan` combines the hardware probe with the
medium's model manifest and shows which disks are eligible and what this machine can run.
Mark this step done (`done plan`) once you have reviewed it.

## Check the IPv6 address
<!-- river-guide:step id=ipv6-address kind=check run="ip -6 addr show scope global" -->

Lists global IPv6 addresses. The install does not need a network. The installed node's
Kubernetes cluster is IPv6-only and takes its node address from one of these, even when the
host also has IPv4. (The first step,
[river#set-up-the-network-first], set up and checked the network as a whole.) No global
IPv6 address usually means router advertisements are not reaching this port.

## Let another machine install this one (LAN install, target)

If another machine on the same LAN segment will drive this install, run `sudo runink-install`
on the second console (**Alt+F2**) and choose **Let another machine install this one**
(`sudo runink-install --mode target` skips the menu). Nothing announces itself or listens on
the network until you do, and only while that screen stays open; Ctrl-C there ends it.

That screen shows this machine's name (`river-install-` and six characters), a one-time
**pairing code** (`XXXX-XXXX`, valid for 15 minutes, locked after 5 wrong codes) and the
**host key fingerprint** of this session. Give the code to the operator and check that the
operator's screen shows the same host key fingerprint. When the operator pairs, this screen
shows `Paired with SHA256:...` and asks `Allow this operator to install this machine?`.
Answer `y` only if the operator's screen shows that same key. The operator then types each
disk's serial, and this screen shows the progress and the recovery key. Record the recovery
key here or on the operator's screen: it is shown once on each. The machine reboots into
the installed system when the operator says so.

## Install other machines on this network (LAN install, operator)

To install other machines from this one, run `sudo runink-install` on the second console
and choose **Install other machines on this network** (or run `river-pair install`). It
only listens: it lists the machines whose owners chose **Let another machine install this
one**, and never scans or probes anything else.

For each machine: pick it, type the pairing code its screen shows, compare the host key
fingerprint with its screen, and ask its owner to answer `y` there. Then review the plan it
makes, type the serial of every disk it will erase, and give the hostname, the model payload
passphrase (blank defers the models) and a file with the admin's SSH public keys. The
secrets are sent over the paired SSH channel and never written to disk. The install output
and the recovery key appear on this screen. Then reboot that machine and go on to the next
one. For datacenter batches, `river-pair install --answers FILE` can list the serials to
erase without typing them; the code and the owner's `y` are still needed for every machine.

## Prepare to record the recovery key
<!-- river-guide:step id=recovery-key kind=info -->

The next step creates an encrypted ZFS pool. With the default `generated` key, the installer
prints a 64-hex recovery key **once** and never writes it to any file. Have a way to record
it offline before you continue. If you choose `own`, you type a passphrase instead, and you
must remember it: there is no other way to unlock the pool.

## Run the installer
<!-- river-guide:step id=install kind=destructive run="sudo runink-install" -->

This step **erases (wipes) the target disk**: it partitions it and creates the pool.
`river-guide` does not run it. Switch to the second console (**Alt+F2**), log in, and run
the install command:

```sh
sudo runink-install
```

Its step 0 is the network: it shows the recorded outcome and asks whether to keep it or set
the network up again. It then shows the install plan. You confirm every disk it will erase **by typing that
disk's serial number**, which the installer shows on this console; the disks are checked
again against a fresh probe right before anything is written. It then asks for the pool name
(`zriver` for a new pool; an existing importable pool is offered instead and keeps its
name), the hostname, the boot-environment name, an optional enrollment file, and the key
mode (`generated` or `own`). You must type `YES` to proceed. It then runs its ordered steps:
preflight, partition and create the encrypted pool, clone the live system onto the pool,
per-node configuration, the boot loader and initramfs, the service user, and export of the
pool.

An existing **unencrypted** pool is refused, because ZFS cannot encrypt data in place.

When it finishes, return here with **Alt+F1** and mark the step done: `done install`.

## Reboot into the installed system
<!-- river-guide:step id=reboot kind=info -->

Remove the install medium and reboot. At boot, the node asks for the pool passphrase or
recovery key on the console, because no automatic unlock provider ships yet.
