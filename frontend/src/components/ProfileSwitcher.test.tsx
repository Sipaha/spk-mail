import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import ProfileSwitcher from './ProfileSwitcher'
import { useStore } from '../store'

vi.mock('../api/client', () => ({
  client: { listProfiles: () => Promise.resolve([
    { id: 1, name: 'Work', color: '#10b981', sort_order: 0 },
    { id: 2, name: 'Personal', color: '#3b82f6', sort_order: 1 },
  ]) }
}))

describe('ProfileSwitcher', () => {
  beforeEach(() => useStore.setState({ profiles: [], activeProfileId: null }))

  it('renders All + per-profile tabs after load', async () => {
    render(<ProfileSwitcher />)
    expect(await screen.findByRole('button', { name: 'All' })).toBeTruthy()
    expect(await screen.findByRole('button', { name: /Work/ })).toBeTruthy()
  })

  it('clicking a profile sets activeProfileId', async () => {
    render(<ProfileSwitcher />)
    fireEvent.click(await screen.findByRole('button', { name: /Work/ }))
    expect(useStore.getState().activeProfileId).toBe(1)
  })
})
