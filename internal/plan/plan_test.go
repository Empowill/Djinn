package plan

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

type clients struct {
	projects  planv1connect.ProjectServiceClient
	wishes    planv1connect.WishServiceClient
	questions planv1connect.QuestionServiceClient
	blocks    planv1connect.BlockServiceClient
	marks     planv1connect.MarkServiceClient
	skills    planv1connect.SkillServiceClient
	inbox     planv1connect.InboxServiceClient
	store     *store.Store
}

// serve runs the plan services on a store in memory, behind a real Connect server.
func serve(t *testing.T, opts ...Option) clients {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	for prefix, h := range Handlers(s, opts...) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return clients{
		projects:  planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:    planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		questions: planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL),
		blocks:    planv1connect.NewBlockServiceClient(srv.Client(), srv.URL),
		marks:     planv1connect.NewMarkServiceClient(srv.Client(), srv.URL),
		skills:    planv1connect.NewSkillServiceClient(srv.Client(), srv.URL),
		inbox:     planv1connect.NewInboxServiceClient(srv.Client(), srv.URL),
		store:     s,
	}
}

func code(err error) connect.Code {
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return cerr.Code()
	}
	return connect.Code(0)
}

func (c clients) wish(t *testing.T) string {
	t.Helper()
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Try Djinn on itself"}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetWish().GetId()
}

func (c clients) ask(t *testing.T, wishID string, options ...string) *planv1.Question {
	t.Helper()
	res, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", Options: options, WishId: wishID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

func TestQuestionCodes(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	w1, w2 := c.wish(t), c.wish(t)

	q1, q2 := c.ask(t, w1, "sqlite", "files"), c.ask(t, w1)
	if q1.GetCode() != "Q01" || q2.GetCode() != "Q02" {
		t.Fatalf("codes = %s, %s; want Q01, Q02", q1.GetCode(), q2.GetCode())
	}
	if other := c.ask(t, w2, "yes", "no"); other.GetCode() != "Q01" {
		t.Errorf("first code of another wish = %s, want Q01", other.GetCode())
	}
	_, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "Lost?", WishId: store.NewID()}))
	if code(err) != connect.CodeNotFound {
		t.Errorf("ask in an unknown wish: %v, want not_found", err)
	}
	_, err = c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "Whose?"}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("ask without a wish: %v, want invalid_argument from the server's validation", err)
	}

	answer := func(ref *planv1.QuestionRef, choice planv1.Choice, wishID string) (*planv1.Question, error) {
		res, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: ref, Choice: choice, Note: "ok", WishId: wishID,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetQuestion(), nil
	}
	byCode := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}
	if _, err := answer(byCode, planv1.Choice_CHOICE_B, ""); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("Q01 exists in two wishes: %v, want failed_precondition", err)
	}
	got, err := answer(byCode, planv1.Choice_CHOICE_B, w1)
	if err != nil || got.GetId() != q1.GetId() || got.GetAnswer().GetChoice() != planv1.Choice_CHOICE_B {
		t.Fatalf("answer by code = %v, %v", got, err)
	}
	if _, err := answer(byCode, planv1.Choice_CHOICE_C, w1); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a letter beyond the options: %v, want invalid_argument", err)
	}
	byID := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q2.GetId()}}
	if _, err := answer(byID, planv1.Choice_CHOICE_A, ""); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a letter on a yes/no question: %v, want invalid_argument", err)
	}
	if got, err := answer(byID, planv1.Choice_CHOICE_YES, ""); err != nil || got.GetCode() != "Q02" {
		t.Errorf("answer by id = %v, %v", got, err)
	}
	if _, err := answer(&planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q09"}}, planv1.Choice_CHOICE_YES, w1); code(err) != connect.CodeNotFound {
		t.Errorf("unknown code: %v, want not_found", err)
	}

	list := func(req *planv1.QuestionServiceListRequest) []string {
		res, err := c.questions.List(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		var codes []string
		for _, q := range res.Msg.GetQuestions() {
			codes = append(codes, q.GetCode())
		}
		return codes
	}
	if got := list(&planv1.QuestionServiceListRequest{WishId: w1}); len(got) != 2 || got[0] != "Q01" || got[1] != "Q02" {
		t.Errorf("questions of the wish = %v, want Q01 then Q02", got)
	}
	if got := list(&planv1.QuestionServiceListRequest{Open: true}); len(got) != 1 {
		t.Errorf("open questions = %v, want the one of the other wish", got)
	}
	// A code is never given twice, answered questions included.
	if q3 := c.ask(t, w1); q3.GetCode() != "Q03" {
		t.Errorf("third code = %s, want Q03", q3.GetCode())
	}
}

// TestAnswerYesNo: on options that say yes and no, yes and no pick them, in English or in another catalog's word,
// and the decision keeps the letter. Elsewhere they stay refused.
func TestAnswerYesNo(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	w := c.wish(t)
	answer := func(q *planv1.Question, choice planv1.Choice) (planv1.Choice, error) {
		res, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice,
		}))
		if err != nil {
			return 0, err
		}
		return res.Msg.GetQuestion().GetAnswer().GetChoice(), nil
	}
	// The edit question of a task says it this way.
	edit := []string{"Yes: it starts again, allowed to edit", "No: it only reads"}
	title := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
	translated := []string{title(locales.T("fr", "answer.no", nil)) + ".", title(locales.T("fr", "answer.yes", nil))}
	for _, tt := range []struct {
		name    string
		options []string
		choice  planv1.Choice
		want    planv1.Choice
	}{
		{"yes picks the option that says yes", edit, planv1.Choice_CHOICE_YES, planv1.Choice_CHOICE_A},
		{"no picks the option that says no", edit, planv1.Choice_CHOICE_NO, planv1.Choice_CHOICE_B},
		{"a letter still works", edit, planv1.Choice_CHOICE_B, planv1.Choice_CHOICE_B},
		{"options in another language", translated, planv1.Choice_CHOICE_YES, planv1.Choice_CHOICE_B},
		{"no in another language", translated, planv1.Choice_CHOICE_NO, planv1.Choice_CHOICE_A},
		{"yes without options", nil, planv1.Choice_CHOICE_YES, planv1.Choice_CHOICE_YES},
	} {
		got, err := answer(c.ask(t, w, tt.options...), tt.choice)
		if err != nil || got != tt.want {
			t.Errorf("%s: %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
	for _, tt := range []struct {
		name    string
		options []string
		choice  planv1.Choice
	}{
		{"no without options", nil, planv1.Choice_CHOICE_NO},
		{"no option says yes", []string{"sqlite", "files"}, planv1.Choice_CHOICE_YES},
		{"a word must start the option", []string{"Say yes", "Say no"}, planv1.Choice_CHOICE_NO},
		{"two options say yes", []string{"Yes, now", "Yes, later", "No"}, planv1.Choice_CHOICE_YES},
		{"a letter beyond the options", edit, planv1.Choice_CHOICE_C},
	} {
		if _, err := answer(c.ask(t, w, tt.options...), tt.choice); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", tt.name, err)
		}
	}
}

func TestProjectAdd(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	add := func(dir, name string) (*planv1.Project, error) {
		res, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: name}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetProject(), nil
	}
	root := t.TempDir()
	api := filepath.Join(root, "Api")
	repo := filepath.Join(root, "repo")
	for _, d := range []string{api, filepath.Join(repo, ".git"), filepath.Join(root, "api-copy")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := filepath.EvalSymlinks(api)

	p, err := add(api+string(filepath.Separator)+".", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.GetName() != "Api" || p.GetDirectory() != want || p.GetGit() || len(p.GetId()) != 36 {
		t.Errorf("project = %v; want name Api, directory %s, not in Git", p, want)
	}
	if p, err := add(repo, "repo"); err != nil || !p.GetGit() {
		t.Errorf("a Git repository = %v, %v; want git", p, err)
	}
	if _, err := add(filepath.Join(root, "api-copy"), "API"); code(err) != connect.CodeAlreadyExists {
		t.Errorf("a name differing only by case: %v, want already_exists", err)
	}
	if _, err := add(api, "other"); code(err) != connect.CodeAlreadyExists {
		t.Errorf("the same folder twice: %v, want already_exists", err)
	}
	if _, err := add("relative", ""); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a relative path: %v, want invalid_argument", err)
	}
	if _, err := add(filepath.Join(root, "missing"), ""); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a missing folder: %v, want invalid_argument", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "api-copy"), link); err == nil {
		got, err := add(link, "")
		if want, _ := filepath.EvalSymlinks(filepath.Join(root, "api-copy")); err != nil || got.GetDirectory() != want || got.GetName() != "api-copy" {
			t.Errorf("a symbolic link = %v, %v; want it resolved to %s", got, err, want)
		}
	}

	res, err := c.projects.List(ctx, connect.NewRequest(&planv1.ProjectServiceListRequest{}))
	if err != nil || len(res.Msg.GetProjects()) < 2 {
		t.Errorf("List = %v, %v", res, err)
	}
}

func TestWishMake(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	dir := t.TempDir()
	p, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	id := p.Msg.GetProject().GetId()
	res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Ship it", ProjectIds: []string{id, id}}))
	if err != nil || len(res.Msg.GetWish().GetProjectIds()) != 1 {
		t.Fatalf("Make = %v, %v; want the project once", res, err)
	}
	_, err = c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Lost", ProjectIds: []string{store.NewID()}}))
	if code(err) != connect.CodeNotFound {
		t.Errorf("an unknown project: %v, want not_found", err)
	}
	list, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil || len(list.Msg.GetWishes()) != 1 {
		t.Errorf("List = %v, %v; want the one wish made", list, err)
	}
}
