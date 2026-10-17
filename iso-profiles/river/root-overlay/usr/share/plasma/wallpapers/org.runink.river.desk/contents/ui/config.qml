// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// The desk's settings page: the picture in the frame, the desk's material, how big the
// frame stands and whether the lamp is on.
//
// Plasma binds one property per key in config/main.xml, named cfg_<Key>. Writing to them
// is what saves; there is no apply step to call.
//
// Imports stay to QtQuick, its Controls and Layouts, plus the file dialog -- all of which
// Plasma's own wallpaper settings pages already use, so this adds nothing to the image.

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import QtQuick.Dialogs

ColumnLayout {
    id: page

    property alias cfg_Image: picture.text
    property alias cfg_Style: styleBox.currentValue
    property alias cfg_FrameScale: size.value
    property alias cfg_Lamp: lamp.checked

    // Default values Plasma reads when the page offers "Defaults". They must match
    // config/main.xml or the button restores something the wallpaper never shipped with.
    property string cfg_ImageDefault: "/usr/share/wallpapers/River/contents/images_dark/3840x2160.jpg"
    property string cfg_StyleDefault: "river"
    property int cfg_FrameScaleDefault: 58
    property bool cfg_LampDefault: true

    readonly property var styles: Styles {}

    spacing: 12

    GridLayout {
        columns: 2
        columnSpacing: 10
        rowSpacing: 10
        Layout.fillWidth: true

        Label {
            text: "Picture"
            Layout.alignment: Qt.AlignRight | Qt.AlignVCenter
        }
        RowLayout {
            Layout.fillWidth: true
            TextField {
                id: picture
                Layout.fillWidth: true
                text: page.cfg_ImageDefault
            }
            Button {
                text: "Choose…"
                onClicked: chooser.open()
            }
        }

        Label {
            text: "Desk"
            Layout.alignment: Qt.AlignRight | Qt.AlignVCenter
        }
        RowLayout {
            spacing: 8
            ComboBox {
                id: styleBox
                model: page.styles.names
                // The list holds the stored value ("wood"); a person reads the label.
                displayText: page.styles.label(currentValue)
                delegate: ItemDelegate {
                    width: styleBox.width
                    contentItem: Text {
                        text: page.styles.label(modelData)
                        color: palette.text
                        verticalAlignment: Text.AlignVCenter
                    }
                    highlighted: styleBox.highlightedIndex === index
                }
            }
            // A swatch of the chosen desk, drawn the way the desk draws it, so the
            // choice is visible before it is applied.
            Rectangle {
                width: 54
                height: 30
                radius: 2
                border.width: 1
                border.color: page.styles.get(styleBox.currentValue).frameEdge
                gradient: Gradient {
                    GradientStop { position: 0.0; color: page.styles.get(styleBox.currentValue).deskA }
                    GradientStop { position: 1.0; color: page.styles.get(styleBox.currentValue).deskB }
                }
                Rectangle {
                    anchors.centerIn: parent
                    width: 20
                    height: 14
                    color: page.styles.get(styleBox.currentValue).mat
                }
            }
        }

        Label {
            text: "Frame size"
            Layout.alignment: Qt.AlignRight | Qt.AlignVCenter
        }
        RowLayout {
            Layout.fillWidth: true
            Slider {
                id: size
                Layout.fillWidth: true
                from: 20
                to: 90
                stepSize: 1
                value: page.cfg_FrameScaleDefault
            }
            Label {
                text: Math.round(size.value) + "%"
                // A fixed width so the row does not twitch as the number changes.
                Layout.minimumWidth: 44
            }
        }

        Label { text: "" }
        CheckBox {
            id: lamp
            text: "Desk lamp"
            checked: page.cfg_LampDefault
        }
    }

    Label {
        Layout.fillWidth: true
        wrapMode: Text.WordWrap
        opacity: 0.7
        text: "The picture stands on the desk in a frame. Runink River is the desk this " +
              "image ships with; the others are yours to choose."
    }

    Item { Layout.fillHeight: true }

    FileDialog {
        id: chooser
        title: "Choose the picture for the frame"
        nameFilters: ["Images (*.png *.jpg *.jpeg *.svg *.webp *.bmp)", "All files (*)"]
        onAccepted: {
            // selectedFile is a URL; the wallpaper reads a path.
            picture.text = chooser.selectedFile.toString().replace(/^file:\/\//, "");
        }
    }
}
