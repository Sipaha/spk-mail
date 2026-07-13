import { GearIcon, PlusIcon } from './icons'

// Pinned chrome at the bottom of the sidebar: the two global destinations.
// Settings used to be reachable ONLY through the system tray menu — if the
// desktop environment doesn't render a tray, accounts could never be managed.
// This footer is the always-present, in-window entry point.
export default function SidebarFooter() {
  const itemClass =
    'flex flex-1 items-center justify-center gap-1.5 rounded px-2 py-1.5 text-xs text-fg-sub hover:bg-ink-800 hover:text-fg'
  return (
    <div className="flex shrink-0 gap-1 border-t border-edge px-2 py-2">
      <a href="#/add-account" className={itemClass}>
        <PlusIcon className="size-3.5" />
        Add account
      </a>
      <a href="#/settings" className={itemClass} aria-label="Settings">
        <GearIcon className="size-3.5" />
        Settings
      </a>
    </div>
  )
}
