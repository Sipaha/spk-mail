# spk-mail

A lightweight, free, telemetry-free email client for Linux.

Single static Go binary with an embedded React UI. Reads mail over IMAP, lives in the system tray, fires native notifications when new mail arrives. No accounts to sign up for, no analytics, no phone-home — your mail and credentials never leave your machine.

## Highlights

- **Free and open source.** Apache-2.0. Use it, fork it, ship it.
- **Zero telemetry.** No analytics SDKs, no crash reporters, no update pings. The binary talks only to your IMAP servers.
- **Lightweight.** Single Go binary (~25 MB) with the frontend embedded. No Electron, no per-account browser. Idle RAM is in the tens of megabytes.
- **Multi-account.** Several IMAP accounts grouped into colour-coded UI profiles; per-profile mute for notifications.
- **Real-time push.** IMAP IDLE on INBOX, periodic poll on Sent / Drafts / Archive / custom folders.
- **Offline-first.** SQLite cache of every message and thread, local FTS5 search, attachments downloaded eagerly to disk.
- **HTML mail, sane defaults.** Sanitised rendering, remote content blocked by default, dark-mode adaptation that preserves subpixel font AA (no `filter: invert`).
- **Tray + native notifications.** XDG/D-Bus toast for new INBOX mail, unread badge on the tray icon.
- **Encrypted credentials.** AES-256-GCM blob, master key in the OS keyring with a PBKDF2 password fallback.

## Status

Alpha. IMAP receive only — sending (SMTP / compose) is on the roadmap. Linux only for the first release; the architecture is portable but Windows/macOS builds aren't wired up yet.

## Privacy

spk-mail is a local-first program. It connects to:

1. The IMAP servers you configure.
2. Remote image hosts in HTML mail — **only after you click "Show remote content"** for a given message.

That's it. There is no other network activity. No data is collected, transmitted, or sold.

## Build

    make build           # frontend + Go binary at build/bin/spk-mail
    make build-desktop   # native Wails desktop binary (build/bin/spk-mail-desktop)

Requirements: Go 1.26, Node 20+, a C toolchain (CGO is enabled for SQLite).

## Run

Desktop:

    make run

Browser dev mode (UI as a localhost web app, useful for development and Playwright):

    make run-browser
    # then open http://localhost:5174

## Linux desktop integration

System tray uses the StatusNotifierItem / AppIndicator protocol.

- KDE Plasma / XFCE / Cinnamon / MATE — works out of the box.
- GNOME — install the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).
- Wayland-only sessions — depends on the compositor (KDE Wayland: fine; Sway: limited).

Notifications use `org.freedesktop.Notifications` (D-Bus) and work on all major DEs.

## Storage layout (XDG)

| Path | Content |
|---|---|
| `~/.config/spk-mail/config.yml` | Account host/port/user/email/colour, UI prefs |
| `~/.local/share/spk-mail/db.sqlite` | Messages, threads, attachments metadata, FTS5 index |
| `~/.local/share/spk-mail/attachments/...` | Downloaded attachment files |
| `~/.local/share/spk-mail/secrets.bin` | AES-256-GCM blob of per-account IMAP passwords |
| `~/.local/share/spk-mail/logs/spk-mail.log` | Rotating slog output |

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full text.

Required attribution notices for third-party Apache-2.0 dependencies are reproduced verbatim in [NOTICE](NOTICE). A complete list of linked Go modules and npm packages with their licenses is in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
