package link

import (
	"errors"
	"testing"
)

const id = "01a1223a-ae45-728f-8c37-c005eee91edb"

func TestParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw string
		want      Link
	}{
		{"tilasm", "djinn://tilasm/" + id, Link{Tilasm, id}},
		{"wish", "djinn://wish/" + id, Link{Wish, id}},
		// The developer's word, any case, and what a browser, a chat or Windows' "%1" may add.
		{"talisman", "djinn://talisman/" + id, Link{Tilasm, id}},
		{"upper case", "DJINN://Tilasm/01A1223A-AE45-728F-8C37-C005EEE91EDB", Link{Tilasm, id}},
		{"trailing slash", "djinn://tilasm/" + id + "/", Link{Tilasm, id}},
		{"spaces query and fragment", "  djinn://wish/" + id + "?from=slack#top\n", Link{Wish, id}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tc.raw)
			if err != nil || got != tc.want {
				t.Errorf("Parse(%q) = %v, %v; want %v", tc.raw, got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, raw string }{
		{"empty", ""},
		{"no kind", "djinn://"},
		{"no id", "djinn://tilasm/"},
		{"a code", "djinn://tilasm/L01"},
		{"extra path", "djinn://tilasm/" + id + "/extra"},
		{"unknown kind", "djinn://task/" + id},
		{"opaque", "djinn:tilasm/" + id},
		{"other scheme", "http://tilasm/" + id},
		{"user", "djinn://user@tilasm/" + id},
		{"port", "djinn://tilasm:80/" + id},
		{"long id", "djinn://tilasm/" + id + "0"},
		{"no host", "djinn:///" + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(tc.raw)
			var unknown UnknownError
			if !errors.As(err, &unknown) || unknown.URL != tc.raw {
				t.Errorf("Parse(%q): %v; want it unknown", tc.raw, err)
			}
		})
	}
}

func TestOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		kind Kind
		id   string
		want string
	}{
		{"upper case id", Tilasm, "01A1223A-AE45-728F-8C37-C005EEE91EDB", "djinn://tilasm/" + id},
		{"wish", Wish, id, "djinn://wish/" + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Of(tc.kind, tc.id); got != tc.want {
				t.Errorf("Of: %s, want %s", got, tc.want)
			}
			// Parse reads it back, and String writes it again the same.
			if l, err := Parse(Of(tc.kind, tc.id)); err != nil || l.String() != tc.want {
				t.Errorf("a link read back: %v, %v", l, err)
			}
		})
	}
}
