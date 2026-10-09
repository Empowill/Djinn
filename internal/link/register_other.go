//go:build !windows

package link

// Register does nothing outside Windows: a desktop entry registers the scheme on Linux (tools/icons), Djinn.app's
// Info.plist on macOS (tools/macapp).
func Register(string) error { return nil }

// Registered is true outside Windows: djinn up has nothing to register there.
func Registered() (bool, error) { return true, nil }
