import { Fragment } from 'react'

const BEGIN = '\x01'
const END   = '\x02'

// Snippet renders search highlights as real React elements. Input contains
// \x01 BEGIN and \x02 END sentinels around matched terms. Anything outside
// becomes a text node; anything inside becomes <mark>. No innerHTML — the
// content is plain text from FTS5 and the only "markup" we render is the
// <mark> tag we explicitly emit here.
export default function Snippet({ text }: { text: string }) {
  const parts: { mark: boolean; text: string }[] = []
  let i = 0
  while (i < text.length) {
    const begin = text.indexOf(BEGIN, i)
    if (begin === -1) { parts.push({ mark: false, text: text.slice(i) }); break }
    if (begin > i) parts.push({ mark: false, text: text.slice(i, begin) })
    const end = text.indexOf(END, begin + 1)
    if (end === -1) { parts.push({ mark: false, text: text.slice(begin + 1) }); break }
    parts.push({ mark: true, text: text.slice(begin + 1, end) })
    i = end + 1
  }
  return (
    <span>
      {parts.map((p, idx) => (
        <Fragment key={idx}>
          {p.mark ? <mark className="rounded bg-brass/25 px-0.5 text-brass">{p.text}</mark> : p.text}
        </Fragment>
      ))}
    </span>
  )
}
