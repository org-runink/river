// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// A text field on the palette: ink on card, a muted edge (3:1), placeholder in line (5.5:1
// on card), and the sage focus ring whenever it has focus.
import QtQuick
import QtQuick.Controls.Basic as QQC

QQC.TextField {
    id: field
    property real s: 1
    RiverPalette { id: pal }
    height: 44 * s
    font.pixelSize: 16 * s
    color: pal.ink
    placeholderTextColor: pal.line
    selectionColor: pal.accent
    selectedTextColor: pal.accentInk
    leftPadding: 14 * s
    rightPadding: 14 * s
    verticalAlignment: TextInput.AlignVCenter
    activeFocusOnTab: true
    background: Rectangle {
        radius: 8 * field.s
        color: pal.card
        border.width: 1
        border.color: field.hovered ? pal.line : pal.muted
        FocusRing { shown: field.activeFocus; scale_: field.s }
    }
}
