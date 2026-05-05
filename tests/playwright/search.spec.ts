import { test, expect } from '@playwright/test'

test('search by free text shows snippets', async ({ page }) => {
  await page.goto('/')
  // Search is reactive (store-bound onChange); typing alone produces
  // results — no Enter / form-submit step.
  await page.getByPlaceholder(/Search…/).fill('milestones')
  await expect(page.getByText(/results for/)).toBeVisible({ timeout: 5_000 })
  await expect(page.locator('mark').first()).toBeVisible()
})

test('from: operator narrows results', async ({ page }) => {
  await page.goto('/#/search?q=' + encodeURIComponent('from:Bob'))
  await expect(page.getByText(/Project update/)).toBeVisible({ timeout: 5_000 })
})
