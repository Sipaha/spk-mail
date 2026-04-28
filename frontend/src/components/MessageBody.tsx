import { useEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import type { MessageDTO } from '../api/types'

// adaptedCSS: render the email HTML on a virtual white background, then invert
// the whole body so it visually appears dark. img/video/canvas/inline-bg-image
// elements are re-inverted so photos and logos look normal. This mirrors
// Mailspring 2.0 / Outlook dark mode — colors shift slightly (red→cyan etc.)
// but contrast stays readable on a dark UI.
const adaptedCSS = `
  html{background:#0a0a0a;color-scheme:dark}
  body{margin:0;padding:12px 16px;background:#ffffff;color:#000;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5;filter:invert(1) hue-rotate(180deg)}
  img,video,picture,canvas,iframe,svg,[style*="background-image"]{filter:invert(1) hue-rotate(180deg)}
  a{color:#1d4ed8}
`

// originalCSS: white card, no filters. Email renders exactly as authored.
const originalCSS = `
  body{margin:0;padding:12px 16px;color-scheme:light;background:#ffffff;color:#1f2937;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5}
  a{color:#1d4ed8}
  img{max-width:100%;height:auto}
`

export default function MessageBody({ msg }: { msg: MessageDTO }) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [html, setHtml] = useState(msg.body_html)
  const [hasBlocked, setHasBlocked] = useState(false)
  const [adapted, setAdapted] = useState(true)

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
  }, [html, adapted])

  if (!html) {
    return <pre className="whitespace-pre-wrap text-sm text-zinc-200 px-4 py-3">{msg.body_text}</pre>
  }

  const css = adapted ? adaptedCSS : originalCSS
  const wrapperBg = adapted ? 'bg-zinc-950' : 'bg-white'

  return (
    <div className="px-4 py-3">
      <div className="flex gap-2 mb-2">
        {hasBlocked && (
          <button
            onClick={async () => {
              const updated = await client.allowRemote(msg.id)
              setHtml(updated); setHasBlocked(false)
            }}
            className="text-xs rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800">
            Show remote content
          </button>
        )}
        <button
          onClick={() => setAdapted(v => !v)}
          title={adapted ? 'Show as authored (light card)' : 'Adapt to dark theme'}
          className="text-xs rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800 ml-auto">
          {adapted ? 'Original' : 'Dark adapt'}
        </button>
      </div>
      <div className={`rounded overflow-hidden ${wrapperBg}`}>
        <iframe
          ref={ref}
          sandbox="allow-same-origin"
          srcDoc={`<!doctype html><html><head><meta charset="utf-8"><base target="_blank"><style>${css}</style></head><body>${html}</body></html>`}
          className="w-full border-0"
          style={{ minHeight: '200px', background: 'transparent' }}
        />
      </div>
    </div>
  )
}
