<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: CC-BY-4.0
-->

# Air-gapped installs and encrypted payloads

A server image built from this repository installs and boots with **no network at all**.
This page covers what makes that true, and the encrypted payloads a *private* install medium
may carry next to the live image. It is the contract for **downstream server distributions**
(docs/BUILD.md, "Downstream distributions"): Runink River itself is a workstation and its
image (`runink-river-<date>-x86_64.iso`) carries no payload. The packaging, the installer
steps and the first-boot runner (`river-firstboot-hooks`, on Runink River too, where it has
no hook to run) all stay in this repository.

## Public and private images

| Image | Contents | Built with |
|---|---|---|
| `<iso-name>-<date>-x86_64.iso` (**public**) | The OS, and the k0s airgap bundle. No models, no container images, no applications. | `build/local-iso.sh` (the default: `RIVER_PUBLIC=1` when there is no downstream payload) |
| `<iso-name>-<variant>-<date>-x86_64.iso` (**private**) | The same OS, a downstream platform's packages, and encrypted payloads: `/river-models` and any number of downstream payloads `/river-<kind>/<group>/`. | `RIVER_PUBLIC=0` (implied by `RIVER_PAYLOAD_DIR`), `RIVER_ISO_VARIANT=<variant>`, `RIVER_PAYLOAD_STAGE=<dir>` |

The build **refuses** to attach any payload to a public image: `local-iso.sh` rejects
`MODEL_PAYLOAD=yes`, `RIVER_PAYLOAD_STAGE` and `RIVER_ISO_VARIANT` under `RIVER_PUBLIC=1`,
`iso-root-stage.sh` refuses a public stage file that names a payload, and it checks the
finished public ISO carries no `/river-*` payload. The same rule applies to every public
artifact (cloud images included): public means no payload of any kind.

## The k0s airgap bundle

k0s is one static binary (`runink-k0s`), but it runs its system components as pods, and a
fresh node would pull their images from `quay.io` and `registry.k8s.io`. The
`runink-k0s-airgap` package carries them:

- `build/k0s-airgap.sh` runs `k0s airgap list-images` for the pinned k0s and the server
  profile's `k0s.yaml` (the reference `build/k0s/k0s.yaml` when the profile has none) (the calico images k0s 1.31 lists whatever the provider are dropped:
  the provider is `kuberouter`). `build/k0s-images.lock` pins each image by the manifest
  digest pulled (linux/amd64) and by its config digest (the image ID). The list and the lock
  must agree, or the build stops (`build/k0s-airgap.sh --update-lock` resolves new digests
  for review). `river lint k0s-pin` checks the package and lock are for the pinned k0s.
- Each image is pulled rootless by digest, its ID compared with the lock, and all of them are
  saved into ONE docker-archive (shared layers once): about 400 MB, 105 MB compressed in the
  package. `river-payloadpack imagecheck` re-reads the archive: every reference present with
  its pinned config digest, no extra image, every layer matching the config's `diff_ids`.
- The package installs it at `/var/lib/k0s/images/k0s-airgap-bundle-<k0s>-amd64.tar`. k0s
  imports every archive in that directory into containerd (namespace `k8s.io`) **before it
  starts kubelet**, and pins each image (`io.cri-containerd.pinned=pinned`) so image GC never
  removes it. `k0s.yaml` sets `spec.images.default_pull_policy: IfNotPresent`, so kubelet
  never pulls them. (Not `Never`: a node that later upgrades k0s online must still pull the
  new version's images.) Keep the archive in place: k0s unpins images whose archive is gone.
- Licences: the images are unmodified upstream images (Apache-2.0 software; the OS packages
  inside carry their own licences, e.g. GPL-2.0 iptables in kube-router). The package ships
  a `NOTICE` with each image, digest, licence and source.

`runink-k0s-airgap` belongs in a server profile's `Packages-Root` only; Runink River does not
install it.

## Payload format

Two formats share one construction (`installer/internal/modelpack`, standard library only):

| | Model payload | Downstream payload |
|---|---|---|
| MANIFEST header | `river-modelpack 1` | `river-payloadpack 1` |
| Lock | `models.lock` in the image | `LOCK`, next to the MANIFEST (below) |
| Parts | `models.rmp.NNN` | `payload.rmp.NNN` |
| Tool | `river-modelpack` | `river-payloadpack` (handles both) |

Pipeline and key, for both: `tar` (lock order, fixed metadata) → `zstd -12` → AES-256-GCM in
4 MiB chunks (STREAM construction: 7-byte random nonce prefix, 32-bit counter, last-chunk
flag) → parts below 2 GiB; the key is PBKDF2-HMAC-SHA256 (2^20 iterations, random 16-byte salt)
of the medium passphrase. The format name is part of every chunk's associated data, so a
payload of one kind never opens as the other. Details and threat model:
[MODEL-PAYLOAD.md](MODEL-PAYLOAD.md).

A downstream payload's `LOCK` (plaintext; its sha256 is the MANIFEST's `lock-sha256`):

```
river-payload-lock 1
kind <kind>
group <group>
file <size> <sha256> <dest>
...
```

`kind` and `group` are lower-case words; Runink River gives them no meaning beyond the medium
layout `/river-<kind>/<group>/`. `river-payloadpack lock --kind K --group G --dir D` writes one.

### One passphrase per medium

The models and every downstream payload are sealed with the **same** passphrase (generated,
64 hex characters, `~/.cache/river-build/secrets/models-passphrase`, 0600, never on the medium,
the repository or the node). They cross the same trust boundary: whoever holds the stick and
the passphrase is the installer of that node, so a second secret would be one more thing to
lose without protecting anything the first does not. Each payload still has its own salt and
nonce prefix.

## What the installer does with them

- `72-models-payload` unpacks `/river-models` into `<pool>/models` (optional: a blank answer,
  or `runink-autoinstall` without `--models-passphrase-file`, defers the models;
  `--skip-models` skips the step). In required mode (`RUNINK_MODELS_REQUIRED=1`, or an edition
  descriptor's `"models_required": true`) each of those FAILS the install instead, as does a boot
  medium without a model payload ([MODEL-PAYLOAD.md](MODEL-PAYLOAD.md), "Installing from it").
- `73-downstream-payloads` **does not open** the downstream payloads and deploys nothing. It
  creates `<pool>/payloads` (encryption inherited, compression off), mounted at
  `/var/lib/runink/payloads`, and runs

  ```sh
  river-payloadpack stage --src <medium root> --dest /var/lib/runink/payloads
  ```

  which copies every `river-<kind>/<group>/` (not `river-models`) **still encrypted**, hashing
  each part against its MANIFEST while copying and checking each LOCK against the MANIFEST's
  `lock-sha256`, refuses any other file, writes files 0600 in 0700 directories, and writes
  `INDEX` (`river-payload-index 1`, then `payload <kind> <group> <lock-sha256> <files>
  <plaintext bytes> <ciphertext bytes>`). Any failure destroys the dataset.
  `runink-autoinstall --payloads-src DIR` takes them from another directory; `--skip-payloads`
  skips the step. The same `stage` command works outside the installer (for example over SSH
  on a target), given a source directory and an empty destination.
- In a **cloud image** (`runink-autoinstall --cloud`, [CLOUD-IMAGES.md](CLOUD-IMAGES.md)) there is no
  encryption root at build time, so step 73 copies the downstream payloads (ciphertext) into
  the image's `/var/lib/runink/payloads` as a plain directory, and `river-cloud-init` moves
  `/var/lib/runink` into the instance's encrypted node-state dataset at first boot. A public
  cloud image carries none (and no models).
- `river-perms` keeps `/var/lib/runink` (payloads included) shadow-grade on every boot.

## First-boot hand-off

On every boot `rc.local` starts `river-firstboot-hooks` in the background after k0s. It waits
(up to 10 minutes) for the k0s API, then runs each executable in
`/usr/local/lib/runink/firstboot.d/` in lexical order, as root, until it has succeeded once
(exit 0; exit 75 defers it to the next boot, see
[below](#when-there-is-no-screen-exit-75-defers); sentinel `/var/lib/runink/firstboot.d/<name>.done`; log `/var/log/runink/firstboot.d/<name>.log`).
It needs no enrollment file. Each hook gets `RIVER_PAYLOADS_DIR=/var/lib/runink/payloads`,
`RIVER_K0S_READY=1|0`, `KUBECONFIG=/var/lib/k0s/pki/admin.conf` and `k0s` on `PATH`, with stdin
from `/dev/null`. A scripted install may pass the downstream setup answers:
`runink-autoinstall --setup-answers FILE` (step `76-setup-answers`) copies the file to
`/var/lib/runink/firstboot.d/setup-answers` (root, 0600), the hooks get its path as
`RIVER_SETUP_ANSWERS`, and it is shredded once every hook has succeeded (or at once when the
image has no hook). Runink River never reads it. A
downstream ships hooks as `core-tree/firstboot.d/*` (packaged by `runink-core`); a hook must not
need the network and must return promptly. What the downstream then decrypts, loads or deploys
is its own business: an image archive it places in `/var/lib/k0s/images/` is imported and pinned
by k0s exactly like the airgap bundle (write it elsewhere on the same filesystem first and
rename it in: k0s imports whatever appears in that directory).

## First-boot setup pages (the graphical hand-off)

A server installed with the graphical installer runs a **graphical first boot** on its local
display ([INSTALL.md](INSTALL.md#installing-with-the-graphical-installer)): network, clock,
firewall and k0s are checked on screen, and then the downstream's own setup appears as the
next step, in the same full-screen window, without Runink River knowing what it is. A hook
does that by **registering a setup page**: a local web page with a title. This is contract
version 1.

### Who runs what

| Process | Started by | User | Notes |
|---|---|---|---|
| `river-firstboot-hooks` | `/etc/s6/rc.local` (the s6-rc `rc-local` oneshot), in the background, every boot | root | init context (the PID-1 tree), never a login session, so elogind never reaps it or anything it starts |
| each hook in `/usr/local/lib/runink/firstboot.d/` | `river-firstboot-hooks`, one at a time, lexical order | root | stdin `/dev/null`; stdout and stderr to `/var/log/runink/firstboot.d/<hook>.log` (0600); `umask 077` |
| the page's server | the hook | root, then whatever it drops to | detached so it outlives the hook (`setsid -f`, or the downstream's own s6-rc longrun started with `s6-rc -u change <service>`); it stays in init context |
| the first-boot UI (`river-installer firstboot`) | `/etc/s6/rc.local`, only while a graphical first boot is pending | root | serves the first-boot screens on `http://[::1]:47660/` only |
| the kiosk (a full-screen web view on the local display) | the first-boot UI | the unprivileged `river-kiosk` user | exists only until the first boot completes, then removed with its packages |

A hook still **returns promptly**: it starts its server, registers the page and exits 0. It
never waits for the operator.

### Registering a page

Before it runs a hook on a node with a graphical first boot pending and a display on this boot,
`river-firstboot-hooks` creates an empty directory for it and passes its path in the environment:

```
RIVER_PAGE_DIR=/run/runink/firstboot-pages/<hook>    root:root 0700 (parent 0711), on tmpfs
```

The hook (or its server, once it listens) writes `$RIVER_PAGE_DIR/setup.json`, **mode 0600**,
atomically (write `setup.json.tmp` in the same directory, then rename it), **before the hook
exits 0**:

```json
{
  "version": 1,
  "title": "Set up the platform",
  "url": "http://[::1]:8420/setup?token=9f1c...64-hex...e2",
  "order": 50
}
```

| Field | Type | Rule |
|---|---|---|
| `version` | integer | `1`. A page with a higher version is not shown (the log says why). |
| `title` | string | 1 to 60 characters, plain text (no markup). The step's name on screen. |
| `url` | string | `http://` on a loopback literal, `[::1]` or `127.0.0.1`, any port, at most 2048 characters, no user info and no fragment. |
| `order` | integer | 0 to 999. Pages are shown in ascending order; equal orders by hook name. |

Unknown fields are ignored, so later versions can add optional ones. The equivalent JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Runink River first-boot setup page, version 1",
  "type": "object",
  "required": ["version", "title", "url", "order"],
  "properties": {
    "version": { "const": 1 },
    "title": { "type": "string", "minLength": 1, "maxLength": 60 },
    "url": { "type": "string", "maxLength": 2048,
             "pattern": "^http://(\\[::1\\]|127\\.0\\.0\\.1)(:[0-9]{1,5})?(/[^#]*)?$" },
    "order": { "type": "integer", "minimum": 0, "maximum": 999 }
  }
}
```

A registration that fails any rule, or whose file is not 0600 and owned by root or by the
owner of `$RIVER_PAGE_DIR`, or whose directory is group- or world-accessible, is refused:
the log names the hook and the rule, never the content.

### When there is no screen: exit 75 defers

`RIVER_PAGE_DIR` is set only when the graphical installer asked for a graphical first boot
(`/var/lib/runink/firstboot-ui` exists) **and** this boot has a display (`/dev/dri/card0` or
`/dev/fb0`). A node installed unattended (`runink-autoinstall`) never gets one; a server
installed graphically but booted once without its monitor gets none on that boot.

A hook's exit status says what happened:

| Exit | Outcome | Runink River then |
|---|---|---|
| `0` and `$RIVER_PAGE_DIR/setup.json` exists | **registered** | shows the page; records `<hook>.done` only when the page writes `done` |
| `0` and no `setup.json` | **done** | records `<hook>.done` at once and never runs the hook again |
| `75` (`EX_TEMPFAIL`) | **deferred**: not done, not a failure | records nothing and runs the hook again at the next boot, headless boots included |
| anything else | **failed** | logs it, records nothing, shows a plain error with **Retry** on the graphical first boot (Retry runs the pending hooks again), and runs it again at the next boot |

Each outcome is also written, for the first-boot UI, to `/run/runink/firstboot-status/<hook>`
(`running 0`, `registered 0`, `done 0`, `deferred <rc>`, `failed <rc>`). `river test firstboot-hooks`
(Tier 1) pins all four.

So a hook without `RIVER_PAGE_DIR` either finishes headless (from `RIVER_SETUP_ANSWERS`, or
its defaults) and exits 0, or exits 75 to wait for a boot that can show its page. An
unattended install never waits for a screen unless its own hook chooses to.

### Completion: a `done` marker

When the operator has finished, the page (its server) creates the regular file
`$RIVER_PAGE_DIR/done` (any content, 0600). Within two seconds Runink River then:

1. records the hook as done, `/var/lib/runink/firstboot.d/<hook>.done` (the same sentinel a
   hook without a page gets when it exits 0), so the hook never runs again and the page is
   never re-registered or re-shown;
2. deletes `$RIVER_PAGE_DIR`, and with it the registration and its token;
3. moves the screen to the next page, or to "Your Runink River server is ready".

A marker was chosen over a status URL on purpose: it still counts when the page's server
exits right after the last screen, which is the usual way a wizard ends. If the node reboots
before `done` exists, `/run` is gone and the hook has no sentinel, so it runs again at the next
boot and registers a fresh token. A hook that registers a page must therefore be idempotent,
and if it can tell its own setup already finished, it exits 0 without registering.

If the page's server drops privileges, it (or the hook, as root, before starting it) must
first `chown` `$RIVER_PAGE_DIR` to that user, so the unprivileged server can still write
`done`. The directory is on tmpfs and `river-perms` does not touch `/run`, so that ownership
lasts for the boot.

### Token handling

The URL usually carries a one-time secret, for example a 256-bit token the page exchanges for
a cookie. Runink River keeps it out of reach of everyone but root and the kiosk:

- It exists in `setup.json` (0600, in a 0700 directory under `/run`, a tmpfs, so it never
  reaches a disk) and in the page server's memory. Nothing of Runink River copies it to a
  file, a log, a command line (argv) or an environment variable.
- The first-boot UI (root) reads the file and hands the URL to the kiosk only in the body of
  its loopback API response (`GET /api/firstboot/pages`). That API answers only connections
  whose client socket belongs to root or to `river-kiosk`: it looks the peer up in
  `/proc/net/tcp6` and `/proc/net/tcp`, the way identd does, so another local user cannot
  read the URL. Its own log says only `page <hook> registered: <title>`.
- The kiosk opens the URL in a frame of the first-boot UI (same site: both are `[::1]`, only
  the port differs). The web view runs in private mode, with no history, cache or cookie jar
  on disk, and it is deleted when the first boot completes.

What the page must do:

- bind only `[::1]` or `127.0.0.1`;
- accept its token once, exchange it for an `HttpOnly`, `SameSite=Strict` cookie, and answer
  with a `303` redirect to a URL without the token;
- send `Referrer-Policy: no-referrer`;
- allow the first-boot UI to frame it: `Content-Security-Policy: frame-ancestors
  http://[::1]:47660`, and no `X-Frame-Options: DENY`;
- write `$RIVER_PAGE_DIR/done` when it is finished, and not before.

The kiosk draws no mouse pointer of its own (WPE WebKit on KMS/DRM, see `river-kiosk`); the
first-boot UI draws one, and a frame's events never reach it. So a page that expects the mouse
should load the same pointer, `<script src="http://[::1]:47660/kiosk-pointer.js"></script>`
(allow it in its `script-src`). It draws only when the frame's URL carries `#kiosk`, which the UI
adds in the kiosk, and does nothing in a desktop browser.

### Example hook

```sh
#!/bin/sh
# /usr/local/lib/runink/firstboot.d/50-example (root; stdin is /dev/null)
set -eu
[ -e /var/lib/example/setup-complete ] && exit 0           # already set up: no page
# No screen on this boot: finish headless from the answers, or `exit 75` to wait for one.
[ -n "${RIVER_PAGE_DIR:-}" ] || exec example-setup --answers "${RIVER_SETUP_ANSWERS:-}"
token="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
( umask 077; printf '%s\n' "$token" > /run/example-token )   # the server reads, then deletes it
setsid -f example-setup --listen '[::1]:8420' --token-file /run/example-token \
	--done-file "$RIVER_PAGE_DIR/done" </dev/null >/dev/null 2>&1
umask 077
printf '{"version":1,"title":"Set up the example","url":"http://[::1]:8420/setup?token=%s","order":50}\n' \
	"$token" > "$RIVER_PAGE_DIR/setup.json.tmp"
mv "$RIVER_PAGE_DIR/setup.json.tmp" "$RIVER_PAGE_DIR/setup.json"
```

With no hook, or no hook that registers a page, the first boot ends at "Your Runink River
server is ready", which shows the node's addresses and the SSH host key fingerprint.

## Checking an air-gapped install

`build/qemu-test.sh --lab --offline` gives the VM a NIC with an address and no route out, and
asserts on the installed node: no name resolves and no outside address is reachable; the k0s
node is Ready; every image of `k0s-images.lock` is in containerd, imported and pinned; every
pod is Running or Succeeded with none waiting on an image; kubelet recorded no pull. Add
`--expect-payloads` for a private medium (the payloads land encrypted, root-only, ciphertext
only, every one verified against its INDEX line; the first-boot hooks succeeded) or `--public`
for a public one (no payload on the medium or the node).
