# After the install

## Unlock the pool at boot

The ESP (mounted at `/boot`) is the only unencrypted part of the disk: it holds the kernel,
the initramfs and the boot loader. The initramfs asks for the key on the console and then
imports the pool. Keep the recovery key available for every boot until an unattended unlock
provider is installed.

## First-boot enrollment
<!-- river-guide:step id=first-boot kind=info -->

On first boot the node runs its enrollment once. Secrets are never on the install medium:
they come from the enrollment file you gave the installer, or are entered at the console. A
downstream platform may define what enrollment provisions; its guide bundle, if loaded,
continues from here.

## Verify the node
<!-- river-guide:step id=verify kind=info -->

Log in on the console and check that every dataset is encrypted:

```sh
zfs get -r encryption,keystatus zriver
```

Every dataset should show `aes-256-gcm` and `available`. Use your pool's name if it is not
`zriver`.

## Secret files

Every secret and state file on a Runink River node is root-only (mode 0600 in a 0700
directory), like `/etc/shadow`. A boot-time check re-asserts those modes and logs any drift.
