package machine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	djinnv1connect "github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/ui"
)

const settingsFile = "settings.json"

// WorkerMemoryFunc returns the engaged memory (sum of peak forecasts) and actual resident memory of running workers.
type WorkerMemoryFunc func(ctx context.Context, p Policy) (engaged, actual uint64, err error)

var loadWatchInterval = time.Second

// SetWatchInterval sets the minimum interval between Watch stream messages for tests.
func SetWatchInterval(d time.Duration) func() {
	old := loadWatchInterval
	loadWatchInterval = d
	return func() { loadWatchInterval = old }
}

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
func LoadHandler(home string, m *Monitor, setPolicy func(Policy), broadcast func(djinnv1.LoadNotch), workerMemory WorkerMemoryFunc) (string, http.Handler) {
	return djinnv1connect.NewLoadServiceHandler(&loadService{
		home:         home,
		monitor:      m,
		setPolicy:    setPolicy,
		broadcast:    broadcast,
		workerMemory: workerMemory,
		subs:         make(map[chan struct{}]struct{}),
	}, connect.WithInterceptors(plan.Validate))
}

type loadService struct {
	djinnv1connect.UnimplementedLoadServiceHandler
	home         string
	monitor      *Monitor
	setPolicy    func(Policy)
	broadcast    func(djinnv1.LoadNotch)
	workerMemory WorkerMemoryFunc

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (s *loadService) notifyWatchers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *loadService) snapshot(ctx context.Context) *djinnv1.LoadServiceWatchResponse {
	resp := &djinnv1.LoadServiceWatchResponse{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
	}
	var policy Policy
	if s.monitor != nil {
		policy = s.monitor.Policy()
		if policy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
			resp.Notch = policy.Notch
		}
		resp.MemoryTotalBytes = s.monitor.Total()
		resp.MemoryAvailableBytes = s.monitor.Available()
	}
	if s.workerMemory != nil {
		engaged, actual, err := s.workerMemory(ctx, policy)
		if err == nil {
			resp.EngagedMemoryBytes = engaged
			resp.WorkerMemoryBytes = actual
		}
	}
	return resp
}

func (s *loadService) Get(
	_ context.Context, _ *connect.Request[djinnv1.LoadServiceGetRequest],
) (*connect.Response[djinnv1.LoadServiceGetResponse], error) {
	notch := djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	if s.monitor != nil {
		if n := s.monitor.Policy().Notch; n != djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
			notch = n
		}
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
	if s.monitor != nil {
		// Preserve any runtime overrides from flags (Workers, WorkerMemory).
		current := s.monitor.Policy()
		p.Workers = current.Workers
		p.WorkerMemory = current.WorkerMemory
		s.monitor.SetPolicy(p)
	}
	if s.setPolicy != nil {
		s.setPolicy(p)
	}
	if s.broadcast != nil {
		s.broadcast(notch)
	}
	s.notifyWatchers()
	return connect.NewResponse(&djinnv1.LoadServiceSetResponse{Notch: notch}), nil
}

func (s *loadService) Watch(
	ctx context.Context, _ *connect.Request[djinnv1.LoadServiceWatchRequest],
	stream *connect.ServerStream[djinnv1.LoadServiceWatchResponse],
) error {
	kick := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[kick] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, kick)
		s.mu.Unlock()
	}()

	interval := loadWatchInterval
	var ticker *time.Ticker
	var tickerC <-chan time.Time
	if interval > 0 {
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
		tickerC = ticker.C
	}

	var lastSent *djinnv1.LoadServiceWatchResponse
	var lastSentAt time.Time
	var delayedTimer *time.Timer
	var delayedC <-chan time.Time
	defer func() {
		if delayedTimer != nil {
			delayedTimer.Stop()
		}
	}()

	sendIfChanged := func() error {
		current := s.snapshot(ctx)
		if !proto.Equal(current, lastSent) {
			if err := stream.Send(current); err != nil {
				return err
			}
			lastSent = current
			lastSentAt = time.Now()
		}
		return nil
	}

	checkAndSend := func() error {
		now := time.Now()
		if interval > 0 && lastSent != nil && now.Sub(lastSentAt) < interval {
			if delayedTimer == nil {
				remain := interval - now.Sub(lastSentAt)
				delayedTimer = time.NewTimer(remain)
				delayedC = delayedTimer.C
			}
			return nil
		}
		return sendIfChanged()
	}

	// Send initial state immediately.
	if err := sendIfChanged(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-delayedC:
			delayedTimer = nil
			delayedC = nil
			if err := sendIfChanged(); err != nil {
				return err
			}
		case <-tickerC:
			if err := checkAndSend(); err != nil {
				return err
			}
		case <-kick:
			if err := checkAndSend(); err != nil {
				return err
			}
		}
	}
}
