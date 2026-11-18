package plan

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// folderBytes reads every file of home, by its path under home.
func folderBytes(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		rel, _ := filepath.Rel(home, path)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// migrateTwice runs Migrate on home twice and fails when the second run changes a byte of home.
func migrateTwice(t *testing.T, home string) {
	t.Helper()
	Migrate(home)
	first := folderBytes(t, home)
	Migrate(home)
	if second := folderBytes(t, home); !maps.Equal(first, second) {
		t.Errorf("a second run changed the folder:\nfirst  %q\nsecond %q", first, second)
	}
}

func TestMigrate_BlankFolder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	migrateTwice(t, home)
	files := folderBytes(t, home)
	if len(files) != 1 {
		t.Errorf("files = %v, want settings.json alone", files)
	}
	if got := readSettings(home); !slices.Equal(got.GetMigrations(), []string{"circle-from-project-settings"}) ||
		len(got.GetCircle()) != 0 || got.GetLead() != nil {
		t.Errorf("settings = %v, want the migration recorded and nothing else", got)
	}
	if p, _, explicit := ResolveWorkerSeat(home, planv1.WorkerKind_WORKER_KIND_WORK); explicit || p != planv1.Provider_PROVIDER_CLAUDE {
		t.Errorf("work seat = %v explicit %v: a blank folder keeps no circle", p, explicit)
	}
}

// The shape of djinn's, dolmen's and whakatuu's local settings in L20: a configured circle (lead Claude, correction and
// question on Antigravity, the rest Same as lead), and three developer files with provider, model and a question model
// Antigravity cannot run read-only, beside other settings; a fourth project holds no seat.
func TestMigrate_TypicalFolder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	seat := func(k planv1.WorkerKind, p planv1.Provider, m string) *planv1.WorkerSeat {
		return &planv1.WorkerSeat{Kind: k, Provider: p, Model: m}
	}
	circle := &uiv1.Settings{
		Lead: &planv1.LeadSeat{Provider: planv1.Provider_PROVIDER_CLAUDE, Model: "claude-opus-5-5"},
		Circle: []*planv1.WorkerSeat{
			seat(planv1.WorkerKind_WORKER_KIND_CORRECTION, planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3.8-flash-high"),
			{Kind: planv1.WorkerKind_WORKER_KIND_REVIEW},
			seat(planv1.WorkerKind_WORKER_KIND_QUESTION, planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-4-argon-high"),
			{Kind: planv1.WorkerKind_WORKER_KIND_WORK},
			{Kind: planv1.WorkerKind_WORKER_KIND_MOCKUP},
			{Kind: planv1.WorkerKind_WORKER_KIND_IFRIT},
			{Kind: planv1.WorkerKind_WORKER_KIND_INVESTIGATION},
			seat(planv1.WorkerKind_WORKER_KIND_LEAD, planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5"),
		},
	}
	data, err := protojson.Marshal(circle)
	if err != nil {
		t.Fatal(err)
	}
	writeSettings(t, filepath.Join(home, settingsFile), string(data))
	seats := "provider: PROVIDER_ANTIGRAVITY\nmodel: \"gemini-4-argon-high\"\nquestion_model: \"gemini-3.8-flash-high\"\n"
	files := map[string][2]string{ // before, after
		"p1": {seats + "push: PROJECT_PUSH_ON_DEMAND\nwatch_commands: \"make watch\"\n",
			"push: PROJECT_PUSH_ON_DEMAND\nwatch_commands: \"make watch\"\n"},
		"p2": {seats + "checks {\n  name: \"lint\"\n  command: \"make lint\"\n  when: CHECK_WHEN_PUSH\n}\nmerge_main: MERGE_MAIN_RELEASE\n" +
			"watch_commands: \"a\"\nserve_commands: \"b\"\n",
			"checks {\n  name: \"lint\"\n  command: \"make lint\"\n  when: CHECK_WHEN_PUSH\n}\nmerge_main: MERGE_MAIN_RELEASE\n" +
				"watch_commands: \"a\"\nserve_commands: \"b\"\n"},
		"p3": {seats, ""},
		"p4": {"watch_commands: \"x\"\nwatch_commands: \"y\"\n", "watch_commands: \"x\"\nwatch_commands: \"y\"\n"},
	}
	for id, f := range files {
		writeSettings(t, DeveloperSettingsFile(home, id), f[0])
	}
	work := func() (planv1.Provider, string) {
		p, m, _ := ResolveWorkerSeat(home, planv1.WorkerKind_WORKER_KIND_WORK)
		return p, m
	}
	wp, wm := work()

	migrateTwice(t, home)

	for id, f := range files {
		if b, err := os.ReadFile(DeveloperSettingsFile(home, id)); err != nil || string(b) != f[1] {
			t.Errorf("%s = %q (%v), want %q", id, b, err, f[1])
		}
	}
	got := readSettings(home)
	if !proto.Equal(&uiv1.Settings{Lead: got.GetLead(), Circle: got.GetCircle()}, circle) {
		t.Errorf("circle = %v, want it unchanged", got.GetCircle())
	}
	if !slices.Equal(got.GetMigrations(), []string{"circle-from-project-settings"}) {
		t.Errorf("migrations = %v", got.GetMigrations())
	}
	if p, m := work(); p != wp || m != wm || p != planv1.Provider_PROVIDER_CLAUDE || m != "claude-opus-5-5" {
		t.Errorf("work seat = %v %s, want %v %s as before", p, m, wp, wm)
	}
	if s, _ := LoadSettings(home, &planv1.Project{Id: "p1"}); s.Provider == planv1.Provider_PROVIDER_ANTIGRAVITY {
		t.Errorf("the project's settings still show antigravity: %+v", s)
	}
}

// A question model Antigravity cannot run read-only no longer keeps the stale lines (L20): the worker seat moves to a
// circle not configured yet, the question seat is dropped and its row written "Same as lead" (Q67).
func TestMigrate_UnmigratableQuestionModel(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	devFile := DeveloperSettingsFile(home, "p1")
	writeSettings(t, devFile, "provider: PROVIDER_ANTIGRAVITY\nmodel: \"gemini-4-argon-high\"\n"+
		"question_model: \"gemini-3.8-flash-high\"\npush: PROJECT_PUSH_ON_DEMAND\n")
	migrateTwice(t, home)
	if b, _ := os.ReadFile(devFile); string(b) != "push: PROJECT_PUSH_ON_DEMAND\n" {
		t.Errorf("devFile = %q, want the stale lines gone", b)
	}
	byKind := map[planv1.WorkerKind]*planv1.WorkerSeat{}
	for _, r := range readSettings(home).GetCircle() {
		byKind[r.GetKind()] = r
	}
	if r := byKind[planv1.WorkerKind_WORKER_KIND_WORK]; r.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY ||
		r.GetModel() != "gemini-4-argon-high" {
		t.Errorf("work row = %v, want the project's seat", r)
	}
	if r, ok := byKind[planv1.WorkerKind_WORKER_KIND_QUESTION]; !ok || !sameAsLead(r) {
		t.Errorf("question row = %v (written %v), want Same as lead", r, ok)
	}
}

// "Same as lead" is a model choice written in the circle (Q67): a circle whose lead is on Claude and whose rows are Same
// as lead keeps every worker on the lead's model after the migration, a project on Antigravity notwithstanding, and
// settings.json then names each row. The circle's three shapes: every row written, the lead moved before any row was
// set, the rows set while the lead kept its default.
func TestMigrate_SameAsLeadStays(t *testing.T) {
	t.Parallel()
	sameRows := func() []*planv1.WorkerSeat {
		var rows []*planv1.WorkerSeat
		for _, kind := range CircleRowOrder[1:] {
			rows = append(rows, &planv1.WorkerSeat{Kind: kind})
		}
		return rows
	}
	opus := &planv1.LeadSeat{Provider: planv1.Provider_PROVIDER_CLAUDE, Model: "claude-opus-5-5"}
	for _, c := range []struct {
		name      string
		settings  *uiv1.Settings
		leadModel string
	}{
		{"every row", &uiv1.Settings{Lead: opus, Circle: append(sameRows(), &planv1.WorkerSeat{
			Kind: planv1.WorkerKind_WORKER_KIND_LEAD, Provider: opus.Provider, Model: opus.Model,
		})}, "claude-opus-5-5"},
		{"lead moved, no row", &uiv1.Settings{Lead: opus}, "claude-opus-5-5"},
		{"rows, lead by default", &uiv1.Settings{Circle: sameRows()}, DefaultCircleModel(planv1.Provider_PROVIDER_CLAUDE)},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			data, err := protojson.Marshal(c.settings)
			if err != nil {
				t.Fatal(err)
			}
			writeSettings(t, filepath.Join(home, settingsFile), string(data))
			devFile := DeveloperSettingsFile(home, "p1")
			writeSettings(t, devFile, "provider: PROVIDER_ANTIGRAVITY\nmodel: \"gemini-4-argon-high\"\n"+
				"question_provider: PROVIDER_CODEX\nquestion_model: \"gpt-5.3-codex\"\npush: PROJECT_PUSH_ON_DEMAND\n")

			migrateTwice(t, home)

			if b, _ := os.ReadFile(devFile); string(b) != "push: PROJECT_PUSH_ON_DEMAND\n" {
				t.Errorf("devFile = %q, want the stale lines gone", b)
			}
			written := map[planv1.WorkerKind]*planv1.WorkerSeat{}
			for _, r := range readSettings(home).GetCircle() {
				written[r.GetKind()] = r
			}
			for _, kind := range CircleRowOrder[1:] {
				if r, ok := written[kind]; !ok || !sameAsLead(r) {
					t.Errorf("%v row = %v (written %v), want Same as lead written in settings.json", kind, r, ok)
				}
				p, m, explicit := ResolveWorkerSeat(home, kind)
				if p != planv1.Provider_PROVIDER_CLAUDE || m != c.leadModel || !explicit {
					t.Errorf("%v seat = %v %s explicit %v, want the lead's CLAUDE %s", kind, p, m, explicit, c.leadModel)
				}
			}
			for _, row := range ReadCircle(home)[1:] {
				if !sameAsLead(row) {
					t.Errorf("circle shows %v, want Same as lead", row)
				}
			}
			if lead := ReadLeadSeat(home); c.settings.Lead != nil && lead.GetModel() != c.leadModel {
				t.Errorf("lead seat = %v, want it unchanged", lead)
			}
		})
	}
}

// A file the migration cannot read stays as it was, the migration is not recorded, and the next start applies it once
// the file reads.
func TestMigrate_CannotApply(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	devFile := DeveloperSettingsFile(home, "p1")
	bad := "provider: PROVIDER_CLAUDE\nmodel: \"opus\"\napi_key: \"sk-ant-0123456789abcdefghijkl\"\n"
	writeSettings(t, devFile, bad)
	Migrate(home)
	if b, _ := os.ReadFile(devFile); string(b) != bad {
		t.Errorf("devFile = %q, want untouched", b)
	}
	if m := readSettings(home).GetMigrations(); len(m) != 0 {
		t.Errorf("migrations = %v, want none recorded", m)
	}
	writeSettings(t, devFile, "provider: PROVIDER_CLAUDE\nmodel: \"opus\"\n")
	Migrate(home)
	if b, _ := os.ReadFile(devFile); len(b) != 0 {
		t.Errorf("devFile = %q, want the lines gone", b)
	}
	if m := readSettings(home).GetMigrations(); !slices.Equal(m, []string{"circle-from-project-settings"}) {
		t.Errorf("migrations = %v", m)
	}
}

// A migration that panics or fails never stops the start, and is not recorded; the others still run.
func TestMigrate_Broken(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ran := 0
	migrate(home, []migration{
		{name: "panics", run: func(string) error { panic("boom") }},
		{name: "fails", run: func(string) error { return os.ErrPermission }},
		{name: "applies", run: func(string) error { ran++; return nil }},
	})
	migrate(home, []migration{{name: "applies", run: func(string) error { ran++; return nil }}})
	if m := readSettings(home).GetMigrations(); !slices.Equal(m, []string{"applies"}) || ran != 1 {
		t.Errorf("migrations = %v, ran %d; want applies alone, run once", m, ran)
	}
}
