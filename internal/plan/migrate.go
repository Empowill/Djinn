package plan

import (
	"errors"
	"fmt"
	"slices"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/logx"
)

// A migration changes the local settings of a Djinn installation (its data folder) once, at start (docs/migrations.md).
// Its name is recorded in settings.json once it applied; run is always idempotent: run twice, it changes nothing the
// second time. run returns an error, leaving the data it could not change as it was, when it cannot apply.
type migration struct {
	name string
	run  func(home string) error
}

// migrations are the migrations of the local settings this build ships, in the order they run. A migration is added
// only for a cost measured (proto versioning first: field numbers, unknown fields, reserved); a name never changes.
var migrations = []migration{
	// The provider and model lines of the developer's project settings move to the Summoning Circle, or go when they
	// cannot: they showed a wrong provider and hid the transcript warning (W103, L20, Q56).
	{name: "circle-from-project-settings", run: migrateCircle},
}

// Migrate runs, on the data folder home, each migration of the local settings not yet applied, and records the ones
// that apply in settings.json. It never fails: a migration that cannot apply is logged, its data left untouched, and
// the next start tries it again. djinn up calls it before it opens the store.
func Migrate(home string) {
	migrate(home, migrations)
}

func migrate(home string, ms []migration) {
	if home == "" {
		return
	}
	applied := readSettings(home).GetMigrations()
	for _, m := range ms {
		if slices.Contains(applied, m.name) {
			continue
		}
		if err := runMigration(m, home); err != nil {
			logx.L().Warn("a migration of the local settings did not apply: its data is left as it was, the next start "+
				"tries again", "migration", m.name, "err", err)
			continue
		}
		err := changeSettings(home, func(s *uiv1.Settings) error {
			if !slices.Contains(s.Migrations, m.name) {
				s.Migrations = append(s.Migrations, m.name)
			}
			return nil
		})
		if err != nil {
			logx.L().Warn("a migration of the local settings applied but is not recorded: the next start runs it again",
				"migration", m.name, "err", err)
			continue
		}
		logx.L().Info("a migration of the local settings applied", "migration", m.name)
	}
}

// runMigration runs m on home, a panic turned into its error: a broken migration never stops djinn up.
func runMigration(m migration, home string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(err, fmt.Errorf("panic: %v", r))
		}
	}()
	return m.run(home)
}
