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
    make release         # production desktop binary (build/bin/spk-mail-release; DevTools off)

Requirements: Go 1.26.5, Node 20+, a C toolchain (CGO is enabled for SQLite). For the Wails desktop binary (`make build-desktop`, `make run`) you also need `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` (see `.github/ci.yml.disabled`).

## Run

Desktop:

    make run

Browser dev mode (UI as a localhost web app, useful for development against a real account):

    make run-browser
    # then open http://localhost:5174

The Playwright suite at `tests/playwright/` launches its own copy of the binary with `--imap-mock --seed=...` flags; it does not consume `make run-browser`.

When the OS keyring is unavailable (headless CI, a locked login keyring), browser mode falls back to a password-derived master key: set **`SPK_MAIL_PASSWORD`** and a `master.salt` file is created next to `secrets.bin`. Without it, and with no keyring, startup fails with a clear message rather than storing credentials unprotected.

## Testing

    make test        # everything: Go, frontend, Playwright
    make test-go     # go test -race ./...
    make test-front  # vitest (frontend/)
    make test-e2e    # builds the binary, then the Playwright suite

`make test-e2e` builds the browser-mode binary and drives it against an in-process mock IMAP server — see `docs/ui-testing.md` for the seeded fixtures and the `/api/_test/*` automation routes. The Go suite includes end-to-end smoke tests under `tests/e2e/` that shell out to that binary; they skip if it hasn't been built, so run `make build` first (or `make test`, which does) to exercise them.

CI mirrors these three jobs. The workflow is currently parked at `.github/ci.yml.disabled`; `git mv` it back under `.github/workflows/` to re-enable.

## Linux desktop integration

System tray uses the StatusNotifierItem / AppIndicator protocol.

- KDE Plasma / XFCE / Cinnamon / MATE — works out of the box.
- GNOME — install the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).
- Wayland-only sessions — depends on the compositor (KDE Wayland: fine; Sway: limited).

Notifications use `org.freedesktop.Notifications` (D-Bus) and work on all major DEs.

## Storage layout

All on-disk state lives under `~/.spk/spk-mail/` so it sits alongside other SPK products (editor, cockpit). `XDG_DATA_HOME`, when set, overrides the root to `$XDG_DATA_HOME/spk-mail/` — used by the e2e and Playwright suites for test isolation.

| Path | Content |
|---|---|
| `~/.spk/spk-mail/db.sqlite` | Messages, threads, attachments metadata, FTS5 index |
| `~/.spk/spk-mail/secrets.bin` | AES-256-GCM blob of per-account IMAP passwords |
| `~/.spk/spk-mail/blobs/...` | Content-addressed attachment blob store |
| `~/.spk/spk-mail/attachments/...` | Legacy per-message attachment tree (pre-v7 installs only; superseded by the blob store) |

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full text.

Required attribution notices for third-party Apache-2.0 dependencies are reproduced verbatim in [NOTICE](NOTICE). A complete list of linked Go modules and npm packages with their licenses is in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
