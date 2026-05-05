import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import MessageMenu from './MessageMenu'

describe('MessageMenu', () => {
  it('opens on trigger click and lists actions', () => {
    render(<MessageMenu onViewRaw={() => {}} onDownloadEml={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /message actions/i }))
    expect(screen.getByText(/view raw/i)).toBeTruthy()
    expect(screen.getByText(/download .eml/i)).toBeTruthy()
  })

  it('fires onViewRaw and closes the menu', () => {
    const onViewRaw = vi.fn()
    render(<MessageMenu onViewRaw={onViewRaw} onDownloadEml={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /message actions/i }))
    fireEvent.click(screen.getByText(/view raw/i))
    expect(onViewRaw).toHaveBeenCalledOnce()
    expect(screen.queryByText(/view raw/i)).toBeNull()
  })

  it('closes on Escape', () => {
    render(<MessageMenu onViewRaw={() => {}} onDownloadEml={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /message actions/i }))
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByText(/view raw/i)).toBeNull()
  })

  it('closes on outside click', () => {
    render(
      <div>
        <span data-testid="outside">outside</span>
        <MessageMenu onViewRaw={() => {}} onDownloadEml={() => {}} />
      </div>,
    )
    fireEvent.click(screen.getByRole('button', { name: /message actions/i }))
    fireEvent.mouseDown(screen.getByTestId('outside'))
    expect(screen.queryByText(/view raw/i)).toBeNull()
  })
})
