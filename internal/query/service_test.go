package query

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/descriptorpb"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
	"github.com/empowill/djinn/gen/go/query/v1/queryv1connect"
)

// route serves QueryService on the frozen set, and gives a client of it.
func route(t *testing.T) queryv1connect.QueryServiceClient {
	t.Helper()
	path, h, err := Handler(openFrozen(t))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return queryv1connect.NewQueryServiceClient(srv.Client(), srv.URL)
}

func TestRoute(t *testing.T) {
	t.Parallel()
	client := route(t)
	tasks := queryv1.Collection_COLLECTION_TASKS

	t.Run("a table in text by default, its usual columns", func(t *testing.T) {
		t.Parallel()
		res, err := client.Query(t.Context(), connect.NewRequest(&queryv1.QueryServiceQueryRequest{
			Collection: tasks, Filter: []string{"wish_id->title=stats"}, Sort: []string{"-code"}}))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(res.Msg.GetText()), "\n")
		if len(lines) != 4 || !strings.HasPrefix(lines[0], "code  title") || !strings.HasPrefix(lines[1], "W2") ||
			lines[3] != "2 rows" {
			t.Errorf("text:\n%s", res.Msg.GetText())
		}
		if res.Msg.GetTotal() != 2 || len(res.Msg.GetArrow()) != 0 {
			t.Errorf("total %d, arrow %d bytes; want 2, none", res.Msg.GetTotal(), len(res.Msg.GetArrow()))
		}
	})

	t.Run("csv", func(t *testing.T) {
		t.Parallel()
		res, err := client.Query(t.Context(), connect.NewRequest(&queryv1.QueryServiceQueryRequest{
			Collection: queryv1.Collection_COLLECTION_QUESTIONS, Select: []string{"code", "answer::delay"},
			Format: queryv1.Format_FORMAT_CSV}))
		if err != nil {
			t.Fatal(err)
		}
		if want := "code,answer::delay\nQ1,1h\nQ2,\n"; res.Msg.GetText() != want {
			t.Errorf("csv %q; want %q", res.Msg.GetText(), want)
		}
	})

	t.Run("arrow, aggregated, limited", func(t *testing.T) {
		t.Parallel()
		res, err := client.Query(t.Context(), connect.NewRequest(&queryv1.QueryServiceQueryRequest{
			Collection: tasks, GroupBy: []string{"wish_id->title"}, Measure: []string{"n=count", "usage.cost_usd::sum"},
			Sort: []string{"-n"}, Limit: 1, Format: queryv1.Format_FORMAT_ARROW}))
		if err != nil {
			t.Fatal(err)
		}
		if res.Msg.GetText() != "" || res.Msg.GetTotal() != 3 {
			t.Errorf("text %q, total %d; want none, 3 groups", res.Msg.GetText(), res.Msg.GetTotal())
		}
		rec := readArrow(t, res.Msg.GetArrow())
		if rec.NumRows() != 1 || rec.Schema().Field(0).Name != "wish_id->title" || rec.Schema().Field(1).Name != "n" {
			t.Errorf("record %v", rec)
		}
	})

	for _, tt := range []struct {
		name string
		req  *queryv1.QueryServiceQueryRequest
		want string
	}{
		{"no collection", &queryv1.QueryServiceQueryRequest{}, "collection"},
		{"a filter without operator", &queryv1.QueryServiceQueryRequest{Collection: tasks, Filter: []string{"status"}},
			"no operator"},
		{"an unknown ref", &queryv1.QueryServiceQueryRequest{Collection: tasks, Select: []string{"nope"}}, "no field nope"},
		{"a hidden ref", &queryv1.QueryServiceQueryRequest{Collection: tasks, Filter: []string{"prompt*=x"}}, "hidden"},
		{"a value of the wrong type", &queryv1.QueryServiceQueryRequest{Collection: tasks,
			Filter: []string{"running>soon"}}, "is not a duration"},
		{"an unknown function", &queryv1.QueryServiceQueryRequest{Collection: tasks, Measure: []string{"running::mean"}},
			"no function"},
		{"bins on a text", &queryv1.QueryServiceQueryRequest{Collection: tasks, GroupBy: []string{"code::bins(a,b)"},
			Measure: []string{"count"}}, "has no bins"},
		{"a negative limit", &queryv1.QueryServiceQueryRequest{Collection: tasks, Limit: -1}, "limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.Query(t.Context(), connect.NewRequest(tt.req))
			var cerr *connect.Error
			if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v; want InvalidArgument with %q", err, tt.want)
			}
		})
	}
}

func TestRouteSchema(t *testing.T) {
	t.Parallel()
	client := route(t)
	res, err := client.Schema(t.Context(), connect.NewRequest(&queryv1.QueryServiceSchemaRequest{
		Collection: queryv1.Collection_COLLECTION_TASKS}))
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string]*queryv1.QueryColumn{}
	var refs []string
	for _, c := range res.Msg.GetColumns() {
		cols[c.GetRef()] = c
		refs = append(refs, c.GetRef())
	}
	if _, ok := cols["prompt"]; ok {
		t.Error("the hidden prompt is a column")
	}
	if c := cols["prompt::size"]; c == nil || c.GetUnit() != "tokens" ||
		c.GetType() != queryv1.ColumnType_COLUMN_TYPE_INT || !strings.Contains(c.GetDoc(), "tokens") ||
		refs[len(refs)-1] != "integration::delay" {
		t.Errorf("prompt::size = %v; derived refs last: %v", c, refs[len(refs)-3:])
	}
	if c := cols["wish_id"]; c == nil || c.GetJump() != queryv1.Collection_COLLECTION_WISHES {
		t.Errorf("wish_id = %v; want a jump to WISHES", c)
	}
	if c := cols["wait_question_id"]; c == nil || c.GetJump() != queryv1.Collection_COLLECTION_QUESTIONS {
		t.Errorf("wait_question_id = %v; want a jump to QUESTIONS", c)
	}
	if c := cols["status"]; c == nil || c.GetType() != queryv1.ColumnType_COLUMN_TYPE_ENUM ||
		!slices.Contains(c.GetValues(), "TASK_STATUS_RUNNING") || slices.Contains(c.GetValues(), "TASK_STATUS_UNSPECIFIED") {
		t.Errorf("status = %v", c)
	}
	// The comments come from the embedded descriptors: the compiled ones have none.
	if c := cols["usage.cost_usd"]; c == nil || c.GetUnit() != "usd" || c.GetDoc() == "" ||
		c.GetLabel() != "Usage cost usd" {
		t.Errorf("usage.cost_usd = %v", c)
	}
	if c := cols["running"]; c == nil || c.GetType() != queryv1.ColumnType_COLUMN_TYPE_DURATION {
		t.Errorf("running = %v", c)
	}
}

// TestColumnTypes keeps the proto's ColumnType on Type's numbers, which Schema converts as they are.
func TestColumnTypes(t *testing.T) {
	t.Parallel()
	for typ := TypeString; typ <= TypeDuration; typ++ {
		name := queryv1.ColumnType(typ).String()
		if want := "COLUMN_TYPE_" + strings.ToUpper(typ.String()); name != want {
			t.Errorf("ColumnType(%d) = %s; want %s", typ, name, want)
		}
	}
}

// TestCollectionsMatch keeps the catalog and the Collection enum together: each value names a collection, and the
// methods only read.
func TestCollectionsMatch(t *testing.T) {
	t.Parallel()
	c, err := Collections(openFrozen(t))
	if err != nil {
		t.Fatal(err)
	}
	values := queryv1.Collection(0).Descriptor().Values()
	for i := 1; i < values.Len(); i++ {
		if _, err := c.Schema(collection(queryv1.Collection(values.Get(i).Number()))); err != nil {
			t.Errorf("%s: %v", values.Get(i).Name(), err)
		}
	}
	if len(c.byName) != values.Len()-1 {
		t.Errorf("%d collections; the enum names %d", len(c.byName), values.Len()-1)
	}
	methods := queryv1.File_query_v1_query_proto.Services().ByName("QueryService").Methods()
	for i := range methods.Len() {
		opts, _ := methods.Get(i).Options().(*descriptorpb.MethodOptions)
		if opts.GetIdempotencyLevel() != descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
			t.Errorf("%s does not say NO_SIDE_EFFECTS", methods.Get(i).Name())
		}
	}
}
