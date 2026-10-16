package harness

import (
	"net/http"
	"sync"
	"time"

	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
	"github.com/empowill/djinn/internal/plan"
)

// Presence is when the developer last did something in Djinn (T51): a key, a click or a scroll in the window in front
// (UiService.Touch), a key in a terminal, an answer, a mark, a choice in the lead's terminal, a tell, a message, a
// djinn command they typed, the Keep lit button of a warning. The lead, the workers and the watchers are no
// activity. It keeps the ifrits lit: each one goes out once the developer left Djinn alone for its idle delay
// (IfritIdle), warned ifritWarnAhead before.
type Presence struct {
	mu      sync.Mutex
	last    time.Time
	now     func() time.Time
	touched func() // the harness looks at its ifrits again
}

// Touch says that the developer is there, now.
func (p *Presence) Touch() {
	p.mu.Lock()
	p.last = p.now()
	p.mu.Unlock()
	if p.touched != nil {
		p.touched()
	}
}

// Last is when the developer last did something in Djinn; zero when they did nothing since it started.
func (p *Presence) Last() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// Presence is the developer's presence, which keeps the ifrits lit.
func (h *Harness) Presence() *Presence { return h.presence }

// CallerHeader is the header in which djinn's command line says who runs it: CallerYou for the developer in a
// shell, CallerLead for the lead's terminal ($DJINN_LEAD=1), CallerDjinn for a process Djinn started ($DJINN_TASK_ID:
// a worker, a watcher). A worker is told apart by its token or its process before this header.
const CallerHeader = "X-Djinn-Caller"

// Who runs a djinn command, in CallerHeader.
const (
	CallerYou   = "you"
	CallerLead  = "lead"
	CallerDjinn = "djinn"
)

// yourActions are the requests of the window that are the developer's own actions. The window's other requests (it
// reads, it follows) come with it open, touched or not. Its touches (UiService.Touch) say so themselves.
var yourActions = map[string]bool{
	terminalv1connect.TerminalServiceWriteProcedure: true,
	planv1connect.QuestionServiceAnswerProcedure:    true,
	planv1connect.QuestionServiceEnlightenProcedure: true,
	planv1connect.MarkServicePutProcedure:           true,
	planv1connect.LeadServiceChooseProcedure:        true,
	planv1connect.LeadServiceTellProcedure:          true,
	planv1connect.TaskServiceSendProcedure:          true,
}

// Touching passes every request on to next, and counts the developer's as their presence: any request of djinn's
// command line they type in a shell, and the window's actions (yourActions). A worker's (plan.CallerFromContext, set
// by server.WorkerContext around it), the lead's and those of the processes Djinn starts are none.
func Touching(next http.Handler, p *Presence) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if yours(r) {
			p.Touch()
		}
		next.ServeHTTP(w, r)
	})
}

// yours tells whether the request is the developer's own action.
func yours(r *http.Request) bool {
	if plan.CallerFromContext(r.Context()) != nil {
		return false
	}
	switch r.Header.Get(CallerHeader) {
	case CallerYou:
		return true
	case "":
		return yourActions[r.URL.Path]
	}
	return false
}
