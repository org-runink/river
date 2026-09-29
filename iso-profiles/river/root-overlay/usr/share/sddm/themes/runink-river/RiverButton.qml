// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// A button on the palette. primary: sage with accent-ink text (9.3:1), the one action colour.
// Otherwise an outline button (ink on card, muted edge), or flat (sage text, no box). Space
// and Enter press it; the sage focus ring shows on keyboard focus.
import QtQuick
import QtQuick.Controls.Basic as QQC

QQC.Button {
    id: button
    property real s: 1
    property bool primary: false
    RiverPalette { id: pal }
    height: 40 * s
    implicitWidth: contentItem.implicitWidth + leftPadding + rightPadding
    leftPadding: 16 * s
    rightPadding: 16 * s
    font.pixelSize: 14 * s
    activeFocusOnTab: true
    Keys.onReturnPressed: clicked()
    Keys.onEnterPressed: clicked()
    contentItem: Text {
        text: button.text
        font: button.font
        color: button.primary ? pal.accentInk : button.flat ? pal.accent : button.enabled ? pal.ink : pal.ink2
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }
    background: Rectangle {
        radius: 8 * button.s
        color: button.primary ? pal.accent
             : button.flat ? "transparent"
             : (button.down ? pal.surface : pal.card)
        border.width: button.primary || button.flat ? 0 : 1
        border.color: button.hovered ? pal.line : pal.muted
        scale: button.down ? 0.97 : 1
        FocusRing { shown: button.visualFocus; scale_: button.s }
    }
}
