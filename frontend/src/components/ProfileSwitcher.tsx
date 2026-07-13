import { useEffect, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import NewProfileDialog from './NewProfileDialog'
import { BellIcon } from './icons'

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
    `px-2 py-1 text-xs rounded-md ${active
      ? 'bg-ink-800 text-fg'
      : 'text-fg-sub hover:text-fg hover:bg-ink-850'}`

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
      <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-edge px-2 py-2">
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
              <span className="mr-1.5 inline-block size-2 rounded-full align-middle" style={{ background: p.color }} />
              {p.name}
            </button>
            {/* Mute toggle. Hidden by default, revealed on hover. Always visible
                when the profile IS muted so the user can find it again to unmute. */}
            <button
              title={p.muted ? 'Unmute' : 'Mute'}
              aria-label={p.muted ? 'Unmute' : 'Mute'}
              className={`p-1 transition-opacity duration-150 ${
                p.muted
                  ? 'text-fg-sub hover:text-fg opacity-100'
                  : 'text-fg-faint hover:text-fg opacity-0 group-hover:opacity-100 focus-visible:opacity-100'
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
        <button className="px-2 py-1 text-xs text-fg-faint hover:text-fg" title="New profile" onClick={() => setCreating(true)}>+</button>
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
          className="fixed z-50 min-w-[160px] rounded-md border border-edge-strong bg-ink-850 py-1 text-xs shadow-lg"
          style={{
            left: Math.min(menu.x, window.innerWidth - 168),
            top: Math.min(menu.y, window.innerHeight - 48),
          }}>
          <button
            type="button"
            onClick={() => onDelete(menu.profileId)}
            className="block w-full px-3 py-1.5 text-left text-danger hover:bg-ink-800">
            Delete profile…
          </button>
        </div>
      )}
    </>
  )
}
