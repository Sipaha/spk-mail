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

  // Switch back to Default — comes back
  await page.getByRole('button', { name: /Default/ }).click()
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 5_000 })

  // Verify via test API that the profile is in the DB
  const dump = await (await request.get('/api/_test/db-dump')).json()
  expect(dump.profiles).toBeTruthy()
  const names = (dump.profiles as Array<{ name: string }>).map(p => p.name).sort()
  expect(names).toContain('Work')
})

test('muted profile dims in the switcher', async ({ page, request }) => {
  await page.goto('/')

  // Create a "Muted" profile via API for determinism
  const p = await (await request.post('/api/AddProfile', { data: { name: 'Muted', color: '#ef4444' } })).json()
  // Mute it via API
  await request.post('/api/SetProfileMuted', { data: { id: p.id, muted: true } })

  // Reload to pick up the muted state in the switcher
  await page.reload()
  const tab = page.getByRole('button', { name: /Muted/ }).first()
  await expect(tab).toBeVisible({ timeout: 5_000 })
  await expect(tab).toHaveClass(/opacity-50/)
})
