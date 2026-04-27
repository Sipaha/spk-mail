export function relative(epoch: number, now = Date.now() / 1000): string {
  const diff = now - epoch
  if (diff < 60) return 'just now'
  if (diff < 3600) return `${Math.floor(diff/60)}m`
  if (diff < 86400) return `${Math.floor(diff/3600)}h`
  if (diff < 7*86400) return `${Math.floor(diff/86400)}d`
  return new Date(epoch * 1000).toLocaleDateString()
}
