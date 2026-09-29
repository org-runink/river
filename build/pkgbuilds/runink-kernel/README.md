# linux-runink — the Runink sovereign kernel

**A fork of zen-kernel on the 7.2.x stable series, pinned to `v7.2.7-zen1`.** This is the
only kernel on a RIVER image, and it is built here. `runink-zfs` (OpenZFS **2.4.4** stable,
META `Linux-Maximum: 7.2`) is built against its headers. The package name stays
`linux-runink` because the installer, the closure-lint, the mkinitcpio preset
(`modules/<rel>/pkgbase`) and runink-zfs's `/usr/lib/modules/*-runink` glob all key off it.

> **Not built or booted yet.** Everything below was validated without compiling a kernel:
> sources, signatures, `prepare()`, the release guard and the config floor.
> `kernel-build.yml` (manual dispatch) has to be run, and the result boot-tested, before
> this replaces a working image.
>
> Update (2026-09-24): the analytics-profile configuration was compiled end to end by the
> AUR packaging (`packaging/aur/linux-runink`, same config.base/delta/require, same pins) in
> a rootless `archlinux` container: sources and signatures verified, config floor and
> kernelrelease guard passed, both packages built, installed, and mkinitcpio produced the
> preset and initramfs; `zfs-linux-runink` (OpenZFS 2.4.4) built and installed against it.
> It has **not been booted**.

## Why zen 7.2.x (and what it costs)

river#84 (2026-09-22) moved `linux-runink` from a linux-zen 7.1.5 fork to kernel.org 6.18.53
LTS, with an optional XanMod switch. The owner then chose zen on the current **stable**
series instead: `linux-runink` is again a fork of the official zen kernel
(<https://github.com/zen-kernel/zen-kernel>), now on 7.2.x. XanMod is gone with it (its LTS
line is 6.18-only).

The cost is cadence, and it is accepted deliberately:

- 7.2 is **not an LTS**. kernel.org ends each mainline stable series roughly **9-10 weeks**
  after its `.0`. Before that happens `linux-runink` must be rebased onto the next series
  (7.3.x-zen), with a new `olddefconfig` port of `config.base`.
- Within the series, bump on **every zen stable release** (`vX.Y.Z-zenN`).
- Every bump re-checks runink-zfs: the new kernel must be inside OpenZFS's declared range.
  2.4.4 covers 7.2; a 7.3 rebase needs an OpenZFS stable that declares 7.3.

The 7.1.5 fork's second problem, that no stable OpenZFS covered it, does not apply to 7.2:
OpenZFS 2.4.4 declares `Linux-Minimum: 4.18` / `Linux-Maximum: 7.2`.

## Pins and verification

| What | Pin | Verified by |
|---|---|---|
| Kernel base | `linux-7.2.7.tar.xz` sha256 `4ac34c47…cad43145a` | `.tar.sign`, Greg KH `647F2865…6092693E` |
| zen patch | `linux-v7.2.7-zen1.patch.zst` sha256 `c85f1c9d…3822e1e1` | `.patch.zst.sig`, Jan Alexander Steffens (heftig) `83BC8889…0F108CDF` |
| OpenZFS | `zfs-2.4.4.tar.gz` sha256 `2a3c70d5…48c8b1` | `.asc`, Tony Hutter `4F3BA9AB…D4598027` |

zen publishes a signed patch with every release tag (the GitHub release assets
`linux-vX.Y.Z-zenN.patch.zst` + `.sig`), so the build uses the kernel.org stable tarball plus
that patch, exactly as Arch's `linux-zen` does. The kernel.org GPG signature therefore still
covers the whole base, and the zen delta (~150 KB compressed) carries its maintainer's
signature. GitHub's auto-generated tag tarball is not used: nobody signs it and GitHub does
not promise it is byte-stable.

`validpgpkeys` lists Linus Torvalds (`ABAF11C6…00411886`), Greg KH and heftig. The public
keys are vendored in `keys/pgp/` (heftig's key is the copy Arch's `linux-zen` packaging
vendors, cross-checked against keys.openpgp.org), and the workflows `gpg --import` them, so
no keyserver is needed. Integrity is enforced: the workflows do not pass `--skipinteg`.

**Guards in the build:**

- `prepare()` fails unless `make kernelrelease` is exactly `7.2.7-zen1-1-runink` (the zen
  patch sets `EXTRAVERSION=-zen1`; `localversion.*` add `-1-runink`).
- `build()` re-checks `include/config/kernel.release`.
- `prepare()` fails unless the final `.config` meets every line of `config.require` (the
  platform floor below).
- runink-zfs's `prepare()` fails unless the installed kernel lies inside the range in
  OpenZFS's `META`.

**Sovereign mirror:** `RUNINK_KERNEL_MIRROR`, `RUNINK_ZEN_MIRROR` and `RUNINK_ZFS_MIRROR`
replace the upstream URLs, for example `RUNINK_KERNEL_MIRROR=https://mirror.example.org/kernel`.
`RUNINK_ZEN_MIRROR` must serve the release-asset layout (`<tag>/linux-<tag>.patch.zst`). The
pin is the checksums and signatures, not the URLs.

## ISA: generic x86-64-v3

zen 7.2 has no ISA-level Kconfig (the old zen `MNATIVE_*` menu is gone, which is why the
delta's `--enable MNATIVE_INTEL` did nothing on 7.1.5 either). Upstream's only option is
`X86_NATIVE_CPU`, which would tie the image to whatever machine built it. So the build
passes `KCFLAGS=-march=x86-64-v3`, and `config.delta` keeps `X86_NATIVE_CPU` off.

- kbuild's explicit `-mno-sse … -mno-avx` (zen adds `-mno-avx2 -fno-tree-vectorize`) still
  win, so kernel context uses no vector code.
- What v3 buys is BMI/LZCNT/MOVBE/POPCNT and v3 tuning.
- Meteor Lake (no AVX-512) has v3. v3 does not use AVX-512.
- runink-zfs builds with kbuild's generic `-march=x86-64`, which is compatible.

## Platform floor (`config.require`)

Every line is checked after `olddefconfig`, and a miss fails the build:

- **cgroups v2:** MEMCG, BLK_CGROUP, CPUSETS, PIDS, FREEZER, DEVICE, HUGETLB, PERF, MISC,
  CFS_BANDWIDTH.
- **Namespaces:** USER_NS (+ zen's USER_NS_UNPRIVILEGED), PID/NET/IPC/UTS/TIME. Also
  SECCOMP_FILTER.
- **eBPF:** BPF_SYSCALL, BPF_JIT + ALWAYS_ON, DEBUG_INFO_BTF, BPF_LSM, CGROUP_BPF,
  BPF_EVENTS, XDP_SOCKETS, NET_CLS_BPF/NET_ACT_BPF (m), SCHED_CLASS_EXT, and
  `LSM="landlock,lockdown,yama,integrity,bpf"`.
- **nftables and netfilter** for runink-fw, k0s iptables-nft and kube-router: NF_TABLES
  inet/ipv6, NFT_CT/NAT/MASQ/COMPAT, ip6tables nat/filter, xt matches, ipset, IP_VS.
- **Networking:** IPv6, bridge + br_netfilter, veth, VXLAN, WireGuard, overlayfs.
- **ZFS prerequisites:** ZLIB_DEFLATE/INFLATE=y, KALLSYMS, and DEBUG_LOCK_ALLOC off.
- **The hardening `config.delta` sets**, including FORTIFY_SOURCE=y and KEXEC/KEXEC_FILE=n
  (the two river#84 fixes: `--set-val` on a bool, and KEXEC_HANDOVER re-selecting
  KEXEC_FILE; `config.delta` still disables KEXEC_HANDOVER and CRASH_DUMP). Verified: dropping
  those two delta lines makes `prepare()` fail on `CONFIG_KEXEC_FILE is 'y', need 'n'`.

## User namespaces (bubblewrap)

zen carries the Debian-origin `kernel.unprivileged_userns_clone` sysctl; its boot default is
zen's `CONFIG_USER_NS_UNPRIVILEGED`. `river-sandbox` (bwrap, unprivileged, not setuid) needs
unprivileged user namespaces, so the image now pins it on in **both** places:

- `config.delta` sets `USER_NS_UNPRIVILEGED=y` (zen's Kconfig default is also y, but a
  default is not a floor) and `config.require` asserts it.
- `sysctl.d/99-runink.conf` sets `kernel.unprivileged_userns_clone=1` explicitly again, next
  to `user.max_user_namespaces=28633`, which still gates it.

History: river#83 added the sysctl line while the kernel was the 7.1.5 zen fork. river#85
removed it because river#84's vanilla 6.18 kernel has no such key and `sysctl` logged an
unknown-key error on every boot. On zen the key exists again, so the explicit setting is
back: it documents the dependency where an operator looks, and a later config change cannot
silently turn bwrap off. `tests/assert-golden.sh` fails if the key is present and not 1.

## Upgrade and rollback

A kernel bump is a paired bump. Within 7.2.x: change `_minor` / `_zenrel` here, update the
two sha256 pins, re-run `makepkg -o` (config floor + release guard), and re-check
`runink-zfs`'s META range. A series rebase (7.2 → 7.3) additionally needs a fresh
`olddefconfig` port of `config.base`, an update of the README config diff, and an OpenZFS
stable that declares the new series. Then:

1. **Keep the previous packages.** Leave the old `linux-runink`, `linux-runink-headers`,
   `runink-zfs` and `runink-zfs-utils` `.pkg.tar.zst` in `localrepo/`. Only `repo-add` the
   new ones; never `repo-remove`/delete the old files until the new set has booted. That
   keeps a downgrade to `pacman -U` of the old files, offline.
2. **Snapshot the boot environment first.** Before upgrading a node, run
   `zfs snapshot <pool>/ROOT/<be>@pre-<kver>`. Better, clone it into a new BE and upgrade
   that clone, keeping the current BE as the GRUB fallback. A bad kernel is then a reboot
   into the old BE, or a `zfs rollback` of the snapshot, and no rebuild.
3. Upgrade the kernel and runink-zfs **in one transaction**. The module must match the
   kernel release, and `mkinitcpio -P` must see both.

**Rolling back to 6.18 LTS** is a revert of this change in git (river#84's PKGBUILD, config
files and the 6.18-era `99-runink.conf`) plus a rebuild. OpenZFS 2.4.4 covers both, so the
ZFS package only needs rebuilding against the reverted headers. On a node, the pre-upgrade
BE snapshot is the rollback; no package from this repo needs to be kept for it.

## Staged rollout (do NOT enable lockdown blind)

1. **v1 — boot it.** Build `linux-runink` (`kernel-build.yml`), then `runink-zfs`
   (`zfs-build.yml`). Test: qemu boot, then install onto a **non-default** BE and boot it via
   `efibootmgr --bootnext`. Confirm the ZFS root imports and the stack comes up.
2. **v2 — sign ZFS.** runink-zfs signs `spl.ko`/`zfs.ko` with the kernel's per-build key
   (sha512, the same as `MODULE_SIG_HASH`) when `runink_signing.pem` is present. Confirm the
   signed modules load.
3. **v3 — lock it down.** Flip `MODULE_SIG_FORCE=y` in `config.delta`, add
   `lockdown=integrity` to the grub cmdline (`40-boot-grub-zfs.sh`), rebuild, and retest.
   Only then does it become the default.

## Build

```sh
# inside an Artix container with base-devel (kernel-build.yml does exactly this):
cd build/pkgbuilds/runink-kernel
gpg --import keys/pgp/*.asc
makepkg -f
# -> linux-runink-*.pkg.tar.zst + linux-runink-headers-*.pkg.tar.zst -> repo-add to localrepo/
```

## Analytics profile

`config.delta` ends with the analytics / throughput profile, which replaces zen's desktop
latency tuning: `ZEN_INTERACTIVE=n`, `PREEMPT_LAZY` (with `PREEMPT_DYNAMIC` kept, so
`preempt=full` still works), `HZ=250`, THP `madvise`, BBR + `fq` as defaults,
`SCHED_AUTOGROUP=n` and `WQ_POWER_EFFICIENT_DEFAULT=n`. The resolved configuration differs
from the pre-profile one in exactly 19 symbols; `config.require` asserts them, together with
the features the profile deliberately keeps (NUMA balancing, MGLRU, zswap, PSI, io_uring,
hugetlbfs, sched_ext, every CPU mitigation). The rationale table with citations is in
[docs/KERNEL.md](../../../docs/KERNEL.md#analytics-profile). The same configuration is
published for Arch Linux by `packaging/aur/linux-runink`, which fetches these three files
from a tag of this repository; `river lint aur-sync` keeps the sha256 pins of both
PKGBUILDs equal to the files.

## Config differences versus the 6.18.53 config

(Written for the 7.2 port, before the analytics profile; the profile's own 19 changes are
listed in docs/KERNEL.md.)

`config.base` is the linux-zen 7.1.5 config (the `config.x86_64` this package shipped before
river#84), put through `make olddefconfig` in a 7.2.7 tree with the zen patch applied. It
was ported from the zen config rather than from the 6.18 `config.base`, because that port
had already dropped every zen-only and 7.x-only symbol (ZEN_INTERACTIVE, -O3, BBRv3, the new
7.x drivers); porting it forward would have left those at Kconfig defaults instead of zen's
choices. The lists below compare the **final resolved 6.18.53 config** (river#84) with the
**final resolved 7.2.7-zen1 config** (base + `config.delta` + `olddefconfig`, taken from
`makepkg -o`). Compiler/toolchain version symbols are omitted.

**Changes that matter:**

- **Zen interactivity tuning came back with the 7.2 port and was then turned OFF by the
  analytics profile** (see below): `ZEN_INTERACTIVE=n`, `HZ=250`, `PREEMPT_LAZY` with
  `PREEMPT_DYNAMIC`. `SCHED_ALT` (BMQ/PDS) and `FORCE_IRQ_THREADING` stay off.
- **`-O2` becomes `-O3`** (`CC_OPTIMIZE_FOR_PERFORMANCE_O3`, zen's choice).
- **`USER_NS_UNPRIVILEGED=y` is back**, now pinned and asserted; see the user namespaces
  section.
- **BBRv3 is back as a module** (`TCP_CONG_BBR3=m`), not selected by default. The analytics
  profile makes upstream BBR (`TCP_CONG_BBR=y`) the default with the `fq` qdisc.
- **`vhba` is back as a module** (zen's CD-emulation driver; unused, never loaded).
- **7.x features return:** `SCHED_CACHE`, `EXT_SUB_SCHED`, `IO_URING_BPF`, `RSEQ_SLICE_EXTENSION`,
  `IOMMU_PT*`, `PCI_TSM`/`TSM`, the new `NTFS` driver, the ML-DSA/SHA3 crypto libraries
  (the ML-DSA module-signing key types stay off; module signing is unchanged), the NFS 4.0/4.2
  split, and new drivers.
- **Symbols that 7.2 removed** fall away (ATM and PCMCIA NICs, old fbdev, ISDN, the DRBG
  menu split, `RANDOM_KMALLOC_CACHES` which was already off, etc.). None is a Meteor Lake
  platform driver.
- **Hardening is unchanged:** the whole `config.require` floor, FORTIFY_SOURCE, KEXEC/
  KEXEC_FILE/KEXEC_HANDOVER/CRASH_DUMP/HIBERNATION off, module signing, and the `lsm=` order
  hold on both.
- Changed values below are either type changes with the same meaning
  (`BOOTPARAM_*_PANIC` became an int: `n` → `0`) or module/builtin flips forced by 7.2
  `select`s.

<details><summary>Full list — changed values (6.18.53 → 7.2.7-zen1)</summary>

```
~CONFIG_BOOTPARAM_HUNG_TASK_PANIC n -> 0
~CONFIG_BOOTPARAM_SOFTLOCKUP_PANIC n -> 0
~CONFIG_CC_OPTIMIZE_FOR_PERFORMANCE y -> n
~CONFIG_COMPACT_UNEVICTABLE_DEFAULT 1 -> 0
~CONFIG_CRYPTO_AEAD2 y -> m
~CONFIG_CRYPTO_LIB_GF128MUL y -> m
~CONFIG_CRYPTO_LZ4 m -> y
~CONFIG_LZ4_COMPRESS m -> y
~CONFIG_MULTIPLEXER m -> y
~CONFIG_NETFILTER_NETLINK m -> y
```

</details>

<details><summary>Full list — symbols in the 6.18.53 config that do not exist in 7.2.7-zen1 (137)</summary>

```
CONFIG_ACENIC=n CONFIG_APPLICOM=m CONFIG_ARCH_ENABLE_MEMORY_HOTREMOVE=y 
CONFIG_ARCH_SUPPORTS_AUTOFDO_CLANG=y CONFIG_ARCH_SUPPORTS_PROPELLER_CLANG=y 
CONFIG_ARCH_SUPPORTS_PT_RECLAIM=y CONFIG_ATALK=m CONFIG_ATM_CLIP=n CONFIG_ATM_DUMMY=n 
CONFIG_ATM_ENI=n CONFIG_ATM_FORE200E=n CONFIG_ATM_HE=n CONFIG_ATM_IA=n CONFIG_ATM_IDT77252=n 
CONFIG_ATM_LANAI=n CONFIG_ATM_LANE=n CONFIG_ATM_NICSTAR=n CONFIG_ATM_TCP=n CONFIG_ATP=n 
CONFIG_BALLOON_COMPACTION=y CONFIG_BT_HCIBLUECARD=m CONFIG_BT_HCIBT3C=m CONFIG_BT_HCIDTL1=m 
CONFIG_CAIF=n CONFIG_CLOCKSOURCE_WATCHDOG_MAX_SKEW_US=125 CONFIG_CRYPTO_AES_TI=n 
CONFIG_CRYPTO_ANSI_CPRNG=n CONFIG_CRYPTO_DES3_EDE_X86_64=n CONFIG_CRYPTO_DRBG_CTR=y 
CONFIG_CRYPTO_DRBG_HASH=y CONFIG_CRYPTO_DRBG_HMAC=y CONFIG_CRYPTO_DRBG_MENU=y 
CONFIG_CRYPTO_FCRYPT=m CONFIG_CRYPTO_GHASH=m CONFIG_CRYPTO_GHASH_CLMUL_NI_INTEL=n 
CONFIG_CRYPTO_HKDF=m CONFIG_CRYPTO_MICHAEL_MIC=m CONFIG_CRYPTO_NHPOLY1305=m 
CONFIG_CRYPTO_NHPOLY1305_AVX2=n CONFIG_CRYPTO_NHPOLY1305_SSE2=n CONFIG_CRYPTO_PCBC=m 
CONFIG_CRYPTO_POLYVAL=m CONFIG_CRYPTO_POLYVAL_CLMUL_NI=n CONFIG_CRYPTO_RNG_DEFAULT=y 
CONFIG_CRYPTO_SM3_AVX_X86_64=n CONFIG_CRYPTO_SM3_GENERIC=n CONFIG_DEBUG_FS_DISALLOW_MOUNT=n 
CONFIG_DEV_SYNC_PROBE=m CONFIG_DMABUF_HEAPS_CMA_LEGACY=y CONFIG_DMABUF_MOVE_NOTIFY=n 
CONFIG_DMABUF_SELFTESTS=n CONFIG_DMABUF_SYSFS_STATS=n CONFIG_DNET=n 
CONFIG_DRM_ANALOGIX_ANX78XX=m CONFIG_DRM_ANALOGIX_DP=m CONFIG_EDAC_LEGACY_SYSFS=y 
CONFIG_EROFS_FS_ONDEMAND=y CONFIG_EZX_PCAP=y CONFIG_FB_ARK=n CONFIG_FB_HGA=n CONFIG_FB_HYPERV=n 
CONFIG_FB_MATROX=n CONFIG_FB_MODE_HELPERS=n CONFIG_FB_S3=n CONFIG_FB_VT8623=n 
CONFIG_GENERIC_TIME_VSYSCALL=y CONFIG_GLOB_SELFTEST=n CONFIG_HAMACHI=n CONFIG_HAMRADIO=n 
CONFIG_HIPPI=n CONFIG_HYPERV_IOMMU=y CONFIG_I2C_DESIGNWARE_SLAVE=n CONFIG_I82092=n 
CONFIG_IIO_INTERRUPT_TRIGGER=m CONFIG_INFINIBAND_OPA_VNIC=n CONFIG_INPUT_PCAP=m 
CONFIG_IOMMU_IO_PGTABLE=y CONFIG_ISDN=n CONFIG_ITCO_VENDOR_SUPPORT=n 
CONFIG_KVM_GENERIC_MMU_NOTIFIER=y CONFIG_LENOVO_WMI_DATA01=m CONFIG_MACHZ_WDT=m 
CONFIG_MDIO_BUS=m CONFIG_MEMORY_BALLOON=y CONFIG_MFD_WL1273_CORE=n CONFIG_MODULE_SIG_SHA1=n 
CONFIG_MWAVE=n CONFIG_NET_VENDOR_ALTEON=y CONFIG_NET_VENDOR_FUJITSU=y 
CONFIG_NET_VENDOR_NETERION=y CONFIG_NET_VENDOR_PACKET_ENGINES=y 
CONFIG_NFSD_V4_DELEG_TIMESTAMPS=n CONFIG_NFS_V4_1=n CONFIG_NF_CT_PROTO_UDPLITE=y 
CONFIG_PAHOLE_HAS_SPLIT_BTF=y CONFIG_PARAVIRT_DEBUG=n CONFIG_PCIEAER_CXL=y 
CONFIG_PCI_PWRCTRL_SLOT=n CONFIG_PCMCIA_3C574=n CONFIG_PCMCIA_3C589=n CONFIG_PCMCIA_AXNET=n 
CONFIG_PCMCIA_FMVJ18X=n CONFIG_PCMCIA_NMCLAN=n CONFIG_PCMCIA_SMC91C92=n CONFIG_PREEMPT_NONE=n 
CONFIG_PREEMPT_VOLUNTARY=n CONFIG_PREFIX_SYMBOLS=y CONFIG_RANDOM_KMALLOC_CACHES=n 
CONFIG_READ_ONLY_THP_FOR_FS=y CONFIG_REGULATOR_PCAP=m 
CONFIG_RPCSEC_GSS_KRB5_ENCTYPES_AES_SHA1=y CONFIG_RPCSEC_GSS_KRB5_ENCTYPES_AES_SHA2=y 
CONFIG_RPCSEC_GSS_KRB5_ENCTYPES_CAMELLIA=y CONFIG_RTC_DRV_PCAP=m CONFIG_S2IO=n 
CONFIG_SENSORS_APDS990X=m CONFIG_SERIAL_8250_DEPRECATED_OPTIONS=y CONFIG_SERIO_CT82C710=n 
CONFIG_SLUB_CPU_PARTIAL=y CONFIG_SND_SOC_AMD_RPL_ACP6x=n CONFIG_TASKS_TRACE_RCU_READ_MB=n 
CONFIG_TCP_SIGPOOL=y CONFIG_TEST_MIN_HEAP=n CONFIG_TEST_UUID=n CONFIG_TLS_TOE=n 
CONFIG_TOUCHSCREEN_PCAP=m CONFIG_USB_CDNSP_GADGET=y CONFIG_USB_CDNSP_HOST=y 
CONFIG_USB_CDNS_HOST=y CONFIG_WIZNET_BUS_ANY=y CONFIG_WIZNET_BUS_DIRECT=n 
CONFIG_WIZNET_BUS_INDIRECT=n CONFIG_WIZNET_W5100_SPI=m CONFIG_WIZNET_W5300=m 
CONFIG_X86_AMD_PSTATE_DYNAMIC_EPP=n CONFIG_XEN_DEBUG_FS=n CONFIG_YELLOWFIN=n 
```

</details>

<details><summary>Full list — symbols new in the 7.2.7-zen1 config (zen-only or 7.x-only; 297)</summary>

```
CONFIG_ABP2030PA=m CONFIG_ABP2030PA_I2C=m CONFIG_ABP2030PA_SPI=m CONFIG_ACPI_APEI_GHES_NVIDIA=m 
CONFIG_AD4134=m CONFIG_AD4691=n CONFIG_AD5446_I2C=m CONFIG_AD5446_SPI=m CONFIG_AD5706R=n 
CONFIG_ADL8113=m CONFIG_ADP810=m CONFIG_ADXL345=m CONFIG_ADXL345_I2C=m CONFIG_ADXL345_SPI=m 
CONFIG_AIR_AN8801_PHY=n CONFIG_AIR_NET_PHYLIB=m CONFIG_ALIBABA_EEA=n CONFIG_AMD_IOMMU_IOMMUFD=n 
CONFIG_APDS9999=n CONFIG_ARCH_HAS_LAZY_MMU_MODE=y CONFIG_ARCH_MEMORY_ORDER_TSO=y 
CONFIG_ARCH_WANTS_CLOCKSOURCE_READ_INLINE=y CONFIG_ASUS_ARMOURY=m 
CONFIG_ASUS_WMI_DEPRECATED_ATTRS=y CONFIG_ATH11K_CFR=y CONFIG_AYANEO_EC=m 
CONFIG_BACKLIGHT_AW99706=m CONFIG_BACKLIGHT_CGBC=m CONFIG_BACKLIGHT_MAX25014=n CONFIG_BALLOON=y 
CONFIG_BALLOON_MIGRATION=y CONFIG_BATTERY_CHARGER_SURFACE_RT=n CONFIG_BATTERY_S2MU005=m 
CONFIG_BITLAND_MIFS_WMI=m CONFIG_BLK_ERROR_INJECTION=n CONFIG_BMA220_I2C=m CONFIG_BMA220_SPI=m 
CONFIG_CAN_DUMMY=n CONFIG_CAN_VIRTIO_CAN=n CONFIG_CC_HAS_COUNTED_BY_PTR=y 
CONFIG_CC_MS_EXTENSIONS="-fms-extensions" CONFIG_CC_OPTIMIZE_FOR_PERFORMANCE_O3=y 
CONFIG_CHARGER_RT9756=m CONFIG_CMDLINE_LOG_WRAP_IDEAL_LEN=1021 CONFIG_CRYPTO_ACOMP=m 
CONFIG_CRYPTO_LIB_AES_ARCH=y CONFIG_CRYPTO_LIB_AES_CBC_MACS=y CONFIG_CRYPTO_LIB_BLAKE2B=y 
CONFIG_CRYPTO_LIB_GF128HASH=y CONFIG_CRYPTO_LIB_GF128HASH_ARCH=y CONFIG_CRYPTO_LIB_MLDSA=m 
CONFIG_CRYPTO_LIB_NH=m CONFIG_CRYPTO_LIB_NH_ARCH=y CONFIG_CRYPTO_LIB_SHA3=y 
CONFIG_CRYPTO_LIB_SM3=m CONFIG_CRYPTO_LIB_SM3_ARCH=y CONFIG_CRYPTO_MLDSA=m CONFIG_CRYPTO_SM3=m 
CONFIG_CXL_ATL=y CONFIG_DAMON_DEBUG_SANITY=n CONFIG_DEBUG_ATOMIC=n 
CONFIG_DEBUG_BUGVERBOSE_DETAILED=y CONFIG_DEBUG_GENERIC_PT=n CONFIG_DELL_DW5826E_RESET=n 
CONFIG_DEV_DAX_FSDEV=m CONFIG_DMABUF_HEAPS_SYSTEM_CC_SHARED=n CONFIG_DM_INLINECRYPT=n 
CONFIG_DPLL_REFCNT_TRACKER=n CONFIG_DRIVER_DEFERRED_PROBE_TIMEOUT=10 CONFIG_DRM_COREBOOTDRM=m 
CONFIG_DRM_PANEL_FOCALTECH_OTA7290B=n CONFIG_DRM_RAS=y CONFIG_DRM_ST7571=m 
CONFIG_DRM_ST7571_SPI=m CONFIG_DRM_ST7920=m CONFIG_DWMAC_MOTORCOMM=m 
CONFIG_DYNAMIC_FTRACE_WITH_JMP=y CONFIG_EDAC_IMH=m CONFIG_EROFS_FS_PAGE_CACHE_SHARE=n 
CONFIG_EXT_SUB_SCHED=y CONFIG_FONT_TER10x18=n CONFIG_FORCE_IRQ_THREADING=n 
CONFIG_FPGA_MGR_EFINIX_SPI=n CONFIG_FUNCTION_SELF_TRACING=n CONFIG_FUTEX_ROBUST_UNLOCK=y 
CONFIG_FWCTL_BNXT=m CONFIG_GENERIC_BITREVERSE=y CONFIG_GENERIC_CLOCKEVENTS_COUPLED=y 
CONFIG_GENERIC_CLOCKEVENTS_COUPLED_INLINE=y CONFIG_GENERIC_PT=y CONFIG_GPIO_BY_PINCTRL=m 
CONFIG_GPIO_NOVALAKE=m CONFIG_GPIO_QIXIS_FPGA=m CONFIG_GPIO_WATCHDOG=n 
CONFIG_GPIO_WAVESHARE_DSI_TOUCH=n CONFIG_GPIO_WCD934X=m CONFIG_HAVE_DYNAMIC_FTRACE_WITH_JMP=y 
CONFIG_HAVE_FUTEX_ROBUST_UNLOCK=y CONFIG_HAVE_KLP_BUILD=y CONFIG_HAVE_PV_STEAL_CLOCK_GEN=y 
CONFIG_HAVE_SINGLE_FTRACE_DIRECT_OPS=y CONFIG_HAVE_TRUSTED_KEYS_DEBUG=y CONFIG_HID_HUAWEI=m 
CONFIG_HID_LENOVO_GO=m CONFIG_HID_LENOVO_GO_S=m CONFIG_HID_OXP=n CONFIG_HID_RAKK=n 
CONFIG_HID_RAPOO=m CONFIG_HRTIMER_REARM_DEFERRED=y CONFIG_HSA_AMD_P2P=y CONFIG_I3C_OR_I2C=y 
CONFIG_INFINIBAND_BNG_RE=m CONFIG_INFINIBAND_USER_ACCESS_CORE=y CONFIG_INTEL_EHL_PSE_IO=m 
CONFIG_INTEL_MEI_CSC=m CONFIG_INTEL_PMC_PWRM_TELEMETRY=m CONFIG_INV_ICM45600=m 
CONFIG_INV_ICM45600_I2C=m CONFIG_INV_ICM45600_SPI=m CONFIG_IOMMU_PT=y CONFIG_IOMMU_PT_AMDV1=y 
CONFIG_IOMMU_PT_RISCV64=n CONFIG_IOMMU_PT_VTDSS=y CONFIG_IOMMU_PT_X86_64=y 
CONFIG_IO_URING_BPF=y CONFIG_IO_URING_BPF_OPS=y CONFIG_KEYBOARD_CHARLIEPLEX=m 
CONFIG_KMALLOC_PARTITION_CACHES=n CONFIG_LEDS_LP5812=m CONFIG_LEDS_MAX5970=m 
CONFIG_LEDS_OSRAM_AMS_AS3668=m CONFIG_LENOVO_WMI_CAPDATA=m CONFIG_MAX14001=m CONFIG_MAX22007=m 
CONFIG_MCP47FEB02=m CONFIG_MFD_MAX5970=m CONFIG_MGBE=m CONFIG_MMC5633=m CONFIG_MMC5983=n 
CONFIG_MODULE_SIG_KEY_TYPE_MLDSA_44=n CONFIG_MODULE_SIG_KEY_TYPE_MLDSA_65=n 
CONFIG_MODULE_SIG_KEY_TYPE_MLDSA_87=n CONFIG_MTD_VIRT_CONCAT=y CONFIG_MUX_CORE=y 
CONFIG_NET_DSA_LANTIQ_COMMON=m CONFIG_NET_DSA_MXL862=m CONFIG_NET_DSA_MXL_GSW1XX=m 
CONFIG_NET_DSA_TAG_MXL_862XX=m CONFIG_NET_DSA_TAG_MXL_GSW1XX=m CONFIG_NET_DSA_TAG_NETC=n 
CONFIG_NET_DSA_TAG_YT921X=m CONFIG_NET_DSA_YT921X=m CONFIG_NET_VENDOR_ALIBABA=y 
CONFIG_NET_VENDOR_MUCSE=y CONFIG_NFSD_V4_2_INTER_SSC=y CONFIG_NFSD_V4_POSIX_ACLS=n 
CONFIG_NFS_V4_0=y CONFIG_NFS_V4_1_IMPLEMENTATION_ID_DOMAIN="kernel.org" 
CONFIG_NFS_V4_1_MIGRATION=n CONFIG_NFS_V4_2=y CONFIG_NFS_V4_2_READ_PLUS=n 
CONFIG_NFS_V4_2_SSC_HELPER=y CONFIG_NFS_V4_SECURITY_LABEL=y CONFIG_NTFS_DEBUG=y 
CONFIG_NTFS_FS_POSIX_ACL=y CONFIG_NUMA_MIGRATION=y CONFIG_NVMEM_QNAP_MCU_EEPROM=m 
CONFIG_NVME_TARGET_AUTH_DEBUG=n CONFIG_OPENSSL_SUPPORTS_ML_DSA=y CONFIG_PCI_IDE=y 
CONFIG_PCI_PWRCTRL=m CONFIG_PCI_PWRCTRL_GENERIC=m CONFIG_PCI_PWRCTRL_TC9563=m CONFIG_PCI_TSM=y 
CONFIG_PERF_GUEST_MEDIATED_PMU=y CONFIG_PHY_COMMON_PROPS=y CONFIG_PHY_GOOGLE_USB=m 
CONFIG_PHY_NXP_TJA1145=n CONFIG_PKCS7_WAIVE_AUTHATTRS_REJECTION_FOR_MLDSA=n 
CONFIG_PM_QOS_CPU_SYSTEM_WAKEUP=y CONFIG_PNFS_BLOCK=m CONFIG_PNFS_FILE_LAYOUT=m 
CONFIG_PNFS_FLEXFILE_LAYOUT=m CONFIG_POWER_RESET_QEMU_VIRT_CTRL=m CONFIG_PPPOX=m 
CONFIG_PREEMPT_RT=n CONFIG_PRINTK_EXECUTION_CTX=y CONFIG_RAID6_PQ_ARCH=y CONFIG_RAMDAX=m 
CONFIG_RCU_DYNTICKS_TORTURE=n CONFIG_REGULATOR_FP9931=m CONFIG_REGULATOR_RT8092=m 
CONFIG_REGULATOR_TPS65185=m CONFIG_RING_BUFFER_PERSISTENT_INJECT=n 
CONFIG_RSEQ_SLICE_EXTENSION=y CONFIG_RTW89_8852AU=m CONFIG_RTW89_8852CU=m CONFIG_RTW89_8922AU=n 
CONFIG_SCHED_ALT=n CONFIG_SCHED_CACHE=y CONFIG_SECURITY_SELINUX_AVC_HASH_BITS=9 
CONFIG_SENSORS_APS_379=m CONFIG_SENSORS_ARCTIC_FAN_CONTROLLER=n CONFIG_SENSORS_D1U74T=n 
CONFIG_SENSORS_E50SN12051=n CONFIG_SENSORS_EMC1812=n CONFIG_SENSORS_GPIO_FAN=m 
CONFIG_SENSORS_HAC300S=m CONFIG_SENSORS_LATTEPANDA_SIGMA_EC=m CONFIG_SENSORS_LTC4283=n 
CONFIG_SENSORS_LX1308=n CONFIG_SENSORS_MAX17616=m CONFIG_SENSORS_MAX20830=n 
CONFIG_SENSORS_MAX20860A=n CONFIG_SENSORS_MCP9982=m CONFIG_SENSORS_MP2925=m 
CONFIG_SENSORS_MP2985=n CONFIG_SENSORS_MP5926=m CONFIG_SENSORS_MP9945=m 
CONFIG_SENSORS_PROM21_XHCI=n CONFIG_SENSORS_STEF48H28=m CONFIG_SENSORS_TSC1641=m 
CONFIG_SENSORS_XDP720=m CONFIG_SENSORS_XDPE1A2G7B=m CONFIG_SENSORS_YOGAFAN=m 
CONFIG_SERIAL_8250_KEBA=m CONFIG_SMBDIRECT=m CONFIG_SMC_HS_CTRL_BPF=y CONFIG_SMI330=m 
CONFIG_SMI330_I2C=m CONFIG_SMI330_SPI=m CONFIG_SND_HDA_SCODEC_CS35L56_CAL_DEBUGFS=n 
CONFIG_SND_SOC_ACPI_AMD_SDCA_QUIRKS=y CONFIG_SND_SOC_AMD_ACP7X=n 
CONFIG_SND_SOC_CS35L56_CAL_DEBUGFS=n CONFIG_SND_SOC_CS35L56_CAL_PERFORM_CTRL=n 
CONFIG_SND_SOC_CS35L56_CAL_SET_CTRL=n CONFIG_SND_SOC_CS42XX8_SPI=n CONFIG_SND_SOC_CS530X_SPI=m 
CONFIG_SND_SOC_ES9356=m CONFIG_SND_SOC_INTEL_SOF_TI_COMMON=m CONFIG_SND_SOC_RT5575=m 
CONFIG_SND_SOC_RT5575_SPI=y CONFIG_SND_SOC_SDCA_CLASS=m CONFIG_SND_SOC_SDCA_CLASS_FUNCTION=m 
CONFIG_SND_SOC_SDCA_FDL=y CONFIG_SND_SOC_SOF_INTEL_NVL=m CONFIG_SND_SOC_SOF_NOVALAKE=m 
CONFIG_SND_SOC_TAC5XX2_SDW=n CONFIG_SND_SOC_TAS675X=n CONFIG_SND_SOC_UDA1380=m 
CONFIG_SPI_MICROCHIP_CORE_SPI=m CONFIG_STMMAC_LIBPCI=m CONFIG_SUNRPC_BACKCHANNEL=y 
CONFIG_SWITCHTEC_DMA=m CONFIG_TASKS_TRACE_RCU_NO_MB=y CONFIG_TCP_CONG_BBR3=m 
CONFIG_TDX_HOST_SERVICES=m CONFIG_TEST_WORKQUEUE=n CONFIG_TIME_NS_VDSO=y CONFIG_TI_ADS1018=m 
CONFIG_TI_ADS131M02=m CONFIG_TOUCHSCREEN_WACOM_W9000=n CONFIG_TRACE_REMOTE_TEST=n 
CONFIG_TRACE_SYSCALL_BUF_SIZE_DEFAULT=63 CONFIG_TRANSPARENT_HUGEPAGE_SHMEM_HUGE_ADVISE=y 
CONFIG_TRANSPARENT_HUGEPAGE_SHMEM_HUGE_ALWAYS=n CONFIG_TRANSPARENT_HUGEPAGE_SHMEM_HUGE_NEVER=n 
CONFIG_TRANSPARENT_HUGEPAGE_SHMEM_HUGE_WITHIN_SIZE=n 
CONFIG_TRANSPARENT_HUGEPAGE_TMPFS_HUGE_ADVISE=y CONFIG_TRANSPARENT_HUGEPAGE_TMPFS_HUGE_ALWAYS=n 
CONFIG_TRANSPARENT_HUGEPAGE_TMPFS_HUGE_NEVER=n 
CONFIG_TRANSPARENT_HUGEPAGE_TMPFS_HUGE_WITHIN_SIZE=n CONFIG_TRUSTED_KEYS_DEBUG=n CONFIG_TSM=y 
CONFIG_UIO_PCI_GENERIC_SVA=m CONFIG_UNIWILL_LAPTOP=m CONFIG_USB4_CONFIGFS=m 
CONFIG_USB4_STREAM=n CONFIG_USB_DWC3_GOOGLE=m CONFIG_USER_NS_UNPRIVILEGED=y CONFIG_VEML3328=n 
CONFIG_VFIO_PCI_DMABUF=y CONFIG_VHBA=m CONFIG_VIDEO_AMD_ISP4_CAPTURE=n CONFIG_VIDEO_HWS=n 
CONFIG_VIDEO_IMX111=m CONFIG_VIDEO_MAX96712=n CONFIG_VIDEO_OS05B10=m CONFIG_VIDEO_S5K3M5=m 
CONFIG_VIDEO_S5KJN1=m CONFIG_VIDEO_T4KA3=m CONFIG_VL53L1X_I2C=m 
CONFIG_X86_PLATFORM_DRIVERS_UNIWILL=y CONFIG_XE_VFIO_PCI=m CONFIG_XOR_BLOCKS_ARCH=y 
CONFIG_ZEN_INTERACTIVE=y 
```

</details>
