import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Each test run gets an isolated XDG_DATA_HOME so the seeded fixture
// is applied to a fresh DB (no UNIQUE-constraint collisions on rerun
// and no stale mock-IMAP port references).
const dataHome = mkdtempSync(join(tmpdir(), 'spk-mail-pw-'))

// Per-test DB isolation: helpers.ts exports a custom `test` fixture with an
// auto `_seedReset` hook that POSTs /api/_test/reset (re-applies basic.yaml)
// before every spec except 01-visual-regression.spec.ts. Import { test, expect }
// from './helpers' in new specs — not from '@playwright/test' directly.

export default defineConfig({
  testDir: '.',
  // Single worker: every spec mutates shared backend state (mock IMAP server,
  // SQLite DB, frozen clock); parallel runs would race on screenshot
  // assertions and bleed data between tests.
  workers: 1,
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  // Diagnostic artefacts — capture only when something interesting happens
  // so a green run doesn't bloat CI storage. trace 'on-first-retry' grabs
  // the playwright trace when a flake retries, which is when we most need
  // it; screenshot/video only on failure. retain-on-failure keeps the
  // video file only when the test failed (not for passing retries).
  use: {
    baseURL: 'http://127.0.0.1:5174',
    // OriginGuard (internal/api/transport/http.go) rejects state-changing
    // requests without Origin/Referer. Playwright's `request` fixture does
    // not attach Origin by default, so every request.post('/api/...') gates
    // 403 unless we set one here. Same-origin value matches baseURL so the
    // CSRF check accepts it.
    extraHTTPHeaders: {
      Origin: 'http://127.0.0.1:5174',
    },
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  webServer: {
    command: '../../build/bin/spk-mail --browser --port=5174 --imap-mock --test-api --seed=../fixtures/basic.yaml',
    url: 'http://127.0.0.1:5174/',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
    env: {
      XDG_DATA_HOME: dataHome,
    },
  },
})
