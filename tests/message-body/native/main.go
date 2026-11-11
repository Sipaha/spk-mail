// A test-owned Wails host for the actual MessageBody component. It proxies the
// local fixture server through the normal wails: asset origin and runtime.
package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"net/http/httputil"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://127.0.0.1:5186")
	app := application.New(application.Options{
		Name:   "MessageBody isolated fixture",
		Assets: application.AssetOptions{Handler: httputil.NewSingleHostReverseProxy(target)},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "MessageBody isolated fixture", Width: 1000, Height: 800,
		URL: "/tests/message-body/?native", Zoom: 1.1,
		BackgroundColour: application.NewRGBA(11, 13, 17, 255),
	})
	app.Run()
}
