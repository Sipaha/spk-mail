export function relative(epoch: number, now = Date.now() / 1000): string {
  const diff = now - epoch
  if (diff < 60) return 'just now'
  if (diff < 3600) return `${Math.floor(diff/60)}m`
  if (diff < 86400) return `${Math.floor(diff/3600)}h`
  if (diff < 7*86400) return `${Math.floor(diff/86400)}d`
  return formatDate(epoch)
}

// formatDate returns DD.MM.YYYY for the given Unix-seconds timestamp,
// independent of the user's OS locale. Used by relative() once a message
// is older than a week, and elsewhere when an absolute date is wanted.
export function formatDate(epoch: number): string {
  const d = new Date(epoch * 1000)
  const dd = String(d.getDate()).padStart(2, '0')
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const yyyy = d.getFullYear()
  return `${dd}.${mm}.${yyyy}`
}
