# Release notes

## 20261114-132942 (3afa6c4)

### Release Notes

This update adds a new release notes document to the repository.

**Added**
*   `docs/release-notes.md`: New file containing release notes for version `20261113-143338`, detailing the introduction of a Wayland-based desktop environment ("DEsk"), improvements to live ISO boot reliability, and updates to dependencies and CI actions.

## 20261113-143338 (91fb454)

## Release Notes

This update introduces a new Wayland-based desktop environment ("DEsk") with a branded greeter, improves live ISO boot reliability, and updates dependencies and CI actions.

### Added
*   **Desktop Environment**: Introduced "DEsk" (built with muon), featuring a Wayland-based greeter, keyring integration at login, automount support, battery status indicators, and Bluetooth/printer management.
*   **Live ISO Features**: Added hardware self-reporting to screen and USB stick, kiosk seat management via `seatd-launch`, and Wi-Fi state reporting.
*   **Documentation**: Added guides for writing the installation stick from Linux, macOS, and Windows.

### Changed
*   **Display Manager**: Refactored to use a generic display manager name instead of hardcoding SDDM; the greeter now runs on Wayland.
*   **Packages**: Updated to wlroots 0.18.3 and included the new

