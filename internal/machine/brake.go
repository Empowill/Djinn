package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/fsx"
)

// DefaultBrakeMemory is the memory brake's threshold when nothing sets it (decision Q22 B): it freezes a worker under
// 5 % of the machine's memory available. djinn up --brake-memory says the same default (cli.Builtins).
const DefaultBrakeMemory = 5

// MostBrakeMemory is the highest threshold the brake takes, in percent: above, it would freeze a calm machine.
const MostBrakeMemory = 95

// ReadBrake reads the memory brake's threshold from settings.json in home (ui.v1.Settings.brake_memory_percent): ok
// false when the file does not say it, or says more than MostBrakeMemory.
func ReadBrake(home string) (percent int, ok bool) {
	if home == "" {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Join(home, settingsFile))
	if err != nil {
		return 0, false
	}
	var settings uiv1.Settings
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, &settings); err != nil {
		return 0, false
	}
	if settings.BrakeMemoryPercent == nil || settings.GetBrakeMemoryPercent() > MostBrakeMemory {
		return 0, false
	}
	return int(settings.GetBrakeMemoryPercent()), true
}

// WriteBrake writes the memory brake's threshold to settings.json in home, keeping the other settings.
func WriteBrake(home string, percent int) error {
	if home == "" {
		return nil
	}
	settings := &uiv1.Settings{}
	path := filepath.Join(home, settingsFile)
	if data, err := os.ReadFile(path); err == nil {
		_ = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, settings)
	}
	p := uint32(percent)
	settings.BrakeMemoryPercent = &p
	data, err := (protojson.MarshalOptions{Multiline: true}).Marshal(settings)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(path, data, 0o600)
}

// BrakeSetting is the memory brake's threshold as LoadService shows and changes it (WithLoadBrake).
type BrakeSetting struct {
	// Percent is the threshold in effect as djinn up starts; 0: no brake.
	Percent int
	// Fixed: djinn up --brake-memory, or DJINN_BRAKE_MEMORY, set it for this run; SetBrake is refused.
	Fixed bool
	// Apply gives the brake a new threshold at once (harness.Harness.SetBrake); nil: it waits for the next djinn up.
	Apply func(percent int)
}

// WithLoadBrake has LoadService show the memory brake's threshold and change it (LoadService.SetBrake). Without it,
// it shows what settings.json says, else DefaultBrakeMemory, and a change waits for the next djinn up.
func WithLoadBrake(b BrakeSetting) LoadOption {
	return func(s *loadService) {
		s.brakeMu.Lock()
		defer s.brakeMu.Unlock()
		s.brake = b
	}
}

// brakeOf is the threshold settings.json in home says, else the default: what djinn up starts the brake with when no
// flag sets it.
func brakeOf(home string) BrakeSetting {
	if p, ok := ReadBrake(home); ok {
		return BrakeSetting{Percent: p}
	}
	return BrakeSetting{Percent: DefaultBrakeMemory}
}

// brakeNow is the threshold in effect and whether a flag fixed it.
func (s *loadService) brakeNow() (percent int, fixed bool) {
	s.brakeMu.Lock()
	defer s.brakeMu.Unlock()
	return s.brake.Percent, s.brake.Fixed
}

func (s *loadService) SetBrake(
	_ context.Context, req *connect.Request[djinnv1.LoadServiceSetBrakeRequest],
) (*connect.Response[djinnv1.LoadServiceSetBrakeResponse], error) {
	percent := int(req.Msg.GetPercent())
	if percent > MostBrakeMemory {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("brake at %d%%: up to %d, or 0 for no brake", percent, MostBrakeMemory))
	}
	s.brakeMu.Lock()
	defer s.brakeMu.Unlock()
	if s.brake.Fixed {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"djinn up --brake-memory (or DJINN_BRAKE_MEMORY) sets the memory brake for this run: start djinn up without it to choose here"))
	}
	if err := WriteBrake(s.home, percent); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write the brake setting: %w", err))
	}
	s.brake.Percent = percent
	if s.brake.Apply != nil {
		s.brake.Apply(percent)
	}
	return connect.NewResponse(&djinnv1.LoadServiceSetBrakeResponse{BrakeMemoryPercent: int32(percent), BrakeOff: percent == 0}), nil
}
