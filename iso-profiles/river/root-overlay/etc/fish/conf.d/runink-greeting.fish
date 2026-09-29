# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# Runink River: the greeting every interactive fish prints, the Runink River mark and this
# machine's summary (fastfetch, /etc/xdg/fastfetch/config.jsonc). Nothing leaves the machine.
#
# The first shell of a terminal plays the mark's arrival first: six frames, about 0.2 s, the
# raft settling on the water (/usr/share/runink/fastfetch/anim/, rendered by branding/render.sh).
# Its last frame is the logo fastfetch prints, and the cursor goes back to the frame's first
# row, so fastfetch draws over it with no jump.
#
#   RUNINK_GREETING=static   the summary without the animation
#   RUNINK_GREETING=off      no greeting at all
# (set -Ux RUNINK_GREETING static, or define your own fish_greeting, to change it for good.)

function fish_greeting
    set -q RUNINK_GREETING; and test "$RUNINK_GREETING" = off; and return
    command -q fastfetch; or return
    # The animation only where it can draw and cannot slow anything down: an interactive
    # terminal, the terminal's first shell (not a nested fish, not a script's), a real TERM.
    set -l frames /usr/share/runink/fastfetch/anim/*.ansi
    if test "$RUNINK_GREETING" != static; and isatty stdout; and test "$TERM" != dumb; and test "$TERM" != linux
        and test (count $frames) -gt 1; and test (string replace -r -- '^$' 1 "$SHLVL") -le 1
        set -l rows (count < $frames[1])
        printf '\e[?25l\n'
        for i in (seq (count $frames))
            test $i -gt 1; and printf '\e[%dA' $rows
            while read -l line
                printf '  %s\e[K\n' $line
            end < $frames[$i]
            test $i -lt (count $frames); and sleep 0.035
        end
        printf '\e[%dA\r\e[?25h' (math $rows + 1)
    end
    # The Linux console (a text VT) skips the modules that open the GPU: Display and GPU probe
    # /dev/dri, and on the live medium VT2's greeting ran them while the desktop compositor was
    # taking the display over, the suspected cause of a black first Ctrl+Alt+F2 (river#134).
    # A text console has no display to report anyway, and cannot draw the truecolor mark: it
    # shows the ASCII mark instead (the one /etc/issue shows).
    if test "$TERM" = linux
        fastfetch --logo /usr/share/runink/fastfetch/river-mark.txt --logo-type file --logo-color-1 white --structure Title:Separator:OS:Host:Kernel:InitSystem:Uptime:Packages:Shell:CPU:Memory:Swap:Disk:LocalIp:Locale:Break:Colors
    else
        fastfetch
    end
end
