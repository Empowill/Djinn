package plan

// The instructions a project's workers get at their launch, by kind of task (W114, decision Q70 B): a general text and
// one for each kind that wants its own (work, investigation, mockup, ifrit), in three layers resolved kind by kind, as
// the other settings are: the Summoning Circle's (settings.json, this machine's default for every project), the
// repository's (settings: instructions: in its djinn.yaml), which wins over it, and the developer's own file of the
// project, which wins over both. A kind no layer sets takes the general text retained. Nothing by agent: each agent has
// its own instructions outside Djinn.

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

// InstructionKinds are the texts of the workers' instructions, in the order the window and djinn project show list
// them: the general one first.
var InstructionKinds = []planv1.InstructionKind{
	planv1.InstructionKind_INSTRUCTION_KIND_GENERAL,
	planv1.InstructionKind_INSTRUCTION_KIND_WORK,
	planv1.InstructionKind_INSTRUCTION_KIND_INVESTIGATION,
	planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP,
	planv1.InstructionKind_INSTRUCTION_KIND_IFRIT,
}

// instructionText is where the text of kind lives in w: nil for a kind with no text of its own.
func instructionText(w *planv1.WorkerInstructions, kind planv1.InstructionKind) **string {
	switch kind {
	case planv1.InstructionKind_INSTRUCTION_KIND_GENERAL:
		return &w.General
	case planv1.InstructionKind_INSTRUCTION_KIND_WORK:
		return &w.Work
	case planv1.InstructionKind_INSTRUCTION_KIND_INVESTIGATION:
		return &w.Investigation
	case planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP:
		return &w.Mockup
	case planv1.InstructionKind_INSTRUCTION_KIND_IFRIT:
		return &w.Ifrit
	default:
		return nil
	}
}

// layerText is the text layer w sets for kind: nil when it sets none, or when w is nil.
func layerText(w *planv1.WorkerInstructions, kind planv1.InstructionKind) *string {
	if w == nil {
		return nil
	}
	if at := instructionText(w, kind); at != nil {
		return *at
	}
	return nil
}

// ResolveInstructions resolves the workers' instructions from the Summoning Circle's machine, the repository's repo
// and the developer's dev, any nil when it sets none: one row per text, in InstructionKinds' order. Kind by kind, the
// developer's win over the repository's, which win over the machine's; a kind no layer sets takes the general text
// retained, and says so.
func ResolveInstructions(machine, repo, dev *planv1.WorkerInstructions) []*planv1.WorkerInstruction {
	rows := make([]*planv1.WorkerInstruction, 0, len(InstructionKinds))
	var general *planv1.WorkerInstruction
	for _, kind := range InstructionKinds {
		row := &planv1.WorkerInstruction{Kind: kind, Source: planv1.SettingSource_SETTING_SOURCE_DEFAULT}
		for _, layer := range []struct {
			text  *string
			from  planv1.SettingSource
			value **string
		}{
			{layerText(machine, kind), planv1.SettingSource_SETTING_SOURCE_MACHINE, &row.Machine},
			{layerText(repo, kind), planv1.SettingSource_SETTING_SOURCE_REPOSITORY, &row.Repository},
			{layerText(dev, kind), planv1.SettingSource_SETTING_SOURCE_DEVELOPER, &row.Developer},
		} {
			if layer.text != nil {
				*layer.value = proto.String(*layer.text)
				row.Text, row.Source = *layer.text, layer.from
			}
		}
		if kind == planv1.InstructionKind_INSTRUCTION_KIND_GENERAL {
			general = row
		} else if row.Source == planv1.SettingSource_SETTING_SOURCE_DEFAULT && general != nil {
			row.Text, row.Source, row.General = general.GetText(), general.GetSource(), true
		}
		rows = append(rows, row)
	}
	return rows
}

// InstructionKindOf is the text of the workers' instructions a worker of kind gets: its own kind's for a work task, an
// investigation, a mockup and an ifrit; the general text for the others (correction, review, question); none for the
// lead, which is no worker of a project.
func InstructionKindOf(kind planv1.WorkerKind) planv1.InstructionKind {
	switch kind {
	case planv1.WorkerKind_WORKER_KIND_WORK:
		return planv1.InstructionKind_INSTRUCTION_KIND_WORK
	case planv1.WorkerKind_WORKER_KIND_INVESTIGATION:
		return planv1.InstructionKind_INSTRUCTION_KIND_INVESTIGATION
	case planv1.WorkerKind_WORKER_KIND_MOCKUP:
		return planv1.InstructionKind_INSTRUCTION_KIND_MOCKUP
	case planv1.WorkerKind_WORKER_KIND_IFRIT:
		return planv1.InstructionKind_INSTRUCTION_KIND_IFRIT
	case planv1.WorkerKind_WORKER_KIND_LEAD, planv1.WorkerKind_WORKER_KIND_UNSPECIFIED:
		return planv1.InstructionKind_INSTRUCTION_KIND_UNSPECIFIED
	default:
		return planv1.InstructionKind_INSTRUCTION_KIND_GENERAL
	}
}

// InstructionsFor is the text a worker of kind gets from the instructions rows resolved: "" when none.
func InstructionsFor(rows []*planv1.WorkerInstruction, kind planv1.WorkerKind) string {
	want := InstructionKindOf(kind)
	for _, r := range rows {
		if r.GetKind() == want {
			return r.GetText()
		}
	}
	return ""
}

// InstructionsFor is the text a worker of kind gets from the project's settings: "" when none.
func (s Settings) InstructionsFor(kind planv1.WorkerKind) string {
	return InstructionsFor(s.Instructions, kind)
}

// ReadMachineInstructions are the workers' instructions of the Summoning Circle, in settings.json in home: nil when it
// sets none.
func ReadMachineInstructions(home string) *planv1.WorkerInstructions {
	return readSettings(home).GetInstructions()
}

// setInstruction sets the text of kind in w to text, or removes it with unset: w changed, made when nil.
func setInstruction(w *planv1.WorkerInstructions, kind planv1.InstructionKind, text string, unset bool) (*planv1.WorkerInstructions, error) {
	if w == nil {
		w = &planv1.WorkerInstructions{}
	}
	at := instructionText(w, kind)
	if at == nil {
		return nil, fmt.Errorf("unknown kind of instructions %v", kind)
	}
	if unset {
		*at = nil
	} else {
		*at = proto.String(text)
	}
	return w, nil
}

// emptyInstructions says whether w sets no text: it is left out of the file.
func emptyInstructions(w *planv1.WorkerInstructions) bool {
	for _, kind := range InstructionKinds {
		if layerText(w, kind) != nil {
			return false
		}
	}
	return true
}

// SetMachineInstructions sets the text of kind in the Summoning Circle's instructions, in settings.json in home, or
// removes it with unset: the instructions then.
func SetMachineInstructions(home string, kind planv1.InstructionKind, text string, unset bool) (*planv1.WorkerInstructions, error) {
	var out *planv1.WorkerInstructions
	err := changeSettings(home, func(settings *uiv1.Settings) error {
		w, err := setInstruction(settings.GetInstructions(), kind, text, unset)
		if err != nil {
			return err
		}
		if emptyInstructions(w) {
			w = nil
		}
		settings.Instructions, out = w, w
		return nil
	})
	return out, err
}

// SaveDeveloperInstructions sets the text of kind in the developer's settings file of the project in home, or removes
// it with unset.
func SaveDeveloperInstructions(home, projectID string, kind planv1.InstructionKind, text string, unset bool) error {
	_, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		w, err := setInstruction(s.GetInstructions(), kind, text, unset)
		if err != nil {
			return err
		}
		if emptyInstructions(w) {
			w = nil
		}
		s.Instructions = w
		return nil
	})
	return err
}

// Instructions implements ProjectService.Instructions: the workers' instructions of the project as the three layers
// resolve them; with a kind, one text set in the developer's own file of the project first.
func (p *Projects) Instructions(
	ctx context.Context, req *connect.Request[planv1.ProjectServiceInstructionsRequest],
) (*connect.Response[planv1.ProjectServiceInstructionsResponse], error) {
	project, err := ProjectNamed(ctx, p.Store, req.Msg.GetProject())
	if err != nil {
		return nil, Status(err)
	}
	if kind := req.Msg.GetKind(); kind != planv1.InstructionKind_INSTRUCTION_KIND_UNSPECIFIED {
		err = write(ctx, p.Store, req.Spec(), req.Msg, func(*store.Tx) error {
			return SaveDeveloperInstructions(p.Home, project.GetId(), kind, req.Msg.GetText(), req.Msg.GetUnset())
		})
		if err != nil {
			return nil, Status(err)
		}
	}
	repo, dev, _, problems := loadSettings(p.Home, project)
	if len(problems) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.Join(problems...))
	}
	return connect.NewResponse(&planv1.ProjectServiceInstructionsResponse{
		Instructions: ResolveInstructions(ReadMachineInstructions(p.Home), repo.GetInstructions(), dev.GetInstructions()),
	}), nil
}

// Instructions implements LeadService.Instructions: the workers' instructions on this machine, the Summoning
// Circle's; with a kind, one text set first.
func (s *LeadService) Instructions(
	ctx context.Context, req *connect.Request[planv1.LeadServiceInstructionsRequest],
) (*connect.Response[planv1.LeadServiceInstructionsResponse], error) {
	home := s.Wishes.home()
	kind := req.Msg.GetKind()
	if kind == planv1.InstructionKind_INSTRUCTION_KIND_UNSPECIFIED {
		return connect.NewResponse(&planv1.LeadServiceInstructionsResponse{Instructions: ReadMachineInstructions(home)}), nil
	}
	var out *planv1.WorkerInstructions
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		var err error
		out, err = SetMachineInstructions(home, kind, req.Msg.GetText(), req.Msg.GetUnset())
		return err
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.LeadServiceInstructionsResponse{Instructions: out}), nil
}
