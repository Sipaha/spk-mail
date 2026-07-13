import { test, expect, waitForAppReady, waitForSseListThreads } from './helpers'

test('reply lands in the same thread', async ({ page, request }) => {
  await page.goto('/')
  await waitForAppReady(page)

  const ssePost1 = waitForSseListThreads(page)
  const r1 = await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Topic A', body_text: 'first' }
  })
  expect(r1.ok(), `inject root failed: ${r1.status()}`).toBeTruthy()
  await ssePost1

  const ssePost2 = waitForSseListThreads(page)
  const r2 = await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Re: Topic A', body_text: 'second' }
  })
  expect(r2.ok(), `inject reply failed: ${r2.status()}`).toBeTruthy()
  await ssePost2

  // Both subjects normalize to "topic a" so they share a thread. The
  // inbox row shows "topic a" lowercased because the thread row uses
  // threads.subject_norm (already lowercased by NormalizeSubject for
  // bucket-matching) instead of the original message subject. Cosmetic
  // mismatch we accept here; fix would require carrying the original
  // case through to the row.
  await expect(page.getByText(/topic a/i)).toBeVisible({ timeout: 10_000 })
  await page.getByText(/topic a/i).first().click()
  // After click, the thread view (<main>) shows both message bodies as <pre>.
  // Scope to <main> because the inbox ThreadRow now shows a body-text snippet
  // and would otherwise collide on getByText('first' | 'second').
  const main = page.getByRole('main')
  await expect(main.getByText('first')).toBeVisible({ timeout: 5_000 })
  await expect(main.getByText('second')).toBeVisible({ timeout: 5_000 })
})