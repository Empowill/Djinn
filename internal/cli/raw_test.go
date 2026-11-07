package cli

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
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

// TestTextFormsAndClientDefault: djinn query reads each filter, sort, group and measure from one text into its typed
// message, on the caller's side, a filter typed by its ref's type from the schema the server gives, read once; and it
// asks a text table unless told another format, where the window, which sends none, reads Arrow.
func TestTextFormsAndClientDefault(t *testing.T) {
	t.Parallel()
	d, err := files().FindDescriptorByName("query.v1.QueryServiceQueryRequest")
	if err != nil {
		t.Fatal(err)
	}
	md := d.(protoreflect.MessageDescriptor)
	req, texts, err := parse(md, []string{"tasks", "--filter", "status=running", "--filter", "running>=1h",
		"--sort", "-code", "--group-by", "running::bins(0s,1m)", "--measure", "n=count"})
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 5 {
		t.Fatalf("%d texts set apart; want 5", len(texts))
	}
	fromEnv(req, func(string) string { return "" })
	var asked []protoreflect.FullName
	schema := func(_ context.Context, method protoreflect.FullName, _, out proto.Message) error {
		asked = append(asked, method)
		out.(*queryv1.QueryServiceSchemaResponse).Columns = []*queryv1.QueryColumn{
			{Ref: "status", Type: queryv1.ColumnType_COLUMN_TYPE_ENUM},
			{Ref: "running", Type: queryv1.ColumnType_COLUMN_TYPE_DURATION},
		}
		return nil
	}
	if err := readTexts(t.Context(), req, texts, schema); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "query.v1.QueryService.Schema" {
		t.Errorf("asked %v; want the tasks' schema once", asked)
	}
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var typed queryv1.QueryServiceQueryRequest
	if err := proto.Unmarshal(b, &typed); err != nil {
		t.Fatal(err)
	}
	if d := typed.GetFilter()[1].GetDuration(); d.GetRef() != "running" || d.GetValue().AsDuration() != time.Hour ||
		d.GetOp() != queryv1.CompareOp_COMPARE_OP_GREATER_THAN_EQUAL {
		t.Errorf("filter %v; want a duration comparison", typed.GetFilter()[1])
	}
	if o := typed.GetSort()[0]; o.GetRef() != "code" || !o.GetDesc() {
		t.Errorf("sort %v", o)
	}
	if g := typed.GetGroupBy()[0]; len(g.GetDurationBins().GetBounds()) != 2 {
		t.Errorf("group %v; want duration bins", g)
	}
	if m := typed.GetMeasure()[0]; m.GetName() != "n" || m.GetFn() != queryv1.MeasureFn_MEASURE_FN_COUNT {
		t.Errorf("measure %v", m)
	}
	if typed.GetFormat() != queryv1.Format_FORMAT_TEXT {
		t.Errorf("format %v; want text", typed.GetFormat())
	}
	if got := format(req); !slices.Contains(got, "running>=1h0m0s") || !slices.Contains(got, "-code") {
		t.Errorf("format = %q; want the texts back", got)
	}
	csv, _, err := parse(md, []string{"tasks", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	fromEnv(csv, func(string) string { return "" })
	if got := csv.Get(md.Fields().ByName("format")).Enum(); got != 2 {
		t.Errorf("format %d; want FORMAT_CSV (2), as asked", got)
	}
}
