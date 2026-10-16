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
	"github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
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
			name: "wish title keeps its positional spelling",
			args: []string{"A short title", "--paused"},
			want: &planv1.WishServiceMakeRequest{Title: "A short title", Paused: true},
		},
		{
			name: "wish prompt may omit positional title",
			args: []string{"--paused", "--prompt", "**The request**\n\n- Keep the complete text"},
			want: &planv1.WishServiceMakeRequest{Prompt: "**The request**\n\n- Keep the complete text", Paused: true},
		},
		{
			name: "djinn wish describe <wish> --text",
			args: []string{wishID, "--text", "Ship the API.\nIts scope: the store."},
			want: &planv1.WishServiceDescribeRequest{WishId: wishID, Text: "Ship the API.\nIts scope: the store."},
		},
		{
			name: "djinn wish rename <wish> --title",
			args: []string{wishID, "--title", "Djinn orchestrator"},
			want: &planv1.WishServiceRenameRequest{WishId: wishID, Title: "Djinn orchestrator"},
		},
		{
			name: "djinn wish add-project <wish> --project-id",
			args: []string{wishID, "--project-id", "api"},
			want: &planv1.WishServiceAddProjectRequest{WishId: wishID, ProjectId: "api"},
		},
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
			name: "an optional flag given empty is present",
			args: []string{"Q03", "--before", ""},
			want: &planv1.QuestionServiceReviseRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, Before: proto.String(""),
			},
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
			name: "the lead reads several notes in one direct command",
			args: []string{"blockers-as-questions,direct-commands"},
			want: &planv1.LeadServiceNotesRequest{Name: "blockers-as-questions,direct-commands"},
		},
		{
			name: "the lead reads every note in one direct command",
			args: []string{"--all"},
			want: &planv1.LeadServiceNotesRequest{All: true},
		},
		{
			name: "an optional bool set false takes its mark off",
			args: []string{wishID, "--urgent=false"},
			want: &planv1.TaskServiceUpdateRequest{TaskId: wishID, Urgent: proto.Bool(false)},
		},
		{
			name: "the brief of the wishes alone",
			args: []string{"--wishes"},
			want: &planv1.LeadServiceBriefRequest{Wishes: true},
		},
		{
			name: "where the tasks stand, in a few lines each",
			args: []string{"--wish-id", wishID, "--short"},
			want: &planv1.TaskServiceListRequest{WishId: wishID, Short: true},
		},
		{
			name: "a task's last events",
			args: []string{wishID, "--last", "5"},
			want: &planv1.TaskServiceWatchRequest{TaskId: wishID, Last: 5},
		},
		{
			name: "an ifrit, with its own idle minutes",
			args: []string{wishID, "--kind", "ifrit", "--idle-minutes", "60"},
			want: &planv1.TaskServiceSpawnRequest{WishId: wishID, Kind: planv1.TaskKind_TASK_KIND_IFRIT, IdleMinutes: 60},
		},
		{
			name: "a question about a task",
			args: []string{"Keep this margin?", wishID, "--about", "W12"},
			want: &planv1.QuestionServiceAskRequest{Text: "Keep this margin?", WishId: wishID, About: "W12"},
		},
		{
			name: "a revision about another task",
			args: []string{"Q03", "--about", "W7"},
			want: &planv1.QuestionServiceReviseRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, About: proto.String("W7"),
			},
		},
		{
			name: "a revision about no task any more",
			args: []string{"Q03", "--about", ""},
			want: &planv1.QuestionServiceReviseRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, About: proto.String(""),
			},
		},
		{
			name: "a serve with its gate, ports and stop command",
			args: []string{"back", "task run-back", "--gate", "stack", "--port", "8080=api", "--port", "5432", "--stop", "task stop-back"},
			want: &planv1.ServeServiceRunRequest{
				Name: "back", Command: "task run-back", Gate: "stack", Port: []string{"8080=api", "5432"}, Stop: "task stop-back",
			},
		},
		{
			name:      "a serve's command after --",
			args:      []string{"front", "--port", "5173=front", "--", "npm run dev"},
			want:      &planv1.ServeServiceRunRequest{Name: "front", Command: "npm run dev", Port: []string{"5173=front"}},
			parseOnly: true,
		},
		{
			name: "a serve's last lines",
			args: []string{"back", "--last", "50"},
			want: &planv1.ServeServiceLogsRequest{Name: "back", Last: 50},
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

type tasks struct {
	planv1connect.UnimplementedTaskServiceHandler
	*fake
}

// Get is a task done whose work a correction worker took over, its batch red, after 412 model turns.
func (f tasks) Get(_ context.Context, req *connect.Request[planv1.TaskServiceGetRequest]) (*connect.Response[planv1.TaskServiceGetResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TaskServiceGetResponse{Task: &planv1.Task{
		Id: req.Msg.GetTaskId(), Code: "W5", Status: planv1.TaskStatus_TASK_STATUS_DONE, Model: "claude-sonnet-5-5", Integration: &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_RED, Branch: "feat/x", Reason: "test exited 1", CorrectedBy: "W9",
		},
		Turns: &planv1.Turns{Run: 37, Limit: 500, Total: 412},
	}}), nil
}

func (f tasks) List(_ context.Context, req *connect.Request[planv1.TaskServiceListRequest]) (*connect.Response[planv1.TaskServiceListResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TaskServiceListResponse{Tasks: []*planv1.Task{
		{Id: taskID, WishId: req.Msg.GetWishId(), Code: "W1", Title: "Ship it", Status: planv1.TaskStatus_TASK_STATUS_DONE, Model: "claude-sonnet-5-5"},
	}}), nil
}

func (f tasks) Update(_ context.Context, req *connect.Request[planv1.TaskServiceUpdateRequest]) (*connect.Response[planv1.TaskServiceUpdateResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TaskServiceUpdateResponse{Task: &planv1.Task{
		Id: req.Msg.GetTaskId(), Code: "W5", Model: req.Msg.GetModel(),
	}}), nil
}

func (f tasks) Cancel(_ context.Context, req *connect.Request[planv1.TaskServiceCancelRequest]) (*connect.Response[planv1.TaskServiceCancelResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TaskServiceCancelResponse{Task: &planv1.Task{
		Id: req.Msg.GetTaskId(), Code: "W5", Status: planv1.TaskStatus_TASK_STATUS_CANCELLED,
		Cancelled: &planv1.Cancellation{Note: req.Msg.GetNote()},
	}}), nil
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

func (f questions) Ask(_ context.Context, req *connect.Request[planv1.QuestionServiceAskRequest]) (*connect.Response[planv1.QuestionServiceAskResponse], error) {
	f.record(req.Msg)
	res := &planv1.QuestionServiceAskResponse{Question: question()}
	if req.Msg.GetContext() != "" {
		res.Warnings = []string{"line 1: a command in the text"}
	}
	return connect.NewResponse(res), nil
}

func (f questions) List(_ context.Context, req *connect.Request[planv1.QuestionServiceListRequest]) (*connect.Response[planv1.QuestionServiceListResponse], error) {
	f.record(req.Msg)
	if req.Msg.GetPageToken() == "" {
		return connect.NewResponse(&planv1.QuestionServiceListResponse{
			Questions: []*planv1.Question{
				{Id: "11111111-1111-1111-1111-111111111111", Code: "Q01", Text: "First?"},
			},
			NextPageToken: "page-2",
			Total:         2,
		}), nil
	}
	return connect.NewResponse(&planv1.QuestionServiceListResponse{
		Questions: []*planv1.Question{
			{Id: "22222222-2222-2222-2222-222222222222", Code: "Q02", Text: "Second?"},
		},
		Total: 2,
	}), nil
}

func (f projects) List(_ context.Context, req *connect.Request[planv1.ProjectServiceListRequest]) (*connect.Response[planv1.ProjectServiceListResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.ProjectServiceListResponse{Projects: []*planv1.Project{
		{Id: projectID, Name: "api", Directory: "/src/api"},
		{Id: wishID, Name: "web", Directory: "/src/web"},
	}}), nil
}

func (f projects) PushStrategy(_ context.Context, req *connect.Request[planv1.ProjectServicePushStrategyRequest]) (*connect.Response[planv1.ProjectServicePushStrategyResponse], error) {
	f.record(req.Msg)
	strategy := planv1.PushStrategy_PUSH_STRATEGY_WISH
	source := planv1.SettingSource_SETTING_SOURCE_DEFAULT
	if req.Msg.Strategy != nil && req.Msg.GetStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		strategy = req.Msg.GetStrategy()
		source = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	}
	return connect.NewResponse(&planv1.ProjectServicePushStrategyResponse{
		Strategy: strategy,
		Source:   source,
	}), nil
}

type wishes struct {
	planv1connect.UnimplementedWishServiceHandler
	*fake
}

func (f wishes) PushStrategy(_ context.Context, req *connect.Request[planv1.WishServicePushStrategyRequest]) (*connect.Response[planv1.WishServicePushStrategyResponse], error) {
	f.record(req.Msg)
	strategy := planv1.PushStrategy_PUSH_STRATEGY_WISH
	source := planv1.SettingSource_SETTING_SOURCE_DEFAULT
	if req.Msg.Strategy != nil && req.Msg.GetStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		strategy = req.Msg.GetStrategy()
		source = planv1.SettingSource_SETTING_SOURCE_WISH
	}
	return connect.NewResponse(&planv1.WishServicePushStrategyResponse{
		Strategy: strategy,
		Source:   source,
	}), nil
}

// AddProject adds the project named api, or of its identifier, to a wish that has web already; refuses web.
func (f wishes) AddProject(_ context.Context, req *connect.Request[planv1.WishServiceAddProjectRequest]) (*connect.Response[planv1.WishServiceAddProjectResponse], error) {
	f.record(req.Msg)
	switch req.Msg.GetProjectId() {
	case "api", projectID:
	case "web":
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf(`project web is already in wish %q`, "Ship it"))
	default:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s: djinn project list shows them", req.Msg.GetProjectId()))
	}
	return connect.NewResponse(&planv1.WishServiceAddProjectResponse{Wish: &planv1.Wish{
		Id: req.Msg.GetWishId(), Title: "Ship it", ProjectIds: []string{taskID, projectID},
	}}), nil
}

func question() *planv1.Question {
	return &planv1.Question{
		Id: questionID, Code: "Q03", Text: "Which store?", Options: []string{"sqlite", "files"},
		CreateTime: timestamppb.New(time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)),
	}
}

type loads struct {
	djinnv1connect.UnimplementedLoadServiceHandler
	*fake
}

func (f loads) Get(_ context.Context, req *connect.Request[djinnv1.LoadServiceGetRequest]) (*connect.Response[djinnv1.LoadServiceGetResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&djinnv1.LoadServiceGetResponse{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM}), nil
}

func (f loads) Set(_ context.Context, req *connect.Request[djinnv1.LoadServiceSetRequest]) (*connect.Response[djinnv1.LoadServiceSetResponse], error) {
	f.record(req.Msg)
	resp := &djinnv1.LoadServiceSetResponse{Notch: req.Msg.GetNotch()}
	if req.Msg.GetNotch() == djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
		resp.Notch = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
		resp.Auto = true
	}
	return connect.NewResponse(resp), nil
}

// serve starts the fake server and returns a function that runs a command line against it.
func serve(t *testing.T) (*fake, func(args ...string) (code int, stdout, stderr string)) {
	t.Helper()
	f := &fake{}
	mux := http.NewServeMux()
	mux.Handle(planv1connect.NewQuestionServiceHandler(questions{fake: f}))
	mux.Handle(planv1connect.NewProjectServiceHandler(projects{fake: f}))
	mux.Handle(planv1connect.NewWishServiceHandler(wishes{fake: f}))
	mux.Handle(planv1connect.NewTilasmServiceHandler(tilasms{fake: f}))
	mux.Handle(planv1connect.NewTaskServiceHandler(tasks{fake: f}))
	mux.Handle(djinnv1connect.NewLoadServiceHandler(loads{fake: f}))
	// ServeService answers unimplemented here: the command line reaches it, which is all these tests check.
	mux.Handle(planv1connect.NewServeServiceHandler(planv1connect.UnimplementedServeServiceHandler{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := Run(context.Background(), args, Config{
			Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdout: &out, Stderr: &errs, Getenv: noEnv,
		})
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
		{name: "warnings after the question", args: []string{"q", "ask", "Which?", wishID, "--context", "Run git log."}, wantOut: "\ncode: Q03\n", wantCalled: true},
		{name: "a warning line each", args: []string{"q", "ask", "Which?", wishID, "--context", "Run git log."}, wantOut: "\nwarning: line 1: a command in the text\n", wantCalled: true},
		{name: "text output", args: []string{"question", "answer", "Q03", "B", "--note", "ok"}, wantOut: "answer:\n  choice: b\n  note: ok\n", wantCalled: true},
		{name: "json output", args: []string{"--json", "q", "answer", questionID, "a"}, wantOut: `"CHOICE_A"`, wantCalled: true},
		{name: "where a task's work stands", args: []string{"task", "get", wishID}, wantOut: "status: done\n" +
			"model: claude-sonnet-5-5\n" +
			"integration:\n  state: red\n  branch: feat/x\n  reason: test exited 1\n  corrected_by: W9\n" +
			"turns:\n  run: 37\n  limit: 500\n  total: 412\n", wantCalled: true},
		{name: "task list output", args: []string{"task", "list", "--wish-id", wishID}, wantOut: "model: claude-sonnet-5-5\n", wantCalled: true},
		{name: "list output", args: []string{"pr", "l"}, wantOut: "- id: " + projectID + "\n  name: api\n", wantCalled: true},
		{name: "server error", args: []string{"q", "answer", "Q99", "a"}, wantCode: 1, wantErr: "not_found: no question Q99", wantCalled: true},
		{name: "validation before sending", args: []string{"q", "answer"}, wantCode: 2, wantErr: "<question>: value is required; expected a match of ^Q[0-9]{2,3}$ or a UUID\n  <choice>: value is required; expected one of yes, no, a, b, c, d"},
		{name: "rule on a positional", args: []string{"q", "ask", "Which?", "W1"}, wantCode: 2, wantErr: "<wish-id>: must be a valid UUID; expected a UUID"},
		{name: "rule on a repeated flag", args: []string{"q", "ask", "Which?", wishID, "--options", "a", "--options", "b", "--options", "c", "--options", "d", "--options", "e"}, wantCode: 2, wantErr: "--options: must contain no more than 4 item(s)"},
		{name: "rule on a flag", args: []string{"q", "answer", "Q03", "b", "--wish-id", "W1"}, wantCode: 2, wantErr: "--wish-id: must be a valid UUID; expected a UUID"},
		{name: "ambiguous method", args: []string{"question", "a"}, wantCode: 2, wantErr: `question method "a" is ambiguous: ask, answer`},
		{name: "unknown command", args: []string{"mission"}, wantCode: 2, wantErr: `unknown command "mission", expected one of: help, version, block, command, gate, inbox, lead, load, machine, mark, plan, project, question, serve, skill, task, tilasm, wish`},
		{name: "load get", args: []string{"load", "get"}, wantOut: "notch: medium\n", wantCalled: true},
		{name: "load set", args: []string{"load", "set", "minimal"}, wantOut: "notch: minimal\n", wantCalled: true},
		{name: "load set auto", args: []string{"load", "set", "auto"}, wantOut: "notch: minimal\nauto: true\n", wantCalled: true},
		{name: "internal service is hidden", args: []string{"ui", "get-environment"}, wantCode: 2, wantErr: `unknown command "ui"`},
		{name: "optional positional help", args: []string{"wish", "make", "--help"}, wantOut: "Usage: djinn wish make [<title>] [flags]"},
		{name: "version", args: []string{"v"}, wantOut: "djinn test\n"},
		{name: "talisman answers as tilasm", args: []string{"talisman", "list", "--search", "model"}, wantOut: "code: L01", wantCalled: true},
		{name: "an alias by a prefix no command takes", args: []string{"tali", "l"}, wantOut: "code: L01", wantCalled: true},
		{name: "a prefix a command takes is not an alias's", args: []string{"t", "list"}, wantCode: 2, wantErr: `command "t" is ambiguous: task, tilasm`},
		{name: "help names the alias", args: []string{"help"}, wantOut: "  tilasm, talisman "},
		{name: "method alias set-agent answers as update", args: []string{"task", "set-agent", taskID, "--model", "o3"}, wantOut: "code: W5\nmodel: o3", wantCalled: true},
		{name: "help on service names the method alias", args: []string{"help", "task"}, wantOut: "  update, set-agent "},
		{name: "help command", args: []string{"help", "pr"}, wantOut: "Methods:\n  add             Add a folder as a project."},
		{name: "paginated list loops across pages", args: []string{"q", "list"}, wantOut: "code: Q01\n  text: First?\n- id: 22222222-2222-2222-2222-222222222222\n  code: Q02\n  text: Second?\n", wantCalled: true},
		{name: "paginated list with explicit page-token", args: []string{"q", "list", "--page-token", "page-2"}, wantOut: "code: Q02\n  text: Second?\n", wantCalled: true},
		{name: "project push strategy show", args: []string{"project", "push-strategy", "api"}, wantOut: "strategy: wish\nsource: default\n", wantCalled: true},
		{name: "project push strategy set", args: []string{"project", "push-strategy", "api", "--strategy", "azima"}, wantOut: "strategy: azima\nsource: developer\n", wantCalled: true},
		{name: "wish push strategy show", args: []string{"wish", "push-strategy", wishID}, wantOut: "strategy: wish\nsource: default\n", wantCalled: true},
		{name: "wish push strategy set", args: []string{"wish", "push-strategy", wishID, "--strategy", "azima"}, wantOut: "strategy: azima\nsource: wish\n", wantCalled: true},
		{name: "wish add project by name", args: []string{"wish", "add-project", wishID, "--project-id", "api"}, wantOut: "project_ids:\n  - " + taskID + "\n  - " + projectID + "\n", wantCalled: true},
		{name: "wish add project already in", args: []string{"wish", "add-project", wishID, "--project-id", "web"}, wantCode: 1, wantErr: `already_exists: project web is already in wish "Ship it"`, wantCalled: true},
		{name: "wish add project unknown", args: []string{"wish", "add-project", wishID, "--project-id", "ghost"}, wantCode: 1, wantErr: "not_found: no project ghost", wantCalled: true},
		{name: "wish add project without one", args: []string{"wish", "add-project", wishID}, wantCode: 2, wantErr: "--project-id: "},
		{name: "task update integration none", args: []string{"task", "update", taskID, "--integration", "none"}, wantCalled: true},
		{name: "task update integration invalid", args: []string{"task", "update", taskID, "--integration", "other"}, wantCode: 2, wantErr: "--integration: must be in list [none, pending]"},
		{name: "task cancel", args: []string{"task", "cancel", taskID, "--note", "not needed"}, wantOut: "status: cancelled", wantCalled: true},
		{name: "an ifrit's idle minutes past 4 hours", args: []string{"task", "spawn", wishID, "--kind", "ifrit", "--idle-minutes", "300"}, wantCode: 2, wantErr: "--idle-minutes: must be greater than or equal to 5 and less than or equal to 240"},
		{name: "an ifrit's idle minutes under 5", args: []string{"task", "spawn", wishID, "--kind", "ifrit", "--idle-minutes", "2"}, wantCode: 2, wantErr: "--idle-minutes: must be greater than or equal to 5"},
		{name: "about a task by neither code nor id", args: []string{"q", "ask", "Which?", wishID, "--about", "Q12"}, wantCode: 2, wantErr: "--about: does not match regex pattern"},
		{name: "a serve's port without a number", args: []string{"serve", "run", "front", "npm run dev", "--task-id", taskID, "--port", "api=5173"}, wantCode: 2, wantErr: "--port: "},
		{name: "a serve's name with a space", args: []string{"serve", "run", "my front", "npm run dev", "--task-id", taskID}, wantCode: 2, wantErr: "<name>: does not match regex pattern"},
		{name: "serve run reaches its stub", args: []string{"serve", "run", "front", "npm run dev", "--task-id", taskID, "--port", "5173=front"}, wantCode: 1, wantErr: "unimplemented"},
		{name: "serve list reaches its stub", args: []string{"serve", "list", "--task-id", taskID}, wantCode: 1, wantErr: "unimplemented"},
		{name: "serve stop reaches its stub", args: []string{"serve", "stop", "front", "--task-id", taskID}, wantCode: 1, wantErr: "unimplemented"},
		{name: "serve logs reaches its stub", args: []string{"serve", "logs", "front", "--task-id", taskID, "--last", "50"}, wantCode: 1, wantErr: "unimplemented"},
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

// noEnv is an environment without any variable: a test run by a Djinn worker does not see its $DJINN_TASK_ID.
func noEnv(string) string { return "" }

// TestFromEnv: a field that defaults to an environment variable takes it when the command line leaves it empty, so
// that a worker's block or question is attributed to its task; a value given wins, and a field without the option
// (a filter) never takes it.
func TestFromEnv(t *testing.T) {
	const task, other = "01a120ea-554a-716c-8e66-43516f2f04c9", "01a120ea-554a-716c-8e66-43516f2f0000"
	env := func(name string) string {
		if name == "DJINN_TASK_ID" {
			return task
		}
		return ""
	}
	for _, c := range []struct {
		msg  proto.Message
		args []string
		want string
	}{
		{&planv1.BlockServicePutRequest{}, []string{wishID, "--kind", "decision"}, task},
		{&planv1.BlockServicePutRequest{}, []string{wishID, "--task-id", other}, other},
		{&planv1.QuestionServiceAskRequest{}, []string{"Which?", wishID}, task},
		{&planv1.QuestionServiceReviseRequest{}, []string{"Q03", "--context", "found"}, task},
		{&planv1.BlockServiceListRequest{}, []string{wishID}, ""},
		// An ifrit names its own serves without saying it.
		{&planv1.ServeServiceRunRequest{}, []string{"front", "npm run dev"}, task},
		{&planv1.ServeServiceListRequest{}, nil, task},
		{&planv1.ServeServiceStopRequest{}, []string{"front", "--task-id", other}, other},
		{&planv1.ServeServiceLogsRequest{}, []string{"front"}, task},
	} {
		md := local(t, c.msg)
		req, err := parse(md, c.args)
		if err != nil {
			t.Fatal(err)
		}
		fromEnv(req, env)
		if got := req.Get(md.Fields().ByName("task_id")).String(); got != c.want {
			t.Errorf("%s %v: task_id %q, want %q", md.Name(), c.args, got, c.want)
		}
	}
	// djinn mcp reads the same variable.
	f, _ := mcpSessionEnv(t, env, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"question_ask",`+
		`"arguments":{"text":"Which?","wish_id":"`+wishID+`"}}}`)
	if seen := f.seen(); len(seen) != 1 || seen[0].(*planv1.QuestionServiceAskRequest).GetTaskId() != task {
		t.Errorf("djinn mcp sent %v, want the task of $DJINN_TASK_ID", seen)
	}

	// DJINN_LEAD fills the lead field on answer and withdraw.
	const leadVal = "1"
	leadEnv := func(name string) string {
		if name == "DJINN_LEAD" {
			return leadVal
		}
		return ""
	}
	for _, c := range []struct {
		msg  proto.Message
		args []string
		want string
	}{
		{&planv1.QuestionServiceAnswerRequest{}, []string{"Q03", "yes"}, leadVal},
		{&planv1.QuestionServiceAnswerRequest{}, []string{"Q03", "yes", "--lead", other}, other},
		{&planv1.QuestionServiceWithdrawRequest{}, []string{"Q03"}, leadVal},
		{&planv1.QuestionServiceWithdrawRequest{}, []string{"Q03", "--lead", other}, other},
	} {
		md := local(t, c.msg)
		req, err := parse(md, c.args)
		if err != nil {
			t.Fatal(err)
		}
		fromEnv(req, leadEnv)
		if got := req.Get(md.Fields().ByName("lead")).String(); got != c.want {
			t.Errorf("%s %v: lead %q, want %q", md.Name(), c.args, got, c.want)
		}
	}

	// djinn lead watch names its session by the variable its agent sets, the first one set of those it lists.
	for _, c := range []struct {
		env  map[string]string
		args []string
		want string
	}{
		{map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}, nil, "claude-1"},
		{map[string]string{"CODEX_THREAD_ID": "codex-1"}, nil, "codex-1"},
		{map[string]string{"ANTIGRAVITY_CONVERSATION_ID": "agy-1"}, nil, "agy-1"},
		{map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1", "CODEX_THREAD_ID": "codex-1"}, nil, "claude-1"},
		{map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}, []string{"--session", "mine"}, "mine"},
		{nil, nil, ""},
	} {
		md := local(t, &planv1.LeadServiceWatchRequest{})
		req, err := parse(md, c.args)
		if err != nil {
			t.Fatal(err)
		}
		fromEnv(req, func(name string) string { return c.env[name] })
		if got := req.Get(md.Fields().ByName("session")).String(); got != c.want {
			t.Errorf("lead watch %v with %v: session %q, want %q", c.args, c.env, got, c.want)
		}
	}
}

// protoMethods are the public methods of the protos, streams included: each method of each service that says
// VISIBILITY_PUBLIC, read from the descriptors, not from the command line's own list. A method added is in it.
func protoMethods(t *testing.T) []protoreflect.MethodDescriptor {
	t.Helper()
	var out []protoreflect.MethodDescriptor
	files().RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				if md := sd.Methods().Get(j); proto.GetExtension(md.Options(), djinnv1.E_Visibility) == djinnv1.Visibility_VISIBILITY_PUBLIC {
					out = append(out, md)
				}
			}
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("the protos have no public method")
	}
	return out
}

// TestEveryPublicMethodIsExpressible keeps the protos within what the convention can type: each public method is a
// command line, djinn <service> <method>, and its fields are flags or arguments with their help.
func TestEveryPublicMethodIsExpressible(t *testing.T) {
	for _, md := range protoMethods(t) {
		line := []string{command(md.Parent().(protoreflect.ServiceDescriptor)), kebab(string(md.Name()))}
		var out, errs bytes.Buffer
		code := Run(t.Context(), append(line, "--help"), Config{Version: "test", Stdout: &out, Stderr: &errs})
		if usage := "Usage: djinn " + strings.Join(line, " "); code != 0 || !strings.HasPrefix(out.String(), usage) {
			t.Errorf("%s: djinn %s --help exited %d: %s%s", md.FullName(), strings.Join(line, " "), code, out.String(), errs.String())
		}
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

// Make accepts either input, but still rejects missing, blank and oversized titles.
func TestMakeInputValidation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"legacy title", []string{"Short title"}, true},
		{"prompt only", []string{"--prompt", "A complete request"}, true},
		{"missing inputs", nil, false},
		{"blank inputs", []string{" ", "--prompt", "\n\t"}, false},
		{"oversized title", []string{strings.Repeat("x", 501)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := parse(local(t, &planv1.WishServiceMakeRequest{}), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			err = check(msg, label)
			if (err == nil) != tt.valid {
				t.Errorf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}
