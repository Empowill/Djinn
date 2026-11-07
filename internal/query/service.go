package query

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/empowill/djinn/gen"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
	"github.com/empowill/djinn/gen/go/query/v1/queryv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Collections are the collections QueryService reads, by the name its Collection enum gives each (COLLECTION_TASKS
// is TASKS), with their usual columns and their derived refs. The tasks come with their times, as of now (Timed).
func Collections(r store.Reader, now func() time.Time) (*Catalog, error) {
	type entry struct {
		name     string
		m        proto.Message
		defaults []string
		derived  []Derived
	}
	var cols []Collection
	for _, e := range []entry{
		{"TASKS", &planv1.Task{}, []string{"code", "title", "status", "kind", "provider", "create_time", "end_time",
			"running", "usage.cost_usd", "wish_id->title"}, TaskRefs},
		{"QUESTIONS", &planv1.Question{}, []string{"code", "text", "create_time", "answer.choice", "answer.create_time",
			"answer::delay", "wish_id->title"}, QuestionRefs},
		{"WISHES", &planv1.Wish{}, []string{"title", "state", "create_time", "grant_time"}, nil},
		{"PROJECTS", &planv1.Project{}, []string{"name", "directory", "create_time"}, nil},
	} {
		src, err := FromStore(r, e.m)
		if err != nil {
			return nil, err
		}
		if _, ok := e.m.(*planv1.Task); ok {
			src = Timed(r, src, now)
		}
		cols = append(cols, Collection{Name: e.name, Source: src, Defaults: e.defaults, Derived: e.derived})
	}
	return NewCatalog(cols...)
}

// Handler serves QueryService on the store r. Every method only reads: a worker calls them as the developer does.
func Handler(r store.Reader) (string, http.Handler, error) {
	c, err := Collections(r, time.Now)
	if err != nil {
		return "", nil, err
	}
	path, h := queryv1connect.NewQueryServiceHandler(&service{cat: c}, connect.WithInterceptors(plan.Validate))
	return path, h, nil
}

type service struct {
	queryv1connect.UnimplementedQueryServiceHandler
	cat *Catalog
}

// collection is the name of the catalog's collection c names.
func collection(c queryv1.Collection) string {
	return strings.TrimPrefix(c.String(), "COLLECTION_")
}

// Request turns a QueryService request, its typed messages, into a Query. Its errors wrap ErrInvalid.
func Request(req *queryv1.QueryServiceQueryRequest) (Query, error) {
	q := Query{Collection: collection(req.GetCollection()), Selects: req.GetSelect(), Limit: int(req.GetLimit())}
	for _, f := range req.GetFilter() {
		filter, err := filterOf(f)
		if err != nil {
			return Query{}, err
		}
		q.Filters = append(q.Filters, filter)
	}
	for _, o := range req.GetSort() {
		srt := Sort{Ref: o.GetRef(), Desc: o.GetDesc()}
		if srt.Ref == "" {
			return Query{}, fmt.Errorf("%w: a sort without ref", ErrInvalid)
		}
		q.Sorts = append(q.Sorts, srt)
	}
	if len(req.GetGroupBy()) > 0 || len(req.GetMeasure()) > 0 {
		q.Aggregation = &Aggregation{}
		for _, g := range req.GetGroupBy() {
			by, err := groupOf(g)
			if err != nil {
				return Query{}, err
			}
			q.Aggregation.By = append(q.Aggregation.By, by)
		}
		for _, m := range req.GetMeasure() {
			measure, err := measureOf(m)
			if err != nil {
				return Query{}, err
			}
			q.Aggregation.Measures = append(q.Aggregation.Measures, measure)
		}
	}
	return q, nil
}

// filterOf is the Filter f says, each comparison bound to the types of ref it may read.
func filterOf(f *queryv1.Filter) (Filter, error) {
	switch w := f.GetWhere().(type) {
	case *queryv1.Filter_Group:
		g := Group{Op: And}
		switch w.Group.GetOp() {
		case queryv1.FilterGroupOp_FILTER_GROUP_OP_OR:
			g.Op = Or
		case queryv1.FilterGroupOp_FILTER_GROUP_OP_NOT:
			g.Op = Not
		}
		for _, sub := range w.Group.GetFilters() {
			filter, err := filterOf(sub)
			if err != nil {
				return nil, err
			}
			g.Filters = append(g.Filters, filter)
		}
		return g, nil
	case *queryv1.Filter_Null:
		return IsNull{Ref: w.Null.GetRef()}, nil
	case *queryv1.Filter_Bool:
		return bind(w.Bool.GetRef(), "bool", Equal{Ref: w.Bool.GetRef(), Value: w.Bool.GetValue()}, TypeBool), nil
	case *queryv1.Filter_String_:
		s := w.String_
		var filter Filter = Equal{Ref: s.GetRef(), Value: s.GetValue()}
		switch s.GetOp() {
		case queryv1.StringOp_STRING_OP_CONTAINS:
			filter = Text{Ref: s.GetRef(), Contains: s.GetValue()}
		case queryv1.StringOp_STRING_OP_BEGINS_WITH:
			filter = Text{Ref: s.GetRef(), BeginsWith: s.GetValue()}
		}
		return bind(s.GetRef(), "string", filter, TypeString, TypeEnum), nil
	case *queryv1.Filter_StringIn:
		s := w.StringIn
		in := In{Ref: s.GetRef(), IncludeNull: s.GetIncludeNull()}
		for _, v := range s.GetValues() {
			in.Values = append(in.Values, v)
		}
		if len(in.Values) == 0 && !in.IncludeNull {
			return nil, fmt.Errorf("%w: string_in on %s: no value", ErrInvalid, s.GetRef())
		}
		return bind(s.GetRef(), "string_in", in, TypeString, TypeEnum), nil
	case *queryv1.Filter_Number:
		n := w.Number
		return bind(n.GetRef(), "number", comparison(n.GetRef(), n.GetOp(), n.GetValue()), TypeInt, TypeFloat), nil
	case *queryv1.Filter_Time:
		t := w.Time
		return bind(t.GetRef(), "time", comparison(t.GetRef(), t.GetOp(), t.GetValue().AsTime()), TypeTime), nil
	case *queryv1.Filter_Duration:
		d := w.Duration
		return bind(d.GetRef(), "duration", comparison(d.GetRef(), d.GetOp(), d.GetValue().AsDuration()), TypeDuration), nil
	}
	return nil, fmt.Errorf("%w: an empty filter", ErrInvalid)
}

// comparison is the filter of a number, a time or a duration comparison.
func comparison(ref string, op queryv1.CompareOp, v any) Filter {
	switch op {
	case queryv1.CompareOp_COMPARE_OP_GREATER_THAN:
		return Range{Ref: ref, Gt: v}
	case queryv1.CompareOp_COMPARE_OP_GREATER_THAN_EQUAL:
		return Range{Ref: ref, Gte: v}
	case queryv1.CompareOp_COMPARE_OP_LESS_THAN:
		return Range{Ref: ref, Lt: v}
	case queryv1.CompareOp_COMPARE_OP_LESS_THAN_EQUAL:
		return Range{Ref: ref, Lte: v}
	}
	return Equal{Ref: ref, Value: v}
}

// groupOf is the GroupBy g says.
func groupOf(g *queryv1.GroupBy) (GroupBy, error) {
	by := GroupBy{Ref: g.GetRef()}
	if by.Ref == "" {
		return GroupBy{}, fmt.Errorf("%w: a group without ref", ErrInvalid)
	}
	switch b := g.GetBucket().(type) {
	case *queryv1.GroupBy_Period:
		by.Granularity = map[queryv1.Period]Granularity{
			queryv1.Period_PERIOD_DAY: Day, queryv1.Period_PERIOD_WEEK: Week, queryv1.Period_PERIOD_MONTH: Month,
		}[b.Period]
	case *queryv1.GroupBy_NumberBins:
		for _, v := range b.NumberBins.GetBounds() {
			by.Bounds = append(by.Bounds, v)
		}
	case *queryv1.GroupBy_DurationBins:
		for _, v := range b.DurationBins.GetBounds() {
			by.Bounds = append(by.Bounds, v.AsDuration())
		}
	}
	return by, nil
}

// measureOf is the Measure m says.
func measureOf(m *queryv1.Measure) (Measure, error) {
	measure := Measure{Ref: m.GetRef(), Fn: Count, Alias: m.GetName()}
	if fn := m.GetFn(); fn != queryv1.MeasureFn_MEASURE_FN_UNSPECIFIED {
		measure.Fn = Fn(fn) // MeasureFn numbers its functions as Fn does.
	}
	return measure, nil
}

// status is the Connect error of err: InvalidArgument for a query that cannot run as written.
func status(err error) error {
	if errors.Is(err, ErrInvalid) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func (s *service) Query(
	ctx context.Context, req *connect.Request[queryv1.QueryServiceQueryRequest],
) (*connect.Response[queryv1.QueryServiceQueryResponse], error) {
	q, err := Request(req.Msg)
	if err != nil {
		return nil, status(err)
	}
	t, err := s.cat.Run(ctx, q)
	if err != nil {
		return nil, status(err)
	}
	res := &queryv1.QueryServiceQueryResponse{Total: int64(t.Total)}
	var b bytes.Buffer
	switch req.Msg.GetFormat() {
	case queryv1.Format_FORMAT_CSV:
		err = t.WriteCSV(&b)
		res.Table = b.Bytes()
	case queryv1.Format_FORMAT_TEXT:
		err = t.WriteText(&b)
		res.Table = b.Bytes()
	default:
		res.Table, err = t.Arrow()
	}
	if err != nil {
		return nil, status(err)
	}
	return connect.NewResponse(res), nil
}

func (s *service) Schema(
	_ context.Context, req *connect.Request[queryv1.QueryServiceSchemaRequest],
) (*connect.Response[queryv1.QueryServiceSchemaResponse], error) {
	cols, err := s.cat.Schema(collection(req.Msg.GetCollection()))
	if err != nil {
		return nil, status(err)
	}
	res := &queryv1.QueryServiceSchemaResponse{}
	for _, c := range cols {
		res.Columns = append(res.Columns, s.cat.queryColumn(c))
	}
	return connect.NewResponse(res), nil
}

// doc is the comment of fd in the protos of api/, on one line: the descriptors compiled in have none.
func doc(fd protoreflect.FieldDescriptor) string {
	d, err := commented().FindDescriptorByName(fd.FullName())
	if err != nil {
		return ""
	}
	c := d.ParentFile().SourceLocations().ByDescriptor(d).LeadingComments
	return strings.Join(strings.Fields(c), " ")
}

// commented are the protos of api/ with their comments, read from the descriptors embedded for the command line.
var commented = sync.OnceValue(func() *protoregistry.Files {
	files := new(protoregistry.Files)
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(gen.Descriptors, &set); err != nil {
		return files
	}
	for _, fdp := range set.GetFile() {
		fd, err := protodesc.NewFile(fdp, imports{files})
		if err == nil {
			_ = files.RegisterFile(fd)
		}
	}
	return files
})

// imports finds a file among local, else among the ones compiled in: the embedded descriptors leave out imports.
type imports struct{ local *protoregistry.Files }

func (r imports) FindFileByPath(path string) (protoreflect.FileDescriptor, error) {
	if fd, err := r.local.FindFileByPath(path); err == nil {
		return fd, nil
	}
	return protoregistry.GlobalFiles.FindFileByPath(path)
}

func (r imports) FindDescriptorByName(name protoreflect.FullName) (protoreflect.Descriptor, error) {
	if d, err := r.local.FindDescriptorByName(name); err == nil {
		return d, nil
	}
	return protoregistry.GlobalFiles.FindDescriptorByName(name)
}

// queryColumn is col as QueryService.Schema gives it: its type, its enum's values, the collection it jumps to.
func (cat *Catalog) queryColumn(c Column) *queryv1.QueryColumn {
	qc := &queryv1.QueryColumn{Ref: c.Ref, Label: Label(c), Type: queryv1.ColumnType(c.Type), List: c.List,
		Unit: c.Unit, Doc: c.Doc}
	if c.Field != nil {
		qc.Doc = doc(c.Field)
	}
	if c.Type == TypeEnum && c.Enum != nil {
		values := c.Enum.Values()
		for i := range values.Len() {
			if v := values.Get(i); v.Number() != 0 {
				qc.Values = append(qc.Values, string(v.Name()))
			}
		}
	}
	if c.Jump != "" {
		for name, src := range cat.byName {
			if src.Source.Descriptor().FullName() == c.Jump {
				qc.Jump = queryv1.Collection(queryv1.Collection_value["COLLECTION_"+name])
			}
		}
	}
	return qc
}
