package main

// The lead of the first active wish comes back at every start: from a menu, after an update, after a crash. The
// developer finds the work where they left it, in the lead's terminal, on its session, in a project: never in the
// home folder. A lead that runs already, reopened by a restart, is left as it is.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// resumeFirstLead resumes the lead of the first active wish, as djinn wish resume does, unless a lead's terminal runs
// already or no wish is active. What it could not do, it says on say.
func resumeFirstLead(ctx context.Context, db *store.Store, terms *terminal.Manager, svc *ui.Service, say io.Writer) {
	for _, t := range terms.Running() {
		if strings.HasPrefix(t.Name, "lead-") {
			return
		}
	}
	actives, err := plan.ActiveWishes(ctx, db)
	if err != nil || len(actives) == 0 {
		return
	}
	first := actives[0]
	wishes := &plan.Wishes{Store: db, Leads: leads{terms, svc}}
	res, err := wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: first.GetId()}))
	if err != nil {
		fmt.Fprintf(say, "djinn: the lead of %q is not resumed: %v\n", first.GetTitle(), err)
		return
	}
	fmt.Fprintf(say, "djinn: the lead of %q resumed in %s\n", first.GetTitle(), res.Msg.GetDirectory())
	if note := res.Msg.GetNote(); note != "" {
		fmt.Fprintln(say, "djinn:", note)
	}
}
