// triggerDownload turns a base64 payload into a Blob URL and triggers
// a download via a synthetic <a download>. Works under both http: (CSP
// permits blob:) and Wails wails: protocols.
export function triggerDownload(b64: string, filename: string, mime = 'message/rfc822'): void {
  const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0))
  const blob = new Blob([bytes], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// b64ToString decodes base64 to a JS string of byte chars (each
// codepoint 0..255). Email bytes — atob returns the bytes verbatim
// without trying to interpret as UTF-8, which is what the modal
// wants: render the literal RFC822 source.
export function b64ToString(b64: string): string {
  return atob(b64)
}
