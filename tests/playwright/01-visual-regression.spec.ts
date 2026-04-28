import { test, expect } from '@playwright/test'

// Filename starts with `01-` so this spec runs before any DB-mutating spec
// (add-account / notification-flow / profiles / threading / unblock-remote).
// The Playwright config uses workers:1 + fullyParallel:false against a SHARED
// in-process backend with no per-test reset; running visual-regression last
// would assert against a DB those earlier specs already mutated, so the
// committed PNG baselines (recorded against a fresh basic.yaml seed) would
// drift on every CI run.

test('clock-frozen screenshots of every page', async ({ page, request }) => {
  await request.post('/api/_test/clock', { data: { now: '2026-04-27T12:00:00Z' } })

  try {
    await page.goto('/')
    await expect(page.getByText(/Test Personal/)).toBeVisible({ timeout: 10_000 })
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
