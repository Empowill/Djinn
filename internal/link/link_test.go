package link

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	const id = "01a1223a-ae45-728f-8c37-c005eee91edb"
	for _, c := range []struct {
		raw  string
		want Link
	}{
		{"djinn://tilasm/" + id, Link{Tilasm, id}},
		{"djinn://wish/" + id, Link{Wish, id}},
		// The developer's word, any case, and what a browser, a chat or Windows' "%1" may add.
		{"djinn://talisman/" + id, Link{Tilasm, id}},
		{"DJINN://Tilasm/01A1223A-AE45-728F-8C37-C005EEE91EDB", Link{Tilasm, id}},
		{"djinn://tilasm/" + id + "/", Link{Tilasm, id}},
		{"  djinn://wish/" + id + "?from=slack#top\n", Link{Wish, id}},
	} {
		got, err := Parse(c.raw)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []string{
		"", "djinn://", "djinn://tilasm/", "djinn://tilasm/L01", "djinn://tilasm/" + id + "/extra",
		"djinn://task/" + id, "djinn:tilasm/" + id, "http://tilasm/" + id, "djinn://user@tilasm/" + id,
		"djinn://tilasm:80/" + id, "djinn://tilasm/" + id + "0", "djinn:///" + id,
	} {
		_, err := Parse(raw)
		var unknown UnknownError
		if !errors.As(err, &unknown) || unknown.URL != raw {
			t.Errorf("Parse(%q): %v; want it unknown", raw, err)
		}
	}
	if got := Of(Tilasm, "01A1223A-AE45-728F-8C37-C005EEE91EDB"); got != "djinn://tilasm/"+id {
		t.Errorf("Of: %s", got)
	}
	if l, err := Parse(Of(Wish, id)); err != nil || l.String() != "djinn://wish/"+id {
		t.Errorf("a link read back: %v, %v", l, err)
	}
}

func TestRegistryValues(t *testing.T) {
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
