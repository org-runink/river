// Runink River — the default Plasma layout for a NEW user.
//
// Identical to Breeze's own layout script (org.kde.breeze.desktop), plus one change: the
// application launcher (Kickoff) button shows the Runink River community mark, hicolor icon
// `runink-river` (branding/icons, shipped by branding/render.sh), which reads on the dark
// panel at every size. Plasma runs this once,
// when a user has no plasma-org.kde.plasma.desktop-appletsrc yet; existing users keep
// their panels. The rest of the package still falls back to Breeze.
loadTemplate("org.kde.plasma.desktop.defaultPanel")

// The desk (usr/share/plasma/wallpapers/org.runink.river.desk): the picture stands on a
// surface in a frame instead of being the surface. A new user gets the Runink River desk
// with the River wallpaper already in the frame; the material and the picture are theirs
// to change in the wallpaper settings, and org.kde.image is still installed for anyone who
// wants a plain picture back.
var desktopsArray = desktopsForActivity(currentActivity());
for (var j = 0; j < desktopsArray.length; j++) {
    desktopsArray[j].wallpaperPlugin = 'org.runink.river.desk';
}

var allPanels = panels();
for (var i = 0; i < allPanels.length; i++) {
    var launchers = allPanels[i].widgets("org.kde.plasma.kickoff");
    for (var k = 0; k < launchers.length; k++) {
        launchers[k].currentConfigGroup = ["General"];
        launchers[k].writeConfig("icon", "runink-river");
    }
}
