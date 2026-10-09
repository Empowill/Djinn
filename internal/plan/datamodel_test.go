package plan

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/empowill/djinn/gen/go/backup/v1"
	_ "github.com/empowill/djinn/gen/go/djinn/v1"
	_ "github.com/empowill/djinn/gen/go/terminal/v1"
	_ "github.com/empowill/djinn/gen/go/ui/v1"
)

// dataPackages are the packages whose messages docs/data-model.md describes; demo.v1 is a sample service.
var dataPackages = []protoreflect.FullName{"plan.v1", "machine.v1", "ui.v1", "terminal.v1", "backup.v1", "djinn.v1"}

// TestDataModelDoc keeps docs/data-model.md honest: it names every message that is not a request or a response, it
// draws every stored message as its table, and a message it draws lists all its fields.
func TestDataModelDoc(t *testing.T) {
	data, err := os.ReadFile("../../docs/data-model.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	classes := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^  class (\w+) \{\n([^}]*)\}`).FindAllStringSubmatch(doc, -1) {
		if _, ok := classes[m[1]]; !ok {
			classes[m[1]] = m[2]
		}
	}
	for _, pkg := range dataPackages {
		protoregistry.GlobalFiles.RangeFilesByPackage(pkg, func(fd protoreflect.FileDescriptor) bool {
			for i := range fd.Messages().Len() {
				md := fd.Messages().Get(i)
				name := string(md.Name())
				if strings.HasSuffix(name, "Request") || strings.HasSuffix(name, "Response") {
					continue
				}
				if !regexp.MustCompile(`\b` + name + `\b`).MatchString(doc) {
					t.Errorf("docs/data-model.md does not name %s", md.FullName())
				}
				body, ok := classes[name]
				if !ok {
					continue
				}
				for j := range md.Fields().Len() {
					field := string(md.Fields().Get(j).Name())
					if !regexp.MustCompile(`\b` + field + `\b`).MatchString(body) {
						t.Errorf("docs/data-model.md: class %s lacks the field %s", name, field)
					}
				}
			}
			return true
		})
	}
	for _, e := range Entities() {
		md := e.ProtoReflect().Descriptor()
		if table := "<<table " + snakeName(string(md.Name())) + ">>"; !strings.Contains(classes[string(md.Name())], table) {
			t.Errorf("docs/data-model.md does not draw the stored message %s as %s", md.FullName(), table)
		}
	}
}

// snakeName is the table name the store gives a message: TaskEvent is task_event.
func snakeName(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
