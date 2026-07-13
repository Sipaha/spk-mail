import { test, expect, folderTree, waitForAppReady } from './helpers'

test('seeded fixture renders in inbox', async ({ page }) => {
  await page.goto('/')
  await waitForAppReady(page)
  // Virtual Unread row appears in the account's FolderTree (one per account).
  const tree = folderTree(page)
  await expect(tree.getByText('Unread')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText(/project update/i).first()).toBeVisible({ timeout: 15_000 })
  await page.getByText(/project update/i).first().click()
  // Thread detail pane shows the subject as a header
  await expect(page.locator('h2').getByText(/project update/i)).toBeVisible()
})