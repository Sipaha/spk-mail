import { test, expect } from '@playwright/test'

test('folder tree renders INBOX and Sent in sidebar', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('INBOX').first()).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('Sent').first()).toBeVisible({ timeout: 5_000 })
})

test('Unread view filters threads to unread only', async ({ page }) => {
  await page.goto('/')
  // wait for list to populate
  await expect(page.getByText('INBOX').first()).toBeVisible({ timeout: 10_000 })
  // click Unread
  await page.getByRole('button', { name: 'Unread', exact: true }).click()
  // there are unread threads in basic.yaml; assert at least one row remains
  await expect(page.locator('button').filter({ hasText: /milestones|Project|Weekly/ }).first()).toBeVisible({ timeout: 5_000 })
})

test('clicking a folder filters threads to that folder', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('Sent').first()).toBeVisible({ timeout: 10_000 })
  // click Sent (in folder tree)
  await page.getByText('Sent').first().click()
  // basic.yaml's Sent has the "Re: Q1 plans" message; expect it
  await expect(page.getByText(/Q1 plans/i)).toBeVisible({ timeout: 5_000 })
})
