// Runink River — the Plasma start-up splash (ksplash), shown while the session starts.
//
// The Runink River lockup with its community mark ANIMATED (RiverLockup.qml: the raft bobs
// gently on the water) on the ground colour, with a thin progress line in the sage accent
// (branding/palette.md). It is the same component, at the same size and place, that the SDDM
// greeter (usr/share/sddm/themes/runink-river) glides its lockup to after a sign-in, and the
// raft's phase comes from the wall clock, so the greeter hands over to the session in one
// continuous picture. The boot splash (Plymouth, usr/share/plymouth/themes/river) shows the
// same lockup, still. No fade-in: its first frame is the greeter's last one.
//
// SOURCE: branding/src/splash/Splash.qml (hand-written); branding/render.sh ships it with
// RiverLockup.qml and its images. ksplash sets `stage` as the session comes up (1..6) and
// closes the window itself; nothing here can hold the login up. QtQuick only, no Plasma or
// Kirigami imports, so the splash cannot fail on a missing module.
import QtQuick

Rectangle {
    id: root
    color: "#212121"

    property int stage

    RiverLockup {
        id: logo
        width: Math.min(root.width * 0.36, 640)
        height: implicitHeight
        x: (root.width - width) / 2
        y: root.height / 2 - root.height * 0.04 - height / 2
    }

    Rectangle {
        id: track
        anchors.top: logo.bottom
        anchors.topMargin: root.height * 0.06
        anchors.horizontalCenter: parent.horizontalCenter
        width: logo.width * 0.6
        height: 3
        radius: 1.5
        color: "#36383A"

        Rectangle {
            width: track.width * Math.min(1, Math.max(0, root.stage) / 6)
            height: parent.height
            radius: parent.radius
            color: "#C0CC7C"
            Behavior on width { NumberAnimation { duration: 300 } }
        }
    }
}
