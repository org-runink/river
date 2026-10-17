// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// The desk's materials. Every style is NUMBERS, not artwork: two surface colours and a
// grain amount, a frame and its edge, a mat, and a light. main.qml draws them. Nothing
// here is a bitmap, so a style costs bytes rather than megabytes and stays sharp on any
// display -- the same reason the rest of this image's artwork is SVG.
//
// `river` is the DEFAULT and the only one that is Runink River brand artwork: it is the
// palette in branding/palette.md and nothing else (ground #212121, surface #2C2E30,
// card #36383A, ink #E4E2D8, muted #717168, one sage accent #C0CC7C for the single lit
// edge). The rest are USER PERSONALISATION -- a person's own desk, chosen in the wallpaper
// settings -- and deliberately do not follow the brand rule, because a brand palette is
// what the product looks like, not what someone's desk has to look like. Nothing here is
// used for a Runink mark, a splash, the greeter or the installer, which stay on-palette.

import QtQuick

QtObject {
    // get returns the material for `name`, falling back to the brand desk.
    function get(name) {
        switch (name) {
        case "wood":
            // A plain walnut desk: warm, mid-dark, visible grain.
            return { deskA: "#5A3A22", deskB: "#3E2716", grain: 0.55,
                     frame: "#2E1B10", frameEdge: "#8A6A42", mat: "#E8E0CE",
                     lamp: "#FFE4B5", overlay: "none", glow: 0.0 };
        case "steampunk":
            // Brass and dark leather, lit like a workshop.
            return { deskA: "#3B2C20", deskB: "#241A12", grain: 0.40,
                     frame: "#8C6A3A", frameEdge: "#D9B068", mat: "#CBB68B",
                     lamp: "#FFD79A", overlay: "none", glow: 0.10 };
        case "cyberpunk":
            // Black acrylic under neon. The glow is the light, not a lamp.
            return { deskA: "#141420", deskB: "#0A0A12", grain: 0.12,
                     frame: "#1E1E2E", frameEdge: "#FF3DCB", mat: "#10101A",
                     lamp: "#36E0FF", overlay: "neon", glow: 0.85 };
        case "dreamcore":
            // Pale and hazy, like a room remembered rather than seen.
            return { deskA: "#CFC3E8", deskB: "#A9B7D9", grain: 0.10,
                     frame: "#F3E8F7", frameEdge: "#FFFFFF", mat: "#FFF6FB",
                     lamp: "#FFF2C9", overlay: "haze", glow: 0.35 };
        case "retro":
            // 80s/90s: a dark grid and a CRT's scanlines.
            return { deskA: "#1A1038", deskB: "#0C0820", grain: 0.08,
                     frame: "#2A1D52", frameEdge: "#36E0FF", mat: "#160F30",
                     lamp: "#FF49B0", overlay: "scanlines", glow: 0.55 };
        default:
            // river -- branding/palette.md, dark-first, one sage accent.
            return { deskA: "#2C2E30", deskB: "#212121", grain: 0.18,
                     frame: "#36383A", frameEdge: "#C0CC7C", mat: "#E4E2D8",
                     lamp: "#E4E2D8", overlay: "none", glow: 0.0 };
        }
    }

    // The styles offered in the settings page, in the order shown.
    readonly property var names: ["river", "wood", "steampunk", "cyberpunk", "dreamcore", "retro"]

    // What a person reads in the settings page. Kept here so the list and the labels
    // cannot drift apart.
    function label(name) {
        switch (name) {
        case "wood":      return "Walnut";
        case "steampunk": return "Brass and leather";
        case "cyberpunk": return "Neon";
        case "dreamcore": return "Dreamcore";
        case "retro":     return "Retro 80s/90s";
        default:          return "Runink River";
        }
    }
}
