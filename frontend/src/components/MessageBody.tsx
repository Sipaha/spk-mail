import { useEffect, useRef, useState } from 'react'
import * as wails from '@wailsio/runtime'
import { client } from '../api/client'
import type { MessageDTO } from '../api/types'

// openExternal opens a URL in the user's system browser. In Wails desktop we
// route through the runtime's Browser API (which calls xdg-open / open / start
// on the host); in browser-mode we fall back to window.open in a new tab.
function openExternal(url: string) {
  try {
    const isWails = typeof window !== 'undefined' && window.location.protocol === 'wails:'
    if (isWails && wails.Browser?.OpenURL) {
      wails.Browser.OpenURL(url)
      return
    }
  } catch {
    // fall through to window.open
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}

// adaptedCSS: render the email HTML on a dark surface by REWRITING common
// light-background and dark-text rules with !important, NOT by using
// `filter: invert(...)`. The filter approach disables subpixel font AA on
// every major browser, which makes text look fuzzy on LCD panels. Targeted
// overrides keep text crisp while still neutralizing the white "card" look
// of typical email templates (Jira / GitHub / Atlassian / Outlook). Inline
// tag colors (Jira's "Medium"/"Urgent" badges, etc.) are preserved.
const adaptedCSS = `
  :root{color-scheme:dark}
  html,body{background:#0a0a0a !important;color:#e4e4e7 !important}
  body{margin:0;padding:12px 16px;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5}
  /* Nuke generic light backgrounds — covers ~all email templates without
     wrecking colored badge backgrounds (those use specific colors, not white). */
  table,tbody,thead,tfoot,tr,td,th,div,p,span,section,article,header,footer,nav,main,aside,blockquote,li,ul,ol{background-color:transparent}
  [bgcolor="#FFFFFF" i],[bgcolor="#FFF" i],[bgcolor="white" i],
  [bgcolor="#F4F5F7" i],[bgcolor="#F8F9FA" i],[bgcolor="#FAFAFA" i],
  [bgcolor="#EEEEEE" i],[bgcolor="#F0F0F0" i]{background-color:transparent !important}
  [style*="background-color:#fff" i],[style*="background-color: #fff" i],
  [style*="background-color:white" i],[style*="background-color: white" i],
  [style*="background:#fff" i],[style*="background: #fff" i],
  [style*="background:white" i],[style*="background: white" i],
  [style*="background-color:#f4f5f7" i],[style*="background-color: #f4f5f7" i],
  [style*="background-color:#f8f9fa" i],[style*="background-color: #f8f9fa" i]{background-color:transparent !important}
  /* Black-on-white text → light. Specific colors (e.g. red error / green ok) stay. */
  [color="#000000" i],[color="#000" i],[color="black" i],
  [style*="color:#000;" i],[style*="color: #000;" i],[style*="color:black;" i],[style*="color: black;" i]{color:#e4e4e7 !important}
  /* Lighten dark hex text that's nearly black (#1f2937 / #2d3748 / #111 etc.) */
  [style*="color:#111" i],[style*="color: #111" i],
  [style*="color:#1f" i],[style*="color: #1f" i],
  [style*="color:#1a" i],[style*="color: #1a" i],
  [style*="color:#222" i],[style*="color: #222" i],
  [style*="color:#333" i],[style*="color: #333" i]{color:#d4d4d8 !important}
  /* Hairline / divider colors — emails often use #e5e7eb / #ddd / #ccc */
  hr{border-color:#3f3f46 !important}
  /* Links: keep blue but readable on dark. */
  a,a *{color:#60a5fa !important}
  img{max-width:100%;height:auto}
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

  // On every iframe (re)load: resize to content height AND wire up a click
  // interceptor so <a> taps open in the system browser instead of trying to
  // navigate inside the sandboxed iframe (which silently does nothing).
  useEffect(() => {
    const f = ref.current; if (!f) return
    const onLoad = () => {
      const doc = f.contentDocument
      if (!doc) return
      try {
        f.style.height = (doc.body.scrollHeight + 8) + 'px'
      } catch { /* noop */ }
      const onClick = (e: Event) => {
        const target = (e.target as Element | null)?.closest?.('a') as HTMLAnchorElement | null
        if (!target) return
        const href = target.getAttribute('href') || ''
        if (!href || href.startsWith('#') || href.startsWith('mailto:')) return
        e.preventDefault()
        openExternal(href)
      }
      doc.addEventListener('click', onClick)
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
      <div className={`relative rounded overflow-hidden ${wrapperBg}`}>
        {/* Floating controls in the top-right corner of the body card —
            keeps the chrome out of the message header but right at hand
            for when the user wants to flip styling or unblock images.
            "Show remote content" (less common, more weighty action) sits
            on the left; the style toggle on the right. */}
        <div className="absolute top-2 right-2 z-10 flex gap-1.5">
          {hasBlocked && (
            <button
              onClick={async () => {
                const updated = await client.allowRemote(msg.id)
                setHtml(updated); setHasBlocked(false)
              }}
              className="text-[11px] rounded border border-zinc-700/80 bg-zinc-900/80 backdrop-blur px-2 py-0.5 text-zinc-300 hover:text-zinc-100 hover:bg-zinc-800">
              Show remote content
            </button>
          )}
          <button
            onClick={() => setAdapted(v => !v)}
            title={adapted ? 'Show as authored (light card)' : 'Adapt to dark theme'}
            className="text-[11px] rounded border border-zinc-700/80 bg-zinc-900/80 backdrop-blur px-2 py-0.5 text-zinc-300 hover:text-zinc-100 hover:bg-zinc-800">
            {adapted ? 'Original' : 'Dark adapt'}
          </button>
        </div>
        <iframe
          ref={ref}
          sandbox="allow-same-origin"
          srcDoc={`<!doctype html><html><head><meta charset="utf-8"><base target="_blank"><style>${css}</style></head><body>${html}</body></html>`}
          className="w-full border-0"
          style={{ height: 0, background: 'transparent' }}
        />
      </div>
    </div>
  )
}
