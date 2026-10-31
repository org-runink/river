# Runink River palette

Every colour in the Runink River artwork (GRUB theme, Plymouth theme,
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
| ground | `#212121` | GRUB, Plymouth, the login screen, the dark wallpaper, the installer page |
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
libxkbcommon, xkeyboard-config, glib2, adwaita-fonts) are already in the closure: resolved
with pactree, plymouth adds only itself to the closure. Under s6 nothing stops the splash
(there is no `plymouth-quit.service`, and greetd has no Plymouth integration), so the
profile ships an s6 oneshot `plymouth-quit` that the `greetd` service depends on.

The theme (`plymouth/river/`) shows the community mark and the wordmark (`logo.png`, rendered from
`src/plymouth/logo.svg`) over three flowing currents.

## Sign-in (greetd)

The login screen is gtkgreet in cage (greetd), on the dark tokens above. It does not yet
carry the community mark or the animated lockup the SDDM theme had: the Runink themes for
DEsk and its greeter are the next branding step (docs/DESKTOP-DESK-PLAN.md, section 5).

## Files

Every visual surface of Runink River carries the **Runink River community mark**, the M2
mascot (a brown-and-white puppy grinning over the front of a three-log raft on the water):
the GRUB theme (live menu and installed), Plymouth, the wallpaper,
the hicolor icon `runink-river` (os-release `LOGO=`, the
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
  the 32-256 px icons, the 32 and 48 px
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
  Plymouth logo and both wallpapers draw it (`RIVER-ART` placeholders; the light
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
- Every mark and everything rendered from it is `LicenseRef-Runink-Trademark` (REUSE.toml),
  unlike the CC-BY artwork. A downstream distribution brings its own marks with its edition
  branding (`RIVER_BRANDING_DIR`, docs/BUILD.md).

All artwork is original to Runink and contains no third-party marks, except the CachyOS
Emerald wallpapers (`wallpapers/emerald/`, GPL-3.0, provenance beside them).
