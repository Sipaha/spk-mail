import { test, expect } from './helpers'

test('user can add an account through the form', async ({ page, request }) => {
  // Cold load directly on #/settings/accounts (no prior visit to "/"): this
  // guards the bootstrap-on-every-route fix — App() used to only fetch
  // accounts/profiles from a component nested under the main-layout branch,
  // which never rendered on this route, leaving Settings.tsx stuck on an
  // empty account list. Waiting for the seeded account name here proves the
  // bootstrap fetch ran on this route too.
  await page.goto('/#/settings/accounts')
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 15_000 })
  await page.getByRole('button', { name: 'Add account', exact: true }).click()
  await page.getByLabel('Display name').fill('My Mail')
  await page.getByLabel('Email').fill('me@example.com')
  await page.getByLabel('IMAP host').fill('localhost')
  await page.getByLabel('Username').fill('me@example.com')
  await page.getByLabel('App password').fill('whatever')
  await page.getByRole('button', { name: /add account/i }).click()

  // UI outcome: the settings list shows the new row (addAccount succeeds even
  // when the mock IMAP connect later fails for an unknown user).
  await expect(page.getByText('My Mail')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('me@example.com')).toBeVisible()
  await expect(page.getByText('Test Personal')).toBeVisible()

  // Authoritative DB check — basic.yaml seeds 1 account; this adds another.
  await expect.poll(async () => {
    const r = await request.get('/api/_test/db-dump')
    const dump = await r.json()
    return dump.accounts?.length ?? 0
  }).toBe(2)
})