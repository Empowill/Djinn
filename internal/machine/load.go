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
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
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
// An old saved "light" maps to Medium.
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
	if *settings.Load == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		return djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	return *settings.Load
}

// WriteNotch writes the operating load notch to settings.json in home, preserving other settings.
func WriteNotch(home string, notch djinnv1.LoadNotch) error {
	if home == "" {
		return nil
	}
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
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
	return fsx.WriteAtomic(path, data)
}

// LoadOption configures loadService.
type LoadOption func(*loadService)

// WithLoadRunningWorkers sets a function returning the count of currently running workers.
func WithLoadRunningWorkers(running func() int) LoadOption {
	return func(s *loadService) {
		s.runningWorkers = running
	}
}

// WithLoadPausedWorkers sets a function returning the count of currently paused workers.
func WithLoadPausedWorkers(paused func() int) LoadOption {
	return func(s *loadService) {
		s.pausedWorkers = paused
	}
}

// WithLoadForcedWorkers sets a function returning how many running workers were started or resumed by hand.
func WithLoadForcedWorkers(forced func() int) LoadOption {
	return func(s *loadService) {
		s.forcedWorkers = forced
	}
}

// WithLoadLowerPauses sets a function telling whether lowering the notch pauses the workers above it.
func WithLoadLowerPauses(pauses func() bool) LoadOption {
	return func(s *loadService) {
		s.lowerPauses = pauses
	}
}

// WithLoadWorkerForecast sets a function returning the measured forecast per worker in bytes.
func WithLoadWorkerForecast(forecast func() uint64) LoadOption {
	return func(s *loadService) {
		s.workerForecast = forecast
	}
}

// WithLoadAutoController sets a custom AutoController on the load service.
func WithLoadAutoController(ac *AutoController) LoadOption {
	return func(s *loadService) {
		s.auto = ac
	}
}

// WithLoadClock sets the clock function used for auto mode.
func WithLoadClock(clock func() time.Time) LoadOption {
	return func(s *loadService) {
		s.clock = clock
	}
}

// WithLoadStreamer provides a function that can be called to stream load changes.
func WithLoadStreamer(attach func(func(ctx context.Context, send func(*djinnv1.LoadServiceWatchResponse) error) error)) LoadOption {
	return func(s *loadService) {
		if attach != nil {
			attach(s.Stream)
		}
	}
}

// WithLoadSignal sets the activity signal function used for auto mode.
func WithLoadSignal(signal ActivitySignal) LoadOption {
	return func(s *loadService) {
		s.signal = signal
	}
}

// WithLoadIdleSource sets the idle source used for auto mode.
func WithLoadIdleSource(source IdleSource) LoadOption {
	return func(s *loadService) {
		s.idleSource = source
	}
}

// WithLoadStepDuration sets the hysteresis minimum interval for auto mode.
func WithLoadStepDuration(d time.Duration) LoadOption {
	return func(s *loadService) {
		s.stepDuration = d
	}
}

// WithLoadTickInterval sets the auto ticker interval (<= 0 disables background ticker).
func WithLoadTickInterval(d time.Duration) LoadOption {
	return func(s *loadService) {
		s.tickInterval = d
	}
}

// LoadHandler returns the Connect handler of LoadService and its path prefix.
func LoadHandler(
	home string,
	m *Monitor,
	setPolicy func(Policy),
	broadcast func(djinnv1.LoadNotch),
	workerMemory WorkerMemoryFunc,
	opts ...LoadOption,
) (string, http.Handler) {
	savedNotch := ReadNotch(home)
	s := &loadService{
		home:         home,
		monitor:      m,
		setPolicy:    setPolicy,
		broadcast:    broadcast,
		workerMemory: workerMemory,
		subs:         make(map[chan struct{}]struct{}),
		tickInterval: time.Second,
		stopAuto:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.auto == nil {
		autoOpts := []AutoOption{
			WithAutoOnChange(func(effective djinnv1.LoadNotch) {
				s.applyNotch(effective)
				s.notifyWatchers()
			}),
		}
		if s.clock != nil {
			autoOpts = append(autoOpts, WithAutoClock(s.clock))
		}
		if s.signal != nil {
			autoOpts = append(autoOpts, WithAutoSignal(s.signal))
		} else {
			autoOpts = append(autoOpts, WithAutoSignal(SystemActivitySignal(m)))
		}
		if s.idleSource != nil {
			autoOpts = append(autoOpts, WithAutoIdleSource(s.idleSource))
		} else {
			autoOpts = append(autoOpts, WithAutoIdleSource(DefaultIdleSource()))
		}
		if s.stepDuration > 0 {
			autoOpts = append(autoOpts, WithAutoStepDuration(s.stepDuration))
		}
		s.auto = NewAutoController(savedNotch, autoOpts...)
	} else {
		oldOnChange := s.auto.onChange
		s.auto.onChange = func(effective djinnv1.LoadNotch) {
			s.applyNotch(effective)
			s.notifyWatchers()
			if oldOnChange != nil {
				oldOnChange(effective)
			}
		}
	}

	if s.tickInterval > 0 {
		go s.runAutoLoop()
	}

	return djinnv1connect.NewLoadServiceHandler(s, connect.WithInterceptors(plan.Validate))
}

type loadService struct {
	djinnv1connect.UnimplementedLoadServiceHandler
	home           string
	monitor        *Monitor
	setPolicy      func(Policy)
	broadcast      func(djinnv1.LoadNotch)
	workerMemory   WorkerMemoryFunc
	runningWorkers func() int
	pausedWorkers  func() int
	forcedWorkers  func() int
	lowerPauses    func() bool
	workerForecast func() uint64

	auto         *AutoController
	clock        func() time.Time
	signal       ActivitySignal
	idleSource   IdleSource
	stepDuration time.Duration
	tickInterval time.Duration
	stopAuto     chan struct{}

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (s *loadService) runAutoLoop() {
	defer logx.Crash("load auto")
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopAuto:
			return
		case <-ticker.C:
			s.Tick()
		}
	}
}

// Tick evaluates developer activity and updates effective load notch in auto mode.
func (s *loadService) Tick() (bool, djinnv1.LoadNotch) {
	if s.auto == nil {
		return false, djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	return s.auto.Tick()
}

func (s *loadService) applyNotch(effective djinnv1.LoadNotch) {
	p := NotchPolicy(effective)
	if s.auto != nil && s.auto.IsAuto() {
		p.Auto = true
	}
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
		s.broadcast(effective)
	}
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
	if s.auto != nil {
		resp.Notch = s.auto.Effective()
		resp.Auto = s.auto.IsAuto()
		resp.ChosenNotch = s.auto.Chosen()
	} else if s.monitor != nil {
		policy := s.monitor.Policy()
		if policy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
			resp.Notch = policy.Notch
			resp.ChosenNotch = policy.Notch
		}
	}
	var policy Policy
	if s.monitor != nil {
		policy = s.monitor.Policy()
		snap := s.monitor.Snapshot()
		forecast := policy.WorkerForecast()
		if s.workerForecast != nil {
			if f := s.workerForecast(); f > 0 {
				forecast = f
			}
		}
		cpuSlots := policy.CPUSlots(snap)
		memSlots := policy.MemorySlots(snap, forecast)
		binding := policy.BindingResource(snap, forecast)
		baseSlots := min(cpuSlots, memSlots)

		resp.MemoryTotalBytes = s.monitor.Total()
		resp.MemoryAvailableBytes = s.monitor.Available()
		resp.WorkerForecastBytes = forecast
		resp.BindingResource = binding
		resp.CpuSlots = int32(cpuSlots)
		resp.MemorySlots = int32(memSlots)
		resp.BaseSlots = int32(baseSlots)
		resp.WorkerPercentile = int32(policy.MemoryPercentile())
		resp.WorkerEngagedAt = policy.EngagedAt()
	}
	if s.workerMemory != nil {
		engaged, actual, err := s.workerMemory(ctx, policy)
		if err == nil {
			resp.EngagedMemoryBytes = engaged
			resp.WorkerMemoryBytes = actual
		}
	}
	w := s.workers()
	resp.RunningWorkers, resp.PausedWorkers, resp.ForcedWorkers = w.running, w.paused, w.forced
	resp.Slots, resp.NotchSlots, resp.LowerPauses = w.slots, w.notchSlots, w.lowerPauses
	return resp
}

// workerCounts is what the load bar says of the workers: how many run, are paused or were started by hand, and how
// many the scheduler lets run, now and at each notch.
type workerCounts struct {
	running, paused, forced, slots int32
	notchSlots                     []*djinnv1.LoadNotchSlots
	lowerPauses                    bool
}

// workers counts the workers as the scheduler does: the slots are the monitor's Capacity, the same reading.
func (s *loadService) workers() workerCounts {
	var w workerCounts
	if s.runningWorkers != nil {
		w.running = int32(s.runningWorkers())
	}
	if s.pausedWorkers != nil {
		w.paused = int32(s.pausedWorkers())
	}
	if s.forcedWorkers != nil {
		w.forced = int32(s.forcedWorkers())
	}
	if s.lowerPauses != nil {
		w.lowerPauses = s.lowerPauses()
	}
	if s.monitor != nil {
		slots, _, _ := s.monitor.Capacity()
		w.slots = int32(slots)
		for _, n := range s.monitor.NotchSlots() {
			w.notchSlots = append(w.notchSlots, &djinnv1.LoadNotchSlots{Notch: n.Notch, Slots: int32(n.Slots)})
		}
	}
	return w
}

func (s *loadService) Get(
	_ context.Context, _ *connect.Request[djinnv1.LoadServiceGetRequest],
) (*connect.Response[djinnv1.LoadServiceGetResponse], error) {
	effective := djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	chosen := djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	isAuto := false
	if s.auto != nil {
		effective = s.auto.Effective()
		isAuto = s.auto.IsAuto()
		chosen = s.auto.Chosen()
	} else if s.monitor != nil {
		if n := s.monitor.Policy().Notch; n != djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
			effective = n
			chosen = n
		}
	}
	resp := &djinnv1.LoadServiceGetResponse{
		Notch:       effective,
		Auto:        isAuto,
		ChosenNotch: chosen,
	}
	if s.monitor != nil {
		policy := s.monitor.Policy()
		snap := s.monitor.Snapshot()
		forecast := policy.WorkerForecast()
		if s.workerForecast != nil {
			if f := s.workerForecast(); f > 0 {
				forecast = f
			}
		}
		cpuSlots := policy.CPUSlots(snap)
		memSlots := policy.MemorySlots(snap, forecast)
		binding := policy.BindingResource(snap, forecast)
		baseSlots := min(cpuSlots, memSlots)

		resp.MemoryTotalBytes = s.monitor.Total()
		resp.MemoryAvailableBytes = s.monitor.Available()
		resp.WorkerForecastBytes = forecast
		resp.BindingResource = binding
		resp.CpuSlots = int32(cpuSlots)
		resp.MemorySlots = int32(memSlots)
		resp.BaseSlots = int32(baseSlots)
		resp.WorkerPercentile = int32(policy.MemoryPercentile())
		resp.WorkerEngagedAt = policy.EngagedAt()
	}
	w := s.workers()
	resp.RunningWorkers, resp.PausedWorkers, resp.ForcedWorkers = w.running, w.paused, w.forced
	resp.Slots, resp.NotchSlots, resp.LowerPauses = w.slots, w.notchSlots, w.lowerPauses
	return connect.NewResponse(resp), nil
}

func (s *loadService) Set(
	_ context.Context, req *connect.Request[djinnv1.LoadServiceSetRequest],
) (*connect.Response[djinnv1.LoadServiceSetResponse], error) {
	notch := req.Msg.GetNotch()
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("notch is required"))
	}
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	if err := WriteNotch(s.home, notch); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write load setting: %w", err))
	}
	effective := notch
	isAuto := false
	if s.auto != nil {
		effective, _ = s.auto.SetNotch(notch)
		isAuto = s.auto.IsAuto()
	}
	s.applyNotch(effective)
	s.notifyWatchers()
	chosen := effective
	if s.auto != nil {
		chosen = s.auto.Chosen()
	}
	return connect.NewResponse(&djinnv1.LoadServiceSetResponse{
		Notch:       effective,
		Auto:        isAuto,
		ChosenNotch: chosen,
	}), nil
}

func (s *loadService) Stream(
	ctx context.Context, send func(*djinnv1.LoadServiceWatchResponse) error,
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
			if err := send(current); err != nil {
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

func (s *loadService) Watch(
	ctx context.Context, _ *connect.Request[djinnv1.LoadServiceWatchRequest],
	stream *connect.ServerStream[djinnv1.LoadServiceWatchResponse],
) error {
	return s.Stream(ctx, stream.Send)
}
