package link

import "testing"

func TestRegistryValues(t *testing.T) {
	t.Parallel()
	exe := `C:\Users\me\go\bin\djinn.exe`
	got := map[string]string{}
	for _, v := range RegistryValues(exe) {
		got[v.Key+"|"+v.Name] = v.Data
	}
	want := map[string]string{
		"|":                   "URL:Djinn link",
		"|URL Protocol":       "",
		"DefaultIcon|":        `"C:\Users\me\go\bin\djinn.exe",0`,
		`shell\open\command|`: `"C:\Users\me\go\bin\djinn.exe" open "%1"`,
	}
	if len(got) != len(want) {
		t.Fatalf("values %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if RegistryKey != `Software\Classes\djinn` {
		t.Errorf("key %s", RegistryKey)
	}
}
