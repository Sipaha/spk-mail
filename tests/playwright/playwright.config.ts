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
  use: { baseURL: 'http://127.0.0.1:5174' },
  webServer: {
    command: '../../build/bin/spk-mail --browser --port=5174 --imap-mock --seed=../fixtures/basic.yaml',
    url: 'http://127.0.0.1:5174/',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
    env: {
      XDG_DATA_HOME: dataHome,
    },
  },
})
