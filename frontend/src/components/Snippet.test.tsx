import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import Snippet from './Snippet'

describe('Snippet', () => {
  it('renders sentinel-wrapped text inside <mark>', () => {
    const { container } = render(<Snippet text={'foo \x01bar\x02 baz'} />)
    const marks = container.querySelectorAll('mark')
    expect(marks).toHaveLength(1)
    expect(marks[0].textContent).toBe('bar')
  })

  it('treats raw HTML as text, not markup (defense-in-depth)', () => {
    const { container } = render(<Snippet text={'<script>x</script>'} />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).toContain('<script>')
  })
})
