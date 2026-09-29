// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// Runink River — the dark-ground lockup (community mark + "Runink River" wordmark + the meaning
// of the name) with the mark ANIMATED: the raft, with the puppy aboard, bobs and rocks on the
// water, the puppy's head nods a little behind the raft's motion, and the wave lines drift, a
// slow loop of `period` ms. The same component in the SDDM greeter
// (usr/share/sddm/themes/runink-river) and the Plasma start-up splash (look-and-feel
// org.runink.river.desktop, contents/splash), so the hand-off from one to the other is
// continuous.
//
// SOURCE: branding/src/qml/RiverLockup.qml. branding/render.sh copies it next to the images
// it draws (images/mark-*.svg, images/wordmark-dark.svg: the layers of
// branding/logo/river-mark.svg, the M2 mascot, and the wordmark of
// branding/logo/river-lockup-dark.svg) and writes LockupGeometry.qml (where the mark sits in
// the lockup). At rest the layers, stacked in this order, are the mark exactly, and the lockup
// is branding/logo/river-lockup-dark.svg.
//
// The phase comes from the wall clock, not from when this item was created, so two processes
// drawing it (the greeter, then the splash) show the raft at the same point of its loop.
// `paused` eases the motion out to the rest position (and stops redrawing); clearing it eases
// the motion back in, so nothing ever jumps. QtQuick only: five Images and property-driven
// transforms, no shader, no video.
import QtQuick

Item {
    id: lockup

    // true: the raft bobs. false: the mark at rest (it eases there).
    property bool paused: false
    // One loop, in ms: the raft bobs gently on the water, slowly.
    property int period: 2800
    // >= 0 pins the loop to that phase (0..1) and stops following the clock: screenshots.
    property real phaseOverride: -1
    // Amplitudes in mark units (the mark is 512 units square; at the splash's 640 px lockup it
    // is about 160 px, so 3 units is about one pixel) and degrees. Kept small on purpose.
    property real bob: 5
    property real rock: 1.2
    property real nod: 2.2
    property real drift: 7

    readonly property alias geometry: geo
    implicitWidth: 640
    implicitHeight: width * geo.viewHeight / geo.viewWidth

    LockupGeometry { id: geo }

    // 0 at rest .. 1 in motion
    property real amplitude: paused ? 0 : 1
    Behavior on amplitude { NumberAnimation { duration: 900; easing.type: Easing.InOutSine } }

    property real phase: 0
    function tick() {
        lockup.phase = lockup.phaseOverride >= 0 ? lockup.phaseOverride
            : (Date.now() % lockup.period) / lockup.period
    }
    Component.onCompleted: tick()
    onPhaseOverrideChanged: tick()

    FrameAnimation {
        running: lockup.visible && lockup.phaseOverride < 0 && (lockup.amplitude > 0 || !lockup.paused)
        onTriggered: lockup.tick()
    }

    readonly property real angle: 2 * Math.PI * phase
    readonly property real k: width / geo.viewWidth           // lockup units -> px
    readonly property real markSize: 512 * geo.markScale * k
    readonly property real u: markSize / 512                  // mark units -> px
    readonly property real dpr: Screen.devicePixelRatio > 0 ? Screen.devicePixelRatio : 1

    Image {
        anchors.fill: parent
        source: "images/wordmark-dark.svg"
        sourceSize: Qt.size(Math.ceil(lockup.width * lockup.dpr), Math.ceil(lockup.height * lockup.dpr))
        smooth: true
        asynchronous: false
    }

    Item {
        id: mark
        x: geo.markX * lockup.k
        y: geo.markY * lockup.k
        width: lockup.markSize
        height: lockup.markSize

        component Layer: Image {
            anchors.fill: parent
            sourceSize: Qt.size(Math.ceil(mark.width * lockup.dpr), Math.ceil(mark.height * lockup.dpr))
            smooth: true
            asynchronous: false
        }

        // the raft with the puppy aboard: back logs and body, head, log ends and front paws
        Item {
            anchors.fill: parent
            transform: [
                Rotation {
                    origin.x: 256 * lockup.u
                    origin.y: 420 * lockup.u
                    angle: lockup.amplitude * lockup.rock * Math.sin(lockup.angle - 0.7)
                },
                // it rides a little lower than it rises, so the logs never lift clear of the water
                Translate { y: lockup.amplitude * lockup.bob * lockup.u * (0.35 - Math.sin(lockup.angle)) }
            ]

            Layer { source: "images/mark-back.svg" }
            Layer {
                source: "images/mark-head.svg"
                // the head nods about the neck, a beat behind the raft
                transform: Rotation {
                    origin.x: 250 * lockup.u
                    origin.y: 262 * lockup.u
                    angle: lockup.amplitude * lockup.nod * Math.sin(lockup.angle - 1.9)
                }
            }
            Layer { source: "images/mark-front.svg" }
        }
        Layer { source: "images/mark-water.svg" }
        Layer {
            source: "images/mark-waves.svg"
            transform: Translate {
                x: lockup.amplitude * lockup.drift * lockup.u * Math.sin(lockup.angle - 1.4)
                y: -lockup.amplitude * 0.8 * lockup.u * Math.sin(lockup.angle - 1.4)
            }
        }
    }
}
