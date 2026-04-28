//go:build wails && production

package desktop

// See devtools_dev.go for the rationale. Production builds disable
// DevTools so a release binary doesn't expose its in-memory state to
// inspection at runtime.
const devToolsEnabled = false
