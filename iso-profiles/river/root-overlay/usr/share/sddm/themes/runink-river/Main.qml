// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// Runink River — the SDDM greeter theme `runink-river` (SDDM 0.21, Qt 6, theme API 2.0).
//
// SDDM loads this file once per screen, with these context properties: sddm (login, power,
// hostName, loginSucceeded/loginFailed/informationMessage), userModel, sessionModel,
// keyboard (capsLock, layouts, currentLayout), config (theme.conf) and primaryScreen. The
// primary screen shows the sign-in form; every screen shows the background, the clock and
// the Runink River lockup with its animated community mark (RiverLockup.qml: the raft bobs
// gently on the water).
//
// On a successful sign-in the form fades out while the mark keeps bobbing and the lockup
// glides to exactly where the Plasma start-up splash draws it (the same component, phase
// taken from the wall clock), so the hand-off to the session is one continuous picture.
// The motion pauses (it eases to rest) while a failed sign-in shakes the form, after 60 s
// without input, and always when theme.conf says motion=false.
//
// QtQuick and QtQuick.Controls.Basic only (qt6-declarative, a dependency of sddm): no Plasma
// or Kirigami import, so the greeter cannot fail on a missing module.
// SOURCE: branding/src/sddm/runink-river/; branding/render.sh ships it.
import QtQuick
import QtQuick.Controls.Basic as QQC

Rectangle {
    id: root
    width: 1920
    height: 1080
    color: pal.ground

    RiverPalette { id: pal }

    // ── the greeter's environment (SDDM context properties, with safe fallbacks) ─────────
    property bool primary: typeof primaryScreen !== "undefined" ? primaryScreen : true
    readonly property bool hasConfig: typeof config !== "undefined" && config !== null
    readonly property bool motion: !hasConfig || String(config.motion) !== "false"
    readonly property bool showClock: !hasConfig || String(config.showClock) !== "false"
    readonly property string backgroundSource: hasConfig && config.background ? String(config.background) : ""
    // >= 0 pins the mark's phase (screenshots, river test sddm-theme); the greeter leaves it.
    property real markPhase: -1

    // One scale for the whole layout: 1 at 1600x1000, 0.75 at 1024x768 (never smaller), 2.16
    // on a 4K screen without HiDPI scaling.
    readonly property real s: Math.max(0.75, Math.min(3, Math.min(width / 1600, height / 1000)))

    // ── state ────────────────────────────────────────────────────────────────────────
    property bool busy: false          // a login request is out
    property bool handoff: false       // the login succeeded: fade the form, keep the mark
    property bool shaking: shake.running
    property bool idle: false
    property bool manualUser: false    // "Other user": type a user name
    readonly property int userCount: typeof userModel !== "undefined" && userModel ? userModel.count : 0
    property string message: ""
    property bool messageIsError: false

    function wake() {
        idle = false;
        idleTimer.restart();
    }

    Timer {
        id: idleTimer
        interval: 60000
        running: true
        onTriggered: root.idle = true
    }
    HoverHandler { onPointChanged: root.wake() }
    Connections {
        target: root.Window.window
        ignoreUnknownSignals: true
        function onActiveFocusItemChanged() { root.wake() }
    }

    function currentUserName() {
        if (!root.manualUser && root.userCount > 0 && userList.currentItem)
            return userList.currentItem.userName;
        return userField.text;
    }

    function login() {
        if (root.busy || root.handoff)
            return;
        const user = currentUserName();
        if (user === "") {
            root.message = qsTr("Type a user name.");
            root.messageIsError = true;
            userField.forceActiveFocus();
            return;
        }
        root.busy = true;
        root.message = "";
        sddm.login(user, passwordField.text, sessionBox.currentIndex);
    }

    Connections {
        target: typeof sddm !== "undefined" ? sddm : null
        ignoreUnknownSignals: true
        function onLoginSucceeded() {
            root.busy = false;
            root.message = "";
            root.handoff = true;
        }
        function onLoginFailed() {
            root.busy = false;
            passwordField.text = "";
            root.message = qsTr("Sign-in failed. Check the password and try again.");
            root.messageIsError = true;
            shake.restart();
            passwordField.forceActiveFocus();
        }
        function onInformationMessage(message) {
            root.message = message;
            root.messageIsError = false;
        }
    }

    // ── background ───────────────────────────────────────────────────────────────────
    Image {
        anchors.fill: parent
        source: root.backgroundSource !== "" ? root.backgroundSource : "background.jpg"
        fillMode: Image.PreserveAspectCrop
        asynchronous: true
        onStatusChanged: if (status === Image.Error && source != Qt.resolvedUrl("background.jpg")) source = "background.jpg"
        // after a sign-in: down to the plain ground, which is what the Plasma splash draws
        opacity: root.handoff ? 0 : 1
        Behavior on opacity { NumberAnimation { duration: 700; easing.type: Easing.InOutCubic } }
    }

    // ── the lockup, with the animated mark ──────────────────────────────────────────
    // Same size and, after a sign-in, same place as the Plasma splash draws it
    // (branding/src/splash/Splash.qml).
    readonly property real lockupWidth: Math.min(width * 0.36, 640)
    readonly property real splashCentreY: height / 2 - height * 0.04
    readonly property real gap: 40 * s
    readonly property real blockTop: Math.max(24 * s, (height - (lockup.height + gap + card.height)) / 2 - 16 * s)

    RiverLockup {
        id: lockup
        width: root.lockupWidth
        height: implicitHeight
        x: (root.width - width) / 2
        y: root.primary && !root.handoff ? root.blockTop : root.splashCentreY - height / 2
        Behavior on y {
            enabled: root.handoff
            NumberAnimation { duration: 700; easing.type: Easing.InOutCubic }
        }
        paused: !root.motion || root.idle || root.shaking
        phaseOverride: root.markPhase
    }

    // ── clock ────────────────────────────────────────────────────────────────────────
    Column {
        id: clock
        visible: root.showClock
        anchors.top: parent.top
        anchors.right: parent.right
        anchors.margins: 36 * root.s
        spacing: 2 * root.s
        opacity: root.handoff ? 0 : 1
        Behavior on opacity { NumberAnimation { duration: 400 } }
        property date now: new Date()
        Timer {
            interval: 1000
            running: clock.visible
            repeat: true
            triggeredOnStart: true
            onTriggered: clock.now = new Date()
        }
        Text {
            anchors.right: parent.right
            text: Qt.formatTime(clock.now, Qt.locale().timeFormat(Locale.ShortFormat))
            color: pal.ink
            font.pixelSize: 44 * root.s
            font.weight: Font.Light
        }
        Text {
            anchors.right: parent.right
            text: Qt.formatDate(clock.now, Qt.locale().dateFormat(Locale.LongFormat))
            color: pal.ink2
            font.pixelSize: 16 * root.s
        }
    }

    // ── the sign-in card (primary screen only) ──────────────────────────────────────
    Rectangle {
        id: card
        visible: root.primary
        enabled: !root.handoff
        width: 420 * root.s
        height: cardColumn.implicitHeight + 2 * pad
        x: (root.width - width) / 2
        y: root.blockTop + lockup.height + root.gap
        radius: 14 * root.s
        color: pal.surface
        border.color: pal.card
        border.width: 1
        opacity: root.handoff ? 0 : 1
        Behavior on opacity { NumberAnimation { duration: 450; easing.type: Easing.OutCubic } }
        readonly property real pad: 28 * root.s

        property real shakeX: 0
        transform: Translate { x: card.shakeX }
        SequentialAnimation {
            id: shake
            NumberAnimation { target: card; property: "shakeX"; to: -14 * root.s; duration: 60 }
            NumberAnimation { target: card; property: "shakeX"; to: 12 * root.s; duration: 80 }
            NumberAnimation { target: card; property: "shakeX"; to: -8 * root.s; duration: 70 }
            NumberAnimation { target: card; property: "shakeX"; to: 5 * root.s; duration: 60 }
            NumberAnimation { target: card; property: "shakeX"; to: 0; duration: 60 }
        }

        Column {
            id: cardColumn
            x: card.pad
            y: card.pad
            width: card.width - 2 * card.pad
            spacing: 16 * root.s

            Text {
                width: parent.width
                text: qsTr("Sign in to %1").arg(typeof sddm !== "undefined" && sddm.hostName ? sddm.hostName : "Runink River")
                color: pal.ink
                font.pixelSize: 20 * root.s
                font.weight: Font.DemiBold
                elide: Text.ElideRight
                horizontalAlignment: Text.AlignHCenter
            }

            // users: tiles when SDDM lists them, else (or for "Other user") a name field
            ListView {
                id: userList
                objectName: "userList"
                visible: !root.manualUser && root.userCount > 0
                anchors.horizontalCenter: parent.horizontalCenter
                width: Math.min(root.userCount, 4) * tileWidth
                height: visible ? tileWidth + 8 * root.s : 0
                readonly property real tileWidth: 112 * root.s
                orientation: ListView.Horizontal
                clip: true
                interactive: root.userCount > 4
                model: typeof userModel !== "undefined" ? userModel : null
                currentIndex: typeof userModel !== "undefined" && userModel.lastIndex >= 0 ? userModel.lastIndex : 0
                activeFocusOnTab: true
                keyNavigationEnabled: true
                Accessible.role: Accessible.List
                Accessible.name: qsTr("Users")
                Keys.onReturnPressed: passwordField.forceActiveFocus()
                Keys.onEnterPressed: passwordField.forceActiveFocus()
                delegate: Item {
                    id: tile
                    required property int index
                    required property string name
                    required property string realName
                    required property string icon
                    readonly property string userName: name
                    readonly property bool selected: ListView.isCurrentItem
                    width: userList.tileWidth
                    height: userList.height
                    Accessible.role: Accessible.ListItem
                    Accessible.name: realName !== "" ? realName : name

                    Rectangle {
                        id: avatar
                        anchors.horizontalCenter: parent.horizontalCenter
                        y: 6 * root.s
                        width: 56 * root.s
                        height: width
                        radius: width / 2
                        color: pal.card
                        border.width: tile.selected ? Math.max(2, 2 * root.s) : 1
                        border.color: tile.selected ? pal.accent : pal.muted
                        FocusRing { shown: tile.selected && userList.activeFocus; scale_: root.s }
                        Text {
                            anchors.centerIn: parent
                            visible: face.status !== Image.Ready
                            text: (tile.realName !== "" ? tile.realName : tile.name).charAt(0).toUpperCase()
                            color: pal.ink
                            font.pixelSize: 24 * root.s
                            font.weight: Font.DemiBold
                        }
                        Image {
                            id: face
                            anchors.fill: parent
                            anchors.margins: parent.border.width + 1
                            source: tile.icon !== "" ? "file://" + tile.icon.replace(/^file:\/\//, "") : ""
                            sourceSize: Qt.size(width * 2, height * 2)
                            fillMode: Image.PreserveAspectCrop
                            visible: status === Image.Ready
                            asynchronous: true
                        }
                    }
                    Text {
                        anchors.top: avatar.bottom
                        anchors.topMargin: 6 * root.s
                        anchors.horizontalCenter: parent.horizontalCenter
                        width: parent.width - 8 * root.s
                        horizontalAlignment: Text.AlignHCenter
                        elide: Text.ElideRight
                        text: tile.realName !== "" ? tile.realName : tile.name
                        color: tile.selected ? pal.ink : pal.ink2
                        font.pixelSize: 14 * root.s
                        font.weight: tile.selected ? Font.DemiBold : Font.Normal
                    }
                    TapHandler {
                        onTapped: {
                            userList.currentIndex = tile.index;
                            passwordField.forceActiveFocus();
                        }
                    }
                }
            }

            RiverField {
                id: userField
                objectName: "userField"
                visible: root.manualUser || root.userCount === 0
                width: parent.width
                s: root.s
                placeholderText: qsTr("User name")
                Accessible.name: qsTr("User name")
                text: typeof userModel !== "undefined" && userModel.lastUser ? userModel.lastUser : ""
                onAccepted: passwordField.forceActiveFocus()
                onTextChanged: root.wake()
            }

            Row {
                width: parent.width
                spacing: 14 * root.s
                RiverField {
                    id: passwordField
                    objectName: "passwordField"
                    width: parent.width - signIn.width - parent.spacing
                    s: root.s
                    echoMode: TextInput.Password
                    passwordCharacter: "•"
                    placeholderText: qsTr("Password")
                    Accessible.name: qsTr("Password")
                    enabled: !root.busy
                    onAccepted: root.login()
                    onTextChanged: {
                        root.wake();
                        if (text !== "" && root.messageIsError) root.message = "";
                    }
                }
                RiverButton {
                    id: signIn
                    s: root.s
                    primary: true
                    width: height * 1.25
                    text: "→"
                    font.pixelSize: 22 * root.s
                    Accessible.name: qsTr("Sign in")
                    enabled: !root.busy
                    onClicked: root.login()
                }
            }

            // messages: a failed sign-in (danger text), SDDM's own messages, caps lock (warning)
            Column {
                width: parent.width
                spacing: 4 * root.s
                Text {
                    width: parent.width
                    visible: root.message !== ""
                    text: root.message
                    color: root.messageIsError ? pal.dangerText : pal.ink2
                    font.pixelSize: 14 * root.s
                    wrapMode: Text.WordWrap
                    horizontalAlignment: Text.AlignHCenter
                    Accessible.role: Accessible.AlertMessage
                }
                Text {
                    width: parent.width
                    visible: typeof keyboard !== "undefined" && keyboard.capsLock
                    text: qsTr("Caps Lock is on")
                    color: pal.warning
                    font.pixelSize: 14 * root.s
                    font.weight: Font.DemiBold
                    horizontalAlignment: Text.AlignHCenter
                    Accessible.role: Accessible.AlertMessage
                }
            }

            RiverButton {
                anchors.horizontalCenter: parent.horizontalCenter
                visible: root.userCount > 0
                s: root.s
                flat: true
                text: root.manualUser ? qsTr("Choose a user") : qsTr("Other user…")
                onClicked: {
                    root.manualUser = !root.manualUser;
                    if (root.manualUser) {
                        userField.text = "";
                        userField.forceActiveFocus();
                    } else {
                        passwordField.forceActiveFocus();
                    }
                }
            }
        }
    }

    // ── bottom bar: session and keyboard layout (left), power (right) ───────────────
    Row {
        id: sessionRow
        visible: root.primary
        enabled: !root.handoff
        opacity: root.handoff ? 0 : 1
        Behavior on opacity { NumberAnimation { duration: 400 } }
        anchors.left: parent.left
        anchors.bottom: parent.bottom
        anchors.margins: 32 * root.s
        spacing: 12 * root.s

        QQC.ComboBox {
            id: sessionBox
            objectName: "sessionBox"
            height: 40 * root.s
            width: 260 * root.s
            model: typeof sessionModel !== "undefined" ? sessionModel : null
            textRole: "name"
            font.pixelSize: 14 * root.s
            Accessible.name: qsTr("Session")

            // Plasma (Wayland) unless another session was chosen before: SDDM's lastIndex is 0
            // both when nothing was remembered and when the first session was, so index 0 is
            // read as "nothing remembered" and the Plasma Wayland session wins there.
            Instantiator {
                id: sessions
                model: sessionBox.model
                delegate: QtObject {
                    required property int index
                    required property string file
                }
            }
            function preferred() {
                const last = typeof sessionModel !== "undefined" ? sessionModel.lastIndex : 0;
                if (last > 0 && last < count)
                    return last;
                for (let i = 0; i < sessions.count; ++i) {
                    const o = sessions.objectAt(i);
                    if (o && o.file.replace(/^.*\//, "") === "plasma.desktop")
                        return i;
                }
                return Math.max(0, last);
            }
            Component.onCompleted: currentIndex = preferred()
            onCountChanged: currentIndex = preferred()

            background: Rectangle {
                radius: 8 * root.s
                color: pal.card
                border.color: sessionBox.hovered ? pal.line : pal.muted
                border.width: 1
                FocusRing { shown: sessionBox.visualFocus || sessionBox.activeFocus && !sessionBox.pressed; scale_: root.s }
            }
            contentItem: Text {
                leftPadding: 14 * root.s
                rightPadding: 28 * root.s
                text: qsTr("Session: %1").arg(sessionBox.displayText)
                color: pal.ink
                font: sessionBox.font
                verticalAlignment: Text.AlignVCenter
                elide: Text.ElideRight
            }
            indicator: Text {
                x: sessionBox.width - width - 12 * root.s
                anchors.verticalCenter: parent.verticalCenter
                text: "▾"
                color: pal.ink2
                font.pixelSize: 14 * root.s
            }
            delegate: QQC.ItemDelegate {
                id: sessionItem
                required property int index
                required property string name
                width: sessionBox.width
                height: 38 * root.s
                highlighted: sessionBox.highlightedIndex === index
                contentItem: Text {
                    text: sessionItem.name
                    color: sessionItem.highlighted ? pal.accentInk : pal.ink
                    font: sessionBox.font
                    verticalAlignment: Text.AlignVCenter
                    elide: Text.ElideRight
                }
                background: Rectangle { color: sessionItem.highlighted ? pal.accent : "transparent"; radius: 6 * root.s }
            }
            popup: QQC.Popup {
                y: -implicitHeight - 6 * root.s
                width: sessionBox.width
                padding: 4 * root.s
                implicitHeight: contentItem.implicitHeight + 2 * padding
                contentItem: ListView {
                    clip: true
                    implicitHeight: contentHeight
                    model: sessionBox.popup.visible ? sessionBox.delegateModel : null
                    currentIndex: sessionBox.highlightedIndex
                }
                background: Rectangle { color: pal.surface; border.color: pal.muted; radius: 8 * root.s }
            }
        }

        // the keyboard layout: shows the current one; with several, a button that cycles them
        RiverButton {
            id: layoutButton
            s: root.s
            readonly property var layouts: typeof keyboard !== "undefined" && keyboard.layouts ? keyboard.layouts : []
            readonly property var current: layouts.length > 0 ? layouts[Math.max(0, keyboard.currentLayout)] : null
            visible: layouts.length > 0
            enabled: layouts.length > 1
            text: current ? qsTr("Keyboard: %1").arg(String(current.shortName).toUpperCase()) : ""
            Accessible.name: current ? qsTr("Keyboard layout: %1").arg(current.longName) : ""
            onClicked: keyboard.currentLayout = (keyboard.currentLayout + 1) % layouts.length
        }
    }

    Row {
        id: powerRow
        visible: root.primary
        enabled: !root.handoff
        opacity: root.handoff ? 0 : 1
        Behavior on opacity { NumberAnimation { duration: 400 } }
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.margins: 32 * root.s
        spacing: 12 * root.s
        readonly property bool haveSddm: typeof sddm !== "undefined"
        RiverButton {
            s: root.s
            visible: powerRow.haveSddm && sddm.canSuspend
            text: qsTr("Suspend")
            onClicked: sddm.suspend()
        }
        RiverButton {
            s: root.s
            visible: powerRow.haveSddm && sddm.canReboot
            objectName: "restartButton"
            text: qsTr("Restart")
            onClicked: sddm.reboot()
        }
        RiverButton {
            s: root.s
            visible: powerRow.haveSddm && sddm.canPowerOff
            text: qsTr("Shut down")
            onClicked: sddm.powerOff()
        }
    }

    Component.onCompleted: {
        if (!root.primary)
            return;
        if (root.userCount === 0 && userField.text === "")
            userField.forceActiveFocus();
        else
            passwordField.forceActiveFocus();
    }
}
