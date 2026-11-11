import { createConnection } from 'node:net'
import { test, expect, waitForAppReady } from './helpers'

// Read fixture FLAGS through an independent TCP IMAP session. Never read bodies
// or infer delivery from the UI/SQLite flag that the API updates optimistically.
async function serverFlags(address: string, uid: number): Promise<string[]> {
  const [host, port] = address.split(':')
  if (host !== '127.0.0.1') throw new Error('Expected isolated mock IMAP')
  const socket = createConnection({ host, port: Number(port) })
  socket.setEncoding('utf8')
  let buffer = ''
  let wake: (() => void) | undefined
  let failure: Error | undefined
  socket.on('data', chunk => { buffer += chunk; wake?.() })
  socket.on('error', error => { failure = error; wake?.() })
  socket.on('close', () => { failure ??= new Error('Fixture IMAP closed'); wake?.() })
  socket.setTimeout(3000, () => socket.destroy(new Error('Fixture IMAP timeout')))
  async function until(pattern: RegExp): Promise<string> {
    while (!pattern.test(buffer)) {
      if (failure) throw failure
      await new Promise<void>(resolve => { wake = resolve })
    }
    wake = undefined
    const result = buffer
    buffer = ''
    return result
  }
  try {
    await until(/\* OK[^\r]*\r\n/)
    // These are public, deterministic test-fixture credentials only.
    socket.write('a LOGIN "alice@example.com" "secret"\r\n')
    await until(/a OK[^\r]*\r\n/)
    socket.write('b SELECT INBOX\r\n')
    await until(/b OK[^\r]*\r\n/)
    socket.write(`c UID FETCH ${uid} (UID FLAGS)\r\n`)
    const response = await until(/c OK[^\r]*\r\n/)
    const flags = response.match(/FLAGS \(([^)]*)\)/)?.[1]
    if (flags === undefined) throw new Error('Fixture UID not returned')
    return flags.split(/\s+/).filter(Boolean).map(flag => flag.toLowerCase())
  } finally { socket.destroy() }
}

test('opening a thread delivers Seen to IMAP and leaves unviewed mail unread', async ({ page, request }, testInfo) => {
  await page.goto('/')
  await waitForAppReady(page)
  await expect(page.getByText(/project update/i).first()).toBeVisible()
  const logs = await (await request.get('/api/_test/logs')).json()
  const address = logs.find((entry: { message: string }) => entry.message.startsWith('mock IMAP started'))?.message.match(/addr=(127\.0\.0\.1:\d+)/)?.[1]
  expect(address, 'mock address must come from this isolated backend').toBeTruthy()
  const dump = await (await request.get('/api/_test/db-dump')).json()
  const viewed = dump.messages.find((m: { subject: string }) => m.subject === 'Project update')
  const unviewed = dump.messages.find((m: { subject: string }) => m.subject === 'Q2 milestones')
  expect(await serverFlags(address, viewed.uid)).not.toContain('\\seen')
  await page.getByText(/project update/i).first().click()
  await expect(page.locator('h2').getByText(/project update/i)).toBeVisible()
  await expect.poll(() => serverFlags(address, viewed.uid)).toContain('\\seen')
  expect(await serverFlags(address, unviewed.uid)).not.toContain('\\seen')
  await page.screenshot({ path: testInfo.outputPath('read-synchronized.png'), fullPage: true })
  await page.reload()
  await waitForAppReady(page)
  expect(await serverFlags(address, viewed.uid)).toContain('\\seen')
})
