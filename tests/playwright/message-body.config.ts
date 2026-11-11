import { defineConfig } from '@playwright/test'
import { resolve } from 'node:path'
export default defineConfig({
  testDir: '../message-body',
  testMatch: '*.spec.ts',
  workers: 1,
  outputDir: '../../frontend/node_modules/.message-body-results',
  use: { baseURL: 'http://127.0.0.1:5186', viewport: { width: 1000, height: 800 }, screenshot: 'only-on-failure' },
  webServer: {
    command: 'frontend/node_modules/.bin/vite --config frontend/message-body.vite.config.ts',
    cwd: resolve(__dirname, '../..'),
    url: 'http://127.0.0.1:5186/tests/message-body/',
    reuseExistingServer: false,
  },
})
