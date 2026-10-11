package main

// The lead comes back at every start: from a menu, after an update, after a crash. The developer finds the work
// where they left it, in the lead's terminal, on its session, in a project: never in the home folder, never a shell.
// A lead that runs already, reopened by a restart, is left as it is. The first start after an update from one lead
// per wish makes a wish's lead the one lead.

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// carryLead makes the lead a wish kept, in a store of a Djinn of one lead per wish, the one lead, once, and returns
// the lead recorded: its session is the one a terminal of the restart's note may run.
func carryLead(ctx context.Context, db *store.Store, say io.Writer) *planv1.Lead {
	carried, err := plan.CarryLead(ctx, db)
	if err != nil {
		fmt.Fprintln(say, "djinn: the lead of the wishes is not carried over:", err)
	} else if carried != nil {
		fmt.Fprintf(say, "djinn: the lead of the wishes is the lead now: %s in %s\n",
			carried.GetSessionId(), carried.GetDirectory())
	}
	lead, _ := store.Get[*planv1.Lead](ctx, db, "lead")
	return lead
}

// resumeLead resumes the lead, as djinn lead resume does, unless the lead's terminal runs
// already: without a session, a new lead starts from the brief, its first message in home. What it could not do,
// it says on say.
func resumeLead(ctx context.Context, db *store.Store, home string, terms *terminal.Manager, svc *ui.Service, say io.Writer) {
	for _, t := range terms.Running() {
		if t.Name == plan.LeadTerminal {
			return
		}
	}
	leadSvc := &plan.LeadService{Wishes: &plan.Wishes{Store: db, Leads: leads{terms, svc}, Home: home}}
	res, err := leadSvc.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
	if err != nil {
		fmt.Fprintf(say, "djinn: the lead is not resumed: %v\n", err)
		return
	}
	fmt.Fprintf(say, "djinn: the lead resumed in %s\n", res.Msg.GetDirectory())
	if note := res.Msg.GetNote(); note != "" {
		fmt.Fprintln(say, "djinn:", note)
	}
}
