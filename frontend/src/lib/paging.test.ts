import { describe, it, expect } from 'vitest'
import { PAGE_SIZE, refetchLimit } from './paging'

describe('refetchLimit', () => {
  it('asks for a full page when nothing is loaded yet', () => {
    expect(refetchLimit(0)).toBe(PAGE_SIZE)
  })

  it('never shrinks below one page', () => {
    expect(refetchLimit(PAGE_SIZE - 5)).toBe(PAGE_SIZE)
  })

  it('preserves pages the user loaded via "Load more"', () => {
    // Without this, an SSE-driven refetch would replace the store's list with
    // just the first page, silently discarding threads the user had paged in.
    expect(refetchLimit(PAGE_SIZE * 3)).toBe(PAGE_SIZE * 3)
  })
})
