import { useEffect, useRef, useState } from 'react'
import * as wails from '@wailsio/runtime'
import { client } from '../api/client'
import type { MessageDTO } from '../api/types'

// openExternal opens a URL in the user's system browser. In Wails desktop we
// route through the runtime's Browser API (which calls xdg-open / open / start
// on the host); in browser-mode we fall back to window.open in a new tab.
//
// Both branches are best-effort. Wails Browser.OpenURL returns a Promise; we
// log rejections so a misconfigured runtime is visible in DevTools instead of
// silently swallowing the click. Falling through to window.open from the
// catch covers the case where the runtime hasn't finished booting (e.g.
// during the very first render after window create).
function openExternal(url: string) {
  const isWails = typeof window !== 'undefined' && window.location.protocol === 'wails:'
  if (isWails) {
    try {
      const p = wails.Browser?.OpenURL(url)
      if (p && typeof p.catch === 'function') {
        p.catch((err: unknown) => {
          console.warn('Browser.OpenURL failed; falling back to window.open', err)
          window.open(url, '_blank', 'noopener,noreferrer')
        })
      }
      return
    } catch (err) {
      console.warn('Browser.OpenURL threw; falling back to window.open', err)
    }
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}

// blockedImagesCSS: hide every <img> still carrying the data-spk-original-src
// placeholder so the layout collapses around them — a 600px-tall hero banner
// from a marketing template no longer leaves an empty white box dominating
// the message. After "Show remote content" the attribute is stripped on the
// server (see UnblockRemote) and the rule no longer matches, so the real
// images appear inline. Both adapted and original CSS include this so the
// gating is uniform regardless of theme. `display:none` (rather than
// visibility:hidden) is what removes the box from layout.
const blockedImagesCSS = `img[data-spk-original-src]{display:none !important}`

// adaptedCSS: outer scaffolding for the dark-mode iframe. Per-element bg/fg
// rewriting is done at runtime (see adaptDarkTheme below) — that catches
// arbitrary inline colors that no CSS rule could enumerate. The CSS here
// only sets the canvas defaults and link/divider colors; everything else is
// resolved against computedStyle of each element after iframe load.
const adaptedCSS = `
  :root{color-scheme:dark}
  html,body{background:#0a0a0a !important;color:#e4e4e7 !important}
  body{margin:0;padding:12px 16px;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5}
  hr{border-color:#3f3f46 !important}
  a,a *{color:#60a5fa !important}
  img{max-width:100%;height:auto}
  ${blockedImagesCSS}
`

const RGB_RE = /^rgba?\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)\s*(?:,\s*([\d.]+))?\s*\)$/

function parseRGB(s: string): { r: number; g: number; b: number; a: number } | null {
  const m = s.match(RGB_RE)
  if (!m) return null
  return { r: +m[1], g: +m[2], b: +m[3], a: m[4] != null ? +m[4] : 1 }
}

function luma(c: { r: number; g: number; b: number }): number {
  return (0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b) / 255
}

// adaptDarkTheme walks every element of the iframe document and:
//  - replaces light backgrounds (luma > 0.78) with transparent so the
//    page's dark canvas shows through, while leaving colored badges alone;
//  - lifts near-black text (luma < 0.30) to a readable off-white, so
//    emails that hard-code dark-on-white in inline styles render legibly
//    on a dark surface.
// Specific decorative colors (red errors, green success, blue links) sit
// in the middle band and pass through untouched.
function adaptDarkTheme(doc: Document) {
  const win = doc.defaultView
  if (!win) return
  for (const el of doc.querySelectorAll<HTMLElement>('*')) {
    const cs = win.getComputedStyle(el)
    const bg = parseRGB(cs.backgroundColor)
    if (bg && bg.a > 0.5 && luma(bg) > 0.78) {
      el.style.setProperty('background-color', 'transparent', 'important')
      // Many email tables also set bgcolor= or background= HTML attributes;
      // strip those so a re-layout (e.g. lazy-loaded image) can't reapply
      // the white surface from the attribute.
      if (el.hasAttribute('bgcolor')) el.removeAttribute('bgcolor')
    }
    const fg = parseRGB(cs.color)
    if (fg && fg.a > 0.5 && luma(fg) < 0.30) {
      el.style.setProperty('color', '#e4e4e7', 'important')
    }
  }
}

// originalCSS: white card, no filters. Email renders exactly as authored.
const originalCSS = `
  body{margin:0;padding:12px 16px;color-scheme:light;background:#ffffff;color:#1f2937;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5}
  a{color:#1d4ed8}
  img{max-width:100%;height:auto}
  ${blockedImagesCSS}
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
  // We track the document and the click handler in a ref-like pair so the
  // cleanup can remove the listener — without that, srcDoc reloads (toggling
  // adapted/original, marking remote-content allowed) would accumulate
  // listeners on the same document object.
  useEffect(() => {
    const f = ref.current; if (!f) return
    let activeDoc: Document | null = null
    let activeWin: Window | null = null
    let activeClick: ((e: Event) => void) | null = null
    let activeWheel: ((e: WheelEvent) => void) | null = null
    const onLoad = () => {
      // Strip any handlers from a previous load before installing new ones.
      if (activeDoc) {
        if (activeClick) {
          activeDoc.removeEventListener('click', activeClick, true)
          activeDoc.removeEventListener('auxclick', activeClick, true)
        }
        if (activeWheel) activeDoc.removeEventListener('wheel', activeWheel, true)
      }
      if (activeWin && activeWheel) {
        activeWin.removeEventListener('wheel', activeWheel, true)
      }
      const doc = f.contentDocument
      if (!doc || !doc.body) return
      // Dark-theme adaptation runs BEFORE the height measurement, since
      // restyling can change body height (transparent backgrounds don't
      // reflow but a future change might).
      if (adapted) adaptDarkTheme(doc)
      // Disarm every external <a href> by moving the URL into a data attr
      // and removing the live href. Without this, even a capture-phase
      // click + preventDefault loses the race against engine activation
      // in some WebKitGTK builds: the iframe ends up navigating to the
      // link target (which then strips our adapter and renders the
      // remote page's white background, or a broken error page if the
      // server refuses framing).
      //
      // Without href, the default click action is nothing — our click
      // handler is then free to read data-spk-href and route through
      // openExternal. cid: and mailto: are passed through openExternal
      // as-is; #anchor jumps stay live (they'd just scroll the iframe).
      for (const a of doc.querySelectorAll<HTMLAnchorElement>('a[href]')) {
        const href = a.getAttribute('href') || ''
        if (!href || href.startsWith('#') || href.startsWith('cid:')) continue
        a.setAttribute('data-spk-href', href)
        a.removeAttribute('href')
        a.style.cursor = 'pointer'
      }
      try {
        f.style.height = (doc.body.scrollHeight + 8) + 'px'
      } catch { /* noop */ }
      const onClick = (e: Event) => {
        const target = (e.target as Element | null)?.closest?.('a') as HTMLAnchorElement | null
        if (!target) return
        const href = target.getAttribute('data-spk-href') || target.getAttribute('href') || ''
        if (!href || href.startsWith('#')) return
        e.preventDefault()
        openExternal(href)
      }
      // Capture phase so we run BEFORE any default behavior the engine
      // might attach to the link's bubble-phase target.
      doc.addEventListener('click', onClick, true)
      doc.addEventListener('auxclick', onClick, true)
      // Wheel events do NOT bubble through iframe boundaries, so spinning
      // the mouse wheel inside the email body would otherwise dead-end at
      // the iframe's window (which itself has nothing to scroll because
      // iframe height === body.scrollHeight). Forward the deltas to the
      // nearest scrollable ancestor of the iframe — typically the <main>
      // panel that wraps ThreadView.
      //
      // deltaMode normalisation: WheelEvent.deltaY can arrive in pixels (0),
      // lines (1) or pages (2) depending on the input device + engine
      // (WebKitGTK and older Chromium often use lines, where deltaY is just
      // 1–5). Forwarding the raw value would scroll only a few pixels per
      // tick and look like the wheel does nothing. Convert to pixels so
      // line/page modes match what the native scroll path would do.
      const LINE_HEIGHT = 16
      const findScroller = (): HTMLElement | null => {
        // Walk the iframe's ancestor chain in the PARENT document, looking
        // for the closest ancestor whose computed overflow-y is auto/scroll
        // AND that actually has scrollable content. Tag-based selectors
        // (looking for <main>) were brittle: a future layout change that
        // wraps <main> in another scroll host would silently regress.
        const ownerWin = f.ownerDocument?.defaultView ?? window
        let el: HTMLElement | null = f.parentElement
        while (el) {
          const cs = ownerWin.getComputedStyle(el)
          if ((cs.overflowY === 'auto' || cs.overflowY === 'scroll') &&
              el.scrollHeight > el.clientHeight) {
            return el
          }
          el = el.parentElement
        }
        return (f.ownerDocument?.scrollingElement as HTMLElement | null) ?? null
      }
      // Cross-binding dedupe: we attach the SAME handler to three targets
      // (iframe element / iframe doc / iframe window) so at least one
      // dispatch path fires under any engine. Once a path delivers an
      // event, the others would deliver THE SAME event again — fold them
      // by remembering the last seen WheelEvent and skipping duplicates.
      // (WheelEvent identity is reliable here because each user-tick
      // produces one trusted event object that gets dispatched on each
      // target in sequence.)
      let lastWheel: WheelEvent | null = null
      const onWheel = (e: WheelEvent) => {
        if (e === lastWheel) return
        lastWheel = e
        const scroller = findScroller()
        if (!scroller) return
        let dx = e.deltaX, dy = e.deltaY
        if (e.deltaMode === 1) { dx *= LINE_HEIGHT; dy *= LINE_HEIGHT }
        else if (e.deltaMode === 2) {
          dx *= scroller.clientWidth
          dy *= scroller.clientHeight
        }
        // Direct scrollTop/Left assignment instead of scrollBy — WebKitGTK
        // quirk: scrollBy with behavior:'auto' occasionally no-ops on a
        // freshly-mounted iframe. Property assignment is the older API but
        // works uniformly across engines.
        scroller.scrollTop += dy
        scroller.scrollLeft += dx
        e.preventDefault()
      }
      // Bind wheel on THREE targets: (a) the iframe ELEMENT in the parent
      // document (capture phase — fires before the iframe consumes the
      // wheel internally on engines that route wheel through the parent
      // first); (b) the iframe document; (c) the iframe contentWindow.
      // Different engines route wheel to different targets and we cannot
      // tell at runtime which path the user's WebKitGTK build uses, so we
      // attach to all three. preventDefault on the first one to fire wins
      // — the others run too but their preventDefault is a no-op once the
      // event is already cancelled, so duplicate scroll deltas are not a
      // concern.
      f.addEventListener('wheel', onWheel as EventListener, { passive: false, capture: true })
      doc.addEventListener('wheel', onWheel, { passive: false, capture: true })
      const win = doc.defaultView
      if (win) {
        win.addEventListener('wheel', onWheel as EventListener, { passive: false, capture: true })
      }
      activeDoc = doc
      activeWin = win
      activeClick = onClick
      activeWheel = onWheel
    }
    f.addEventListener('load', onLoad)
    // srcDoc loads synchronously in some engines: by the time this effect
    // runs the iframe's "load" event has already fired and we'd never
    // attach the click handler. Detect a live document and run onLoad
    // immediately when that's the case. We use readyState on the iframe's
    // own document, NOT the parent's.
    const doc = f.contentDocument
    if (doc && doc.readyState === 'complete' && doc.body) {
      onLoad()
    }
    return () => {
      f.removeEventListener('load', onLoad)
      if (activeDoc) {
        if (activeClick) {
          activeDoc.removeEventListener('click', activeClick, true)
          activeDoc.removeEventListener('auxclick', activeClick, true)
        }
        if (activeWheel) activeDoc.removeEventListener('wheel', activeWheel, true)
      }
      if (activeWin && activeWheel) {
        activeWin.removeEventListener('wheel', activeWheel as EventListener, true)
      }
    }
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
          // allow-scripts is required for event dispatch INSIDE the iframe
          // to function on WebKitGTK: without it, the iframe's JS engine
          // is dormant and listeners attached from parent JS on
          // contentDocument/contentWindow are never invoked, so wheel
          // forwarding and link-click capture silently no-op. (Chromium
          // is more lenient and dispatches anyway, which is why
          // browser-mode worked.) Email-supplied JS cannot run because
          // bluemonday's UGC policy strips <script>, on*= handlers, and
          // javascript:/expression()/@import URL schemes BEFORE the body
          // is persisted — see internal/mime/sanitize.go. The only code
          // that runs in the iframe is the empty default; we still apply
          // dark theme + disarm anchors via parent contentDocument
          // mutation (which allow-same-origin permits).
          sandbox="allow-same-origin allow-scripts"
          // No <base target="_blank"> — without `allow-popups` in the
          // sandbox, target="_blank" navigation is silently blocked by the
          // engine before our capture-phase click handler can act on
          // it. Default target=_self is fine: the click handler runs first
          // (capture phase, preventDefault) and routes through
          // openExternal, so the iframe never attempts the navigation.
          srcDoc={`<!doctype html><html><head><meta charset="utf-8"><style>${css}</style></head><body>${html}</body></html>`}
          className="w-full border-0"
          style={{ height: 0, background: 'transparent' }}
        />
      </div>
    </div>
  )
}
