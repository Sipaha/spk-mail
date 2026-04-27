# UI Testing Guide for Claude Code

This page is the entry point when you (Claude) are asked to verify, screenshot, or debug the spk-mail UI. The app exposes a localhost HTTP mode so the standard Playwright MCP works without any spk-mail-specific tooling.

## Quick start

    make build
    ./build/bin/spk-mail \
        --browser --port=5174 \
        --imap-mock \
        --seed=tests/fixtures/basic.yaml &
    # then drive: http://127.0.0.1:5174

When you're done:

    pkill -f 'spk-mail --browser'

## Available fixtures

| File | What it gives you |
|---|---|
| `tests/fixtures/basic.yaml` | One account with a few messages including a PDF attachment — minimal sanity check |
| `tests/fixtures/multi-account.yaml` | Two accounts with different colors — exercises unified inbox |
| `tests/fixtures/attachments.yaml` | Message with a PDF attachment — tests download UI |
| `tests/fixtures/html-tracking.yaml` | HTML email with a remote tracking pixel — tests blocked-image flow |

## Test API endpoints (only present in `--browser` builds)

| Endpoint | Body | Purpose |
|---|---|---|
| `POST /api/_test/seed` | a `Fixture` JSON (same shape as the YAML fixtures) | Add accounts + messages at runtime |
| `POST /api/_test/inject-message` | `{email, from, subject, body_text, folder?}` | Trigger the IDLE → notification flow |
| `POST /api/_test/clock` | `{now: "RFC3339"}` or `{reset: true}` | Freeze "now" for deterministic screenshots |
| `GET  /api/_test/db-dump` | — | JSON snapshot of accounts/folders/messages/threads |
| `GET  /api/_test/logs` | — | Recent slog entries (in-memory ring buffer) |

These routes return 404 if you build with `-tags desktop_only` (the production desktop binary built via `make build-desktop`).

## Recipe — verify the new-message notification flow

```bash
# 1. Start the app with mock IMAP and a seeded account
./build/bin/spk-mail --browser --port=5174 --imap-mock --seed=tests/fixtures/basic.yaml &

# 2. Wait for boot (Playwright will do this automatically via webServer config)

# 3. Inject a message via the test API
curl -s -X POST http://127.0.0.1:5174/api/_test/inject-message \
    -H 'Content-Type: application/json' \
    -d '{"email":"alice@example.com","from":"Bob <b@x>","subject":"Hello","body_text":"hi"}'

# 4. Open the browser via Playwright MCP and assert the message shows up
```

In Playwright MCP form:

- `browser_navigate http://127.0.0.1:5174`
- `browser_wait_for text="Hello"` (with a timeout — IDLE flow takes <1s)
- `browser_take_screenshot` if visual confirmation is needed

## Recipe — visual regression run

1. `POST /api/_test/clock { now: "2026-04-27T12:00:00Z" }` — freezes relative-time strings.
2. Navigate every interesting URL: `/`, `/#/settings/accounts`, `/#/search?q=update`.
3. `browser_take_screenshot` on each; pass to `toHaveScreenshot()` in tests.
4. `POST /api/_test/clock { reset: true }` to release the freeze.

The committed Playwright spec `tests/playwright/visual-regression.spec.ts` already does this end-to-end.

## Recipe — debug an event-handling bug

1. Open `/api/_test/logs` to see recent slog entries.
2. `browser_console_messages` to read frontend errors.
3. `browser_network_requests` to see which `/api/*` calls fired and their responses.
4. `GET /api/_test/db-dump` to confirm what made it into SQLite.

## Adding a new fixture

1. Drop the YAML under `tests/fixtures/`.
2. Reference it from a Playwright spec using `--seed=tests/fixtures/your.yaml` in `playwright.config.ts` or via `POST /api/_test/seed` at runtime.
3. Keep the file deterministic: explicit `date:` fields rather than relative ("now"); explicit colors and IDs; no random data.

## Caveats

- `--imap-mock` runs an in-process server; `Use TLS` should be `false` in any account configuration that points at it.
- The mock server resets when the binary restarts. To preserve state, persist via the on-disk DB (`XDG_DATA_HOME=…/data` set per run).
- Notifications (`MessageArrived` event) only fire for INBOX-role folders with `\Seen` flag NOT set.
- The Playwright suite uses `mkdtempSync` for `XDG_DATA_HOME` per test run (see `tests/playwright/playwright.config.ts`); local re-runs do NOT reuse the seeded DB.
