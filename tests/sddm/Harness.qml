// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// Screenshot harness for the Runink River SDDM theme and Plasma splash (river test sddm-theme).
// It stands in for the SDDM greeter: it provides the context the greeter gives a theme
// (sddm, userModel, sessionModel, keyboard, config), loads the theme's Main.qml (or the
// splash's Splash.qml) at a given size, drives one scenario and saves one PNG, then quits.
// Run under QT_QPA_PLATFORM=offscreen with qml6; arguments are key=value:
//   dir=<theme or splash dir> out=<png> w=<px> h=<px> scenario=<name> phase=<0..1>
// Scenarios: rest, focus, failed, handoff, tabfocus, secondary, users0, splash.
import QtQuick

Window {
    id: win

    function arg(k, d) {
        const a = Qt.application.arguments;
        for (let i = 0; i < a.length; ++i)
            if (a[i].startsWith(k + "="))
                return a[i].substring(k.length + 1);
        return d;
    }
    readonly property string dir: arg("dir", "")
    readonly property string out: arg("out", "shot.png")
    readonly property string scenario: arg("scenario", "rest")
    readonly property real phase: Number(arg("phase", "0.25"))
    width: Number(arg("w", "1920"))
    height: Number(arg("h", "1080"))
    visible: true
    color: "black"

    // ── the greeter's context, mocked ─────────────────────────────────────────────────
    QtObject {
        id: sddm
        property string hostName: "workstation"
        property bool canSuspend: true
        property bool canReboot: true
        property bool canPowerOff: true
        signal loginSucceeded()
        signal loginFailed()
        signal informationMessage(string message)
        function login(user, password, session) {
            console.log("harness: login", user, "session", session);
            if (win.scenario === "handoff") loginSucceeded(); else loginFailed();
        }
        function suspend() {}
        function reboot() {}
        function powerOff() {}
    }
    ListModel {
        id: userModel
        property int lastIndex: 0
        property string lastUser: "runink"
        ListElement { name: "runink"; realName: "Runink"; icon: ""; needsPassword: true }
        ListElement { name: "ada"; realName: "Ada Lovelace"; icon: ""; needsPassword: true }
        Component.onCompleted: if (win.scenario === "users0") clear()
    }
    ListModel {
        id: sessionModel
        property int lastIndex: 0
        ListElement { name: "Plasma (X11)"; file: "/usr/share/xsessions/plasmax11.desktop"; type: 1 }
        ListElement { name: "Plasma (Wayland)"; file: "/usr/share/wayland-sessions/plasma.desktop"; type: 2 }
    }
    QtObject {
        id: keyboard
        property bool capsLock: win.scenario === "failed"
        property int currentLayout: 0
        property var layouts: [ { shortName: "us", longName: "English (US)" }, { shortName: "de", longName: "German" } ]
    }
    QtObject {
        id: config
        property string background: ""
        property string motion: "true"
        property string showClock: "true"
    }

    function find(item, name) {
        if (!item) return null;
        if (item.objectName === name) return item;
        const kids = item.children || [];
        for (let i = 0; i < kids.length; ++i) {
            const f = find(kids[i], name);
            if (f) return f;
        }
        return null;
    }
    function findLockup(item) {
        if (!item) return null;
        if (item.phaseOverride !== undefined) return item;
        const kids = item.children || [];
        for (let i = 0; i < kids.length; ++i) {
            const f = findLockup(kids[i]);
            if (f) return f;
        }
        return null;
    }

    Loader {
        id: loader
        anchors.fill: parent
        Component.onCompleted: {
            if (win.scenario === "splash") {
                setSource(win.dir + "/Splash.qml", { stage: 3 });
            } else {
                setSource(win.dir + "/Main.qml", { primary: win.scenario !== "secondary", markPhase: win.phase });
            }
        }
        onStatusChanged: if (status === Loader.Error) { console.error("harness: cannot load"); Qt.exit(2) }
    }

    // run the scenario once everything has loaded, then save the frame
    Timer {
        interval: 400
        running: loader.status === Loader.Ready
        onTriggered: {
            const t = loader.item;
            const lock = win.findLockup(t);
            if (win.scenario === "splash" && lock) lock.phaseOverride = win.phase;
            const pw = win.find(t, "passwordField");
            if (win.scenario === "focus") {
                pw.forceActiveFocus();
                pw.text = "correct horse";
            } else if (win.scenario === "failed") {
                pw.text = "wrong";
                t.login();
            } else if (win.scenario === "handoff") {
                pw.text = "right";
                t.login();
            } else if (win.scenario === "tabfocus") {
                win.find(t, "restartButton").forceActiveFocus(Qt.TabFocusReason);
            }
            saveTimer.start();
        }
    }
    Timer {
        id: saveTimer
        // after the shake (400 ms) and the hand-off glide (700 ms)
        interval: win.scenario === "failed" || win.scenario === "handoff" ? 1400 : 300
        onTriggered: win.contentItem.grabToImage(function (r) {
            if (!r.saveToFile(win.out)) { console.error("harness: cannot write", win.out); Qt.exit(3) }
            console.log("harness: wrote", win.out);
            Qt.quit();
        })
    }
}
