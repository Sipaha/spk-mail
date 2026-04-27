import { test, expect } from '@playwright/test'

test('user can create a profile, switch to it, and see only its accounts', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 10_000 })

  // New profile via the "+" button in the switcher
  await page.getByRole('button', { name: '+', exact: true }).click()
  await page.getByLabel('Name').fill('Work')
  await page.getByRole('button', { name: 'Create' }).click()

  // The new tab appears
  await expect(page.getByRole('button', { name: /Work/ })).toBeVisible({ timeout: 5_000 })

  // Switch to Work — the seeded "Test Personal" account belongs to Default,
  // so it must vanish from the visible accounts list.
  await page.getByRole('button', { name: /Work/ }).click()
  await expect(page.getByText('Test Personal')).not.toBeVisible()

  // Switch to All — comes back
  await page.getByRole('button', { name: 'All', exact: true }).click()
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 5_000 })

  // Verify via test API that the profile is in the DB
  const dump = await (await request.get('/api/_test/db-dump')).json()
  expect(dump.profiles).toBeTruthy()
  const names = (dump.profiles as Array<{ name: string }>).map(p => p.name).sort()
  expect(names).toContain('Work')
})
