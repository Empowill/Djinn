package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/locales"
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

// parseCase is a command line and the request it gives, or the error parse finds in it.
type parseCase struct {
	name      string
	args      []string
	want      proto.Message // the request; its type only when parse fails
	parseOnly bool          // the arguments are a variant format does not produce
	wantErr   string        // a part of parse's error
}

// parseCases are the convention both ways (the arguments give the request, and the request gives the arguments),
// then the command lines it refuses.
func parseCases(t *testing.T) []parseCase {
	since := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	dir, err := filepath.Abs("api")
	if err != nil {
		t.Fatal(err)
	}
	answer := &planv1.QuestionServiceAnswerRequest{}
	return []parseCase{
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
			name: "a task's last events, then the ones that come",
			args: []string{wishID, "--last", "5", "--follow"},
			want: &planv1.TaskServiceWatchRequest{TaskId: wishID, Last: 5, Follow: true},
		},
		{
			name: "one question of a wish, by its code",
			args: []string{"Q12", "--wish-id", wishID},
			want: &planv1.QuestionServiceGetRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q12"}}, WishId: wishID,
			},
		},
		{
			name: "one of the lead's events whole",
			args: []string{wishID},
			want: &planv1.LeadServiceEventRequest{Id: wishID},
		},
		{
			name: "the lead speaks in the Lamp's bubble",
			args: []string{wishID, "Two tasks run on the lamp.", "--decided"},
			want: &planv1.LeadServiceSayRequest{WishId: wishID, Text: "Two tasks run on the lamp.", Decided: true},
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
		{name: "unknown flag", want: answer, args: []string{"Q03", "b", "--force"}, wantErr: "unknown flag --force"},
		{name: "flag without value", want: answer, args: []string{"Q03", "b", "--note"}, wantErr: "--note needs a value"},
		{name: "too many arguments", want: answer, args: []string{"Q03", "b", "c"}, wantErr: `unexpected argument "c"`},
		{name: "unknown enum value", want: answer, args: []string{"Q03", "e"}, wantErr: `<choice>: "e" is not one of yes, no, a, b, c, d`},
		{name: "zero enum value", want: answer, args: []string{"Q03", "unspecified"}, wantErr: `"unspecified" is not one of`},
		{
			name: "oneof fits no member", want: answer, args: []string{"q3", "b"},
			wantErr: `<question>: "q3" fits none of: code (does not match regex pattern ` + "`^Q[0-9]{2,3}$`" + `), id (must be a valid UUID)`,
		},
		{name: "bad time", want: &planv1.QuestionServiceListRequest{}, args: []string{"--since", "yesterday"}, wantErr: `--since: "yesterday" is not an RFC 3339 time`},
		{name: "bad bool", want: &planv1.QuestionServiceListRequest{}, args: []string{"--open=maybe"}, wantErr: `--open: "maybe" is not a valid bool`},
		{name: "positional is not a flag", want: &planv1.ProjectServiceAddRequest{}, args: []string{"--directory", "api"}, wantErr: "unknown flag --directory"},
		{name: "pair without its key", want: &planv1.TaskServiceDependRequest{}, args: []string{taskID, "--also", "W5,W3"}, wantErr: `--also: "W5,W3" is not task=after,…`},
	}
}

func TestParse(t *testing.T) {
	for _, tt := range parseCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parse(local(t, tt.want), tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("parse(%q) error = %v, want it to contain %q", tt.args, err, tt.wantErr)
				}
				return
			}
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
		})
	}
}

func TestFormat(t *testing.T) {
	for _, tt := range parseCases(t) {
		if tt.parseOnly || tt.wantErr != "" {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if args := format(tt.want.ProtoReflect()); !slices.Equal(args, tt.args) {
				t.Errorf("format(%v) = %q, want %q", tt.want, args, tt.args)
			}
		})
	}
}

// TestCheck: djinn wish make accepts a title or a prompt, but still rejects missing, blank and oversized titles.
func TestCheck(t *testing.T) {
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
			t.Parallel()
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

// TestFromEnv: a field that defaults to an environment variable takes it when the command line leaves it empty, so
// that a worker's block or question is attributed to its task; a value given wins, and a field without the option
// (a filter) never takes it. DJINN_LEAD fills the lead field on answer and withdraw; djinn lead watch names its
// session by the variable its agent sets, the first one set of those it lists.
func TestFromEnv(t *testing.T) {
	const task, other = "01a120ea-554a-716c-8e66-43516f2f04c9", "01a120ea-554a-716c-8e66-43516f2f0000"
	inTask := map[string]string{"DJINN_TASK_ID": task}
	lead := map[string]string{"DJINN_LEAD": "1"}
	for _, tt := range []struct {
		name  string
		env   map[string]string
		msg   proto.Message
		args  []string
		field protoreflect.Name
		want  string
	}{
		{"a block takes the task", inTask, &planv1.BlockServicePutRequest{}, []string{wishID, "--kind", "decision"}, "task_id", task},
		{"a block's task given wins", inTask, &planv1.BlockServicePutRequest{}, []string{wishID, "--task-id", other}, "task_id", other},
		{"a question takes the task", inTask, &planv1.QuestionServiceAskRequest{}, []string{"Which?", wishID}, "task_id", task},
		{"a revision takes the task", inTask, &planv1.QuestionServiceReviseRequest{}, []string{"Q03", "--context", "found"}, "task_id", task},
		{"a filter never takes it", inTask, &planv1.BlockServiceListRequest{}, []string{wishID}, "task_id", ""},
		// An ifrit names its own serves without saying it.
		{"a serve run takes the task", inTask, &planv1.ServeServiceRunRequest{}, []string{"front", "npm run dev"}, "task_id", task},
		{"a serve list takes the task", inTask, &planv1.ServeServiceListRequest{}, nil, "task_id", task},
		{"a serve stop's task given wins", inTask, &planv1.ServeServiceStopRequest{}, []string{"front", "--task-id", other}, "task_id", other},
		{"serve logs take the task", inTask, &planv1.ServeServiceLogsRequest{}, []string{"front"}, "task_id", task},
		{"an answer takes the lead", lead, &planv1.QuestionServiceAnswerRequest{}, []string{"Q03", "yes"}, "lead", "1"},
		{"an answer's lead given wins", lead, &planv1.QuestionServiceAnswerRequest{}, []string{"Q03", "yes", "--lead", other}, "lead", other},
		{"a withdraw takes the lead", lead, &planv1.QuestionServiceWithdrawRequest{}, []string{"Q03"}, "lead", "1"},
		{"a withdraw's lead given wins", lead, &planv1.QuestionServiceWithdrawRequest{}, []string{"Q03", "--lead", other}, "lead", other},
		{"claude's session", map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}, &planv1.LeadServiceWatchRequest{}, nil, "session", "claude-1"},
		{"codex's session", map[string]string{"CODEX_THREAD_ID": "codex-1"}, &planv1.LeadServiceWatchRequest{}, nil, "session", "codex-1"},
		{"agy's session", map[string]string{"ANTIGRAVITY_CONVERSATION_ID": "agy-1"}, &planv1.LeadServiceWatchRequest{}, nil, "session", "agy-1"},
		{"the first session set", map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1", "CODEX_THREAD_ID": "codex-1"}, &planv1.LeadServiceWatchRequest{}, nil, "session", "claude-1"},
		{"a session given wins", map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}, &planv1.LeadServiceWatchRequest{}, []string{"--session", "mine"}, "session", "mine"},
		{"no session", nil, &planv1.LeadServiceWatchRequest{}, nil, "session", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			md := local(t, tt.msg)
			req, err := parse(md, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			fromEnv(req, func(name string) string { return tt.env[name] })
			if got := req.Get(md.Fields().ByName(tt.field)).String(); got != tt.want {
				t.Errorf("%s %v with %v: %s %q, want %q", md.Name(), tt.args, tt.env, tt.field, got, tt.want)
			}
		})
	}
}

// TestSupported keeps the protos within what the convention can type: each public method is a command line,
// djinn <service> <method>, and its fields are flags or arguments with their help.
func TestSupported(t *testing.T) {
	for _, md := range protoMethods(t) {
		t.Run(string(md.FullName()), func(t *testing.T) {
			t.Parallel()
			line := []string{command(md.Parent().(protoreflect.ServiceDescriptor)), kebab(string(md.Name()))}
			var out, errs bytes.Buffer
			code := Run(t.Context(), append(line, "--help"), Config{Version: "test", Stdout: &out, Stderr: &errs})
			if usage := "Usage: djinn " + strings.Join(line, " "); code != 0 || !strings.HasPrefix(out.String(), usage) {
				t.Errorf("djinn %s --help exited %d: %s%s", strings.Join(line, " "), code, out.String(), errs.String())
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
		})
	}
}
