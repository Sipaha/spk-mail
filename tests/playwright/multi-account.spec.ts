import { test, expect, resetSeed, waitForAppReady } from './helpers'

test('unified inbox shows threads from both accounts', async ({ page, request }) => {
  await resetSeed(request, 'multi-account.yaml')
  await page.goto('/')
  await expect(page.getByText('Personal')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('Work')).toBeVisible()
  await expect(page.getByText(/project update/i).first()).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText(/Q2 planning/i).first()).toBeVisible({ timeout: 15_000 })
})