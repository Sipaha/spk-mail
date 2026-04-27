# spk-mail

Linux desktop email client (IMAP only) with system tray and notifications. Single Go + Wails binary with an embedded React UI.

See `docs/superpowers/specs/2026-04-27-spk-mail-design.md` for the full design.

## Build

    make build

## Run (browser mode for development)

    make run-browser

Then open http://localhost:5174.

## Tray support

spk-mail uses Linux system-tray protocols.

- KDE / XFCE / Cinnamon / MATE: works out of the box.
- GNOME: install the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).
- Wayland-only sessions: depends on the compositor.

Notifications use `org.freedesktop.Notifications` (D-Bus) and work on all major DEs.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full text.

Required attribution notices for third-party Apache-2.0 dependencies are reproduced in [NOTICE](NOTICE). A complete list of linked Go modules and their licenses is in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
