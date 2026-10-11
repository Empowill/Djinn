//go:build windows

package main

import (
	"testing"

	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	if os.Getenv("DJINN_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	testx.Main(m)
}
