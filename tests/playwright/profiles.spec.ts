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

test('right-click profile tab opens menu and deletes when no accounts attached', async ({ page, request }) => {
  await page.goto('/')

  // Create an empty "Disposable" profile via API — no accounts attached so
  // the delete is allowed by storage.DeleteProfile (otherwise ErrProfileInUse).
  const p = await (await request.post('/api/AddProfile', { data: { name: 'Disposable', color: '#ec4899' } })).json()
  await page.reload()

  const tab = page.getByRole('button', { name: /Disposable/ })
  await expect(tab).toBeVisible({ timeout: 5_000 })

  // Auto-accept the window.confirm the delete handler raises before issuing
  // the DeleteProfile call. Must be wired BEFORE the dialog can fire.
  page.once('dialog', d => d.accept())
  await tab.click({ button: 'right' })
  await page.getByRole('button', { name: /Delete profile/ }).click()

  // Tab must vanish from the switcher.
  await expect(page.getByRole('button', { name: /Disposable/ })).not.toBeVisible({ timeout: 5_000 })

  // And gone from the DB.
  const dump = await (await request.get('/api/_test/db-dump')).json()
  const ids = (dump.profiles as Array<{ id: number }>).map(x => x.id)
  expect(ids).not.toContain(p.id)
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
  // Assert via data-muted (set by ProfileSwitcher.tsx) rather than the
  // Tailwind `opacity-50` class — the data-attr is the contract; the
  // class is the implementation. A theme tweak that swaps opacity-50
  // for, say, brightness-75 would falsely fail the class assertion.
  await expect(tab).toHaveAttribute('data-muted', 'true')
})
