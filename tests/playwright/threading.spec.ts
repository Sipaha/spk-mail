import { test, expect } from '@playwright/test'

test('reply lands in the same thread', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 10_000 })

  // Inject root + reply (subject normalization buckets them via the SAME normalized subject)
  await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Topic A', body_text: 'first' }
  })
  await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Re: Topic A', body_text: 'second' }
  })

  // Both subjects normalize to "topic a" so they share a thread; the inbox row shows "topic a" (lowercased — known issue tracked in plan-7 backlog)
  await expect(page.getByText(/topic a/i)).toBeVisible({ timeout: 10_000 })
  await page.getByText(/topic a/i).first().click()
  await expect(page.getByText('first')).toBeVisible({ timeout: 5_000 })
  await expect(page.getByText('second')).toBeVisible({ timeout: 5_000 })
})
