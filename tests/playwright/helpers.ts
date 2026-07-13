import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test'

async function apiTokenFromIndex(request: APIRequestContext, baseURL: string): Promise<string> {
  const html = await (await request.get(baseURL)).text()
  const m = html.match(/name="spk-mail-api-token" content="([^"]+)"/)
  return m?.[1] ?? ''
}

/** Folder tree landmark — scopes folder-name lookups away from thread snippets. */
export function folderTree(page: Page) {
  return page.getByRole('navigation', { name: 'Folders' }).first()
}

/** Wait until the startup seed account is visible (page-load ListThreads barrier). */
export async function waitForAppReady(page: Page) {
  await expect(page.getByText('Test Personal')).toBeVisible({ timeout: 15_000 })
}

/**
 * Arm before inject/seed mutations, then await after POST. Observes the
 * SSE-driven ListThreads refetch (see notification-flow.spec.ts).
 */
export function waitForSseListThreads(page: Page) {
  return page.waitForResponse(
    resp => resp.url().endsWith('/api/ListThreads') && resp.request().method() === 'POST',
    { timeout: 10_000 },
  )
}

/** Wipe DB + mock IMAP and re-apply a YAML fixture from tests/fixtures/. */
export async function resetSeed(request: APIRequestContext, fixture?: string) {
  const url = fixture ? `/api/_test/reset?fixture=${encodeURIComponent(fixture)}` : '/api/_test/reset'
  const r = await request.post(url)
  expect(r.ok(), `reset failed: ${r.status()} ${await r.text()}`).toBeTruthy()
  // Reset returns before sync workers finish their first fetch; poll until
  // at least one account row exists so the next page.goto has data to show.
  await expect.poll(async () => {
    const dump = await (await request.get('/api/_test/db-dump')).json()
    return (dump.accounts as unknown[] | undefined)?.length ?? 0
  }, { timeout: 10_000 }).toBeGreaterThan(0)
}

// Per-test seed reset via auto fixture. Skipped for 01-visual-regression
// (runs first against the webServer's fresh DB; reset would fight its clock
// freeze) and for the first test in that file only when order changes.
const test = base.extend<{ request: APIRequestContext; _seedReset: void }>({
  request: async ({ playwright, baseURL }, use) => {
    const origin = baseURL ?? 'http://127.0.0.1:5174'
    const probe = await playwright.request.newContext({ baseURL: origin, extraHTTPHeaders: { Origin: origin } })
    const token = await apiTokenFromIndex(probe, origin)
    await probe.dispose()
    const headers: Record<string, string> = { Origin: origin }
    if (token) headers.Authorization = `Bearer ${token}`
    const ctx = await playwright.request.newContext({ baseURL: origin, extraHTTPHeaders: headers })
    await use(ctx)
    await ctx.dispose()
  },
  // Depend on `page` so Playwright's webServer URL probe finishes before
  // we POST /api/_test/reset (auto fixtures alone can race the server boot).
  _seedReset: [async ({ page, request }, use, testInfo) => {
    if (!testInfo.file.includes('01-visual-regression')) {
      await resetSeed(request)
    }
    await use()
  }, { auto: true }],
})

export { test, expect }