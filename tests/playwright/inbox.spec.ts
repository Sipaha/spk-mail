import { test, expect } from '@playwright/test'

test('seeded fixture renders in inbox', async ({ page }) => {
  await page.goto('/')
  // Virtual Unread row appears in the account's FolderTree (one per account).
  // Scope to the <nav aria-label="Folders"> landmark so the assertion
  // doesn't pick up "Unread" from a future ThreadRow snippet.
  const tree = page.getByRole('navigation', { name: 'Folders' }).first()
  await expect(tree.getByText('Unread')).toBeVisible({ timeout: 5_000 })
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('Project update')).toBeVisible({ timeout: 15_000 })
  await page.getByText('Project update').click()
  // Thread detail pane shows the subject as a header
  await expect(page.locator('h2').getByText('Project update')).toBeVisible()
})
