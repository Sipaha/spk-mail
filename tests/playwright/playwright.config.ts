import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Each test run gets an isolated XDG_DATA_HOME so the seeded fixture
// is applied to a fresh DB (no UNIQUE-constraint collisions on rerun
// and no stale mock-IMAP port references).
const dataHome = mkdtempSync(join(tmpdir(), 'spk-mail-pw-'))

export default defineConfig({
  testDir: '.',
  // Single worker: every spec mutates shared backend state (mock IMAP server,
  // SQLite DB, frozen clock); parallel runs would race on screenshot
  // assertions and bleed data between tests.
  workers: 1,
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  use: { baseURL: 'http://127.0.0.1:5174' },
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
