package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// instructionRow writes a resolved row as "machine | repository | developer -> text (source)", an unset layer "-",
// and "general" after a kind that takes the general text.
func instructionRow(r *planv1.WorkerInstruction) string {
	side := func(v *string) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("%q", *v)
	}
	out := side(r.Machine) + " | " + side(r.Repository) + " | " + side(r.Developer) + " -> " +
		fmt.Sprintf("%q", r.GetText()) + " (" + strings.TrimPrefix(r.GetSource().String(), "SETTING_SOURCE_") + ")"
	if r.GetGeneral() {
		out += " general"
	}
	return out
}

// TestResolveInstructions (W114, Q70 B): three layers kind by kind, the developer's over the repository's over the
// machine's; a kind no layer sets takes the general text retained; an empty text is a text.
func TestResolveInstructions(t *testing.T) {
	t.Parallel()
	machine := &planv1.WorkerInstructions{General: proto.String("m general"), Mockup: proto.String("m mockup")}
	for _, tc := range []struct {
		name               string
		machine, repo, dev *planv1.WorkerInstructions
		// kind -> its row, for the kinds the case looks at.
		want map[planv1.InstructionKind]string
	}{
		{
			name: "one kind, three layers", machine: machine,
			repo: &planv1.WorkerInstructions{Mockup: proto.String("r mockup")},
			dev:  &planv1.WorkerInstructions{Mockup: proto.String("d mockup")},
			want: map[planv1.InstructionKind]string{
				planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP: `"m mockup" | "r mockup" | "d mockup" -> "d mockup" (DEVELOPER)`,
			},
		},
		{
			name: "the repository wins over the machine", machine: machine,
			repo: &planv1.WorkerInstructions{General: proto.String("r general")},
			want: map[planv1.InstructionKind]string{
				planv1.InstructionKind_INSTRUCTION_KIND_GENERAL: `"m general" | "r general" | - -> "r general" (REPOSITORY)`,
				planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP:  `"m mockup" | - | - -> "m mockup" (MACHINE)`,
			},
		},
		{
			name: "a kind no layer sets takes the general text", machine: machine,
			dev: &planv1.WorkerInstructions{General: proto.String("d general")},
			want: map[planv1.InstructionKind]string{
				planv1.InstructionKind_INSTRUCTION_KIND_WORK:  `- | - | - -> "d general" (DEVELOPER) general`,
				planv1.InstructionKind_INSTRUCTION_KIND_IFRIT: `- | - | - -> "d general" (DEVELOPER) general`,
			},
		},
		{
			name: "an empty text is a text",
			repo: &planv1.WorkerInstructions{General: proto.String("r general"), Work: proto.String("r work")},
			dev:  &planv1.WorkerInstructions{Work: proto.String("")},
			want: map[planv1.InstructionKind]string{
				planv1.InstructionKind_INSTRUCTION_KIND_WORK: `- | "r work" | "" -> "" (DEVELOPER)`,
			},
		},
		{
			name: "no layer",
			want: map[planv1.InstructionKind]string{
				planv1.InstructionKind_INSTRUCTION_KIND_GENERAL:       `- | - | - -> "" (DEFAULT)`,
				planv1.InstructionKind_INSTRUCTION_KIND_INVESTIGATION: `- | - | - -> "" (DEFAULT) general`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := ResolveInstructions(tc.machine, tc.repo, tc.dev)
			if len(rows) != len(InstructionKinds) {
				t.Fatalf("%d rows; want one per kind, %d", len(rows), len(InstructionKinds))
			}
			for i, r := range rows {
				if r.GetKind() != InstructionKinds[i] {
					t.Errorf("row %d is %v; want %v", i, r.GetKind(), InstructionKinds[i])
				}
				if want, ok := tc.want[r.GetKind()]; ok {
					if got := instructionRow(r); got != want {
						t.Errorf("%v: %s\nwant %s", r.GetKind(), got, want)
					}
				}
			}
		})
	}
}

// TestInstructionsFor: a work task, an investigation, a mockup and an ifrit take their kind's text; a correction, a
// review and a question worker the general one; the lead none.
func TestInstructionsFor(t *testing.T) {
	t.Parallel()
	rows := ResolveInstructions(nil, &planv1.WorkerInstructions{
		General: proto.String("all"), Work: proto.String("work"), Investigation: proto.String("read"),
		Mockup: proto.String("draw"), Ifrit: proto.String("talk"),
	}, nil)
	for kind, want := range map[planv1.WorkerKind]string{
		planv1.WorkerKind_WORKER_KIND_WORK:          "work",
		planv1.WorkerKind_WORKER_KIND_INVESTIGATION: "read",
		planv1.WorkerKind_WORKER_KIND_MOCKUP:        "draw",
		planv1.WorkerKind_WORKER_KIND_IFRIT:         "talk",
		planv1.WorkerKind_WORKER_KIND_CORRECTION:    "all",
		planv1.WorkerKind_WORKER_KIND_REVIEW:        "all",
		planv1.WorkerKind_WORKER_KIND_QUESTION:      "all",
		planv1.WorkerKind_WORKER_KIND_LEAD:          "",
	} {
		if got := InstructionsFor(rows, kind); got != want {
			t.Errorf("%v: %q; want %q", kind, got, want)
		}
	}
}

// TestInstructions: the window sets the Summoning Circle's text in settings.json (LeadService.Instructions) and
// yours in your own file of the project (ProjectService.Instructions); the repository's comes from its djinn.yaml;
// djinn project show and the settings a worker is launched with resolve the three; unset gives a kind back to the
// layers below.
func TestInstructions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	dir := t.TempDir()
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: "app"}))
	if err != nil {
		t.Fatal(err)
	}
	project := add.Msg.GetProject()
	writeSettings(t, filepath.Join(dir, ".agents", "djinn.yaml"), teamYAML(
		"instructions:\n  general: Run the tasks, not the tools.\n  mockup: Both themes.\n"))

	setMachine := func(kind planv1.InstructionKind, text string, unset bool) *planv1.WorkerInstructions {
		t.Helper()
		res, err := c.lead.Instructions(ctx, connect.NewRequest(&planv1.LeadServiceInstructionsRequest{
			Kind: kind, Text: text, Unset: unset,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetInstructions()
	}
	setMine := func(kind planv1.InstructionKind, text string, unset bool) []*planv1.WorkerInstruction {
		t.Helper()
		res, err := c.projects.Instructions(ctx, connect.NewRequest(&planv1.ProjectServiceInstructionsRequest{
			Project: "app", Kind: kind, Text: text, Unset: unset,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetInstructions()
	}
	kinds := func(rows []*planv1.WorkerInstruction) map[planv1.InstructionKind]string {
		out := map[planv1.InstructionKind]string{}
		for _, r := range rows {
			out[r.GetKind()] = instructionRow(r)
		}
		return out
	}

	setMachine(planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP, "Light first.", false)
	got := setMachine(planv1.InstructionKind_INSTRUCTION_KIND_IFRIT, "Say the URL.", false)
	if got.GetMockup() != "Light first." || got.GetIfrit() != "Say the URL." || got.General != nil {
		t.Errorf("machine %v; want mockup and ifrit", got)
	}
	circle, err := c.lead.GetCircle(ctx, connect.NewRequest(&planv1.LeadServiceGetCircleRequest{}))
	if err != nil || !proto.Equal(circle.Msg.GetInstructions(), got) {
		t.Errorf("GetCircle instructions %v, %v; want %v", circle.Msg.GetInstructions(), err, got)
	}
	rows := kinds(setMine(planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP, "Dark first.", false))
	for kind, want := range map[planv1.InstructionKind]string{
		planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP: `"Light first." | "Both themes." | "Dark first." -> "Dark first." (DEVELOPER)`,
		planv1.InstructionKind_INSTRUCTION_KIND_IFRIT:  `"Say the URL." | - | - -> "Say the URL." (MACHINE)`,
		planv1.InstructionKind_INSTRUCTION_KIND_WORK:   `- | - | - -> "Run the tasks, not the tools." (REPOSITORY) general`,
	} {
		if rows[kind] != want {
			t.Errorf("%v: %s\nwant %s", kind, rows[kind], want)
		}
	}
	show, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "app"}))
	if err != nil {
		t.Fatal(err)
	}
	if shown := kinds(show.Msg.GetInstructions()); fmt.Sprint(shown) != fmt.Sprint(rows) {
		t.Errorf("show %v\nwant %v", shown, rows)
	}
	settings, err := LoadSettings(home, project)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.InstructionsFor(planv1.WorkerKind_WORKER_KIND_MOCKUP); got != "Dark first." {
		t.Errorf("a mockup worker gets %q; want yours", got)
	}

	// Unset, the repository's text applies again; your file keeps no empty instructions.
	rows = kinds(setMine(planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP, "", true))
	if want := `"Light first." | "Both themes." | - -> "Both themes." (REPOSITORY)`; rows[planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP] != want {
		t.Errorf("mockup unset: %s\nwant %s", rows[planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP], want)
	}
	if b, err := os.ReadFile(DeveloperSettingsFile(home, project.GetId())); err != nil || strings.Contains(string(b), "instructions") {
		t.Errorf("your file %q, %v; want no instructions left", b, err)
	}
	if got := setMachine(planv1.InstructionKind_INSTRUCTION_KIND_IFRIT, "", true); got.GetIfrit() != "" || got.Mockup == nil {
		t.Errorf("machine after unset %v; want the mockup alone", got)
	}

	// Without a kind, each only shows.
	if got := setMachine(planv1.InstructionKind_INSTRUCTION_KIND_UNSPECIFIED, "ignored", false); got.GetMockup() != "Light first." ||
		got.General != nil {
		t.Errorf("machine shown %v; want it unchanged", got)
	}
	shown := kinds(setMine(planv1.InstructionKind_INSTRUCTION_KIND_UNSPECIFIED, "ignored", false))
	if want := `- | "Run the tasks, not the tools." | - -> "Run the tasks, not the tools." (REPOSITORY)`; shown[planv1.InstructionKind_INSTRUCTION_KIND_GENERAL] != want {
		t.Errorf("general shown: %s\nwant %s", shown[planv1.InstructionKind_INSTRUCTION_KIND_GENERAL], want)
	}
}
