<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: CC-BY-4.0
-->

# One USB stick for several images

`build/make-multiboot-usb.sh` writes up to four ISOs onto one stick (the Runink River ISO
and, say, a downstream distribution's images), with a GRUB menu that boots any of them, and
turns the rest of the stick into an exFAT data partition. It needs UEFI firmware (as every
image does) with Secure Boot off (the standalone GRUB it installs is not signed).

## Layout

| # | GPT name | Type | Size | Contents |
|---|---|---|---|---|
| 1 | `RUNINK_BOOT` | EFI system partition, FAT32 (`-s 8`), label `RUNINK_BOOT` | 512 MiB | `EFI/BOOT/BOOTX64.EFI` (standalone GRUB) and `boot/grub/grub.cfg` (the menu) |
| 2.. | the ISO's label (`RIVER` for Runink River) | Linux data | ISO + 64 MiB | each ISO, written raw, in the order given |
| last | `RUNINK_DATA` | Microsoft basic data, exFAT, label `RUNINK_DATA` | the rest | anything (readable on every OS) |

Partitions are 1 MiB aligned. The 64 MiB after each ISO lets a slightly larger rebuild
reuse the layout; a much larger one needs the stick rewritten.

**How the menu boots an image.** The GRUB on `RUNINK_BOOT` loads only partition-table and
filesystem modules, finds `RUNINK_BOOT` by label and reads its menu. Each menu entry finds
its image by the **ISO 9660 volume label** (the label buildiso gives each profile, read from
the ISO when the stick is written) and runs that ISO's own `boot/grub/grub.cfg`. From there
the image boots exactly as from its own stick: its live initramfs finds its root by the same
label, which now names a partition. That is why the labels must differ, and the script
refuses two ISOs that share one. The GPT names (each ISO's label) are for people and `lsblk`;
nothing boots by them. Each menu entry is titled "Runink River" for the label `RIVER`, the
label otherwise, or what `--title` after its `--iso` says.

A private server medium's encrypted payloads ([MODEL-PAYLOAD.md](MODEL-PAYLOAD.md)) sit inside
its ISO (`/river-models`), so they travel with its partition and the installer finds them the
same way as on a single-image stick.

Tested: the layout, filesystems and read-back of two images were checked on a sparse file of
a 233 GB stick's exact size, and that image booted under QEMU/OVMF from emulated USB: the
`RUNINK_BOOT` menu, then the first ISO's own menu, then its live system. (2026-09-26: the
multi-ISO form, `--iso` repeated, was checked the same way on a sparse 16 GB image with two
ISOs; not booted.)

## Commands

Run from a Runink River checkout, in this order. Only step 5 needs root, and only step 5
writes to the stick.

**1. The ISOs**, each with its `.sha256` next to it (the build writes both):

```sh
ls ~/.cache/river-build/iso-out/*.iso ~/.cache/river-build/iso-out/*.iso.sha256
```

**2. The GRUB binary** (as your user; rootless podman, in the Artix builder image):

```sh
build/make-multiboot-usb.sh prepare-grub          # -> ~/.cache/river-build/usb/BOOTX64.EFI
```

**3. A dry run on an image file** of the stick's exact size (as your user; sparse, so it
takes little space, but writing it reads the whole size once: about 5 minutes for 233 GB):

```sh
lsblk -bdno SIZE /dev/sdX                          # the stick's size in bytes
build/make-multiboot-usb.sh write \
    --iso ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso \
    --iso /path/to/another-<date>-x86_64.iso --title "Another image" \
    --grub-efi ~/.cache/river-build/usb/BOOTX64.EFI \
    --size-bytes <bytes> --image-file ~/.cache/river-build/usb/stick.img
```

It prints the layout and reads every image back. Delete the file afterwards.

**4. Identify the stick and free it.** Check the model, size and serial, and make sure
nothing on it is mounted (unmount each mounted partition; the script refuses otherwise):

```sh
lsblk -o NAME,SIZE,TRAN,RM,MODEL,SERIAL,MOUNTPOINTS /dev/sdX
lsblk -dno SERIAL /dev/sdX | tail -c 9             # the last 8 characters of the serial
udisksctl unmount -b /dev/sdX1                     # for every mounted partition
```

**5. Write the stick** (root). The stick is erased.

```sh
sudo build/make-multiboot-usb.sh write \
    --device /dev/sdX --serial-suffix <last 8 characters> --size-bytes <bytes> \
    --iso ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso \
    --iso /path/to/another-<date>-x86_64.iso --title "Another image" \
    --grub-efi ~/.cache/river-build/usb/BOOTX64.EFI
```

Before it writes anything the script refuses unless **all** of these hold:

- it runs as root, and `--device` is a whole disk named `/dev/sdX`;
- the disk is attached over USB (`TRAN=usb`) and is removable (`/sys/block/sdX/removable`);
- its size is exactly `--size-bytes`;
- its serial (read at run time with `lsblk -dno SERIAL`) is at least 8 characters and ends in
  `--serial-suffix`;
- nothing on it is mounted, used as swap or held by another device (dm-crypt, LVM, RAID);
- every ISO matches its `.sha256`, they carry different ISO 9660 labels, and they fit.

It then shows the device and asks you to **type the last 8 characters of the serial again**;
anything else aborts with nothing written. There is no option to skip that question. It
checks the device is still idle, partitions it, writes GRUB and the menu, writes each ISO
with `dd ... oflag=direct conv=fsync`, creates the exFAT partition, and finally reads every
ISO partition back and compares them with the ISOs' sha256.

## Updating one image later

Rewrite only that partition when the new ISO still fits in it (ISO size <= partition
size; `lsblk -b -o NAME,SIZE /dev/sdX`), then compare it back:

```sh
sudo dd if=runink-river-<date>-x86_64.iso of=/dev/sdX2 bs=4M oflag=direct conv=fsync status=progress
sudo head -c "$(stat -c %s runink-river-<date>-x86_64.iso)" /dev/sdX2 | sha256sum   # = the .sha256
```

The menu finds images by label, so it needs no change. When the image no longer fits,
rewrite the whole stick (step 5).
