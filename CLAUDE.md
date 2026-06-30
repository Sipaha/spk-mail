# Claude Code project guide

When you work on UI changes in spk-mail, read **`docs/ui-testing.md` first** — it documents how to launch the app in browser mode against an in-process mock IMAP server, the seeded fixtures, and the `/api/_test/*` automation routes.

The end-to-end manual verification rule from the global CLAUDE.md applies here. For UI work, exercise the change in `--browser` mode (or via the Playwright suite under `tests/playwright/`) before reporting it as done.

## Architecture in one paragraph

Single Wails v3 process. `internal/sync` runs one `AccountWorker` goroutine per IMAP account; each worker uses IDLE on INBOX and periodic poll on other folders, and serializes its `syncFolder` calls through a per-account mutex (one folder fetched at a time per account). Parsed messages flow through a single `StoreWriter`, which delegates the thread+message+attachments+stats write to `storage.InsertParsedMessageBundle` (a single SQLite tx via `storage.WithTx`) so a mid-flight failure rolls back atomically — there are no orphan thread or message rows on retry. The store opens SQLite with `MaxOpenConns=1`; mixing `*sql.DB` and `*sql.Tx` writes inside one logical operation will deadlock, so always go through `WithTx`. The API layer (`internal/api` + `internal/api/transport`) reads from SQLite directly. The browser-mode HTTP server applies an Origin/Host CSRF guard via `transport.OriginGuard`; `_test` automation routes are gated behind `--test-api`. Frontend is React 19 + Zustand + Tailwind, embedded in the binary via `cmd/spk-mail/dist`.

## Things that bite

- **Wails v3 alpha bindings** are exposed under their full Go FQN. The frontend uses `Call.ByName('github.com/spk/spk-mail/internal/api/transport.API.<Method>', ...)` — there is no `window.wails` global on alpha.95.
- **`@wailsio/runtime`** package version must match the Wails Go module (`v3.0.0-alpha.95`).
- **`window.location.protocol === 'wails:'`** is the desktop-vs-browser discriminator.
- **Filter switches in `ThreadList`** clear `threads` in the Zustand store synchronously before refetching; otherwise the previous scope's threads briefly leak into the new view.
- **Attachment filenames** are synthesized (`att-<part>.<ext>`) when the email omits Content-Disposition filename and Content-Type name — see `internal/mime/parser.go::SynthFilename`.
