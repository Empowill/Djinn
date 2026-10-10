package machine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	"github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/ui"
)

const settingsFile = "settings.json"

// ReadNotch reads the operating load notch from settings.json in home.
// It returns LOAD_NOTCH_MEDIUM if the file does not exist, cannot be read, or notch is unspecified.
func ReadNotch(home string) djinnv1.LoadNotch {
	if home == "" {
		return djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	data, err := os.ReadFile(filepath.Join(home, settingsFile))
	if err != nil {
		return djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	var settings uiv1.Settings
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, &settings); err != nil {
		return djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	if settings.Load == nil || *settings.Load == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
		return djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	return *settings.Load
}

// WriteNotch writes the operating load notch to settings.json in home, preserving other settings.
func WriteNotch(home string, notch djinnv1.LoadNotch) error {
	if home == "" {
		return nil
	}
	settings := &uiv1.Settings{}
	path := filepath.Join(home, settingsFile)
	if data, err := os.ReadFile(path); err == nil {
		_ = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, settings)
	}
	settings.Load = &notch
	data, err := (protojson.MarshalOptions{Multiline: true}).Marshal(settings)
	if err != nil {
		return err
	}
	return ui.WriteAtomic(path, data)
}

// LoadHandler returns the Connect handler of LoadService and its path prefix.
func LoadHandler(home string, m *Monitor, setPolicy func(Policy), broadcast func(djinnv1.LoadNotch)) (string, http.Handler) {
	return djinnv1connect.NewLoadServiceHandler(&loadService{
		home:      home,
		monitor:   m,
		setPolicy: setPolicy,
		broadcast: broadcast,
	}, connect.WithInterceptors(plan.Validate))
}

type loadService struct {
	djinnv1connect.UnimplementedLoadServiceHandler
	home      string
	monitor   *Monitor
	setPolicy func(Policy)
	broadcast func(djinnv1.LoadNotch)
}

func (s *loadService) Get(
	_ context.Context, _ *connect.Request[djinnv1.LoadServiceGetRequest],
) (*connect.Response[djinnv1.LoadServiceGetResponse], error) {
	notch := s.monitor.Policy().Notch
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
		notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	return connect.NewResponse(&djinnv1.LoadServiceGetResponse{Notch: notch}), nil
}

func (s *loadService) Set(
	_ context.Context, req *connect.Request[djinnv1.LoadServiceSetRequest],
) (*connect.Response[djinnv1.LoadServiceSetResponse], error) {
	notch := req.Msg.GetNotch()
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("notch is required"))
	}
	if err := WriteNotch(s.home, notch); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write load setting: %w", err))
	}
	p := NotchPolicy(notch)
	// Preserve any runtime overrides from flags (Workers, WorkerMemory).
	current := s.monitor.Policy()
	p.Workers = current.Workers
	p.WorkerMemory = current.WorkerMemory
	s.monitor.SetPolicy(p)
	if s.setPolicy != nil {
		s.setPolicy(p)
	}
	if s.broadcast != nil {
		s.broadcast(notch)
	}
	return connect.NewResponse(&djinnv1.LoadServiceSetResponse{Notch: notch}), nil
}
