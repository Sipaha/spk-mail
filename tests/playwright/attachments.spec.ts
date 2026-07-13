import { test, expect } from './helpers'

test('attachment chip becomes enabled after download', async ({ page }) => {
  await page.goto('/')
  await page.getByText(/Weekly digest/i).click()
  // chip starts disabled (downloading); poll for enable. Tight regex
  // (`weekly.pdf`) — the broader /\.pdf/ would also match a hypothetical
  // future test fixture with multiple pdfs and pick the wrong button.
  const chip = page.getByRole('button', { name: /weekly\.pdf/i }).first()
  await expect(chip).toBeVisible({ timeout: 10_000 })
  await expect(chip).toBeEnabled({ timeout: 15_000 })
})
