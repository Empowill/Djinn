package harness

import (
	"context"
	"errors"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// HoldsLong tells the lead of the task's wish, as a health event, that the task holds a gate, or an admission of its
// command, long: line says which and how long (gate.LongTimes). The gates never take it back from a command that lives
// (G5 of L04): the lead sees whether it hangs. It does not call the gates back.
func (h *Harness) HoldsLong(taskID, line string) {
	go func() {
		defer logx.Crash("tell a long hold", "task", taskID)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t, err := store.Get[*planv1.Task](ctx, h.store, taskID)
		if err != nil || t.GetWishId() == "" {
			logx.L().Warn("a gate held long", "task", taskID, "line", line, "err", err)
			return
		}
		if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, t.GetWishId(), t.GetCode(), line); err != nil &&
			!errors.Is(err, plan.ErrNoLead) {
			logx.L().Warn("tell the lead", "wish", t.GetWishId(), "err", err)
		}
	}()
}
