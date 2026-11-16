import { createConnection, createServer, type Server, type Socket } from 'node:net'
import { test, expect, folderTree } from './helpers'

interface SynthMsg {
  uid: number
  raw: string
  flags: string[]
}

interface SynthMailbox {
  name: string
  attrs: string[]
  uidValidity: number
  uidNext: number
  msgs: SynthMsg[]
}

class SyntheticIMAPServer {
  private server: Server
  private sockets = new Set<Socket>()
  private idleNotifiers = new Set<() => void>()
  public mailboxes = new Map<string, SynthMailbox>()
  public rejectInboxSelect = false
  public rejectConnections = false
  public idleConnectionsDropped = 0
  public port = 0

  constructor(private email: string, private password: string) {
    this.mailboxes.set('INBOX', { name: 'INBOX', attrs: ['\\HasNoChildren'], uidValidity: 1001, uidNext: 1, msgs: [] })
    this.mailboxes.set('Sent', { name: 'Sent', attrs: ['\\HasNoChildren', '\\Sent'], uidValidity: 1002, uidNext: 1, msgs: [] })

    this.server = createServer((socket) => {
      if (this.rejectConnections) {
        socket.destroy()
        return
      }
      this.sockets.add(socket)
      socket.on('close', () => this.sockets.delete(socket))
      this.handleConn(socket)
    })
  }

  async start(): Promise<number> {
    await new Promise<void>((resolve) => this.server.listen(0, '127.0.0.1', resolve))
    const addr = this.server.address()
    if (!addr || typeof addr === 'string') throw new Error('unexpected address')
    this.port = addr.port
    return this.port
  }

  async stop(): Promise<void> {
    for (const s of this.sockets) s.destroy()
    this.sockets.clear()
    await new Promise<void>((resolve) => this.server.close(() => resolve()))
  }

  append(folder: string, raw: string, flags: string[] = []): number {
    const mb = this.mailboxes.get(folder)
    if (!mb) throw new Error(`unknown mailbox ${folder}`)
    const uid = mb.uidNext++
    mb.msgs.push({ uid, raw, flags: [...flags] })
    if (folder === 'INBOX') {
      for (const notify of this.idleNotifiers) notify()
    }
    return uid
  }

  dropActiveSockets(): void {
    for (const s of this.sockets) {
      this.idleConnectionsDropped++
      s.destroy()
    }
    this.sockets.clear()
  }

  private handleConn(socket: Socket) {
    socket.setEncoding('utf8')
    let buf = ''
    let selected: SynthMailbox | undefined
    let idling = false
    let idleNotify: (() => void) | undefined

    socket.write('* OK [CAPABILITY IMAP4rev1 IDLE] Synthetic IMAP ready\r\n')

    const cleanupIdle = () => {
      if (idleNotify) {
        this.idleNotifiers.delete(idleNotify)
        idleNotify = undefined
      }
      idling = false
    }
    socket.on('close', cleanupIdle)
    socket.on('error', cleanupIdle)

    socket.on('data', (chunk: string) => {
      buf += chunk
      while (true) {
        const idx = buf.indexOf('\r\n')
        if (idx < 0) break
        const line = buf.slice(0, idx)
        buf = buf.slice(idx + 2)

        if (idling) {
          if (line.toUpperCase() === 'DONE') {
            const tag = currentIdleTag
            cleanupIdle()
            socket.write(`${tag} OK IDLE terminated\r\n`)
          }
          continue
        }

        const sp = line.indexOf(' ')
        const tag = sp >= 0 ? line.slice(0, sp) : line
        const rest = sp >= 0 ? line.slice(sp + 1) : ''
        const cmdSp = rest.indexOf(' ')
        const cmd = (cmdSp >= 0 ? rest.slice(0, cmdSp) : rest).toUpperCase()
        const args = cmdSp >= 0 ? rest.slice(cmdSp + 1) : ''

        if (cmd === 'LOGIN') {
          socket.write(`${tag} OK [CAPABILITY IMAP4rev1 IDLE] Logged in\r\n`)
        } else if (cmd === 'CAPABILITY') {
          socket.write('* CAPABILITY IMAP4rev1 IDLE\r\n')
          socket.write(`${tag} OK CAPABILITY completed\r\n`)
        } else if (cmd === 'LIST') {
          for (const mb of this.mailboxes.values()) {
            socket.write(`* LIST (${mb.attrs.join(' ')}) "/" "${mb.name}"\r\n`)
          }
          socket.write(`${tag} OK LIST completed\r\n`)
        } else if (cmd === 'SELECT') {
          const name = args.replace(/^"|"$/g, '')
          const mb = Array.from(this.mailboxes.values()).find(m => m.name.toLowerCase() === name.toLowerCase())
          if (!mb || (mb.name === 'INBOX' && this.rejectInboxSelect)) {
            socket.write(`${tag} NO Mailbox unavailable\r\n`)
            continue
          }
          selected = mb
          socket.write(`* ${mb.msgs.length} EXISTS\r\n`)
          socket.write(`* OK [UIDVALIDITY ${mb.uidValidity}] ok\r\n`)
          socket.write(`* OK [UIDNEXT ${mb.uidNext}] ok\r\n`)
          socket.write(`${tag} OK [READ-WRITE] SELECT completed\r\n`)
        } else if (cmd === 'UID') {
          const subSp = args.indexOf(' ')
          const subCmd = (subSp >= 0 ? args.slice(0, subSp) : args).toUpperCase()
          const subArgs = subSp >= 0 ? args.slice(subSp + 1) : ''
          if (!selected) {
            socket.write(`${tag} BAD No mailbox selected\r\n`)
            continue
          }
          if (subCmd === 'SEARCH') {
            const m = subArgs.match(/UID\s+(\d+):(\*|\d+)/i)
            const lo = m ? Number(m[1]) : 1
            const hi = m && m[2] !== '*' ? Number(m[2]) : Number.MAX_SAFE_INTEGER
            const hits = selected.msgs.filter(msg => msg.uid >= lo && msg.uid <= hi).map(msg => msg.uid)
            socket.write(hits.length > 0 ? `* SEARCH ${hits.join(' ')}\r\n` : '* SEARCH\r\n')
            socket.write(`${tag} OK SEARCH completed\r\n`)
          } else if (subCmd === 'FETCH') {
            const setPart = subArgs.split(' ')[0] ?? ''
            const wanted = new Set<number>()
            for (const part of setPart.split(',')) {
              if (part.includes(':')) {
                const [a, b] = part.split(':')
                const lo = Number(a), hi = b === '*' ? Number.MAX_SAFE_INTEGER : Number(b)
                for (const msg of selected.msgs) if (msg.uid >= lo && msg.uid <= hi) wanted.add(msg.uid)
              } else if (part) {
                wanted.add(Number(part))
              }
            }
            const wantBody = /BODY/i.test(subArgs)
            selected.msgs.forEach((msg, i) => {
              if (!wanted.has(msg.uid)) return
              const seq = i + 1
              if (wantBody) {
                const rawBytes = Buffer.byteLength(msg.raw, 'utf8')
                socket.write(`* ${seq} FETCH (UID ${msg.uid} FLAGS (${msg.flags.join(' ')}) INTERNALDATE "16-Nov-2026 07:30:00 +0000" BODY[] {${rawBytes}}\r\n${msg.raw})\r\n`)
              } else {
                socket.write(`* ${seq} FETCH (UID ${msg.uid} FLAGS (${msg.flags.join(' ')}))\r\n`)
              }
            })
            socket.write(`${tag} OK UID FETCH completed\r\n`)
          } else if (subCmd === 'STORE') {
            const m = subArgs.match(/^(\S+)\s+([+-]?FLAGS(?:\.SILENT)?)\s+\(([^)]*)\)/i)
            if (m) {
              const uidList = m[1].split(',').map(Number)
              const op = m[2].toUpperCase()
              const flagList = m[3].split(/\s+/).filter(Boolean)
              selected.msgs.forEach((msg, i) => {
                if (!uidList.includes(msg.uid)) return
                if (op.startsWith('+')) {
                  for (const fl of flagList) if (!msg.flags.includes(fl)) msg.flags.push(fl)
                } else if (op.startsWith('-')) {
                  msg.flags = msg.flags.filter(fl => !flagList.includes(fl))
                }
                socket.write(`* ${i + 1} FETCH (UID ${msg.uid} FLAGS (${msg.flags.join(' ')}))\r\n`)
              })
            }
            socket.write(`${tag} OK UID STORE completed\r\n`)
          } else {
            socket.write(`${tag} OK done\r\n`)
          }
        } else if (cmd === 'IDLE') {
          idling = true
          currentIdleTag = tag
          socket.write('+ idling\r\n')
          idleNotify = () => {
            if (!idling || !selected) return
            socket.write(`* ${selected.msgs.length} EXISTS\r\n`)
          }
          this.idleNotifiers.add(idleNotify)
        } else if (cmd === 'LOGOUT') {
          socket.write('* BYE logging out\r\n')
          socket.write(`${tag} OK LOGOUT completed\r\n`)
          socket.end()
        } else {
          socket.write(`${tag} OK done\r\n`)
        }
      }
    })
    let currentIdleTag = ''
  }
}

async function queryServerInboxUIDsAndFlags(port: number, email: string, password: string): Promise<Array<{ uid: number; flags: string[] }>> {
  const socket = createConnection({ host: '127.0.0.1', port })
  socket.setEncoding('utf8')
  let buffer = ''
  let wake: (() => void) | undefined
  socket.on('data', (c) => { buffer += c; wake?.() })
  async function until(re: RegExp): Promise<string> {
    while (!re.test(buffer)) {
      await new Promise<void>(r => { wake = r })
    }
    const out = buffer
    buffer = ''
    return out
  }
  try {
    await until(/\* OK[^\r]*\r\n/)
    socket.write(`a LOGIN "${email}" "${password}"\r\n`)
    await until(/a OK[^\r]*\r\n/)
    socket.write('b SELECT INBOX\r\n')
    await until(/b OK[^\r]*\r\n/)
    socket.write('c UID FETCH 1:* (UID FLAGS)\r\n')
    const resp = await until(/c OK[^\r]*\r\n/)
    const rows: Array<{ uid: number; flags: string[] }> = []
    for (const m of resp.matchAll(/FETCH \(UID (\d+) FLAGS \(([^)]*)\)\)/g)) {
      rows.push({
        uid: Number(m[1]),
        flags: m[2].split(/\s+/).filter(Boolean).map(f => f.toLowerCase()),
      })
    }
    return rows
  } finally {
    socket.destroy()
  }
}

test('synthetic IMAP IDLE drop recovers live mail, outage updates Synced truthfully, and Sent stays out of Inbox', async ({ page, request }) => {
  const imap = new SyntheticIMAPServer('sync-test@example.com', 'secret')
  const port = await imap.start()
  try {
    // Seed initial messages:
    // - INBOX UID 1 (Nov 14): Ticket 77 from Support
    // - INBOX UID 2 (Nov 15): Daily Alert from Alerts
    // - Sent  UID 1 (Nov 16): Re: Ticket 77 from SyncTest (replying to INBOX UID 1!)
    imap.append('INBOX', [
      'From: Support <support@example.test>',
      'To: sync-test@example.com',
      'Subject: Ticket 77',
      'Date: Sat, 14 Nov 2026 10:00:00 +0000',
      'Message-ID: <ticket-77@example.test>',
      'Content-Type: text/plain',
      '',
      'Incoming support question',
    ].join('\r\n'), ['\\Seen'])

    imap.append('INBOX', [
      'From: Alerts <alerts@example.test>',
      'To: sync-test@example.com',
      'Subject: Daily alert',
      'Date: Sun, 15 Nov 2026 12:00:00 +0000',
      'Message-ID: <alert-1@example.test>',
      'Content-Type: text/plain',
      '',
      'Alert body',
    ].join('\r\n'), ['\\Seen'])

    imap.append('Sent', [
      'From: SyncTest <sync-test@example.com>',
      'To: support@example.test',
      'Subject: Re: Ticket 77',
      'Date: Mon, 16 Nov 2026 14:30:00 +0000',
      'Message-ID: <sent-77@example.com>',
      'In-Reply-To: <ticket-77@example.test>',
      'Content-Type: text/plain',
      '',
      'Outbound reply body',
    ].join('\r\n'), ['\\Seen'])

    // Reset DB to empty accounts, then add only our synthetic TCP IMAP account.
    await request.post('/api/_test/reset', { data: { accounts: [] } })
    const dump0 = await (await request.get('/api/_test/db-dump')).json()
    const profileId = dump0.profiles[0].id

    const addResp = await request.post('/api/AddAccount', {
      data: {
        name: 'SyncTest',
        email: 'sync-test@example.com',
        imap_host: '127.0.0.1',
        imap_port: port,
        imap_username: 'sync-test@example.com',
        imap_password: 'secret',
        use_tls: false,
        color: '#3b82f6',
        profile_id: profileId,
      },
    })
    expect(addResp.ok()).toBeTruthy()

    await page.goto('/')
    const sidebarAcc = page.locator('section[aria-label="Account SyncTest"]')
    await expect(sidebarAcc.getByText('Synced')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/daily alert/i).first()).toBeVisible({ timeout: 15_000 })

    // 1) Verify Inbox does NOT show Sent reply metadata or message body.
    const tree = folderTree(page)
    await tree.getByText('Inbox').click()
    await expect(page.getByText('Incoming support question')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Outbound reply body')).toHaveCount(0)

    const inboxRows = page.locator('[role="button"]').filter({ hasText: /daily alert|ticket 77/i })
    await expect(inboxRows).toHaveCount(2)
    await expect(inboxRows.nth(0)).toContainText(/daily alert/i)
    await expect(inboxRows.nth(1)).toContainText(/ticket 77/i)

    // Click Ticket 77 inside Inbox: reading pane must show only the 1 Inbox message.
    await page.getByText(/ticket 77/i).first().click()
    const main = page.getByRole('main')
    await expect(main.getByText('1 message')).toBeVisible({ timeout: 5_000 })
    await expect(main.getByText('Incoming support question')).toBeVisible()
    await expect(main.getByText('Outbound reply body')).toHaveCount(0)

    // Switch to Sent: shows Outbound reply body.
    await tree.getByText('Sent').click()
    await expect(page.getByText('Outbound reply body')).toBeVisible({ timeout: 10_000 })

    // Switch back to All mail: shows both messages in Ticket 77.
    await page.getByRole('button', { name: /All mail/i }).click()
    await expect(page.getByText('Outbound reply body').first()).toBeVisible({ timeout: 10_000 })

    // 2) Drop IDLE socket on server side, append new unread mail, verify live delivery without restart!
    imap.dropActiveSockets()
    const uid3 = imap.append('INBOX', [
      'From: Bob <bob@example.com>',
      'To: sync-test@example.com',
      'Subject: Live after IDLE drop',
      'Date: Mon, 16 Nov 2026 14:32:00 +0000',
      'Message-ID: <live-drop@example.test>',
      'Content-Type: text/plain',
      '',
      'Fresh mail after reconnect',
    ].join('\r\n'), [])

    const serverMsgs = await queryServerInboxUIDsAndFlags(port, 'sync-test@example.com', 'secret')
    expect(serverMsgs.some(m => m.uid === uid3 && !m.flags.includes('\\seen'))).toBeTruthy()

    await expect(page.getByText(/live after idle drop/i).first()).toBeVisible({ timeout: 10_000 })
    await expect.poll(async () => {
      const dump = await (await request.get('/api/_test/db-dump')).json()
      return (dump.messages as Array<{ uid: number; subject: string }>).some(m => m.uid === uid3 && m.subject === 'Live after IDLE drop')
    }, { timeout: 10_000 }).toBeTruthy()

    // Open the newly arrived message and verify Seen propagates back to synthetic IMAP server.
    await page.getByText(/live after idle drop/i).first().click()
    await expect.poll(async () => {
      const rows = await queryServerInboxUIDsAndFlags(port, 'sync-test@example.com', 'secret')
      return rows.find(r => r.uid === uid3)?.flags ?? []
    }, { timeout: 10_000 }).toContain('\\seen')

    // 3) Outage test: break INBOX SELECT and drop socket -> Synced MUST become error, then recover!
    imap.rejectInboxSelect = true
    imap.dropActiveSockets()
    await expect(sidebarAcc.getByText("Can't connect — details in Settings")).toBeVisible({ timeout: 10_000 })
    await expect(sidebarAcc.getByText('Synced')).toHaveCount(0)

    imap.rejectInboxSelect = false
    imap.append('INBOX', [
      'From: Bob <bob@example.com>',
      'To: sync-test@example.com',
      'Subject: Recovered after outage',
      'Date: Mon, 16 Nov 2026 14:35:00 +0000',
      'Message-ID: <recovered@example.test>',
      'Content-Type: text/plain',
      '',
      'Outage recovered cleanly',
    ].join('\r\n'), [])

    await expect(sidebarAcc.getByText('Synced')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/recovered after outage/i).first()).toBeVisible({ timeout: 15_000 })
  } finally {
    await imap.stop()
  }
})
