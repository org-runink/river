// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// The focus ring (branding/palette.md): a sage outline, 3 px (more on large screens), 3 px
// clear of the control it surrounds, so it shows on every control, the sage button included.
import QtQuick

Rectangle {
    id: ring
    property Item target: parent
    property bool shown: false
    property real scale_: 1
    readonly property real w: Math.max(3, Math.round(3 * scale_))
    anchors.fill: target
    anchors.margins: -2 * w
    radius: (target && target.radius !== undefined ? target.radius : 0) + 2 * w
    color: "transparent"
    border.width: w
    border.color: "#C0CC7C"
    visible: shown
}
