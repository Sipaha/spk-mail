import { test, expect } from '@playwright/test'

test('injecting a message updates the inbox via SSE', async ({ page, request }) => {
  await page.goto('/')
  // Wait for the seed account to render — also serves as the "page-load
  // ListThreads completed" barrier, so the waitForResponse below reliably
  // picks up the NEXT (SSE-driven) ListThreads, not the one fired during
  // initial render.
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 10_000 })

  // Arm the waitForResponse before injecting. The SSE-driven refetch path
  // is: server emits MessageInserted on /api/events → frontend events.ts
  // handler → client.listThreads(...) POST. If SSE were broken, the POST
  // would not fire, and this wait would time out before the visual
  // assertion. Observing this network footprint is the closest we can
  // get to "the event arrived through the SSE channel" from a black-box
  // Playwright test without injecting a custom EventSource shim.
  const ssePost = page.waitForResponse(
    resp => resp.url().endsWith('/api/ListThreads') && resp.request().method() === 'POST',
    { timeout: 10_000 },
  )

  const r = await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Live update', body_text: 'hello live' }
  })
  expect(r.ok(), `inject-message failed: ${r.status()}`).toBeTruthy()

  // First check: the SSE-driven refetch happened.
  await ssePost

  // Then the visual: the row is now in the DOM.
  await expect(page.getByText('Live update')).toBeVisible({ timeout: 10_000 })
})
