const nextFrame = () => new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
const frames = () => Array.from(document.querySelectorAll('iframe'))
const check = (value: unknown, message: string) => { if (!value) throw new Error(message) }

function sample() {
  return frames().map(f => {
    const doc = f.contentDocument
    const table = doc?.querySelector('table')
    return {
      height: f.clientHeight, url: doc?.URL, state: doc?.readyState,
      visible: getComputedStyle(f).visibility === 'visible',
      adapted: f.srcdoc.includes(':root{color-scheme:dark}'),
      background: table && f.contentWindow!.getComputedStyle(table).backgroundColor,
      text: doc?.body?.textContent,
    }
  })
}

/** Observe every animation frame, including the initial about:blank document. */
async function observe(count: number, dark = true) {
  const trace = []
  for (let i = 0; i < count; i++) {
    const state = sample()
    trace.push(state)
    for (const f of state) {
      if (!f.visible || !f.height) continue
      check(f.url === 'about:srcdoc', 'Initial about:blank was exposed')
      if (dark && f.adapted) check(f.background !== 'rgb(255, 255, 255)', 'White email background exposed before dark adaptation')
    }
    await nextFrame()
  }
  return trace
}

async function ready(dark = true) {
  const trace = await observe(12, dark)
  const state = sample()
  check(state.length === 7 && state.every(f => f.visible && f.height > 50), 'All seven messages must become readable before image load')
  if (dark) check(state.every(f => f.adapted && f.background !== 'rgb(255, 255, 255)'), 'Final adapted state must be dark')
  return trace
}

export async function runTransitionChecks(checkpoint: (stage: string, evidence: unknown) => Promise<void>) {
  while (!document.getElementById('open')) await nextFrame()
  const evidence: Record<string, unknown> = { agent: navigator.userAgent }
  document.getElementById('open')!.click()
  evidence.firstOpen = await ready()
  check(frames()[0].clientWidth > 700, 'Fixture must include production width utilities')
  check(frames().every(f => f.contentDocument!.readyState !== 'complete'), 'Resource delay must actually hold iframe load')
  await checkpoint('before-load', evidence)

  for (let i = 0; i < 3; i++) {
    document.getElementById('switch')!.click()
    await nextFrame()
  }
  evidence.rapidSwitch = await ready()
  check(frames().every(f => f.contentDocument!.body.textContent!.includes('Message 3-')), 'Only latest thread may be shown')
  document.getElementById('open')!.click()
  await nextFrame()
  check(frames().length === 0, 'Closing must unmount messages')
  document.getElementById('open')!.click()
  evidence.reopen = await ready()

  const first = () => frames()[0]
  const toggle = () => document.querySelector('section button')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  const reservedHeight = first().clientHeight
  toggle()
  await nextFrame()
  if (getComputedStyle(first()).visibility === 'hidden') {
    check(first().clientHeight === reservedHeight, 'Mode reload must reserve the previous height')
  }
  evidence.original = await ready(false)
  check(first().contentWindow!.getComputedStyle(first().contentDocument!.querySelector('table')!).backgroundColor === 'rgb(255, 255, 255)', 'Original must preserve authored white background')
  await checkpoint('original', sample())
  toggle()
  evidence.backToDark = await ready()

  // Preserve routing, selection, quotes and wheel forwarding after repeated loads.
  const opened: string[] = []
  const originalOpen = window.open
  window.open = ((url: string) => { opened.push(url); return null }) as typeof window.open
  try {
    const a = first().contentDocument!.querySelector('a')!
    check(!a.hasAttribute('href'), 'External anchors must be disarmed before reveal')
    if (location.protocol === 'wails:') {
      const frameRect = first().getBoundingClientRect()
      const anchorRect = a.getBoundingClientRect()
      await checkpoint('native-link', { href: a.getAttribute('data-spk-href'), x: frameRect.x + anchorRect.x + anchorRect.width / 2, y: frameRect.y + anchorRect.y + anchorRect.height / 2 })
      check(opened.length === 0, 'Native routing should use Browser.OpenURL')
    } else {
      a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
      check(opened.length === 1 && opened[0] === 'https://example.test/link', 'Link must route once through openExternal')
    }
  } finally { window.open = originalOpen }
  const doc = first().contentDocument!
  const range = doc.createRange()
  range.selectNodeContents(doc.querySelector('blockquote')!)
  const selection = first().contentWindow!.getSelection()!
  selection.removeAllRanges(); selection.addRange(range)
  check(selection.toString() === 'Quoted text', 'Email text must remain selectable')
  const main = document.querySelector('main')!
  main.scrollTop = 0
  doc.dispatchEvent(new WheelEvent('wheel', { deltaY: 3, deltaMode: 1, bubbles: true, cancelable: true }))
  check(main.scrollTop === 48, 'Wheel line deltas must forward exactly once')
  main.scrollTop = 0
  if (location.protocol === 'wails:') {
    const rect = first().getBoundingClientRect()
    await checkpoint('native-wheel', { x: rect.x + 50, y: rect.y + 50 })
    check(main.scrollTop > 0, 'Trusted native wheel must scroll the parent')
    main.scrollTop = 0
  }

  // The initial load must not be the only opportunity to size the document.
  const before = first().clientHeight
  // Separate pool: all normal-origin connections may be occupied by images.
  await fetch('http://localhost:5186/release')
  await observe(12)
  check(first().contentDocument!.readyState === 'complete', 'Delayed image must finish loading')
  check(first().clientHeight > before + 100, 'Iframe must grow after delayed image load')
  const img = first().contentDocument!.querySelector('img')!
  img.style.height = '300px'
  await observe(6)
  check(first().clientHeight >= before + 250, 'Sizing must follow later content changes')
  img.style.height = '20px'
  await observe(6)
  check(first().clientHeight < before + 50, 'Iframe must also shrink after content changes')
  evidence.afterResources = sample()
  await checkpoint('after-resources', evidence.afterResources)
  const paragraph = first().contentDocument!.createElement('p')
  paragraph.textContent = 'Wrapped long email text '.repeat(40)
  first().contentDocument!.body.append(paragraph)
  await observe(6)
  const wide = first().clientHeight
  main.style.width = '350px'
  await observe(6)
  check(first().clientHeight > wide, 'Width changes must remeasure wrapped email text')
  main.style.width = ''
  paragraph.remove()
  await observe(6)
  document.getElementById('viewport')!.click()
  await observe(12)
  const viewportStart = first().clientHeight
  await observe(20)
  check(first().clientHeight === viewportStart, 'Viewport-relative mail must not cause an iframe resize feedback loop')
  evidence.viewportHeight = sample()
  await checkpoint('viewport-height', evidence.viewportHeight)
  return evidence
}
