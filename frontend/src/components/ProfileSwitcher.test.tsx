import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ProfileSwitcher from './ProfileSwitcher'
import { useStore } from '../store'

let mockProfiles = [
  { id: 1, name: 'Work', color: '#10b981', sort_order: 0, muted: false },
  { id: 2, name: 'Personal', color: '#3b82f6', sort_order: 1, muted: false },
]
vi.mock('../api/client', () => ({
  client: {
    listProfiles: () => Promise.resolve(mockProfiles),
    setProfileMuted: (id: number, muted: boolean) => {
      mockProfiles = mockProfiles.map(p => p.id === id ? { ...p, muted } : p)
      return Promise.resolve()
    },
  }
}))

describe('ProfileSwitcher', () => {
  beforeEach(() => {
    mockProfiles = [
      { id: 1, name: 'Work', color: '#10b981', sort_order: 0, muted: false },
      { id: 2, name: 'Personal', color: '#3b82f6', sort_order: 1, muted: false },
    ]
    useStore.setState({ profiles: [], activeProfileId: null })
  })

  it('renders per-profile tabs after load', async () => {
    render(<ProfileSwitcher />)
    expect(await screen.findByRole('button', { name: /Work/ })).toBeTruthy()
  })

  it('auto-selects first profile when none is active', async () => {
    render(<ProfileSwitcher />)
    await screen.findByRole('button', { name: /Work/ })
    // After listProfiles resolves, store should have activeProfileId = 1
    expect(useStore.getState().activeProfileId).toBe(1)
  })

  it('clicking a profile sets activeProfileId', async () => {
    render(<ProfileSwitcher />)
    fireEvent.click(await screen.findByRole('button', { name: /Personal/ }))
    expect(useStore.getState().activeProfileId).toBe(2)
  })

  it('clicking the bell toggles mute on a profile', async () => {
    render(<ProfileSwitcher />)
    // Find the bell button next to Work; bells render with 🔕 emoji when unmuted
    const bells = await screen.findAllByTitle('Mute')
    expect(bells.length).toBeGreaterThan(0)
    fireEvent.click(bells[0])
    // After the click, Work should now be muted; the tab gains opacity-50
    await waitFor(() => {
      const dimmed = document.querySelectorAll('.opacity-50')
      expect(dimmed.length).toBeGreaterThan(0)
    })
  })
})
