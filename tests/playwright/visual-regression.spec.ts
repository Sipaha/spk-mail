import { test, expect } from '@playwright/test'

test('clock-frozen screenshots of every page', async ({ page, request }) => {
  await request.post('/api/_test/clock', { data: { now: '2026-04-27T12:00:00Z' } })

  await page.goto('/')
  await expect(page.getByText(/Test Personal/)).toBeVisible({ timeout: 10_000 })
  await expect(page).toHaveScreenshot('inbox.png', { maxDiffPixelRatio: 0.05 })

  await page.getByText(/project update/i).first().click()
  await expect(page).toHaveScreenshot('thread.png', { maxDiffPixelRatio: 0.05 })

  await page.goto('/#/settings/accounts')
  await expect(page.getByText('Accounts').first()).toBeVisible({ timeout: 5_000 })
  await expect(page).toHaveScreenshot('settings.png', { maxDiffPixelRatio: 0.05 })

  await request.post('/api/_test/clock', { data: { reset: true } })
})
