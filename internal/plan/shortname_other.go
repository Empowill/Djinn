//go:build !windows

package plan

// systemShortName finds no short name: only Windows gives a folder one.
func systemShortName(string) (string, bool) { return "", false }
