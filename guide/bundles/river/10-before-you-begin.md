# Before you begin

## Hardware requirements

- **UEFI firmware.** Legacy BIOS boot is not supported: the installer writes a UEFI
  removable-media boot loader (`EFI/BOOT/BOOTX64.EFI`), so the node boots without a firmware
  boot entry.
- **An x86-64-v3 CPU** (AVX2, FMA, F16C, BMI1/2, MOVBE) with at least 4 physical cores. Most
  x86-64 CPUs from 2013 or later qualify. AVX-512 is not required.
- **At least 16 GB of RAM installed** (15360 MiB as the kernel reports it). The model tiers
  the node must serve can raise that floor; the install plan says by how much.
- **At least one eligible target disk of 64 GiB or more**, and about 200 GiB usable in the
  pool. Eligible means not the install medium, not USB or removable, not read-only and not
  in use. Disks are partitioned whole: an EFI system partition and a ZFS partition.

The install plan (`plan`) is the authority: it measures this machine, and it refuses a
machine below these minimums and says why.

## Network requirements

A Runink River host is dual-stack by default: it takes IPv4 by DHCP and IPv6 by SLAAC or
DHCPv6, and IPv6-only sites can turn IPv4 off. The k0s cluster network inside it is
IPv6-only either way, so the node needs a global IPv6 address to run its cluster. The
install itself needs no network at all, because the installer copies the running live system to the disk.

Setting up the network is still the first step of the live session
([river#set-up-the-network-first]): it records whether the machine is online, on a LAN only,
or offline, and each of those is fine for the install.

A network is needed only for the optional remote channel and for fetching a platform guide
bundle. GitHub publishes no IPv6 address, so on an IPv6-only network those need NAT64/DNS64
or an HTTPS proxy (`river.guide.proxy=` on the kernel command line).

## What you need to have ready

- A way to record a 64-character recovery key that the installer shows **once**. It unlocks
  the encrypted pool; without it, the data on the disk is unrecoverable.
- A hostname for the node.
- Optionally, an enrollment file for first boot, if the platform you deploy provides one.
