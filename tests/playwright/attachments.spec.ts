import { test, expect } from '@playwright/test'

test('attachment chip becomes enabled after download', async ({ page }) => {
  await page.goto('/')
  await page.getByText(/Weekly digest/i).click()
  // chip starts disabled (downloading); poll for enable
  const chip = page.getByRole('button', { name: /\.pdf/ }).first()
  await expect(chip).toBeVisible({ timeout: 10_000 })
  await expect(chip).toBeEnabled({ timeout: 15_000 })
})
