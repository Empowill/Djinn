package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// mcpSession runs djinn mcp against the fake server on the given requests, one per line, and returns its answers
// by request id.
func mcpSession(t *testing.T, requests ...string) (*fake, map[string]rpcMessage) {
	t.Helper()
	return mcpSessionEnv(t, noEnv, requests...)
}

// mcpSessionEnv is mcpSession in the environment getenv reads.
func mcpSessionEnv(t *testing.T, getenv func(string) string, requests ...string) (*fake, map[string]rpcMessage) {
	t.Helper()
	f := &fake{}
	mux := http.NewServeMux()
	mux.Handle(planv1connect.NewQuestionServiceHandler(questions{fake: f}))
	mux.Handle(planv1connect.NewProjectServiceHandler(projects{fake: f}))
	mux.Handle(planv1connect.NewTaskServiceHandler(tasks{fake: f}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var out, errs bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	cfg := Config{Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdin: in, Stdout: &out, Stderr: &errs, Getenv: getenv}
	if code := Run(context.Background(), []string{"mcp"}, cfg); code != 0 {
		t.Fatalf("djinn mcp exited with %d: %s", code, errs.String())
	}
	answers := map[string]rpcMessage{}
	for line := range strings.Lines(out.String()) {
		var msg struct {
			rpcMessage
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("not JSON on stdout: %q", line)
		}
		msg.rpcMessage.Result = msg.Result
		answers[string(msg.ID)] = msg.rpcMessage
	}
	return f, answers
}

func result[T any](t *testing.T, msg rpcMessage) T {
	t.Helper()
	var v T
	if msg.Error != nil {
		t.Fatalf("error %d: %s", msg.Error.Code, msg.Error.Message)
	}
	if err := json.Unmarshal(msg.Result.(json.RawMessage), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTools: an MCP client that starts the session and lists the tools sees every public method that answers once,
// with its comment, its arguments and what it changes.
func TestTools(t *testing.T) {
	_, answers := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(answers) != 2 {
		t.Fatalf("got %d answers, want 2 (a notification gets none): %v", len(answers), answers)
	}
	init := result[struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct{ Name, Version string }
	}](t, answers["1"])
	if init.ProtocolVersion != "2025-06-18" || init.ServerInfo.Name != "djinn" || init.ServerInfo.Version != "test" {
		t.Errorf("initialize: %+v", init)
	}

	list := result[struct {
		Tools []struct {
			Name        string
			Description string
			InputSchema struct {
				Required   []string
				Properties map[string]struct {
					Type        string
					Enum        []string
					Description string
					Items       *struct{ Type string }
				}
			}
			Annotations map[string]bool
		}
	}](t, answers["2"])
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	// Every public method of the protos that answers once is a tool, with its comment; a stream is none, nor a
	// command only the developer types (djinn lead tell: no agent writes to the lead through a tool).
	var want []string
	for _, md := range protoMethods(t) {
		if md.IsStreamingClient() || md.IsStreamingServer() ||
			proto.GetExtension(md.Options(), djinnv1.E_CommandLineOnly).(bool) {
			continue
		}
		want = append(want, toolName(md))
		if i := slices.Index(names, toolName(md)); i < 0 || list.Tools[i].Description != comment(md) {
			t.Errorf("%s is no tool %s with its comment", md.FullName(), toolName(md))
		}
	}
	for _, name := range names {
		if !slices.Contains(want, name) {
			t.Errorf("tool %s is no public method that answers once", name)
		}
	}
	for _, want := range []string{"question_answer", "lead_set", "project_list", "machine_show"} {
		if !slices.Contains(names, want) {
			t.Errorf("no tool %s in %v", want, names)
		}
	}
	for _, unwanted := range []string{"wish_watch", "gate_hold", "ui_get_environment", "lead_tell"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("tool %s should not be served", unwanted)
		}
	}
	// A read says so, and a write says whether it deletes: MCP takes a tool that says nothing for one that may.
	for name, want := range map[string]map[string]bool{
		"project_list":    {"readOnlyHint": true, "destructiveHint": false},
		"wish_brief":      {"readOnlyHint": true, "destructiveHint": false},
		"question_answer": {"readOnlyHint": false, "destructiveHint": false},
		"task_delete":     {"readOnlyHint": false, "destructiveHint": true},
		"skill_unsummon":  {"readOnlyHint": false, "destructiveHint": true},
	} {
		if got := list.Tools[slices.Index(names, name)].Annotations; !maps.Equal(got, want) {
			t.Errorf("%s: annotations %v, want %v", name, got, want)
		}
	}
	makeTool := list.Tools[slices.Index(names, "wish_make")]
	if len(makeTool.InputSchema.Required) != 0 {
		t.Errorf("wish_make permits prompt-only requests, required %v", makeTool.InputSchema.Required)
	}
	tool := list.Tools[slices.Index(names, "question_answer")]
	if tool.Description != "Answer a question, which turns it into a decision." {
		t.Errorf("description %q", tool.Description)
	}
	props := tool.InputSchema.Properties
	if !slices.Equal(tool.InputSchema.Required, []string{"question", "choice"}) {
		t.Errorf("required %v", tool.InputSchema.Required)
	}
	if c := props["choice"]; c.Type != "string" || !slices.Equal(c.Enum, []string{"yes", "no", "a", "b", "c", "d"}) {
		t.Errorf("choice %+v", c)
	}
	if q := props["question"]; q.Type != "string" || !strings.Contains(q.Description, "Expects a match of ^Q[0-9]{2,3}$ or a UUID.") {
		t.Errorf("question %+v", q)
	}
	ask := list.Tools[slices.Index(names, "question_ask")].InputSchema.Properties["options"]
	if ask.Type != "array" || ask.Items == nil || ask.Items.Type != "string" {
		t.Errorf("options %+v", ask)
	}
	// A pair is read from one string, as on the command line.
	also := list.Tools[slices.Index(names, "task_depend")].InputSchema.Properties["also"]
	if also.Type != "array" || also.Items == nil || also.Items.Type != "string" || !strings.Contains(also.Description, "Expects task=after,….") {
		t.Errorf("also %+v", also)
	}
}

// TestServeMCP: a tool call is checked as the command line checks it, sent, and answered with the response or the
// error; what is no tool, no method or no JSON is a JSON-RPC error.
func TestServeMCP(t *testing.T) {
	const task = "01a120ea-554a-716c-8e66-43516f2f04c9"
	call := func(name, arguments string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `}}`
	}
	for _, tt := range []struct {
		name    string
		env     map[string]string
		request string
		want    string        // the tool's text: a part of it when the call passes, all of it when it fails
		isError bool          // the tool says the call failed
		code    int           // the JSON-RPC error; 0 when the call is answered
		sent    proto.Message // the request the server received; nil when none reached it
	}{
		{
			name: "a call answers the response", request: call("question_answer", `{"question":"Q03","choice":"b","note":"ship it"}`),
			want: `"choice": "CHOICE_B"`,
			sent: &planv1.QuestionServiceAnswerRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}}, Choice: planv1.Choice_CHOICE_B, Note: "ship it",
			},
		},
		{
			name: "arguments are checked before sending", request: call("question_answer", `{"choice":"b","wish_id":"W1"}`), isError: true,
			want: "question: value is required; expected a match of ^Q[0-9]{2,3}$ or a UUID\nwish_id: must be a valid UUID; expected a UUID",
		},
		{
			name: "the server's error", request: call("question_answer", `{"question":"Q99","choice":"a"}`), isError: true,
			want: "not_found: no question Q99",
			sent: &planv1.QuestionServiceAnswerRequest{Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q99"}}, Choice: planv1.Choice_CHOICE_A},
		},
		{name: "an unknown argument", request: call("question_answer", `{"question":"Q03","choice":"b","colour":"red"}`), isError: true, want: `unknown argument "colour"`},
		{name: "an internal method is no tool", request: call("ui_get_environment", `{}`), code: rpcInvalidParams},
		// No agent's client types into the lead through a tool: djinn lead tell is the developer's command.
		{name: "the developer's command is no tool", request: call("lead_tell", `{"text":"merge W3"}`), code: rpcInvalidParams},
		{name: "an unknown method", request: `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, code: rpcMethodNotFound},
		{name: "not JSON", request: `not json`, code: rpcParseError},
		// W234: a false is sent, not dropped, so that the urgent mark comes off.
		{
			name: "a false is sent", request: call("task_update", `{"task_id":"`+taskID+`","urgent":false}`), want: `"code": "W5"`,
			sent: &planv1.TaskServiceUpdateRequest{TaskId: taskID, Urgent: proto.Bool(false)},
		},
		{
			name: "a true is sent", request: call("task_update", `{"task_id":"`+taskID+`","feedback":true}`), want: `"code": "W5"`,
			sent: &planv1.TaskServiceUpdateRequest{TaskId: taskID, Feedback: proto.Bool(true)},
		},
		{
			name: "a field takes its variable", env: map[string]string{"DJINN_TASK_ID": task},
			request: call("question_ask", `{"text":"Which?","wish_id":"`+wishID+`"}`), want: `"code": "Q03"`,
			sent: &planv1.QuestionServiceAskRequest{Text: "Which?", WishId: wishID, TaskId: task},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, answers := mcpSessionEnv(t, func(name string) string { return tt.env[name] }, tt.request)
			if len(answers) != 1 {
				t.Fatalf("%d answers, want 1: %v", len(answers), answers)
			}
			var answer rpcMessage
			for _, answer = range answers {
			}
			switch {
			case tt.code != 0:
				if answer.Error == nil || answer.Error.Code != tt.code {
					t.Errorf("error %+v, want code %d", answer.Error, tt.code)
				}
			default:
				res := result[mcpCallResult](t, answer)
				if len(res.Content) != 1 || res.IsError != tt.isError ||
					(tt.isError && res.Content[0].Text != tt.want) || !strings.Contains(res.Content[0].Text, tt.want) {
					t.Errorf("result %+v, want isError %v and the text %q", res, tt.isError, tt.want)
				}
			}
			calls := f.seen()
			if tt.sent == nil && len(calls) != 0 || tt.sent != nil && (len(calls) != 1 || !proto.Equal(calls[0], tt.sent)) {
				t.Errorf("the server received %v, want %v", calls, tt.sent)
			}
		})
	}
}
