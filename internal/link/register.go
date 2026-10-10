package link

// RegistryKey is where Windows reads the handler of djinn:// links for the user, in HKEY_CURRENT_USER: no
// administrator rights.
const RegistryKey = `Software\Classes\` + Scheme

// RegistryValue is one string value of the registry under RegistryKey: Key is a subkey ("" for RegistryKey itself),
// Name the value's name ("" for its default value).
type RegistryValue struct {
	Key, Name, Data string
}

// RegistryValues are the values that make exe the handler of djinn:// links on Windows: the URL Protocol value names
// the key a scheme, and its open command runs `djinn open <link>`.
func RegistryValues(exe string) []RegistryValue {
	return []RegistryValue{
		{"", "", "URL:Djinn link"},
		{"", "URL Protocol", ""},
		{"DefaultIcon", "", `"` + exe + `",0`},
		{`shell\open\command`, "", OpenCommand(exe)},
	}
}

// OpenCommand is the command Windows runs for a link: exe open "%1".
func OpenCommand(exe string) string { return `"` + exe + `" open "%1"` }
