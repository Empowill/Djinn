package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenAPIIsFresh fails when docs/openapi.json was not regenerated with the protos.
func TestOpenAPIIsFresh(t *testing.T) {
	want, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Error("docs/openapi.json differs from the protos: run go tool task gen")
	}
}

// TestOpenAPI checks the shape of one method: its path, its request and response, and the rules of its fields.
func TestOpenAPI(t *testing.T) {
	b, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]struct {
			Post struct {
				OperationID string `json:"operationId"`
				Summary     string
				RequestBody struct {
					Content map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						}
					}
				}
			}
		}
		Components struct {
			Schemas map[string]struct {
				Required   []string
				Properties map[string]map[string]any
			}
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("openapi %q", doc.OpenAPI)
	}
	// The unary public methods: the streams use Connect's streaming protocol.
	if len(doc.Paths) != 43 {
		t.Errorf("%d paths, want 43", len(doc.Paths))
	}
	if _, ok := doc.Paths["/plan.v1.WishService/Watch"]; ok {
		t.Error("a stream is described")
	}
	if _, ok := doc.Paths["/ui.v1.UiService/GetEnvironment"]; ok {
		t.Error("an internal method is described")
	}
	op := doc.Paths["/plan.v1.QuestionService/Answer"].Post
	if op.OperationID != "QuestionService_Answer" || op.Summary != "djinn question answer" {
		t.Errorf("operation %+v", op)
	}
	if ref := op.RequestBody.Content["application/json"].Schema.Ref; ref != "#/components/schemas/plan.v1.QuestionServiceAnswerRequest" {
		t.Errorf("request %q", ref)
	}
	req := doc.Components.Schemas["plan.v1.QuestionServiceAnswerRequest"]
	if len(req.Required) != 2 || req.Required[0] != "question" || req.Required[1] != "choice" {
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
