import { test, expect } from '@playwright/test'

test('injecting a message updates the inbox via SSE', async ({ page, request }) => {
  await page.goto('/')
  // Wait for the seed account to render
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 10_000 })

  await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Live update', body_text: 'hello live' }
  })

  await expect(page.getByText('Live update')).toBeVisible({ timeout: 10_000 })
})
