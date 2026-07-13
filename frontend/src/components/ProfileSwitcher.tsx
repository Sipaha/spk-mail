import { useEffect, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import NewProfileDialog from './NewProfileDialog'

// Heroicons-style bell / bell-slash inline SVG (licensed MIT, traced from heroicons.com).
// Kept inline so we don't pull in an icon package for two glyphs.
function BellIcon({ slashed, className = '' }: { slashed: boolean; className?: string }) {
  if (slashed) {
    return (
      <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.143 17.082a24.25 24.25 0 0 0 3.844.148m-3.844-.148a23.86 23.86 0 0 1-5.455-1.31 8.96 8.96 0 0 0 2.3-5.542m3.155 6.852a3 3 0 0 0 5.667 1.97m1.965-2.277L21 21M4.281 15.772a14.94 14.94 0 0 1-.831-1.252M6 9.75V9c0-.43.045-.85.13-1.255M7.5 5.25v.005a6.013 6.013 0 0 1 4.5-2.005 6 6 0 0 1 5.85 7.503M3 3l18 18" />
      </svg>
    )
  }
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 0 0 5.454-1.31A8.967 8.967 0 0 1 18 9.75V9A6 6 0 0 0 6 9v.75a8.967 8.967 0 0 1-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 0 1-5.714 0m5.714 0a3 3 0 1 1-5.714 0" />
    </svg>
  )
}

type Menu = { profileId: number; x: number; y: number }

export default function ProfileSwitcher() {
  const profiles = useStore(s => s.profiles)
  const setProfiles = useStore(s => s.setProfiles)
  const activeProfileId = useStore(s => s.activeProfileId)
  const setActiveProfile = useStore(s => s.setActiveProfile)
  const [creating, setCreating] = useState(false)
  const [menu, setMenu] = useState<Menu | null>(null)

  // No mount-time listProfiles() here: App's useBootstrap already loads
  // profiles into the store on every route, and a second fetch on mount just
  // duplicated that round trip. Profiles are refreshed from the store's
  // setProfiles (which also keeps activeProfileId valid) after create/delete.

  useEffect(() => {
    if (!menu) return
    const onDown = () => setMenu(null)
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenu(null) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [menu])

  const tabClass = (active: boolean) =>
    `px-2 py-1 text-xs rounded-t border-b-2 ${active
      ? 'border-zinc-200 text-zinc-100'
      : 'border-transparent text-zinc-500 hover:text-zinc-300'}`

  const onDelete = async (id: number) => {
    setMenu(null)
    const target = profiles.find(p => p.id === id)
    if (!target) return
    if (!window.confirm(`Delete profile "${target.name}"?`)) return
    try {
      await client.deleteProfile(id)
      const fresh = await client.listProfiles()
      setProfiles(fresh)
      if (activeProfileId === id) setActiveProfile(fresh[0]?.id ?? null)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      // ErrProfileInUse from the backend includes "profile has attached
      // accounts" — surface a friendlier hint instead of the raw error.
      if (/attached accounts/i.test(msg)) {
        window.alert('This profile still has accounts attached. Move or remove them first.')
      } else {
        window.alert(`Delete failed: ${msg}`)
      }
    }
  }

  return (
    <>
      <div className="flex items-center gap-1 px-3 pt-2 border-b border-zinc-800 overflow-x-auto">
        {profiles.map(p => (
          <span key={p.id} className="group inline-flex items-center">
            <button
              data-muted={p.muted ? 'true' : undefined}
              className={tabClass(activeProfileId === p.id) + (p.muted ? ' opacity-50' : '')}
              onClick={() => setActiveProfile(p.id)}
              onContextMenu={(e) => {
                e.preventDefault()
                setMenu({ profileId: p.id, x: e.clientX, y: e.clientY })
              }}>
              <span className="inline-block size-2 rounded-full mr-1.5 align-middle" style={{ background: p.color }} />
              {p.name}
            </button>
            {/* Mute toggle. Hidden by default, revealed on hover. Always visible
                when the profile IS muted so the user can find it again to unmute. */}
            <button
              title={p.muted ? 'Unmute' : 'Mute'}
              aria-label={p.muted ? 'Unmute' : 'Mute'}
              className={`p-1 transition-opacity duration-150 ${
                p.muted
                  ? 'text-zinc-300 hover:text-zinc-100 opacity-100'
                  : 'text-zinc-500 hover:text-zinc-200 opacity-0 group-hover:opacity-100 focus-visible:opacity-100'
              }`}
              onClick={async (e) => {
                e.stopPropagation()
                await client.setProfileMuted(p.id, !p.muted)
                setProfiles(await client.listProfiles())
              }}>
              <BellIcon slashed={p.muted} className="size-3.5" />
            </button>
          </span>
        ))}
        <button className="px-2 py-1 text-xs text-zinc-400 hover:text-zinc-200" title="New profile" onClick={() => setCreating(true)}>+</button>
      </div>
      {creating && <NewProfileDialog onDone={async () => { setCreating(false); setProfiles(await client.listProfiles()) }} onCancel={() => setCreating(false)} />}
      {menu && (
        <div
          role="menu"
          // Stop the document-level mousedown handler from running when clicks
          // land INSIDE the menu — otherwise the menu closes before the
          // <button> below registers its own click.
          onMouseDown={(e) => e.stopPropagation()}
          // Clamp x/y so a right-click near the right or bottom viewport
          // edge can't push the menu offscreen. menu width/height are
          // approximate (160×40); a tighter clamp would need a post-mount
          // measure pass via ref, which isn't worth the extra render here.
          className="fixed z-50 min-w-[160px] rounded border border-zinc-800 bg-zinc-900 shadow-lg py-1 text-xs"
          style={{
            left: Math.min(menu.x, window.innerWidth - 168),
            top: Math.min(menu.y, window.innerHeight - 48),
          }}>
          <button
            type="button"
            onClick={() => onDelete(menu.profileId)}
            className="block w-full text-left px-3 py-1.5 text-rose-400 hover:bg-zinc-800">
            Delete profile…
          </button>
        </div>
      )}
    </>
  )
}
