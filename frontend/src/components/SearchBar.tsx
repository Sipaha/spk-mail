import { useState } from 'react'

export default function SearchBar() {
  const [q, setQ] = useState('')
  return (
    <form
      className="px-3 py-2 border-b border-zinc-800"
      onSubmit={(e) => { e.preventDefault(); window.location.hash = '#/search?q=' + encodeURIComponent(q) }}>
      <input
        value={q} onChange={(e) => setQ(e.target.value)}
        placeholder="Search… (try: from:bob has:attachment unread)"
        className="w-full rounded bg-zinc-900 border border-zinc-700 focus:border-zinc-500 px-2 py-1.5 text-sm" />
    </form>
  )
}
