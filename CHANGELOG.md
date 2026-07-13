# Changelog

All notable changes to spk-mail are documented here. The project is in **alpha** — APIs, storage layout, and UI may change between releases.

## Unreleased

Alpha: IMAP receive only; Linux desktop + browser dev mode for UI automation.

### Security

- Browser mode now requires a per-run bearer token on every `/api/*` route (injected into `index.html` as a `<meta>` tag; `index.html` is served `no-store` so a cached page can't hold a stale token). The `?token=` fallback exists only for `EventSource`, and on the `_test` routes it is accepted for GET/HEAD only — state-mutating test routes require the header. Token comparison is constant-time.
- A `crypto/rand` failure now panics instead of degrading the auth token to a predictable constant.

### Fixed

- Mail arriving in the DONE→FETCH window of an IDLE session is no longer lost. The IMAP client records a push it could not deliver and replays it on the next `Idle()`, so the worker re-syncs (INBOX has no polling backstop, so such a message previously stayed invisible until the 25-minute session bounce).
- Messages queued at shutdown are drained on a fresh context instead of the cancelled run context — they were being dropped roughly half the time. An empty queue no longer delays shutdown by the full 5s grace period.
- Deleting an account or a folder now sweeps orphan thread rows in the same transaction; the thread list no longer shows ghost rows after an account is removed.
- `MarkRead` sends one bulk IMAP STORE per (account, folder) instead of one per message, and flag-op submission takes a context — marking a large thread read no longer stalls the request for minutes during an IMAP outage.
- SSE-driven refreshes preserve pages loaded via "Load more" instead of collapsing the list back to the first page.
- A stale response from a previous filter/profile scope can no longer overwrite the current thread list.
- Accounts and profiles are fetched on every route, so a cold load on `#/settings` no longer shows an empty account list.
- Raw-message blobs are refcount-decremented on folder/account deletion, so the blob sweeper can reclaim them.

### Changed

- Event emission is non-blocking again: a wedged SSE subscriber gets its events dropped (with a warning) rather than throttling the shared write pipeline.
- The sync engine supervises account workers with tiered backoff and restarts them after a panic.