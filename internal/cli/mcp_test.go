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

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// mcpSession runs djinn mcp against the fake server on the given requests, one per line, and returns its answers
// by request id.
func mcpSession(t *testing.T, requests ...string) (*fake, map[string]rpcMessage) {
	t.Helper()
	f := &fake{}
	mux := http.NewServeMux()
	mux.Handle(planv1connect.NewQuestionServiceHandler(questions{fake: f}))
	mux.Handle(planv1connect.NewProjectServiceHandler(projects{fake: f}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var out, errs bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	cfg := Config{Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdin: in, Stdout: &out, Stderr: &errs}
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

func TestMCPListTools(t *testing.T) {
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
	// Every public method of the protos that answers once is a tool, with its comment; a stream is none.
	var want []string
	for _, md := range protoMethods(t) {
		if md.IsStreamingClient() || md.IsStreamingServer() {
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
	for _, want := range []string{"question_answer", "wish_set_lead", "project_list", "machine_show"} {
		if !slices.Contains(names, want) {
			t.Errorf("no tool %s in %v", want, names)
		}
	}
	for _, unwanted := range []string{"wish_watch", "gate_hold", "ui_get_environment"} {
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

func TestMCPCallTool(t *testing.T) {
	f, answers := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"question_answer","arguments":{"question":"Q03","choice":"b","note":"ship it"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"question_answer","arguments":{"choice":"b","wish_id":"W1"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"question_answer","arguments":{"question":"Q99","choice":"a"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"question_answer","arguments":{"question":"Q03","choice":"b","colour":"red"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ui_get_environment","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`,
		`not json`,
	)
	ok := result[mcpCallResult](t, answers["1"])
	if ok.IsError || len(ok.Content) != 1 || !strings.Contains(ok.Content[0].Text, `"choice": "CHOICE_B"`) {
		t.Errorf("call: %+v", ok)
	}
	want := &planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q03"}},
		Choice:   planv1.Choice_CHOICE_B,
		Note:     "ship it",
	}
	calls := f.seen()
	if !slices.ContainsFunc(calls, func(m proto.Message) bool { return proto.Equal(m, want) }) {
		t.Errorf("the server did not receive %v: %v", want, calls)
	}
	if len(calls) != 2 {
		t.Errorf("the server got %d calls, want 2 (the call that passes, the one it refuses): %v", len(calls), calls)
	}

	for id, wantText := range map[string]string{
		"2": "question: value is required; expected a match of ^Q[0-9]{2,3}$ or a UUID\nwish_id: must be a valid UUID; expected a UUID",
		"3": "not_found: no question Q99",
		"4": `unknown argument "colour"`,
	} {
		res := result[mcpCallResult](t, answers[id])
		if !res.IsError || len(res.Content) != 1 || res.Content[0].Text != wantText {
			t.Errorf("call %s: %+v, want the error %q", id, res, wantText)
		}
	}
	for id, code := range map[string]int{"5": rpcInvalidParams, "6": rpcMethodNotFound, "null": rpcParseError} {
		if e := answers[id].Error; e == nil || e.Code != code {
			t.Errorf("answer %s: error %+v, want code %d", id, e, code)
		}
	}
}
