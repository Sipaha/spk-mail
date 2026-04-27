import { useEffect, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import NewProfileDialog from './NewProfileDialog'

export default function ProfileSwitcher() {
  const profiles = useStore(s => s.profiles)
  const setProfiles = useStore(s => s.setProfiles)
  const activeProfileId = useStore(s => s.activeProfileId)
  const setActiveProfile = useStore(s => s.setActiveProfile)
  const [creating, setCreating] = useState(false)

  useEffect(() => { client.listProfiles().then(setProfiles) }, [setProfiles])

  const tabClass = (active: boolean) =>
    `px-2 py-1 text-xs rounded-t border-b-2 ${active
      ? 'border-zinc-200 text-zinc-100'
      : 'border-transparent text-zinc-500 hover:text-zinc-300'}`

  return (
    <>
      <div className="flex items-center gap-1 px-3 pt-2 border-b border-zinc-800 overflow-x-auto">
        <button className={tabClass(activeProfileId === null)} onClick={() => setActiveProfile(null)}>All</button>
        {profiles.map(p => (
          <button key={p.id} className={tabClass(activeProfileId === p.id)} onClick={() => setActiveProfile(p.id)}>
            <span className="inline-block size-2 rounded-full mr-1 align-middle" style={{ background: p.color }} />
            {p.name}
          </button>
        ))}
        <button className="px-2 py-1 text-xs text-zinc-400 hover:text-zinc-200" title="New profile" onClick={() => setCreating(true)}>+</button>
      </div>
      {creating && <NewProfileDialog onDone={async () => { setCreating(false); setProfiles(await client.listProfiles()) }} onCancel={() => setCreating(false)} />}
    </>
  )
}
