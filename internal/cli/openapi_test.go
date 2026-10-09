package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// openAPIOperation is what the tests read of an operation of docs/openapi.json.
type openAPIOperation struct {
	OperationID string `json:"operationId"`
	Description string
	RequestBody struct {
		Content map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			}
		}
	}
}

type openAPIDocument struct {
	OpenAPI string `json:"openapi"`
	Info    struct{ Title string }
	Paths   map[string]struct {
		Get  *openAPIOperation
		Post *openAPIOperation
	}
	Components struct {
		Schemas map[string]struct {
			Required   []string
			Properties map[string]map[string]any
		}
	}
}

func readOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc openAPIDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestOpenAPI checks docs/openapi.json, which protoc-gen-connect-openapi writes (buf.gen.yaml), against the protos
// the command line embeds: every public method is there, with its comment, its request and a GET when it only reads,
// and no internal one. It is the API tab of the documentation site (docs/site).
func TestOpenAPI(t *testing.T) {
	doc := readOpenAPI(t)
	if doc.OpenAPI != "3.1.0" || doc.Info.Title != "Djinn" {
		t.Errorf("openapi %q, title %q", doc.OpenAPI, doc.Info.Title)
	}
	described := map[string]bool{}
	for path := range doc.Paths {
		described[path] = true
	}
	files().RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				path := "/" + string(sd.FullName()) + "/" + string(md.Name())
				delete(described, path)
				item, ok := doc.Paths[path]
				if !slices.Contains(public(sd), md) {
					if ok {
						t.Errorf("%s is internal and described: add it to exclude_types in buf.gen.yaml", path)
					}
					continue
				}
				if !ok || item.Post == nil {
					t.Errorf("%s is public and not described: run go tool task gen", path)
					continue
				}
				if want := string(sd.Name()) + "_" + string(md.Name()); item.Post.OperationID != want {
					t.Errorf("%s: operationId %q, want %q", path, item.Post.OperationID, want)
				}
				if got := strings.Join(strings.Fields(item.Post.Description), " "); got != comment(md) {
					t.Errorf("%s: description %q, want the comment %q: run go tool task gen", path, got, comment(md))
				}
				if (item.Get != nil) != (readOnly(md) && !md.IsStreamingClient() && !md.IsStreamingServer()) {
					t.Errorf("%s: a GET is described when the method only reads, and only then", path)
				}
				checkRequest(t, doc, path, item.Post, md.Input())
			}
		}
		return true
	})
	for path := range described {
		t.Errorf("%s is described and is no method of the protos: run go tool task gen", path)
	}
}

// checkRequest checks that the request of an operation is the input message, field by field.
func checkRequest(t *testing.T, doc openAPIDocument, path string, op *openAPIOperation, in protoreflect.MessageDescriptor) {
	t.Helper()
	var ref string
	for _, c := range op.RequestBody.Content {
		ref = c.Schema.Ref
		break
	}
	if want := "#/components/schemas/" + string(in.FullName()); ref != want {
		t.Errorf("%s: request %q, want %q", path, ref, want)
		return
	}
	schema := doc.Components.Schemas[string(in.FullName())]
	var fields, props []string
	for i := range in.Fields().Len() {
		fields = append(fields, in.Fields().Get(i).JSONName())
	}
	for name := range schema.Properties {
		props = append(props, name)
	}
	slices.Sort(fields)
	slices.Sort(props)
	if !slices.Equal(fields, props) {
		t.Errorf("%s: request fields %v, want %v: run go tool task gen", path, props, fields)
	}
}

// TestOpenAPIRules checks that the rules of protovalidate reach the schemas, on one request.
func TestOpenAPIRules(t *testing.T) {
	doc := readOpenAPI(t)
	req := doc.Components.Schemas["plan.v1.QuestionServiceAnswerRequest"]
	if !slices.Equal(req.Required, []string{"question", "choice"}) {
		t.Errorf("required %v", req.Required)
	}
	if f := req.Properties["wishId"]; f["format"] != "uuid" {
		t.Errorf("wishId %v", f)
	}
	if f := req.Properties["note"]; f["maxLength"] != 2000.0 {
		t.Errorf("note %v", f)
	}
	if _, ok := doc.Components.Schemas["plan.v1.Choice"]; !ok {
		t.Error("no schema for the enum plan.v1.Choice")
	}
}
