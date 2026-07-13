import { test, expect } from './helpers'

test('blocked remote image becomes visible after unblock', async ({ page, request }) => {
  // Add a fixture with a remote image at runtime. Use a per-test unique
  // email so reuseExistingServer (local) doesn't accumulate "Mkt" duplicates
  // across runs and trip a UNIQUE-constraint failure on the second run.
  const uniqueEmail = `mkt-${Date.now()}@example.com`
  const r = await request.post('/api/_test/seed', {
    data: { accounts: [{ name: 'Mkt', email: uniqueEmail, color: '#f59e0b', use_mock: true,
      folders: [{ name: 'INBOX', messages: [{ from: 'n@example.com', subject: 'Offer',
        date: '2026-04-27T09:00:00Z',
        body_html: '<p>Hello</p><img src="https://tracker.example.com/p.png" width="1" height="1">' }] }] }] }
  })
  expect(r.ok(), `seed failed: ${r.status()}`).toBeTruthy()

  // Verify the seed actually landed under the unique email — the previous
  // assertion only checked the rendered subject, which a stale db could
  // satisfy with a leftover row from a prior run. db-dump is the authoritative
  // surface for "this exact account exists in this DB right now".
  const dump = await (await request.get('/api/_test/db-dump')).json()
  const emails = (dump.accounts as Array<{ email: string }>).map(a => a.email)
  expect(emails).toContain(uniqueEmail)

  await page.goto('/')
  await expect(page.getByText(/Offer/i)).toBeVisible({ timeout: 15_000 })
  await page.getByText(/Offer/i).click()
  await page.getByRole('button', { name: /show remote content/i }).click({ timeout: 10_000 })
  // The iframe should now contain an img with the original tracker.example src
  const frame = page.frameLocator('iframe').first()
  await expect(frame.locator('img')).toHaveAttribute('src', /tracker\.example\.com/, { timeout: 5_000 })
})
