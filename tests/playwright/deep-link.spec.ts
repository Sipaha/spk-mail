import { test, expect, waitForAppReady } from './helpers'

test('notification deep-link opens the thread', async ({ page, request }) => {
  await page.goto('/')
  await waitForAppReady(page)
  // Sync must finish before db-dump thread ids are meaningful.
  await expect(page.getByText(/project update/i).first()).toBeVisible({ timeout: 15_000 })

  const dump = await (await request.get('/api/_test/db-dump')).json()
  const threads = dump.threads as Array<{ id: number; subject_norm?: string }>
  const row = threads.find(t => (t.subject_norm ?? '').includes('project update'))
  expect(row, 'seed thread for Project update').toBeTruthy()

  // Tray notifications use SetURL (hash change), not a full navigation — mirror that.
  await page.evaluate((id) => { window.location.hash = `#/thread/${id}` }, row!.id)
  const main = page.getByRole('main')
  await expect(main.getByText(/update on our project/i)).toBeVisible({ timeout: 15_000 })
  await expect(main.locator('h2').getByText(/project update/i)).toBeVisible()
})