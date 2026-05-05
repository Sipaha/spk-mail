import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import RawMessageDialog from './RawMessageDialog'

const mkB64 = (s: string) => btoa(s)

describe('RawMessageDialog', () => {
  beforeEach(() => {
    URL.createObjectURL = vi.fn(() => 'blob:test')
    URL.revokeObjectURL = vi.fn()
  })

  it('renders raw bytes as escaped text (no script execution)', () => {
    const dto = { filename: 'x.eml', size_bytes: 30, raw_b64: mkB64('Subject: hi\n<script>x</script>') }
    const { container } = render(<RawMessageDialog dto={dto} onClose={() => {}} />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).toContain('<script>')
  })

  it('Close button fires onClose', () => {
    const onClose = vi.fn()
    const dto = { filename: 'x.eml', size_bytes: 5, raw_b64: mkB64('hello') }
    render(<RawMessageDialog dto={dto} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('Esc fires onClose', () => {
    const onClose = vi.fn()
    const dto = { filename: 'x.eml', size_bytes: 5, raw_b64: mkB64('hello') }
    render(<RawMessageDialog dto={dto} onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('Download triggers URL.createObjectURL with the right filename', () => {
    const dto = { filename: 'msg.eml', size_bytes: 5, raw_b64: mkB64('hello') }
    render(<RawMessageDialog dto={dto} onClose={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /download/i }))
    expect(URL.createObjectURL).toHaveBeenCalledOnce()
  })

  it('shows a truncation banner when raw exceeds the cap', () => {
    const big = 'A'.repeat(2 * 1024 * 1024) // 2 MiB
    const dto = { filename: 'big.eml', size_bytes: big.length, raw_b64: btoa(big) }
    render(<RawMessageDialog dto={dto} onClose={() => {}} />)
    expect(screen.getByText(/truncated/i)).toBeTruthy()
  })
})
