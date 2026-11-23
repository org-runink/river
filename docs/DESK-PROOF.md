<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# The desk proof

`build/qemu-gui-test.sh --desk-proof` shows that the installed image gives a user the DEsk
experience, not just a process called `desk`. The old `desk-login` check passed with a `desk`
process and a screendump with some pixel entropy, so a black or crash-looping desk passed.

## Run it

Build the ISO first (a heavy build; see [BUILD.md](BUILD.md)), then on a host with KVM and OVMF:

```sh
build/qemu-gui-test.sh --desk-proof ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
```

It installs through the graphical installer as before, then after the login runs the 30 rows of
`build/desk-checklist.json` (plus the hardware-only rows). Results are in
`<work>/artifacts/`: `results.json`, `results.tsv`, `rows.txt` and every frame as
`NNN-<row id>[-label].png`. Each row is a check `desk-NN-<id>` in the verdict: PASS, FAIL, or
SKIP for a row QEMU cannot prove (printed as NOT PROVABLE IN QEMU, with the reason).

## How it proves things

- Keys and the pointer go in through QMP (`send-key`, `input-send-event`); an absolute tablet,
  an HDA sound card (null backend), a second display head, a hot-plugged USB stick and a packet
  capture are added to the installed VM.
- The screen is judged by structure (`installer/guitest/screen.go`): luma entropy, colour count,
  the fraction of warm (wood, lamp light), paper-like, dark and red pixels in a region, and how
  much of a region changed against an earlier frame. None depends on exact pixels or resolution.
  A black frame, a flat frame or a black frame with a cursor fails the `lit` preset.
- Each row also asks the guest, through an agent on the guest's second serial port, for process
  and IPC evidence: `pgrep`, `wpctl`, a `notify-send` typed into foot, the same `desk` pid across
  lock, unlock and reset, files in `~/Pictures/Screenshots`.
- A row says what part QEMU cannot show (`unprovable`); the thresholds are first-run values.
  Tune them from the first real boot's frames, in the data file or in the presets.

`go test ./guitest` (in `installer/`) checks the helpers and that the checklist file is valid.
