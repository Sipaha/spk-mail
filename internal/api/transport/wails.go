//go:build wails

package transport

import "github.com/spk/spk-mail/internal/api"

// Wails is the production-desktop transport. Plan 3 wires it to the Wails v3
// service binder. For plan 1 it is just a placeholder so the build tag exists.
type Wails struct {
	api    api.API
	events *api.Emitter
}

func NewWails(a api.API, em *api.Emitter) *Wails {
	return &Wails{api: a, events: em}
}
