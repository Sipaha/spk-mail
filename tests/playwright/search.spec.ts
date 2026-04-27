import { test, expect } from '@playwright/test'

test('search by free text shows snippets', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder(/Search…/).fill('milestones')
  await page.keyboard.press('Enter')
  await expect(page.getByText(/results for/)).toBeVisible({ timeout: 5_000 })
  await expect(page.locator('mark').first()).toBeVisible()
})

test('from: operator narrows results', async ({ page }) => {
  await page.goto('/#/search?q=' + encodeURIComponent('from:Bob'))
  await expect(page.getByText(/Project update/)).toBeVisible({ timeout: 5_000 })
})
