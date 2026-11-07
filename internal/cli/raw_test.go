package cli

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TestRaw: a response field marked raw prints as it is, without its name, so that djinn query query tasks --format
// csv pipes a CSV; the fields printed with --json only stay out.
func TestRaw(t *testing.T) {
	t.Parallel()
	d, err := files().FindDescriptorByName("query.v1.QueryServiceQueryResponse")
	if err != nil {
		t.Fatal(err)
	}
	md := d.(protoreflect.MessageDescriptor)
	for _, tt := range []struct{ text, want string }{
		{"code,title\nW1,Core\n", "code,title\nW1,Core\n"},
		{"code  title\n1 row", "code  title\n1 row\n"},
	} {
		m := dynamicpb.NewMessage(md)
		m.Set(md.Fields().ByName("text"), protoreflect.ValueOfString(tt.text))
		m.Set(md.Fields().ByName("arrow"), protoreflect.ValueOfBytes([]byte{1, 2}))
		m.Set(md.Fields().ByName("total"), protoreflect.ValueOfInt64(9))
		var out bytes.Buffer
		writeText(&out, m)
		if out.String() != tt.want {
			t.Errorf("printed %q; want %q", out.String(), tt.want)
		}
	}
}
