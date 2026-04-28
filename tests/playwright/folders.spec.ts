import { test, expect } from '@playwright/test'

// All three specs scope folder-name lookups to the FolderTree's
// `<nav aria-label="Folders">` landmark. The unscoped getByText('Sent')
// also matched ThreadRow snippets that happened to contain the word
// "Sent", introducing brittle .first() races.

test('folder tree renders INBOX and Sent in sidebar', async ({ page }) => {
  await page.goto('/')
  const tree = page.getByRole('navigation', { name: 'Folders' }).first()
  await expect(tree.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  await expect(tree.getByText('Sent')).toBeVisible({ timeout: 5_000 })
})

test('Unread view filters threads to unread only', async ({ page }) => {
  await page.goto('/')
  const tree = page.getByRole('navigation', { name: 'Folders' }).first()
  await expect(tree.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  await tree.getByText('Unread').click()
  // there are unread threads in basic.yaml; assert at least one row remains
  await expect(page.locator('button').filter({ hasText: /milestones|Project|Weekly/ }).first()).toBeVisible({ timeout: 5_000 })
})

test('clicking a folder filters threads to that folder', async ({ page }) => {
  await page.goto('/')
  const tree = page.getByRole('navigation', { name: 'Folders' }).first()
  await expect(tree.getByText('Sent')).toBeVisible({ timeout: 10_000 })
  await tree.getByText('Sent').click()
  // basic.yaml's Sent has the "Re: Q1 plans" message; expect it
  await expect(page.getByText(/Q1 plans/i)).toBeVisible({ timeout: 5_000 })
})
