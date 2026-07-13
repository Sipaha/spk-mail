import { test, expect, folderTree, waitForSseListThreads } from './helpers'

test('folder tree renders INBOX and Sent in sidebar', async ({ page }) => {
  await page.goto('/')
  const tree = folderTree(page)
  await expect(tree.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  await expect(tree.getByText('Sent')).toBeVisible({ timeout: 5_000 })
})

test('Unread view filters threads to unread only', async ({ page }) => {
  await page.goto('/')
  const tree = folderTree(page)
  await expect(tree.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  const refetch = waitForSseListThreads(page)
  await tree.getByText('Unread').click()
  await refetch
  // basic.yaml unread threads (case-insensitive subject match)
  await expect(
    page.getByRole('button', { name: /milestones|project update|weekly digest/i }).first(),
  ).toBeVisible({ timeout: 10_000 })
})

test('clicking a folder filters threads to that folder', async ({ page }) => {
  await page.goto('/')
  const tree = folderTree(page)
  await expect(tree.getByText('Sent')).toBeVisible({ timeout: 10_000 })
  await tree.getByText('Sent').click()
  // basic.yaml's Sent has the "Re: Q1 plans" message; expect it
  await expect(page.getByText(/Q1 plans/i)).toBeVisible({ timeout: 5_000 })
})