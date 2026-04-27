import { ReactNode } from 'react'
export default function Layout({ sidebar, list, detail }: { sidebar: ReactNode; list: ReactNode; detail: ReactNode }) {
  return (
    <div className="grid h-screen w-screen grid-cols-[260px_minmax(320px,420px)_1fr] grid-rows-1 bg-zinc-950 text-zinc-100">
      <aside className="border-r border-zinc-800 overflow-y-auto">{sidebar}</aside>
      <section className="border-r border-zinc-800 overflow-y-auto">{list}</section>
      <main className="overflow-y-auto">{detail}</main>
    </div>
  )
}
