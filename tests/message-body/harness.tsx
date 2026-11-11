import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import MessageBody from '../../frontend/src/components/MessageBody'
import '../../frontend/src/styles/tailwind.css'
import type { MessageDTO } from '../../frontend/src/api/types'
import { runTransitionChecks } from './transition-checks'

function App() {
  const [generation, setGeneration] = useState(0)
  const [open, setOpen] = useState(false)
  const [viewport, setViewport] = useState(false)
  return <>
    <nav className="flex gap-5 p-3">
      <button id="open" onClick={() => setOpen(v => !v)}>Open / close</button>
      <button id="switch" onClick={() => { setGeneration(v => v + 1); setOpen(true) }}>Switch thread</button>
      <button id="viewport" onClick={() => setViewport(true)}>Viewport-height mail</button>
    </nav>
    <main style={{ height: 650, overflowY: 'auto' }}>
      {open && Array.from({ length: 7 }, (_, i) => {
        const msg: MessageDTO = {
          id: generation * 10 + i, account_id: 1, folder_id: 1, subject: 'Paint fixture',
          from_addr: 'sender@example.test', to_addrs: [], date: 0, flags: [], attachments: [], body_text: 'Plain text',
          // Represents sanitized HTML with an already allowed remote image.
          // The local server holds it until the test explicitly releases it.
          body_html: `<table bgcolor="#ffffff" style="background-color:#ffffff;color:#111111;width:100%"><tbody><tr><td>Message ${generation}-${i} <a href="https://example.test/link">External link</a><blockquote>Quoted text</blockquote><img src="/probe.img?g=${generation}&i=${i}"></td></tr></tbody></table>${viewport && i === 0 ? '<div style="height:100vh">Viewport-dependent section</div>' : ''}`,
        }
        return <section key={msg.id} className="border-b border-edge">
          <header className="px-5 pt-4 text-fg-sub">Sender {i + 1}</header>
          <MessageBody msg={msg} />
        </section>
      })}
    </main>
  </>
}
createRoot(document.getElementById('root')!).render(<App />)

// The same assertions run in Chromium and an actual Wails/WebKitGTK window.
// Only native screenshots need a handshake with the isolated Xvfb launcher.
if (new URLSearchParams(location.search).has('native')) {
  const checkpoint = async (stage: string, evidence: unknown) => {
    await fetch('http://localhost:5186/report', { method: 'POST', body: JSON.stringify({ stage, evidence }) })
  }
  void runTransitionChecks(checkpoint).then(
    evidence => checkpoint('passed', evidence),
    error => checkpoint('failed', { error: String(error), stack: error.stack }),
  )
}
