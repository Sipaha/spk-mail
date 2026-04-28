//go:build wails && !production

package desktop

// devToolsEnabled controls whether the embedded webview ships with DevTools
// open to F12 / right-click → Inspect. In a development build this is true so
// IPC traffic + DOM state are inspectable. Production builds (built with the
// `production` tag) flip this to false in devtools_prod.go: DevTools in a
// signed release binary are a credible attack surface (browser extensions
// and local malware can read in-memory state, including unwrapped IMAP
// credentials sitting in goroutines).
const devToolsEnabled = true
