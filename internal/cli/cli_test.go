package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
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
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

const (
	questionID = "0199c0a1-7b2e-7c3d-8e4f-5a6b7c8d9e0f"
	wishID     = "0199c0a1-7b2e-7c3d-8e4f-000000000001"
	projectID  = "0199c0a1-7b2e-7c3d-8e4f-000000000002"
	taskID     = "0199c0a1-7b2e-7c3d-8e4f-000000000003"
)

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

func (f tilasms) Comments(_ context.Context, req *connect.Request[planv1.TilasmServiceCommentsRequest]) (*connect.Response[planv1.TilasmServiceCommentsResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&planv1.TilasmServiceCommentsResponse{
		Text: fmt.Sprintf("L01 · The objects in the database · version 1\n\nsend %d · 1 comment\n", req.Msg.GetSend()),
		Comments: []*planv1.TilasmComment{{
			Code: "C1", Text: "Say what a wish holds.", State: planv1.CommentState_COMMENT_STATE_SENT, Send: req.Msg.GetSend(),
		}},
	}), nil
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

// Watch sends the medium notch, then the minimal one in auto mode, and ends.
func (f loads) Watch(_ context.Context, req *connect.Request[djinnv1.LoadServiceWatchRequest], s *connect.ServerStream[djinnv1.LoadServiceWatchResponse]) error {
	f.record(req.Msg)
	for _, res := range []*djinnv1.LoadServiceWatchResponse{
		{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, Auto: true},
	} {
		if err := s.Send(res); err != nil {
			return err
		}
	}
	return nil
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

// runCase is a command line Run runs against the fake server, and what it gives.
type runCase struct {
	name       string
	args       []string
	wantCode   int
	wantOut    string // substring of stdout
	wantErr    string // substring of stderr
	wantCalled bool
}

func TestRun(t *testing.T) {
	tests := []runCase{
		{name: "prefixes", args: []string{"q", "ans", "Q03", "b"}, wantOut: "code: Q03", wantCalled: true},
		{name: "a warning line each, after the question", args: []string{"q", "ask", "Which?", wishID, "--context", "Run git log."}, wantOut: "id: " + questionID + "\ncode: Q03\n" +
			"text: Which store?\noptions:\n  - sqlite\n  - files\ncreate_time: 2026-10-07T09:00:00Z\nwarning: line 1: a command in the text\n", wantCalled: true},
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
		{name: "a stream prints each message", args: []string{"load", "watch"}, wantOut: "notch: medium\n\nnotch: minimal\nauto: true\n", wantCalled: true},
		{name: "a stream in JSON", args: []string{"--json", "load", "watch"}, wantOut: `"LOAD_NOTCH_MINIMAL"`, wantCalled: true},
		{name: "a service's help lists its commands written by hand", args: []string{"gate"}, wantOut: "\n  run "},
	}
	for _, name := range []string{"up", "open", "update", "backup", "version", "mcp", "--addr value"} {
		tests = append(tests, runCase{name: "help lists " + name, wantOut: "\n  " + name + " "})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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

// TestJSONOnly: a response field marked json_only prints with --json only, so that djinn tilasm comments shows the
// text the lead reads and not the same comments again, field by field.
func TestJSONOnly(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		send int32
		want func(out string) error
	}{
		{"the text leaves it out", []string{"tilasm", "comments", "L01", "--send", "2"}, 2, func(out string) error {
			if want := "text: L01 · The objects in the database · version 1\n\nsend 2 · 1 comment\n\n"; out != want {
				return fmt.Errorf("want %q", want)
			}
			return nil
		}},
		{"--json prints it", []string{"--json", "tilasm", "comments", "L01"}, 0, func(out string) error {
			// protojson spaces its output at random, one space or two: read it back rather than compare its text.
			var printed struct {
				Comments []struct{ Text string } `json:"comments"`
			}
			if err := json.Unmarshal([]byte(out), &printed); err != nil {
				return err
			}
			if len(printed.Comments) != 1 || printed.Comments[0].Text != "Say what a wish holds." {
				return fmt.Errorf("comments %+v", printed.Comments)
			}
			return nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, run := serve(t)
			code, out, errs := run(tt.args...)
			if err := tt.want(out); code != 0 || err != nil {
				t.Errorf("djinn %s: code %d, %v, stdout:\n%s\nstderr: %s", strings.Join(tt.args, " "), code, err, out, errs)
			}
			if got := f.seen()[0].(*planv1.TilasmServiceCommentsRequest); got.GetTilasm().GetCode() != "L01" || got.GetSend() != tt.send {
				t.Errorf("request = %v", got)
			}
		})
	}
}

// noEnv is an environment without any variable: a test run by a Djinn worker does not see its $DJINN_TASK_ID.
func noEnv(string) string { return "" }

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

// TestFiles fails when gen/djinn.binpb was not regenerated with the Go code: each file embedded is the one compiled
// in.
func TestFiles(t *testing.T) {
	files().RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		t.Run(fd.Path(), func(t *testing.T) {
			compiled, err := protoregistry.GlobalFiles.FindFileByPath(fd.Path())
			if err != nil {
				t.Fatalf("%s is embedded but not compiled in: %v", fd.Path(), err)
			}
			// Only the comments and the file options buf's managed mode adds to the generated code may differ.
			embedded, generated := protodesc.ToFileDescriptorProto(fd), protodesc.ToFileDescriptorProto(compiled)
			embedded.SourceCodeInfo, embedded.Options, generated.Options = nil, nil, nil
			if !proto.Equal(embedded, generated) {
				t.Errorf("%s differs from the generated code: run go tool task gen", fd.Path())
			}
		})
		return true
	})
}

// TestWrites checks what writes and readOnly say of a few methods, then fails on a public method that answers once
// and says neither that it only reads (option idempotency_level = NO_SIDE_EFFECTS) nor what it changes (option
// (djinn.v1.writes)), or says both: the next method is classified when it is added.
func TestWrites(t *testing.T) {
	for _, tt := range []struct {
		method   protoreflect.FullName
		readOnly bool
		writes   djinnv1.Writes
	}{
		{"plan.v1.ProjectService.List", true, djinnv1.Writes_WRITES_UNSPECIFIED},
		{"plan.v1.TaskService.Get", true, djinnv1.Writes_WRITES_UNSPECIFIED},
		{"machine.v1.MachineService.Show", true, djinnv1.Writes_WRITES_UNSPECIFIED},
		{"plan.v1.QuestionService.Answer", false, djinnv1.Writes_WRITES_CHANGE},
		{"plan.v1.TaskService.Delete", false, djinnv1.Writes_WRITES_DELETE},
		{"plan.v1.BlockService.Delete", false, djinnv1.Writes_WRITES_DELETE},
		{"plan.v1.SkillService.Unsummon", false, djinnv1.Writes_WRITES_DELETE},
	} {
		t.Run(string(tt.method), func(t *testing.T) {
			d, err := files().FindDescriptorByName(tt.method)
			if err != nil {
				t.Fatal(err)
			}
			md := d.(protoreflect.MethodDescriptor)
			if r, w := readOnly(md), writes(md); r != tt.readOnly || w != tt.writes {
				t.Errorf("readOnly %v, writes %v; want %v, %v", r, w, tt.readOnly, tt.writes)
			}
		})
	}
	t.Run("every method says what it changes", func(t *testing.T) {
		for _, sd := range commands() {
			for _, md := range public(sd) {
				if md.IsStreamingClient() || md.IsStreamingServer() {
					continue
				}
				switch r, w := readOnly(md), writes(md); {
				case r && w != djinnv1.Writes_WRITES_UNSPECIFIED:
					t.Errorf("%s says it only reads, and that it writes: keep one", md.FullName())
				case !r && w == djinnv1.Writes_WRITES_UNSPECIFIED:
					t.Errorf("%s says nothing of what it changes: add option idempotency_level = NO_SIDE_EFFECTS if it "+
						"only reads, else option (djinn.v1.writes) = WRITES_CHANGE or WRITES_DELETE", md.FullName())
				}
			}
		}
	})
}

// TestNewClient: with no address given, the command line reads the one djinn up wrote, a Unix socket or an http URL
// whose token goes as a bearer token; --addr wins over it. No address file is a hint to run djinn up, and the real
// data directory is refused under test.
func TestNewClient(t *testing.T) {
	const token = "s3cret"
	f := &fake{}
	// A Unix socket path is limited to about 100 bytes; t.TempDir is longer than that on macOS.
	dir, err := os.MkdirTemp("", "dj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, server.SocketFile)
	ln, err := server.ListenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(t.Context(), ln, handler(f)) //nolint:errcheck // Stops with the test.
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()
	srv.Config.Handler = server.Guard(handler(f), token, origin)
	srv.Start()
	defer srv.Close()

	list := []string{"project", "list"}
	for _, tt := range []struct {
		name      string
		file      string // the address djinn up wrote; none when empty
		real      bool   // run in the real data directory
		args      []string
		wantCode  int
		wantOut   string
		wantErr   string
		wantCalls int
	}{
		{name: "no address file", args: list, wantCode: 1, wantErr: "djinn up"},
		{name: "the file's socket", file: "unix://" + socket, args: list, wantOut: "name: api", wantCalls: 1},
		{name: "the file's token", file: origin + "/?token=" + token, args: list, wantOut: "name: api", wantCalls: 1},
		{name: "--addr wins over the file", file: origin + "/?token=" + token, args: append([]string{"--addr", origin}, list...), wantCode: 1, wantErr: "unauthenticated"},
		{name: "a wrong token", args: append([]string{"--addr", origin + "/?token=wrong"}, list...), wantCode: 1, wantErr: "unauthenticated"},
		{name: "the real data directory", real: true, args: []string{"wish", "list"}, wantCode: 1, wantErr: "refusing to use real data directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.real {
				if home = ui.RealHome(); home == "" {
					t.Skip("no real home directory found on this system")
				}
			}
			if tt.file != "" {
				if _, err := server.WriteAddr(home, tt.file); err != nil {
					t.Fatal(err)
				}
			}
			f.mu.Lock()
			f.calls = nil
			f.mu.Unlock()
			code, out, errs := runIn(home, tt.args...)
			if code != tt.wantCode || !strings.Contains(out, tt.wantOut) || !strings.Contains(errs, tt.wantErr) {
				t.Errorf("djinn %s: code %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(tt.args, " "), code, tt.wantCode, out, errs)
			}
			if calls := len(f.seen()); calls != tt.wantCalls {
				t.Errorf("server calls: %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

// resumes answers LeadService.Resume, which is marked autostart.
type resumes struct {
	planv1connect.UnimplementedLeadServiceHandler
}

func (resumes) Resume(
	_ context.Context, req *connect.Request[planv1.LeadServiceResumeRequest],
) (*connect.Response[planv1.LeadServiceResumeResponse], error) {
	return connect.NewResponse(&planv1.LeadServiceResumeResponse{Terminal: "lead"}), nil
}

// TestAutostart: a method marked autostart starts a djinn when none answers, and only then; the others fail as before.
// The steps run in order, on the same data directory.
func TestAutostart(t *testing.T) {
	lp, lh := planv1connect.NewLeadServiceHandler(resumes{})
	qp, qh := planv1connect.NewQuestionServiceHandler(questions{fake: &fake{}})
	srv := httptest.NewServer(server.Handler(fstest.MapFS{"index.html": {}}, nil, map[string]http.Handler{lp: lh, qp: qh}))
	defer srv.Close()
	home := t.TempDir()
	started := 0
	start := func(context.Context) (string, error) {
		started++
		if _, err := server.WriteAddr(home, srv.URL); err != nil {
			return "", err
		}
		return srv.URL, nil
	}
	for _, tt := range []struct {
		name        string
		file        string // the address left before the step; none when empty
		args        []string
		wantCode    int
		wantOut     string
		wantErr     string
		wantStarted int
	}{
		{name: "a method without autostart fails", args: []string{"question", "list"}, wantCode: 1, wantErr: "djinn up"},
		// An address left by a crash is not a djinn: resume starts one, then calls it.
		{name: "a crash's address starts one", file: "http://127.0.0.1:1/?token=old", args: []string{"lead", "resume"}, wantOut: "lead", wantStarted: 1},
		{name: "once one answers, nothing starts", args: []string{"lead", "resume"}, wantOut: "lead", wantStarted: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.file != "" {
				if _, err := server.WriteAddr(home, tt.file); err != nil {
					t.Fatal(err)
				}
			}
			var out, errs bytes.Buffer
			code := Run(t.Context(), tt.args, Config{Home: home, Stdout: &out, Stderr: &errs, Start: start})
			if code != tt.wantCode || started != tt.wantStarted || !strings.Contains(out.String(), tt.wantOut) ||
				!strings.Contains(errs.String(), tt.wantErr) {
				t.Errorf("djinn %s: code %d, started %d\nstdout:\n%s\nstderr:\n%s", strings.Join(tt.args, " "), code, started, out.String(), errs.String())
			}
		})
	}
}
