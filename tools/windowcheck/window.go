//go:build !headless

package main

import (
	"context"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/empowill/djinn/internal/server"
)

// showWindow opens a small native window on url, served by assets through wails:// when assets is not nil, and
// returns once the window is closed or ctx is done.
func showWindow(ctx context.Context, url string, assets http.Handler) error {
	opts := application.Options{
		Name: "Djinn window check",
		Mac:  application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	}
	if assets != nil {
		opts.Assets = application.AssetOptions{Handler: server.WholeWrites(assets)}
	}
	app := application.New(opts)
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Djinn window check", Width: 480, Height: 220, URL: url})
	go func() {
		<-ctx.Done()
		app.Quit()
	}()
	return app.Run()
}
