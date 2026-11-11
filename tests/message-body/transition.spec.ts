import { test, expect } from '../playwright/node_modules/@playwright/test'

test('no white frame while seven email documents parse and resources wait', async ({ page }, testInfo) => {
  await page.goto('/tests/message-body/')
  await page.exposeFunction('checkpoint', async (stage: string, evidence: unknown) => {
    await page.screenshot({ path: testInfo.outputPath(`${stage}.png`) })
    await testInfo.attach(stage, { body: JSON.stringify(evidence), contentType: 'application/json' })
  })
  const result = await page.evaluate(async () => {
    const { runTransitionChecks } = await import('/tests/message-body/transition-checks.ts')
    return runTransitionChecks((window as any).checkpoint)
  })
  expect(result.afterResources).toHaveLength(7)
})
