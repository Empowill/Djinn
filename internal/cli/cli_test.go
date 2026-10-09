package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	_ "github.com/empowill/djinn/gen/go/backup/v1"
	_ "github.com/empowill/djinn/gen/go/demo/v1"
	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	_ "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	_ "github.com/empowill/djinn/gen/go/terminal/v1"
	_ "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/locales"
)

const (
	questionID = "0199c0a1-7b2e-7c3d-8e4f-5a6b7c8d9e0f"
	wishID     = "0199c0a1-7b2e-7c3d-8e4f-000000000001"
	projectID  = "0199c0a1-7b2e-7c3d-8e4f-000000000002"
	taskID     = "0199c0a1-7b2e-7c3d-8e4f-000000000003"
)

// local returns the descriptor the command line uses for the message of want.
func local(t *testing.T, want proto.Message) protoreflect.MessageDescriptor {
	t.Helper()
	d, err := files().FindDescriptorByName(want.ProtoReflect().Descriptor().FullName())
	if err != nil {
		t.Fatal(err)
	}
	return d.(protoreflect.MessageDescriptor)
}

// TestConvention checks both ways: the arguments give the request, and the request gives the arguments.
func TestConvention(t *testing.T) {
	since := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	dir, err := filepath.Abs("api")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		args      []string
		want      proto.Message
		parseOnly bool // the arguments are a variant format does not produce
	}{
		{
			name: "oneof takes a code",
			args: []string{"Q03", "b", "--note", "ship it"},
			want: &planv1.QuestionServiceAnswerRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}},
				Choice:   planv1.Choice_CHOICE_B,
				Note:     "ship it",
			},
		},
		{
			name: "oneof takes a uuid",
			args: []string{questionID, "yes"},
			want: &planv1.QuestionServiceAnswerRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: questionID}},
				Choice:   planv1.Choice_CHOICE_YES,
			},
		},
		{
			name: "repeated flag",
			args: []string{"Which store?", wishID, "--options", "sqlite", "--options", "files"},
			want: &planv1.QuestionServiceAskRequest{Text: "Which store?", Options: []string{"sqlite", "files"}, WishId: wishID},
		},
		{
			name: "bool flag and timestamp",
			args: []string{"--open", "--since", "2026-10-07T09:00:00Z"},
			want: &planv1.QuestionServiceListRequest{Open: true, Since: timestamppb.New(since)},
		},
		{
			name: "positional then flag",
			args: []string{dir, "--name", "api"},
			want: &planv1.ProjectServiceAddRequest{Directory: dir, Name: "api"},
		},
		{
			name:      "a relative directory starts from the current one",
			args:      []string{"api"},
			want:      &planv1.ProjectServiceAddRequest{Directory: dir},
			parseOnly: true,
		},
		{
			name:      "a relative file starts from the current directory",
			args:      []string{"plan.djinn", "--replace"},
			want:      &planv1.WishServiceImportRequest{File: filepath.Join(filepath.Dir(dir), "plan.djinn"), Replace: true},
			parseOnly: true,
		},
		{name: "empty request", args: nil, want: &planv1.ProjectServiceListRequest{}},
		{
			name: "a string and a list of strings in one input, repeated",
			args: []string{taskID, "--after", "W1,W2", "--also", "W6=W5,W3", "--also", "W7="},
			want: &planv1.TaskServiceDependRequest{
				TaskId: taskID, After: []string{"W1,W2"},
				Also: []*planv1.TaskAfter{{Task: "W6", After: []string{"W5", "W3"}}, {Task: "W7"}},
			},
		},
		{
			name:      "spaces around the pieces of a pair",
			args:      []string{taskID, "--also", " W6 = W5 , W3 "},
			want:      &planv1.TaskServiceDependRequest{TaskId: taskID, Also: []*planv1.TaskAfter{{Task: "W6", After: []string{"W5", "W3"}}}},
			parseOnly: true,
		},
		{
			name:      "enum ignores case and accepts its full name",
			args:      []string{"--note=later", "Q03", "CHOICE_c"},
			want:      &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, Choice: planv1.Choice_CHOICE_C, Note: "later"},
			parseOnly: true,
		},
		{
			name:      "no answers a question whose options say yes and no",
			args:      []string{"Q03", "no"},
			want:      &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, Choice: planv1.Choice_CHOICE_NO},
			parseOnly: true,
		},
		{
			name:      "yes and no in another language Djinn speaks",
			args:      []string{"Q03", strings.ToUpper(locales.T("fr", "answer.yes", nil))},
			want:      &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, Choice: planv1.Choice_CHOICE_YES},
			parseOnly: true,
		},
		{
			name:      "bool with a value, time with an offset",
			args:      []string{"--open=false", "--since", "2026-10-07T11:00:00+02:00"},
			want:      &planv1.QuestionServiceListRequest{Since: timestamppb.New(since)},
			parseOnly: true,
		},
		{
			name:      "a relative path starts from the current directory, and a flag repeats",
			args:      []string{"notes", "--wish", wishID, "--cites", "T29", "--cites", "W12"},
			want:      &planv1.TilasmServicePutRequest{Path: filepath.Join(filepath.Dir(dir), "notes"), Wish: wishID, Cites: []string{"T29", "W12"}},
			parseOnly: true,
		},
		{
			name:      "-- ends the flags",
			args:      []string{"--", "--verbose?"},
			want:      &planv1.QuestionServiceAskRequest{Text: "--verbose?"},
			parseOnly: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(local(t, tt.want), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			b, err := proto.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			typed := tt.want.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(b, typed); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(typed, tt.want) {
				t.Errorf("parse(%q) = %v, want %v", tt.args, typed, tt.want)
			}
			if tt.parseOnly {
				return
			}
			if args := format(tt.want.ProtoReflect()); !slices.Equal(args, tt.args) {
				t.Errorf("format(%v) = %q, want %q", tt.want, args, tt.args)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	answer := &planv1.QuestionServiceAnswerRequest{}
	tests := []struct {
		name string
		msg  proto.Message
		args []string
		want string
	}{
		{"unknown flag", answer, []string{"Q03", "b", "--force"}, "unknown flag --force"},
		{"flag without value", answer, []string{"Q03", "b", "--note"}, "--note needs a value"},
		{"too many arguments", answer, []string{"Q03", "b", "c"}, `unexpected argument "c"`},
		{"unknown enum value", answer, []string{"Q03", "e"}, `<choice>: "e" is not one of yes, no, a, b, c, d`},
		{"zero enum value", answer, []string{"Q03", "unspecified"}, `"unspecified" is not one of`},
		{
			"oneof fits no member", answer, []string{"q3", "b"},
			`<question>: "q3" fits none of: code (does not match regex pattern ` + "`^Q[0-9]{2,3}$`" + `), id (must be a valid UUID)`,
		},
		{"bad time", &planv1.QuestionServiceListRequest{}, []string{"--since", "yesterday"}, `--since: "yesterday" is not an RFC 3339 time`},
		{"bad bool", &planv1.QuestionServiceListRequest{}, []string{"--open=maybe"}, `--open: "maybe" is not a valid bool`},
		{"positional is not a flag", &planv1.ProjectServiceAddRequest{}, []string{"--directory", "api"}, "unknown flag --directory"},
		{"pair without its key", &planv1.TaskServiceDependRequest{}, []string{taskID, "--also", "W5,W3"}, `--also: "W5,W3" is not task=after,…`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(local(t, tt.msg), tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse(%q) error = %v, want it to contain %q", tt.args, err, tt.want)
			}
		})
	}
}

// fake is an in-memory server that records the requests it receives.
type fake struct {
	mu    sync.Mutex // djinn mcp calls side by side
	calls []proto.Message
}

func (f *fake) record(m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, m)
}

func (f *fake) seen() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type questions struct {
	planv1connect.UnimplementedQuestionServiceHandler
	*fake
}

type tilasms struct {
	planv1connect.UnimplementedTilasmServiceHandler
	*fake
}

func (f tilasms) List(_ context.Context, req *connect.Request[planv1.TilasmServiceListRequest]) (*connect.Response[planv1.TilasmServiceListResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TilasmServiceListResponse{Tilasms: []*planv1.Tilasm{
		{Id: questionID, WishId: wishID, Code: "L01", Title: "The objects in the database"},
	}}), nil
}

type projects struct {
	planv1connect.UnimplementedProjectServiceHandler
	*fake
}

func (f questions) Answer(_ context.Context, req *connect.Request[planv1.QuestionServiceAnswerRequest]) (*connect.Response[planv1.QuestionServiceAnswerResponse], error) {
	f.record(req.Msg)
	if req.Msg.GetQuestion().GetCode() == "Q99" {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no question Q99"))
	}
	q := question()
	q.Answer = &planv1.Answer{Choice: req.Msg.GetChoice(), Note: req.Msg.GetNote(), CreateTime: timestamppb.New(time.Date(2026, 10, 7, 9, 5, 0, 0, time.UTC))}
	return connect.NewResponse(&planv1.QuestionServiceAnswerResponse{Question: q}), nil
}

func (f projects) List(_ context.Context, req *connect.Request[planv1.ProjectServiceListRequest]) (*connect.Response[planv1.ProjectServiceListResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.ProjectServiceListResponse{Projects: []*planv1.Project{
		{Id: projectID, Name: "api", Directory: "/src/api"},
		{Id: wishID, Name: "web", Directory: "/src/web"},
	}}), nil
}

func question() *planv1.Question {
	return &planv1.Question{
		Id: questionID, Code: "Q03", Text: "Which store?", Options: []string{"sqlite", "files"},
		CreateTime: timestamppb.New(time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)),
	}
}

// serve starts the fake server and returns a function that runs a command line against it.
func serve(t *testing.T) (*fake, func(args ...string) (code int, stdout, stderr string)) {
	t.Helper()
	f := &fake{}
	mux := http.NewServeMux()
	mux.Handle(planv1connect.NewQuestionServiceHandler(questions{fake: f}))
	mux.Handle(planv1connect.NewProjectServiceHandler(projects{fake: f}))
	mux.Handle(planv1connect.NewTilasmServiceHandler(tilasms{fake: f}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := Run(context.Background(), args, Config{Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdout: &out, Stderr: &errs})
		return code, out.String(), errs.String()
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantOut    string // substring of stdout
		wantErr    string // substring of stderr
		wantCalled bool
	}{
		{name: "prefixes", args: []string{"q", "ans", "Q03", "b"}, wantOut: "code: Q03", wantCalled: true},
		{name: "text output", args: []string{"question", "answer", "Q03", "B", "--note", "ok"}, wantOut: "answer:\n  choice: b\n  note: ok\n", wantCalled: true},
		{name: "json output", args: []string{"--json", "q", "answer", questionID, "a"}, wantOut: `"CHOICE_A"`, wantCalled: true},
		{name: "list output", args: []string{"pr", "l"}, wantOut: "- id: " + projectID + "\n  name: api\n", wantCalled: true},
		{name: "server error", args: []string{"q", "answer", "Q99", "a"}, wantCode: 1, wantErr: "not_found: no question Q99", wantCalled: true},
		{name: "validation before sending", args: []string{"q", "answer"}, wantCode: 2, wantErr: "<question>: value is required; expected a match of ^Q[0-9]{2,3}$ or a UUID\n  <choice>: value is required; expected one of yes, no, a, b, c, d"},
		{name: "rule on a positional", args: []string{"q", "ask", "Which?", "W1"}, wantCode: 2, wantErr: "<wish-id>: must be a valid UUID; expected a UUID"},
		{name: "rule on a repeated flag", args: []string{"q", "ask", "Which?", wishID, "--options", "a", "--options", "b", "--options", "c", "--options", "d", "--options", "e"}, wantCode: 2, wantErr: "--options: must contain no more than 4 item(s)"},
		{name: "rule on a flag", args: []string{"q", "answer", "Q03", "b", "--wish-id", "W1"}, wantCode: 2, wantErr: "--wish-id: must be a valid UUID; expected a UUID"},
		{name: "ambiguous method", args: []string{"question", "a"}, wantCode: 2, wantErr: `question method "a" is ambiguous: ask, answer`},
		{name: "unknown command", args: []string{"mission"}, wantCode: 2, wantErr: `unknown command "mission", expected one of: help, version, block, command, gate, inbox, machine, mark, plan, project, question, skill, task, tilasm, wish`},
		{name: "internal service is hidden", args: []string{"ui", "get-environment"}, wantCode: 2, wantErr: `unknown command "ui"`},
		{name: "version", args: []string{"v"}, wantOut: "djinn test\n"},
		{name: "talisman answers as tilasm", args: []string{"talisman", "list", "--search", "model"}, wantOut: "code: L01", wantCalled: true},
		{name: "an alias by a prefix no command takes", args: []string{"tali", "l"}, wantOut: "code: L01", wantCalled: true},
		{name: "a prefix a command takes is not an alias's", args: []string{"t", "list"}, wantCode: 2, wantErr: `command "t" is ambiguous: task, tilasm`},
		{name: "help names the alias", args: []string{"help"}, wantOut: "  tilasm, talisman "},
		{name: "help comes from the proto comments", args: []string{"q", "answer", "--help"}, wantOut: "Usage: djinn question answer <question> <choice> [flags]\n\nAnswer a question, which turns it into a decision."},
		{name: "help command", args: []string{"help", "pr"}, wantOut: "Methods:\n  add    Add a folder as a project."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, run := serve(t)
			code, out, errs := run(tt.args...)
			if code != tt.wantCode || !strings.Contains(out, tt.wantOut) || !strings.Contains(errs, tt.wantErr) {
				t.Errorf("djinn %s\ncode %d, want %d\nstdout:\n%s\nstderr:\n%s\nwant stdout to contain %q and stderr %q",
					strings.Join(tt.args, " "), code, tt.wantCode, out, errs, tt.wantOut, tt.wantErr)
			}
			if called := len(f.calls) > 0; called != tt.wantCalled {
				t.Errorf("server called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}

// TestEveryPublicMethodIsExpressible keeps the protos within what the convention can type.
func TestEveryPublicMethodIsExpressible(t *testing.T) {
	var methods int
	for _, sd := range commands() {
		for _, md := range public(sd) {
			methods++
			for _, fd := range byNumber(md.Input()) {
				if err := supported(fd); err != nil {
					t.Errorf("%s: %v", fd.FullName(), err)
				}
				if comment(fd) == "" {
					t.Errorf("%s has no comment, so no help", fd.FullName())
				}
			}
			if comment(md) == "" {
				t.Errorf("%s has no comment, so no help", md.FullName())
			}
		}
	}
	if methods != 64 {
		t.Errorf("found %d public methods, want 64", methods)
	}
}

// TestEmbeddedDescriptorsAreFresh fails when gen/djinn.binpb was not regenerated with the Go code.
func TestEmbeddedDescriptorsAreFresh(t *testing.T) {
	files().RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		compiled, err := protoregistry.GlobalFiles.FindFileByPath(fd.Path())
		if err != nil {
			t.Errorf("%s is embedded but not compiled in: %v", fd.Path(), err)
			return true
		}
		// Only the comments and the file options buf's managed mode adds to the generated code may differ.
		embedded, generated := protodesc.ToFileDescriptorProto(fd), protodesc.ToFileDescriptorProto(compiled)
		embedded.SourceCodeInfo, embedded.Options, generated.Options = nil, nil, nil
		if !proto.Equal(embedded, generated) {
			t.Errorf("%s differs from the generated code: run go tool task gen", fd.Path())
		}
		return true
	})
}

// TestEveryMethodSaysWhatItChanges fails on a public method that answers once and says neither that it only reads
// (option idempotency_level = NO_SIDE_EFFECTS) nor what it changes (option (djinn.v1.writes)), or says both: the
// next method is classified when it is added.
func TestEveryMethodSaysWhatItChanges(t *testing.T) {
	var reads, deletes []string
	for _, sd := range commands() {
		for _, md := range public(sd) {
			if md.IsStreamingClient() || md.IsStreamingServer() {
				continue
			}
			name := string(md.FullName())
			switch r, w := readOnly(md), writes(md); {
			case r && w != djinnv1.Writes_WRITES_UNSPECIFIED:
				t.Errorf("%s says it only reads, and that it writes: keep one", name)
			case !r && w == djinnv1.Writes_WRITES_UNSPECIFIED:
				t.Errorf("%s says nothing of what it changes: add option idempotency_level = NO_SIDE_EFFECTS if it "+
					"only reads, else option (djinn.v1.writes) = WRITES_CHANGE or WRITES_DELETE", name)
			case r:
				reads = append(reads, name)
			case w == djinnv1.Writes_WRITES_DELETE:
				deletes = append(deletes, name)
			}
		}
	}
	for _, want := range []string{"plan.v1.ProjectService.List", "plan.v1.TaskService.Get", "machine.v1.MachineService.Show"} {
		if !slices.Contains(reads, want) {
			t.Errorf("%s is not read-only: %v", want, reads)
		}
	}
	for _, want := range []string{"plan.v1.TaskService.Delete", "plan.v1.BlockService.Delete", "plan.v1.SkillService.Unsummon"} {
		if !slices.Contains(deletes, want) {
			t.Errorf("%s does not delete: %v", want, deletes)
		}
	}
}
