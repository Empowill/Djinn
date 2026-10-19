//go:build headless

package main

import (
	"context"
	"strings"
	"testing"
)

// TestShowWindow: a headless build opens no window, and says how to get one.
func TestShowWindow(t *testing.T) {
	t.Parallel()
	if err := showWindow(context.Background(), "/", nil); err == nil || !strings.Contains(err.Error(), "check-window") {
		t.Fatalf("showWindow = %v, want the native build named", err)
	}
}
