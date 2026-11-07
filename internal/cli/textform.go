package cli

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
	"github.com/empowill/djinn/internal/querytext"
)

// A textForm reads the text a person types for a message field into the message, on the caller's side: the server
// only ever reads the typed message. A query's filter status=running becomes a StringComparison, typed by the ref's
// type, which the collection's schema gives (internal/querytext).
type textForm struct {
	// help says the grammar of the text: what the help of the flag expects.
	help string
	// read reads text, for a field of req; lookup asks the server what only it knows, such as a ref's type.
	read func(ctx context.Context, text string, req protoreflect.Message, lookup lookup) (proto.Message, error)
	// write is the text of m, which read reads back.
	write func(m proto.Message) string
}

// lookup calls method on the server with in, and reads its answer into out.
type lookup func(ctx context.Context, method protoreflect.FullName, in, out proto.Message) error

// textForms are the messages the command line and djinn mcp fill from a text, by their full name.
var textForms = map[protoreflect.FullName]textForm{
	"query.v1.Filter": {help: querytext.FilterHelp,
		read: func(ctx context.Context, s string, req protoreflect.Message, l lookup) (proto.Message, error) {
			return querytext.Filter(ctx, s, collectionOf(req), schemaTypes(l))
		},
		write: func(m proto.Message) string { return querytext.FilterText(m.(*queryv1.Filter)) }},
	"query.v1.Order": {help: querytext.OrderHelp,
		read: func(_ context.Context, s string, _ protoreflect.Message, _ lookup) (proto.Message, error) {
			return querytext.Order(s)
		},
		write: func(m proto.Message) string { return querytext.OrderText(m.(*queryv1.Order)) }},
	"query.v1.GroupBy": {help: querytext.GroupHelp,
		read: func(ctx context.Context, s string, req protoreflect.Message, l lookup) (proto.Message, error) {
			return querytext.Group(ctx, s, collectionOf(req), schemaTypes(l))
		},
		write: func(m proto.Message) string { return querytext.GroupText(m.(*queryv1.GroupBy)) }},
	"query.v1.Measure": {help: querytext.MeasureHelp,
		read: func(_ context.Context, s string, _ protoreflect.Message, _ lookup) (proto.Message, error) {
			return querytext.Measure(s)
		},
		write: func(m proto.Message) string { return querytext.MeasureText(m.(*queryv1.Measure)) }},
}

// collectionOf is the collection a query request reads.
func collectionOf(req protoreflect.Message) queryv1.Collection {
	fd := req.Descriptor().Fields().ByName("collection")
	if fd == nil {
		return queryv1.Collection_COLLECTION_UNSPECIFIED
	}
	return queryv1.Collection(req.Get(fd).Enum())
}

// schemaTypes reads the types of refs from QueryService.Schema, through l.
func schemaTypes(l lookup) *querytext.Types {
	return querytext.NewTypes(func(ctx context.Context, c queryv1.Collection) ([]*queryv1.QueryColumn, error) {
		res := &queryv1.QueryServiceSchemaResponse{}
		err := l(ctx, "query.v1.QueryService.Schema", &queryv1.QueryServiceSchemaRequest{Collection: c}, res)
		return res.GetColumns(), err
	})
}

// hasForm tells whether fd's message has a text form.
func hasForm(fd protoreflect.FieldDescriptor) bool {
	_, ok := formOf(fd)
	return ok
}

// formOf is the text form of fd's message, when it has one.
func formOf(fd protoreflect.FieldDescriptor) (textForm, bool) {
	if fd.Kind() != protoreflect.MessageKind {
		return textForm{}, false
	}
	f, ok := textForms[fd.Message().FullName()]
	return f, ok
}

// pendingText is a text given for a field with a text form, read once the server answers.
type pendingText struct {
	fd   protoreflect.FieldDescriptor
	text string
}

// readTexts reads the texts of msg's fields with a text form into their messages, in the order given, asking the
// server through l; each lookup with the same input is asked once.
func readTexts(ctx context.Context, msg *dynamicpb.Message, texts []pendingText, l lookup) error {
	answers := map[string][]byte{}
	once := func(ctx context.Context, method protoreflect.FullName, in, out proto.Message) error {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(in)
		if err != nil {
			return err
		}
		key := string(method) + "\x00" + string(b)
		if a, ok := answers[key]; ok {
			return proto.Unmarshal(a, out)
		}
		if err := l(ctx, method, in, out); err != nil {
			return err
		}
		answers[key], err = proto.Marshal(out)
		return err
	}
	for _, p := range texts {
		form, _ := formOf(p.fd)
		m, err := form.read(ctx, p.text, msg, once)
		if err != nil {
			return fmt.Errorf("%s: %w", label(p.fd), err)
		}
		b, err := proto.Marshal(m)
		if err != nil {
			return err
		}
		if p.fd.IsList() {
			list := msg.Mutable(p.fd).List()
			el := list.NewElement()
			if err := proto.Unmarshal(b, el.Message().Interface()); err != nil {
				return err
			}
			list.Append(el)
			continue
		}
		v := msg.NewField(p.fd)
		if err := proto.Unmarshal(b, v.Message().Interface()); err != nil {
			return err
		}
		msg.Set(p.fd, v)
	}
	return nil
}

// serverLookup calls the methods of the djinn cfg reaches.
func serverLookup(cfg Config) lookup {
	return func(ctx context.Context, method protoreflect.FullName, in, out proto.Message) error {
		d, err := files().FindDescriptorByName(method)
		if err != nil {
			return err
		}
		md, ok := d.(protoreflect.MethodDescriptor)
		if !ok {
			return fmt.Errorf("%s is not a method", method)
		}
		req := dynamicpb.NewMessage(md.Input())
		b, err := proto.Marshal(in)
		if err == nil {
			err = proto.Unmarshal(b, req)
		}
		if err != nil {
			return err
		}
		res, err := call(ctx, cfg, md, req)
		if err != nil {
			return err
		}
		if b, err = proto.Marshal(res); err != nil {
			return err
		}
		return proto.Unmarshal(b, out)
	}
}

// formText is the text of v, a value of fd's message, as its text form writes it.
func formText(fd protoreflect.FieldDescriptor, form textForm, v protoreflect.Value) string {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(fd.Message().FullName())
	if err != nil {
		return ""
	}
	typed := mt.New().Interface()
	b, err := proto.Marshal(v.Message().Interface())
	if err != nil || proto.Unmarshal(b, typed) != nil {
		return ""
	}
	return form.write(typed)
}
