import { test, expect } from '@playwright/test'

test('blocked remote image becomes visible after unblock', async ({ page, request }) => {
  // Add a fixture with a remote image at runtime
  await request.post('/api/_test/seed', {
    data: { accounts: [{ name: 'Mkt', email: 'mkt@example.com', color: '#f59e0b', use_mock: true,
      folders: [{ name: 'INBOX', messages: [{ from: 'n@example.com', subject: 'Offer',
        date: '2026-04-27T09:00:00Z',
        body_html: '<p>Hello</p><img src="https://tracker.example.com/p.png" width="1" height="1">' }] }] }] }
  })
  await page.goto('/')
  await expect(page.getByText(/Offer/i)).toBeVisible({ timeout: 15_000 })
  await page.getByText(/Offer/i).click()
  await page.getByRole('button', { name: /show remote content/i }).click({ timeout: 10_000 })
  // The iframe should now contain an img with the original tracker.example src
  const frame = page.frameLocator('iframe').first()
  await expect(frame.locator('img')).toHaveAttribute('src', /tracker\.example\.com/, { timeout: 5_000 })
})
