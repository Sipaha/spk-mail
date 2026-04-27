//go:build wails

// Package desktop launches the Wails application: a single window loading the
// embedded React UI and an event bus that mirrors api.Emitter into the
// frontend's CallByName/Events.On surface.
package desktop

import (
	"context"
	"io/fs"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/transport"
)

// Options bundles the dependencies the desktop runner needs.
type Options struct {
	FrontendFS fs.FS
	API        api.API
	Emitter    *api.Emitter
	IconPNG    []byte
}

// Run starts the Wails event loop. Returns when the user quits via the tray
// menu (plan 4 wires the tray; plan 3 returns when the window is closed and
// the OS quits the app on last-window-close).
func Run(ctx context.Context, opts Options) error {
	svc := transport.NewWails(opts.API, opts.Emitter)

	app := application.New(application.Options{
		Name:        "spk-mail",
		Description: "Linux desktop email client (IMAP)",
		Icon:        opts.IconPNG,
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(opts.FrontendFS),
		},
	})

	// Bridge api.Emitter -> Wails custom events so the frontend can listen
	// via the runtime's Events.On(name, ...) API.
	go func() {
		ch, unsub := opts.Emitter.Subscribe()
		defer unsub()
		for ev := range ch {
			app.Event.Emit(ev.Type, ev.Payload)
		}
	}()

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "spk-mail",
		Width:            1280,
		Height:           800,
		BackgroundColour: application.NewRGBA(10, 10, 10, 255),
		URL:              "/",
	})

	go func() {
		<-ctx.Done()
		app.Quit()
	}()
	return app.Run()
}
