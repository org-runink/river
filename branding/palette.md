# Runink River palette

Every colour in the Runink River artwork (GRUB theme, Plymouth theme, Plasma splash,
wallpapers) and in the graphical installer comes from the Runink palette below: dark-first,
high contrast, one accent. The previous warm palette (orange, cream, brown grounds) is
retired; nothing in the tree uses it.

The colour rule: **one accent, sage, for the places a person acts** (the selected GRUB entry
and its countdown, primary buttons, links, focus, the one lit strand in the river lines).
Everything else is the grey ramp. Semantic colours (danger, success, warning) stay separate
from the accent and are never used for decoration.

## Dark (the default everywhere)

| Token | Hex | Used for |
|---|---|---|
| ground | `#212121` | GRUB, Plymouth, SDDM, the Plasma splash, the dark wallpaper, the installer page |
| surface | `#2C2E30` | the bottom of the dark gradients; installer header, cards, dialogs |
| card | `#36383A` | river lines, the GRUB countdown track, installer inputs |
| ink | `#E4E2D8` | the wordmark, headlines, primary text |
| ink-2 | `#C6C7B5` | body text, the GRUB menu items, the meaning of the name and the tagline |
| line | `#B2B3A4` | the GRUB countdown label; installer secondary text and outline buttons |
| muted | `#717168` | river lines, control edges (3:1 on ground), never body text (3.3:1) |
| accent (sage) | `#C0CC7C` | THE action colour (above) |
| accent-ink | `#212121` | text on the accent (9.3:1) |

## Light (the light wallpaper only)

| Token | Hex | Used for |
|---|---|---|
| ground | `#F4F3EE` → `#EAE9E2` | the light wallpaper gradient |
| ink | `#212121` | the wordmark on light |
| ink-2 | `#3E3F3A` | the meaning of the name and the tagline on light |
| line | `#B2B3A4` | river lines |
| muted | `#6A6B62` | river lines |
| accent | `#6E7A2E` | the one strand (sage darkened for contrast on light) |

## Semantic (the installer)

| Token | Hex | Used for |
|---|---|---|
| danger | `#E06C5A` | "Erase and install" (text `#212121`, 4.9:1), the disks that will be erased, failures |
| danger text | `#F09A8A` | error messages (5.4:1 on card, 7.8:1 on ground) |
| success | `#8FC79A` | the done tick (with `#212121`) |
| warning | `#D8B25A` | notes and the reconnect banner (with `#212121`) |

Contrast in the installer: every text pair is at least 4.5:1, most above 7:1; control edges
are at least 3:1; focus is a 3 px sage outline with a 3 px offset, visible on every control
including the sage and red buttons.

## Type

Figtree 700 for the wordmark and Figtree for the meaning of the name (outlined to paths by
`render.sh` in outline mode, so no render depends on an installed font); Playfair Display
Italic for the tagline ("Data You Can Trust. Decisions You Can Defend."), used once, on the
wallpaper. Both are OFL-1.1. The installer runs offline and fetches no font: it asks for
Figtree first and falls back to the system sans.

The community mark itself keeps its own colours, not the palette above: the M2 mascot's
browns (`#8a4b2a`, `#b0683a`, `#5c2c17`), cream and white (`#fffaf0`, `#fdf5e6`), log tan
`#efc48f`, water aqua `#9ed8d2`, coral `#f4978e` and its dark outline `#1d2b3a`. It has no
badge or ground of its own: its outline and fills carry it on the dark ground and on the
light one alike, so it is always drawn straight onto the surface's ground.

## Boot animation

**Plymouth.** It is in Artix `[world]`. Its mkinitcpio hook works without systemd:
`install/plymouth` calls `add_systemd_unit` only when the systemd hook is present and
otherwise `add_runscript`, the busybox `run_hook` starts `plymouthd`, and `run_latehook`
hands it the new root. The one other file it needs, `71-seat.rules`, comes from `elogind`.
Its dependencies (cairo, pango, fontconfig, freetype2, libx11, libdrm, libevdev, libpng,
libxkbcommon, xkeyboard-config, glib2, adwaita-fonts) all come with Plasma already: resolved
with pactree, plymouth adds only itself to the closure. Under s6 nothing stops the splash
(there is no `plymouth-quit.service`, and SDDM 0.21 has no Plymouth integration), so the
profile ships an s6 oneshot `plymouth-quit` that `sddm-srv` depends on.

The theme (`plymouth/river/`) shows the community mark and the wordmark (`logo.png`, rendered from
`src/plymouth/logo.svg`) over three flowing currents. The Plasma start-up splash (the
look-and-feel package's `contents/splash/`) shows the same lockup, its mark animated, with a progress line in
the sage accent, so the boot flows from one into the other.

## Sign-in and session start (SDDM, Plasma splash)

The SDDM greeter theme `runink-river` and the Plasma start-up splash show the lockup with
its community mark ANIMATED: the raft, with the puppy aboard, bobs and rocks gently on the
water, the puppy's head nods a little a beat behind it, and the wave lines drift. The loop
is slow (2.8 s) and the motion is small, a pixel or two at the splash's size. Both surfaces
use one component, `src/qml/RiverLockup.qml`, and take the loop's phase from the wall
clock, so after a sign-in the greeter fades its form and
background to the ground, glides the lockup to where the splash draws it (width
min(36 % of the screen, 640 px), centred 4 % above the middle) and the splash picks up the
same picture. The motion eases to rest after 60 s without input, during a failed sign-in's
shake, and always with `motion=false` in the theme's `theme.conf` (SDDM exposes no
reduced-motion setting).

The mark is split into five layers, which `render.sh` generates from `logo/river-mark.svg`
and never draws by hand, in the mark's own drawing order: back (the raft's back logs and the
puppy's body), head (the puppy's head), front (the log ends and the front paws on them),
water (the water band) and waves (the wave lines and the two leaves on the water). back, head
and front ride the raft together; the water stays put. Stacked in order at rest they are the
mark: `render.sh` checks that the layers' elements are the mark's, byte for byte. It is five
Images and property animations in QtQuick, no shader and no video. The wordmark is
`logo/river-lockup-dark.svg` without its mark. The greeter's colours are the dark tokens
above: ink and ink-2 text
(at least 7:1), placeholders in line (5.5:1 on card), control edges in muted, a failed
sign-in in danger text, Caps Lock in warning, and sage only for the sign-in button, the
selected user and the 3 px focus ring. Its background is the dark wallpaper's river lines
without the lockup (`background.jpg`, 2560x1440).

## Files

Every visual surface of Runink River carries the **Runink River community mark**, the M2
mascot (a brown-and-white puppy grinning over the front of a three-log raft on the water):
the GRUB theme (live menu and installed), Plymouth, the Plasma splash, SDDM, the wallpaper,
the Kickoff button, the hicolor icon `runink-river` (os-release `LOGO=`, Info Center, the
installer's edition icon, so its header mark and favicon), the favicons, the terminal
greeting (fastfetch) and `/etc/issue`. On the dark grounds it stands beside a light wordmark
and the meaning of the name, "RIVER · Raft-Integrated Validated Event Runtime".

- `mascot/river-mascot.svg`: the M2 mascot, the ONE source of the mark. Every file below that
  draws the mark is generated from it by `render.sh`; edit it and re-run (and re-run the
  outline mode, which draws the mark into the dark lockup). `mascot/river-mascot.png` is its
  512 px render for people who need a bitmap.
- `logo/river-mark.svg`: the community mark, primary. GENERATED by `render.sh` from the
  mascot: its art, one element per line, on a 512-unit square that holds the whole drawing
  (the mascot file's own viewBox is a tighter bust crop that clips an ear tip and the
  water). No ground of its own. Use it at 32 px and up: the scalable `runink-river` icon,
  the 32-256 px icons, the SDDM and splash layers, the Kickoff button, the 32 and 48 px
  favicons, the terminal greeting.
- `logo/river-mark-small.svg`: the small mark, GENERATED from the same art cropped to the
  puppy's head (every element is still drawn; the crop only frames the face), for 24 px and
  below, where the whole puppy on its raft closes up: the 16-24 px icons, the 16 px favicon,
  `favicon.svg`.
- `logo/river-lockup.svg`: the mark + two-line "Runink River" wordmark (Jost SemiBold,
  outlined paths), in ink, for light grounds in documents. GENERATED by `render.sh` from
  `src/logo/river-lockup.svg`.
- `logo/river-lockup-dark.svg`: the mark + the light "Runink River" wordmark (Figtree 700,
  ink) + the meaning of the name (ink-2), for the dark grounds. GENERATED by `render.sh` in
  outline mode from `src/logo/river-lockup-dark.svg` (live text); the GRUB background, the
  Plymouth and Plasma splash and both wallpapers draw it (`RIVER-ART` placeholders; the light
  wallpaper recolours it to ink on light).
- `logo/runink-tagline.svg`: the tagline in Playfair Display Italic, outlined the same way
  from `src/logo/runink-tagline.svg`; the wallpapers draw it.
- `ascii/river-mark-small.txt`: the mark as plain 7-bit ASCII (the puppy's face and ears over
  its paws on the three logs, on the water), for `/etc/issue` and the text-console greeting (in
  white, the title block beside art lines 3-5).
- `icons/favicon/`: `favicon-{16,32,48}.png`, `favicon.ico` and `favicon.svg`, for web pages
  the image serves; `render.sh` does not ship them into an overlay. The installer takes its
  favicon and header mark from the running edition's descriptor (`icon`, served at
  `/edition-icon`), so a downstream edition shows its own mark.
- `src/`: the SVG sources for every render. `render.sh` renders them (the GRUB background as
  a baseline JPEG, the wallpaper as one 3840x2160 JPEG per variant) and copies the results
  into the profile overlay, and `river lint branding-sync` checks that the copies have
  not drifted, that the mark files are generated from the current mascot (their recorded
  `mascot-sha256`), that every surface names the community mark, that the retired palette is
  gone, and the size budgets.
- `src/qml/RiverLockup.qml`, `src/sddm/runink-river/`, `src/splash/Splash.qml`: the
  hand-written QML of the animated lockup, the SDDM greeter theme and the Plasma splash.
  `render.sh` assembles `sddm/runink-river/` and `splash/` from them, the mark layers
  (`images/mark-*.svg`), the wordmark (`images/wordmark-dark.svg`) and `LockupGeometry.qml`
  (where the mark sits in the lockup), and ships both into the overlay. The lint checks
  that the assembled QML equals its source, and that the greeter and splash import only
  QtQuick, QtQml and QtQuick.Controls.Basic. `river test sddm-theme` renders every greeter
  state and the splash offscreen, for review.
- Every mark and everything rendered from it is `LicenseRef-Runink-Trademark` (REUSE.toml),
  unlike the CC-BY artwork. A downstream distribution brings its own marks with its edition
  branding (`RIVER_BRANDING_DIR`, docs/BUILD.md).

All artwork is original to Runink and contains no third-party marks, except the CachyOS
Emerald wallpapers (`wallpapers/emerald/`, GPL-3.0, provenance beside them).
