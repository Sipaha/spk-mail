import { test, expect, waitForAppReady, waitForSseListThreads } from './helpers'

test('injecting a message updates the inbox via SSE', async ({ page, request }) => {
  await page.goto('/')
  await waitForAppReady(page)

  const ssePost = waitForSseListThreads(page)

  const r = await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Live update', body_text: 'hello live' }
  })
  expect(r.ok(), `inject-message failed: ${r.status()}`).toBeTruthy()

  await ssePost

  await expect(page.getByText('Live update')).toBeVisible({ timeout: 10_000 })
})