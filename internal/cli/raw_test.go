package cli

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TestRaw: a response field marked raw prints as it is, without its name, so that djinn query query tasks --format
// csv pipes a CSV; the fields printed with --json only stay out. djinn mcp returns the same text, and the JSON of the
// response when the bytes are not text, as Arrow is.
func TestRaw(t *testing.T) {
	t.Parallel()
	d, err := files().FindDescriptorByName("query.v1.QueryServiceQueryResponse")
	if err != nil {
		t.Fatal(err)
	}
	md := d.(protoreflect.MessageDescriptor)
	for _, tt := range []struct{ table, want string }{
		{"code,title\nW1,Core\n", "code,title\nW1,Core\n"},
		{"code  title\n1 row", "code  title\n1 row\n"},
	} {
		m := dynamicpb.NewMessage(md)
		m.Set(md.Fields().ByName("table"), protoreflect.ValueOfBytes([]byte(tt.table)))
		m.Set(md.Fields().ByName("total"), protoreflect.ValueOfInt64(9))
		var out bytes.Buffer
		writeText(&out, m)
		if out.String() != tt.want {
			t.Errorf("printed %q; want %q", out.String(), tt.want)
		}
		if got, ok := rawText(m); !ok || got != tt.table {
			t.Errorf("mcp text %q, %v; want %q", got, ok, tt.table)
		}
	}
	m := dynamicpb.NewMessage(md)
	m.Set(md.Fields().ByName("table"), protoreflect.ValueOfBytes([]byte{0xff, 0xfe, 0}))
	if _, ok := rawText(m); ok {
		t.Error("bytes that are not UTF-8 came back as text")
	}
}

// TestTextFieldAndClientDefault: djinn query fills each filter, sort, group and measure from one text, into the
// field marked (djinn.v1.text), and asks a text table unless told another format; the window, which sends none,
// reads Arrow.
func TestTextFieldAndClientDefault(t *testing.T) {
	t.Parallel()
	d, err := files().FindDescriptorByName("query.v1.QueryServiceQueryRequest")
	if err != nil {
		t.Fatal(err)
	}
	md := d.(protoreflect.MessageDescriptor)
	req, err := parse(md, []string{"tasks", "--filter", "status=running", "--filter", "type=work",
		"--sort", "-code", "--group-by", "create_time::day", "--measure", "n=count"})
	if err != nil {
		t.Fatal(err)
	}
	fromEnv(req, func(string) string { return "" })
	filters := req.Get(md.Fields().ByName("filter")).List()
	if filters.Len() != 2 {
		t.Fatalf("%d filters; want 2", filters.Len())
	}
	f := filters.Get(1).Message()
	if got := f.Get(f.Descriptor().Fields().ByName("text")).String(); got != "type=work" {
		t.Errorf("filter text %q; want type=work", got)
	}
	if got := text(md.Fields().ByName("sort"), req.Get(md.Fields().ByName("sort")).List().Get(0)); got != "-code" {
		t.Errorf("sort read back %q; want -code", got)
	}
	if got := req.Get(md.Fields().ByName("format")).Enum(); got != 3 {
		t.Errorf("format %d; want FORMAT_TEXT (3)", got)
	}
	csv, err := parse(md, []string{"tasks", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	fromEnv(csv, func(string) string { return "" })
	if got := csv.Get(md.Fields().ByName("format")).Enum(); got != 2 {
		t.Errorf("format %d; want FORMAT_CSV (2), as asked", got)
	}
}
