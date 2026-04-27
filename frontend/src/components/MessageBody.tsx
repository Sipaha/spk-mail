import { useEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import type { MessageDTO } from '../api/types'

export default function MessageBody({ msg }: { msg: MessageDTO }) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [html, setHtml] = useState(msg.body_html)
  const [hasBlocked, setHasBlocked] = useState(false)

  useEffect(() => {
    setHtml(msg.body_html)
    setHasBlocked(/data-spk-original-src/.test(msg.body_html))
  }, [msg.body_html])

  // Resize iframe to its content
  useEffect(() => {
    const f = ref.current; if (!f) return
    const onLoad = () => {
      try {
        const h = f.contentDocument?.body.scrollHeight ?? 0
        f.style.height = (h + 8) + 'px'
      } catch {}
    }
    f.addEventListener('load', onLoad)
    return () => f.removeEventListener('load', onLoad)
  }, [html])

  if (!html) {
    return <pre className="whitespace-pre-wrap text-sm text-zinc-200 px-4 py-3">{msg.body_text}</pre>
  }

  return (
    <div className="px-4 py-3">
      {hasBlocked && (
        <button
          onClick={async () => {
            const updated = await client.allowRemote(msg.id)
            setHtml(updated); setHasBlocked(false)
          }}
          className="text-xs rounded border border-zinc-700 px-2 py-1 mb-2 hover:bg-zinc-800">
          Show remote content
        </button>
      )}
      <iframe
        ref={ref}
        sandbox="allow-same-origin"
        srcDoc={`<!doctype html><html><head><meta charset="utf-8"><base target="_blank"><style>body{margin:0;padding:0;color-scheme:dark;background:#0a0a0a;color:#e4e4e7;font-family:system-ui,sans-serif;font-size:14px}a{color:#60a5fa}</style></head><body>${html}</body></html>`}
        className="w-full border-0"
        style={{ minHeight: '200px' }}
      />
    </div>
  )
}
