package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestSpawnIfrit: djinn task spawn --kind ifrit stores an ifrit, with its own idle minutes when given, and its run
// keeps them; --idle-minutes is for an ifrit only, and an ifrit is an agent in a project (T51).
func TestSpawnIfrit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.mustSpawn(t, wishID, "Front of the lamp", "text listening", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, IdleMinutes: 60,
	})
	if task.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT || task.GetIfrit().GetIdleMinutes() != 60 {
		t.Errorf("spawned %v %v, want an ifrit of 60 minutes", task.GetKind(), task.GetIfrit())
	}
	if got := e.ended(t, task.GetId()); got.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT || got.GetIfrit().GetIdleMinutes() != 60 {
		t.Errorf("after its run: %v %v, want the ifrit of 60 minutes still", got.GetKind(), got.GetIfrit())
	}
	plain := e.mustSpawn(t, wishID, "Default pace", "text hi", &planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_IFRIT})
	if plain.GetIfrit() == nil || plain.GetIfrit().GetIdleMinutes() != 0 {
		t.Errorf("an ifrit without --idle-minutes: %v, want one at this machine's default (0)", plain.GetIfrit())
	}
	e.ended(t, plain.GetId())
	work := e.mustSpawn(t, wishID, "Work", "text hi", nil)
	if work.GetIfrit() != nil || work.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT {
		t.Errorf("a work task is an ifrit: %v %v", work.GetKind(), work.GetIfrit())
	}
	e.ended(t, work.GetId())

	refused := map[string]*planv1.TaskServiceSpawnRequest{
		"--idle-minutes is for an ifrit": {IdleMinutes: 30},
		"an ifrit is an agent":           {Kind: planv1.TaskKind_TASK_KIND_IFRIT, Provider: planv1.Provider_PROVIDER_WATCH},
	}
	for want, req := range refused {
		if _, err := e.spawnReq(t, wishID, "Refused", "text hi", req); connect.CodeOf(err) != connect.CodeInvalidArgument ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("spawn %v: %v, want invalid_argument: %s", req, err, want)
		}
	}
	bare, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Nowhere"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.spawnReq(t, bare.Msg.GetWish().GetId(), "Ifrit", "text hi", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT,
	}); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "an ifrit works in a project") {
		t.Errorf("an ifrit outside any project: %v", err)
	}
}

// TestIfritIdle: how long an ifrit stays lit without the developer is its own, else djinn up --ifrit-idle, else the
// window's settings, else 30 minutes.
func TestIfritIdle(t *testing.T) {
	home := t.TempDir()
	own := &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{IdleMinutes: 60}}
	plain := &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{}}
	h := &Harness{home: home}
	if got := h.IfritIdle(plain); got != DefaultIfritIdle {
		t.Errorf("no setting: %v, want %v", got, DefaultIfritIdle)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"ifritIdleMinutes": 20}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := h.IfritIdle(plain); got != 20*time.Minute {
		t.Errorf("the window's settings: %v, want 20m", got)
	}
	WithIfritIdle(45 * time.Minute)(h)
	if got := h.IfritIdle(plain); got != 45*time.Minute {
		t.Errorf("djinn up --ifrit-idle: %v, want 45m", got)
	}
	if got := h.IfritIdle(own); got != time.Hour {
		t.Errorf("its own: %v, want 1h", got)
	}
}
