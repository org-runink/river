// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// The desk. The desktop is a surface with your picture standing on it in a frame,
// instead of the picture being the desktop.
//
// WHERE THE IDEA CAME FROM. The desk metaphor is taken, with thanks, from DEsk
// (https://github.com/fyzugx/DEsk, MIT) -- "A Wayland desktop for Linux in realistic
// skeuomorphism", whose README puts it as "Here the desktop is a desk." That project
// renders a whole room: a walnut desk, windows as sheets of paper with dog-eared corners,
// a drawer for a dock, a bookshelf of ring binders for the launcher, a post box for
// notifications, and a front door for the lock screen.
//
// NOTHING IS FORKED AND NO CODE IS TAKEN. This is a Plasma wallpaper plugin written here,
// in QML, and it borrows one idea rather than an implementation. Two reasons it could not
// be otherwise even if we wanted it to be: DEsk is a wlroots compositor that integrates
// with systemd, and invariant 1 of this image is s6, never systemd; and Runink River is
// KDE Plasma on Wayland (AGENTS.md §1), so replacing the compositor is a different product,
// not a wallpaper. Keeping it in-house also keeps the medium's closure unchanged -- this
// file adds no package and no dependency.
//
// What is borrowed is the metaphor and the restraint behind it: one surface, one light,
// objects that sit ON the desk rather than float over an image. What is ours is the
// framed photograph as the desktop's subject, the drawn-not-textured materials below, and
// the Runink palette the default wears.
//
// Everything here is DRAWN: the grain, the frame, the bevel, the lamp and the overlays
// are gradients and shapes, not textures. That keeps the whole wallpaper a few kilobytes
// of QML (the image in the frame is the only file), keeps it sharp at any resolution, and
// means a new material is a row in Styles.qml rather than a new asset to render and sync.
//
// Imports are QtQuick plus the one Plasma module a wallpaper plugin MUST have. Everything
// drawn below is still QtQuick only: no Effects module, nothing that is not already on the
// medium for the session itself.
//
// THE ROOT MUST BE WallpaperItem, NOT Rectangle. This file was written against the Plasma 5
// contract, where `wallpaper` was a global context property and any Item could be the root.
// Plasma 6 hands the configuration to a WallpaperItem root and provides no `wallpaper`
// global, so the previous version failed at load with
//
//   kf.plasma.quick: Root item of wallpaper "org.runink.river.desk" not a WallpaperItem
//                    instance, instead is QQuickRectangle_QML_127
//   main.qml:46: ReferenceError: wallpaper is not defined
//   main.qml:48,55,56,65,128,139: TypeError: Cannot read property 'deskB'/'deskA'/'grain'/
//                    'frame'/'mat' of undefined
//
// and every shape below it drew nothing: the desktop was a bare black rectangle. It had
// never rendered. `org.kde.image` on the same medium shows the shape to copy — QtQuick,
// org.kde.plasma.plasmoid, `WallpaperItem {`.
//
// WallpaperItem is not a Rectangle, so the desk colour moves to the surface child below.

import QtQuick
import org.kde.plasma.plasmoid

WallpaperItem {
    id: desk

    readonly property var styles: Styles {}
    readonly property var m: styles.get(desk.configuration.Style)
    // The frame's share of the shorter screen edge. Clamped so a stored value from a
    // future version, or a hand-edited config, cannot put the frame off the desk.
    readonly property real frameScale: Math.max(0.2, Math.min(0.9,
        (desk.configuration.FrameScale || 58) / 100))

    // ---- the desk surface -------------------------------------------------------
    // Two colours down the screen: the far edge is darker, as a lit surface is. This also
    // carries the base colour that `color:` held while the root was a Rectangle —
    // WallpaperItem has no such property.
    Rectangle {
        anchors.fill: parent
        color: desk.m.deskB
        gradient: Gradient {
            GradientStop { position: 0.0; color: desk.m.deskA }
            GradientStop { position: 1.0; color: desk.m.deskB }
        }
    }

    // ---- grain ------------------------------------------------------------------
    // Long, uneven strokes along the desk. Deterministic: the same desk every boot,
    // because a surface that reshuffles itself on login reads as a glitch.
    Canvas {
        anchors.fill: parent
        opacity: desk.m.grain
        onPaint: {
            const ctx = getContext("2d");
            ctx.clearRect(0, 0, width, height);
            ctx.lineWidth = 1;
            // A small LCG rather than Math.random(), so the grain is stable.
            let seed = 20261017;
            const rnd = function () {
                seed = (seed * 1103515245 + 12345) % 2147483648;
                return seed / 2147483648;
            };
            const lines = Math.max(40, Math.round(height / 9));
            for (let i = 0; i < lines; i++) {
                const y = rnd() * height;
                ctx.strokeStyle = (i % 3 === 0) ? "#FFFFFF" : "#000000";
                ctx.globalAlpha = 0.03 + rnd() * 0.05;
                ctx.beginPath();
                ctx.moveTo(0, y);
                // Three control points: grain wanders, it does not ripple evenly.
                ctx.bezierCurveTo(width * 0.33, y + (rnd() - 0.5) * 14,
                                  width * 0.66, y + (rnd() - 0.5) * 14,
                                  width, y + (rnd() - 0.5) * 8);
                ctx.stroke();
            }
        }
    }

    // ---- the desk lamp ----------------------------------------------------------
    // One soft pool from the top left. It is the only light in the room; the frame's
    // shadow below is cast away from it, so the two agree.
    Rectangle {
        anchors.fill: parent
        visible: desk.configuration.Lamp
        gradient: Gradient {
            orientation: Gradient.Vertical
            GradientStop { position: 0.0; color: Qt.rgba(1, 1, 1, 0.10) }
            GradientStop { position: 0.5; color: "transparent" }
            GradientStop { position: 1.0; color: Qt.rgba(0, 0, 0, 0.18) }
        }
    }

    // ---- the photo frame --------------------------------------------------------
    Item {
        id: frame
        anchors.centerIn: parent
        readonly property real shortEdge: Math.min(desk.width, desk.height)
        width: shortEdge * desk.frameScale * 1.34   // a landscape frame, 4:3 outer
        height: shortEdge * desk.frameScale

        // The shadow it casts on the desk: offset down-right, away from the lamp.
        Rectangle {
            anchors.fill: parent
            anchors.margins: -2
            x: 10
            y: 14
            radius: 3
            color: Qt.rgba(0, 0, 0, 0.45)
            visible: desk.configuration.Lamp
        }

        // The frame itself.
        Rectangle {
            anchors.fill: parent
            color: desk.m.frame
            radius: 3
            border.width: 2
            border.color: desk.m.frameEdge

            // The mat: the card between frame and picture. It is what makes a framed
            // photograph read as framed rather than as a picture with a border.
            Rectangle {
                id: mat
                anchors.fill: parent
                anchors.margins: Math.max(10, parent.width * 0.035)
                color: desk.m.mat

                Image {
                    id: photo
                    anchors.fill: parent
                    anchors.margins: Math.max(6, mat.width * 0.018)
                    source: desk.configuration.Image
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: true
                    // A picture that cannot be read leaves the mat empty rather than
                    // leaving a broken-image box on the desk.
                    visible: status === Image.Ready
                }

                // Glass: a single diagonal highlight. Enough to read as glass, not
                // enough to fight the picture underneath it.
                Rectangle {
                    anchors.fill: parent
                    rotation: -18
                    opacity: 0.07
                    gradient: Gradient {
                        GradientStop { position: 0.00; color: "transparent" }
                        GradientStop { position: 0.45; color: "#FFFFFF" }
                        GradientStop { position: 0.55; color: "#FFFFFF" }
                        GradientStop { position: 1.00; color: "transparent" }
                    }
                }
            }
        }

        // The neon styles light the frame rather than the room.
        Rectangle {
            anchors.fill: parent
            anchors.margins: -3
            radius: 5
            color: "transparent"
            border.width: 3
            border.color: desk.m.frameEdge
            opacity: desk.m.glow * 0.5
            visible: desk.m.glow > 0
        }
    }

    // ---- overlays ---------------------------------------------------------------
    // Drawn over the whole desk, after the frame, because a CRT's scanlines are in the
    // room's air and not under the glass.
    Canvas {
        anchors.fill: parent
        visible: desk.m.overlay === "scanlines"
        opacity: 0.16
        onPaint: {
            const ctx = getContext("2d");
            ctx.clearRect(0, 0, width, height);
            ctx.fillStyle = "#000000";
            for (let y = 0; y < height; y += 3) {
                ctx.fillRect(0, y, width, 1);
            }
        }
    }

    Rectangle {
        anchors.fill: parent
        visible: desk.m.overlay === "haze"
        opacity: 0.22
        gradient: Gradient {
            GradientStop { position: 0.0; color: "#FFFFFF" }
            GradientStop { position: 0.6; color: "transparent" }
            GradientStop { position: 1.0; color: "#FFD9F2" }
        }
    }

    Rectangle {
        anchors.fill: parent
        visible: desk.m.overlay === "neon"
        opacity: 0.12
        gradient: Gradient {
            GradientStop { position: 0.0; color: "#36E0FF" }
            GradientStop { position: 0.5; color: "transparent" }
            GradientStop { position: 1.0; color: "#FF3DCB" }
        }
    }
}
