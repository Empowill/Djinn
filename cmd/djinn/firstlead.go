package main

// The lead of the first active wish comes back at every start: from a menu, after an update, after a crash. The
// developer finds the work where they left it, in the lead's terminal, on its session, in a project: never in the
// home folder. A lead that runs already, reopened by a restart, is left as it is.

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

// resumeLead resumes the lead, as djinn lead resume does, unless the lead's terminal runs
// already. What it could not do, it says on say.
func resumeLead(ctx context.Context, db *store.Store, terms *terminal.Manager, svc *ui.Service, say io.Writer) {
	for _, t := range terms.Running() {
		if t.Name == plan.LeadTerminal {
			return
		}
	}
	leadSvc := &plan.LeadService{Wishes: &plan.Wishes{Store: db, Leads: leads{terms, svc}}}
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
