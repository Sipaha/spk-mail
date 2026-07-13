import { ReactNode } from 'react'

// Three-pane shell. Surfaces encode structure: the sidebar sits on the panel
// tint (ink-900), list and reading pane on the canvas (ink-950), separated by
// hairlines. The sidebar is a flex column so its footer (Settings / Add
// account) can pin to the bottom while the account tree scrolls.
export default function Layout({ sidebar, list, detail }: { sidebar: ReactNode; list: ReactNode; detail: ReactNode }) {
  return (
    <div className="grid h-full w-full grid-cols-[264px_minmax(320px,430px)_minmax(0,1fr)] grid-rows-1">
      <aside className="flex min-h-0 flex-col border-r border-edge bg-ink-900">{sidebar}</aside>
      <section className="min-h-0 overflow-y-auto border-r border-edge">{list}</section>
      <main className="min-h-0 overflow-y-auto">{detail}</main>
    </div>
  )
}
