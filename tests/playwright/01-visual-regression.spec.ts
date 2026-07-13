import { test, expect, waitForAppReady } from './helpers'

// Filename starts with `01-` so this spec runs before any DB-mutating spec.
// helpers.ts skips the per-test /api/_test/reset for this file so screenshots
// hit the webServer's fresh basic.yaml seed; later specs reset before each test.

test('clock-frozen screenshots of every page', async ({ page, request }) => {
  await request.post('/api/_test/clock', { data: { now: '2026-04-27T12:00:00Z' } })

  try {
    await page.goto('/')
    await waitForAppReady(page)
    await expect(page).toHaveScreenshot('inbox.png', { maxDiffPixelRatio: 0.05 })

    await page.getByText(/project update/i).first().click()
    await expect(page).toHaveScreenshot('thread.png', { maxDiffPixelRatio: 0.05 })

    await page.goto('/#/settings/accounts')
    await expect(page.getByText('Accounts').first()).toBeVisible({ timeout: 5_000 })
    await expect(page).toHaveScreenshot('settings.png', { maxDiffPixelRatio: 0.05 })
  } finally {
    await request.post('/api/_test/clock', { data: { reset: true } })
  }
})
