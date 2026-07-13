// How many threads one listThreads page holds. Shared by ThreadList (which
// pages through the list) and the SSE handlers (which refetch it wholesale):
// a refetch that asked for a fixed first page would silently throw away every
// page the user had loaded via "Load more".
export const PAGE_SIZE = 200

// refetchLimit returns how many threads a wholesale refresh must request so it
// preserves what the user currently has loaded. `loaded` is the length of the
// current list.
export function refetchLimit(loaded: number): number {
  return Math.max(PAGE_SIZE, loaded)
}
