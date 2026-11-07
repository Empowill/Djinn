package query

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

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
// is TASKS), with their usual columns and their derived refs.
func Collections(r store.Reader) (*Catalog, error) {
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
		cols = append(cols, Collection{Name: e.name, Source: src, Defaults: e.defaults, Derived: e.derived})
	}
	return NewCatalog(cols...)
}

// Handler serves QueryService on the store r. Every method only reads: a worker calls them as the developer does.
func Handler(r store.Reader) (string, http.Handler, error) {
	c, err := Collections(r)
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

// Request turns a QueryService request into a Query: its texts read by ParseFilter, ParseSort, ParseGroup and
// ParseMeasure. Its errors wrap ErrInvalid.
func Request(req *queryv1.QueryServiceQueryRequest) (Query, error) {
	q := Query{Collection: collection(req.GetCollection()), Selects: req.GetSelect(), Limit: int(req.GetLimit())}
	for _, s := range req.GetFilter() {
		f, err := ParseFilter(s)
		if err != nil {
			return Query{}, err
		}
		q.Filters = append(q.Filters, f)
	}
	for _, s := range req.GetSort() {
		srt, err := ParseSort(s)
		if err != nil {
			return Query{}, err
		}
		q.Sorts = append(q.Sorts, srt)
	}
	if len(req.GetGroupBy()) > 0 || len(req.GetMeasure()) > 0 {
		q.Aggregation = &Aggregation{}
		for _, s := range req.GetGroupBy() {
			g, err := ParseGroup(s)
			if err != nil {
				return Query{}, err
			}
			q.Aggregation.By = append(q.Aggregation.By, g)
		}
		for _, s := range req.GetMeasure() {
			m, err := ParseMeasure(s)
			if err != nil {
				return Query{}, err
			}
			q.Aggregation.Measures = append(q.Aggregation.Measures, m)
		}
	}
	return q, nil
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
	case queryv1.Format_FORMAT_ARROW:
		res.Arrow, err = t.Arrow()
	case queryv1.Format_FORMAT_CSV:
		err = t.WriteCSV(&b)
	default:
		err = t.WriteText(&b)
	}
	if err != nil {
		return nil, status(err)
	}
	res.Text = b.String()
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
		qc := &queryv1.QueryColumn{Ref: c.Ref, Label: Label(c), Type: queryv1.ColumnType(c.Type), List: c.List,
			Unit: c.Unit, Doc: c.Doc}
		if c.Field != nil {
			qc.Doc = doc(c.Field)
			if c.Type == TypeEnum {
				values := c.Field.Enum().Values()
				for i := range values.Len() {
					if v := values.Get(i); v.Number() != 0 {
						qc.Values = append(qc.Values, string(v.Name()))
					}
				}
			}
		}
		if c.Jump != "" {
			for name, src := range s.cat.byName {
				if src.Source.Descriptor().FullName() == c.Jump {
					qc.Jump = queryv1.Collection(queryv1.Collection_value["COLLECTION_"+name])
				}
			}
		}
		res.Columns = append(res.Columns, qc)
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
