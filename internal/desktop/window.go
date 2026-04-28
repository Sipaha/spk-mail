//go:build wails

// Package desktop launches the Wails application: a single window loading the
// embedded React UI and an event bus that mirrors api.Emitter into the
// frontend's CallByName/Events.On surface.
package desktop

import (
	"context"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/transport"
	"github.com/spk/spk-mail/internal/tray"
)

// Options bundles the dependencies the desktop runner needs.
type Options struct {
	FrontendFS    fs.FS
	API           api.API
	Emitter       *api.Emitter
	IconPNG       []byte
	UnreadIconPNG []byte // optional accent variant; tray uses it when unread > 0
}

// Run starts the Wails event loop. Closing the window hides it instead of
// quitting; the user quits via the tray menu (or ctx cancellation).
func Run(ctx context.Context, opts Options) error {
	svc := transport.NewAPI(opts.API)

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

	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "spk-mail",
		Width:            1280,
		Height:           800,
		BackgroundColour: application.NewRGBA(10, 10, 10, 255),
		URL:              "/",
		// Build-tag gated. Dev builds keep DevTools on for IPC/DOM
		// inspection (see devtools_dev.go); release builds (built with
		// `-tags production`, e.g. via `make release`) flip this off so
		// in-memory state — including unwrapped IMAP credentials living
		// in goroutines — is not inspectable from the embedded webview.
		DevToolsEnabled: devToolsEnabled,
	})

	// Hide the window on close instead of quitting. RegisterHook fires before
	// the default close handler; calling Cancel() suppresses the destroy and
	// Hide() takes the window off-screen — the tray menu is the way back.
	w.RegisterHook(events.Common.WindowClosing, func(ev *application.WindowEvent) {
		ev.Cancel()
		w.Hide()
	})

	if _, err := tray.NewController(app, opts.API, opts.Emitter, opts.IconPNG, opts.UnreadIconPNG, w); err != nil {
		log.Printf("desktop: tray controller unavailable: %v", err)
	}

	go func() {
		<-ctx.Done()
		app.Quit()
	}()
	return app.Run()
}
