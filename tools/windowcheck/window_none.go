//go:build headless

package main

import (
	"context"
	"errors"
	"net/http"
)

// showWindow is unavailable in a headless build: the check needs the native window.
func showWindow(context.Context, string, http.Handler) error {
	return errors.New("this build has no native window: run go tool task check-window")
}
