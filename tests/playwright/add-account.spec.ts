import { test, expect } from '@playwright/test'

test('user can add an account through the form', async ({ page, request }) => {
  await page.goto('/#/settings/accounts')
  await page.getByRole('button', { name: 'Add account', exact: true }).click()
  await page.getByLabel('Display name').fill('My Mail')
  await page.getByLabel('Email').fill('me@example.com')
  await page.getByLabel('IMAP host').fill('localhost')
  await page.getByLabel('Username').fill('me@example.com')
  await page.getByLabel('App password').fill('whatever')
  await page.getByRole('button', { name: /add account/i }).click()
  // The mock IMAP server will reject this because the user wasn't pre-created;
  // we still expect the API call to register the row before the connect failure.
  await expect.poll(async () => {
    const r = await request.get('/api/_test/db-dump')
    const dump = await r.json()
    return dump.accounts?.length ?? 0
  }).toBeGreaterThanOrEqual(2)  // basic.yaml seeds 1 account; this adds another
})
