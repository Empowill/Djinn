// Package plan implements the services of plan.v1 on the store: projects, wishes and questions. Tasks, which run
// workers, are served by the harness package.
package plan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Entities are the messages the store keeps: those of plan.v1, and what each command of a project costs.
func Entities() []proto.Message {
	return []proto.Message{
		&planv1.Project{}, &planv1.Wish{}, &planv1.Task{}, &planv1.TaskEvent{}, &planv1.Question{}, &planv1.Block{},
		&planv1.InboxItem{}, &planv1.PluggedSource{}, &machinev1.CommandCost{}, &planv1.Tilasm{},
	}
}

// actor is who sent a command, as the journal records it. Every caller is the local user until agents get an
// identity of their own.
const actor = "local"

// Pusher pushes an integration branch and computes how far it is out of sync with its remote.
type Pusher interface {
	Push(ctx context.Context, wishID, projectID string) (*planv1.IntegrationPush, error)
	Sync(ctx context.Context, wish *planv1.Wish, project *planv1.Project) (*planv1.ProjectSync, error)
}

// Option sets what the plan services reach beyond the store.
type Option func(*options)

type options struct {
	leads           Leads
	pages           *Pages
	language        string
	home            string
	watchers        SpawnWatcher
	workers         WishWorkers
	answered        []AnswerHook
	show            func(wishID, tilasmID string) bool
	url             func(id string) string
	enlight         []func(context.Context, *planv1.Question, string)
	load            func() djinnv1.LoadNotch
	loadBroadcaster *func(djinnv1.LoadNotch)
	pusher          Pusher
}

// WithPusher gives ProjectService the pusher for pushing and computing sync status.
func WithPusher(p Pusher) Option {
	return func(o *options) { o.pusher = p }
}

// WithShowTilasm gives TilasmService.Open the window: show shows a tilasm in its wish's Tilasms tab, and tells whether
// a native window came to the front.
func WithShowTilasm(show func(wishID, tilasmID string) bool) Option {
	return func(o *options) { o.show = show }
}

// WithTilasmURL gives TilasmService.Get the local http address of a tilasm's files, by its identifier; empty while
// Djinn serves no http.
func WithTilasmURL(url func(id string) string) Option { return func(o *options) { o.url = url } }

// WithWorkers gives the wishes their workers: a paused wish stops them (Wishes.Workers).
func WithWorkers(w WishWorkers) Option { return func(o *options) { o.workers = w } }

// WithLanguage writes the texts Djinn puts in the plan for the developer, such as the question that routes a
// request, in language; English by default.
func WithLanguage(language string) Option { return func(o *options) { o.language = language } }

// WithHome gives the services Djinn's data folder: each developer's own settings of a project, and the tilasms' files.
func WithHome(home string) Option { return func(o *options) { o.home = home } }

// WithLeads gives the wishes the terminals of their leads, for WishService.Resume.
func WithLeads(l Leads) Option { return func(o *options) { o.leads = l } }

// WithAnswered calls f with a question once its answer is stored: the harness starts again the worker a yes
// allows to edit. With WithLeads, the wish's lead is told next (Wishes.Answered), with what f did.
func WithAnswered(f AnswerHook) Option {
	return func(o *options) { o.answered = append(o.answered, f) }
}

// WithEnlightened calls f with a question and the developer's note once a request to investigate it is stored: the
// harness starts its investigator. With WithLeads, the wish's lead is told next (Wishes.Enlightened).
func WithEnlightened(f func(context.Context, *planv1.Question, string)) Option {
	return func(o *options) { o.enlight = append(o.enlight, f) }
}

// WithLoad gives the wishes Djinn's current load notch, for WishService.Watch.
func WithLoad(load func() djinnv1.LoadNotch) Option { return func(o *options) { o.load = load } }

// WithLoadBroadcaster gives the caller what notifies the open Watch streams of a load notch change.
func WithLoadBroadcaster(b *func(djinnv1.LoadNotch)) Option {
	return func(o *options) { o.loadBroadcaster = b }
}

// Handlers returns the Connect handlers of the plan services, by path prefix.
func Handlers(s *store.Store, opts ...Option) map[string]http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	out := map[string]http.Handler{}
	opt := connect.WithInterceptors(Validate)
	p, h := planv1connect.NewProjectServiceHandler(&Projects{Store: s, Home: o.home, Pusher: o.pusher}, opt)
	out[p] = h
	wishes := &Wishes{Store: s, Leads: o.leads, Pages: o.pages, Language: o.language, Watchers: o.watchers,
		Workers: o.workers, Home: o.home, Load: o.load}
	if o.loadBroadcaster != nil {
		*o.loadBroadcaster = wishes.ChangeLoad
	}
	var told Told
	if o.leads != nil {
		told = wishes.Answered // The lead learns each answer, after the harness, and what it did.
	}
	p, h = planv1connect.NewWishServiceHandler(wishes, opt)
	out[p] = h
	if o.leads != nil {
		o.enlight = append(o.enlight, wishes.Enlightened) // The lead learns each request to investigate, after the harness.
	}
	questions := &Questions{Store: s, Answered: o.answered, Told: told, Enlightened: o.enlight, Settle: wishes.settle, Workers: o.workers}
	p, h = planv1connect.NewQuestionServiceHandler(questions, opt)
	out[p] = h
	p, h = planv1connect.NewBlockServiceHandler(&Blocks{Store: s}, opt)
	out[p] = h
	p, h = planv1connect.NewMarkServiceHandler(&Marks{Store: s, Answered: o.answered, Told: told, Settle: wishes.settle}, opt)
	out[p] = h
	p, h = planv1connect.NewSkillServiceHandler(&Skills{Store: s}, opt)
	out[p] = h
	p, h = planv1connect.NewInboxServiceHandler(&Inbox{Wishes: wishes}, opt)
	out[p] = h
	tilasms := &Tilasms{Store: s, Home: o.home, Show: o.show, URL: o.url}
	p, h = planv1connect.NewTilasmServiceHandler(tilasms, opt)
	out[p] = h
	out[server.TilasmPrefix] = tilasms.Files()
	return out
}

// Validate checks every unary request against the rules of its proto, as the command line did before sending it.
var Validate = connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if m, ok := req.Any().(proto.Message); ok {
			if err := protovalidate.Validate(m); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
		}
		return next(ctx, req)
	}
})

// write journals req, then runs fn, in one transaction. Store errors become Connect errors.
func write(ctx context.Context, s *store.Store, spec connect.Spec, req proto.Message, fn func(*store.Tx) error) error {
	return Status(s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, spec.Procedure, req); err != nil {
			return err
		}
		return fn(tx)
	}))
}

// Status gives a Connect code to the errors of the store.
func Status(err error) error {
	var cerr *connect.Error
	switch {
	case err == nil, errors.As(err, &cerr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrDuplicate):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, ErrNoProject):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

// Projects implements ProjectService.
type Projects struct {
	planv1connect.UnimplementedProjectServiceHandler
	Store *store.Store
	// Home is Djinn's data folder, which holds the developer's own settings of each project; empty: none are read.
	Home   string
	Pusher Pusher
}

func (p *Projects) fillProject(ctx context.Context, project *planv1.Project) {
	if project == nil {
		return
	}
	repo, dev, _ := loadSettings(p.Home, project)
	settings := ResolveSettings(repo, dev)
	project.Push = settings.Push
	project.PushStrategy = settings.PushStrategy

	if p.Pusher != nil {
		wish, _ := p.wishForProject(ctx, project.GetId(), "")
		if wish != nil {
			if sync, err := p.Pusher.Sync(ctx, wish, project); err == nil && sync != nil {
				project.Sync = sync
			}
		}
	}
}

func (p *Projects) wishForProject(ctx context.Context, projectID, wishNameOrID string) (*planv1.Wish, error) {
	if wishNameOrID != "" {
		if w, err := store.Get[*planv1.Wish](ctx, p.Store, wishNameOrID); err == nil {
			return w, nil
		}
		all, err := store.List[*planv1.Wish](ctx, p.Store, nil)
		if err != nil {
			return nil, err
		}
		for _, w := range all {
			if strings.EqualFold(w.GetId(), wishNameOrID) || strings.EqualFold(w.GetTitle(), wishNameOrID) {
				return w, nil
			}
		}
		return nil, fmt.Errorf("no wish %q", wishNameOrID)
	}

	wishes, err := store.List[*planv1.Wish](ctx, p.Store, nil)
	if err != nil {
		return nil, err
	}
	var bestActive, bestPaused, bestOther *planv1.Wish
	for _, w := range wishes {
		if !slices.Contains(w.GetProjectIds(), projectID) {
			continue
		}
		switch w.GetState() {
		case planv1.WishState_WISH_STATE_ACTIVE:
			if bestActive == nil || w.GetRank() < bestActive.GetRank() {
				bestActive = w
			}
		case planv1.WishState_WISH_STATE_PAUSED:
			if bestPaused == nil || (w.GetCreateTime() != nil && bestPaused.GetCreateTime() != nil && w.GetCreateTime().AsTime().After(bestPaused.GetCreateTime().AsTime())) {
				bestPaused = w
			}
		default:
			if bestOther == nil || (w.GetCreateTime() != nil && bestOther.GetCreateTime() != nil && w.GetCreateTime().AsTime().After(bestOther.GetCreateTime().AsTime())) {
				bestOther = w
			}
		}
	}
	if bestActive != nil {
		return bestActive, nil
	}
	if bestPaused != nil {
		return bestPaused, nil
	}
	return bestOther, nil
}

func (p *Projects) Add(
	ctx context.Context, req *connect.Request[planv1.ProjectServiceAddRequest],
) (*connect.Response[planv1.ProjectServiceAddResponse], error) {
	dir, err := canonical(req.Msg.GetDirectory())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("directory: %w", err))
	}
	name := req.Msg.GetName()
	if name == "" {
		name = filepath.Base(dir)
	}
	remote := remoteOf(dir)
	project := &planv1.Project{
		Id: store.NewID(), Name: name, Directory: dir, Git: inGit(dir), Remote: remote, CreateTime: timestamppb.Now(),
	}
	err = write(ctx, p.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		same, err := store.List[*planv1.Project](ctx, tx, store.Where{"directory": dir})
		if err != nil {
			return err
		}
		if len(same) > 0 {
			return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf(
				"project %s already has this folder, case ignored: %s", same[0].GetName(), same[0].GetDirectory()))
		}
		// A project an imported wish named, still without a folder, is attached rather than added twice.
		if waiting, err := unattached(ctx, tx, remote, name); err != nil || waiting != nil {
			if err != nil {
				return err
			}
			waiting.Directory, waiting.Git = dir, project.GetGit()
			if waiting.GetRemote() == "" {
				waiting.Remote = remote
			}
			project = waiting
			return tx.Put(project)
		}
		// The unique index refuses a duplicate name anyway; checking first says which project has it.
		if same, err = store.List[*planv1.Project](ctx, tx, store.Where{"name": name}); err != nil {
			return err
		}
		if len(same) > 0 {
			return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf(
				"project %s already has this name, case ignored: %s", same[0].GetName(), same[0].GetDirectory()))
		}
		return tx.Put(project)
	})
	if err != nil {
		return nil, err
	}
	p.fillProject(ctx, project)
	return connect.NewResponse(&planv1.ProjectServiceAddResponse{Project: project}), nil
}

func (p *Projects) List(
	ctx context.Context, _ *connect.Request[planv1.ProjectServiceListRequest],
) (*connect.Response[planv1.ProjectServiceListResponse], error) {
	projects, err := store.List[*planv1.Project](ctx, p.Store, nil)
	if err != nil {
		return nil, Status(err)
	}
	for _, project := range projects {
		p.fillProject(ctx, project)
	}
	return connect.NewResponse(&planv1.ProjectServiceListResponse{Projects: projects}), nil
}

func (p *Projects) Show(
	ctx context.Context, req *connect.Request[planv1.ProjectServiceShowRequest],
) (*connect.Response[planv1.ProjectServiceShowResponse], error) {
	project, err := ProjectNamed(ctx, p.Store, req.Msg.GetProject())
	if err != nil {
		return nil, Status(err)
	}
	p.fillProject(ctx, project)
	repo, dev, problems := loadSettings(p.Home, project)
	settings := ResolveSettings(repo, dev)
	out := &planv1.ProjectServiceShowResponse{
		Project: project, Settings: settings.Rows(), Setup: settings.Setup, Checks: settings.Checks,
	}
	out.RepositoryFile, out.DeveloperFile = settingsFiles(p.Home, project)
	for _, err := range problems {
		out.Problems = append(out.Problems, err.Error())
	}
	return connect.NewResponse(out), nil
}

func (p *Projects) Push(
	ctx context.Context, req *connect.Request[planv1.ProjectServicePushRequest],
) (*connect.Response[planv1.ProjectServicePushResponse], error) {
	if p.Pusher == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("push is unavailable"))
	}
	project, err := ProjectNamed(ctx, p.Store, req.Msg.GetProject())
	if err != nil {
		return nil, Status(err)
	}
	wish, err := p.wishForProject(ctx, project.GetId(), req.Msg.GetWish())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if wish == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("no wish for project %s", project.GetName()))
	}
	push, err := p.Pusher.Push(ctx, wish.GetId(), project.GetId())
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) {
			return nil, cerr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&planv1.ProjectServicePushResponse{Push: push}), nil
}

func (p *Projects) SetPush(
	ctx context.Context, req *connect.Request[planv1.ProjectServiceSetPushRequest],
) (*connect.Response[planv1.ProjectServiceSetPushResponse], error) {
	project, err := ProjectNamed(ctx, p.Store, req.Msg.GetProject())
	if err != nil {
		return nil, Status(err)
	}
	err = write(ctx, p.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		return SaveDeveloperPush(p.Home, project.GetId(), req.Msg.GetPush())
	})
	if err != nil {
		return nil, Status(err)
	}
	p.fillProject(ctx, project)
	return connect.NewResponse(&planv1.ProjectServiceSetPushResponse{Project: project}), nil
}

func (p *Projects) PushStrategy(
	ctx context.Context, req *connect.Request[planv1.ProjectServicePushStrategyRequest],
) (*connect.Response[planv1.ProjectServicePushStrategyResponse], error) {
	project, err := ProjectNamed(ctx, p.Store, req.Msg.GetProject())
	if err != nil {
		return nil, Status(err)
	}
	if req.Msg.Strategy != nil && req.Msg.GetStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		err = write(ctx, p.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
			return SaveDeveloperPushStrategy(p.Home, project.GetId(), req.Msg.GetStrategy())
		})
		if err != nil {
			return nil, Status(err)
		}
	}
	repo, dev, _ := loadSettings(p.Home, project)
	settings := ResolveSettings(repo, dev)
	return connect.NewResponse(&planv1.ProjectServicePushStrategyResponse{
		Strategy: settings.PushStrategy,
		Source:   settings.PushStrategyFrom,
	}), nil
}

// unattached returns the project without a folder that has this remote, or else this name, case ignored; nil
// when there is none.
func unattached(ctx context.Context, tx *store.Tx, remote, name string) (*planv1.Project, error) {
	all, err := store.List[*planv1.Project](ctx, tx, store.Where{"directory": ""})
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if sameRemote(p.GetRemote(), remote) {
			return p, nil
		}
	}
	for _, p := range all {
		if strings.EqualFold(p.GetName(), name) {
			return p, nil
		}
	}
	return nil, nil
}

// canonical returns the absolute path of the folder dir, with its symbolic links resolved.
func canonical(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%q is not an absolute path", dir)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", dir)
	}
	return filepath.Clean(dir), nil
}

// inGit tells whether dir is inside a Git repository: dir or a parent holds .git, a directory or, in a worktree
// or a submodule, a file.
func inGit(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// Wishes implements WishService.
type Wishes struct {
	planv1connect.UnimplementedWishServiceHandler
	Store *store.Store
	// Leads runs the leads' terminals; nil where djinn up does not serve them, and Resume is then unavailable.
	Leads Leads
	// Pages renders the wishes' pages and keeps the synced ones up to date; nil where djinn up does not run them,
	// and Sync is then unavailable.
	Pages *Pages
	// Language of the texts Djinn writes in the plan for the developer; English when empty.
	Language string
	// Watchers starts the watcher of a wish made from a template; nil where djinn up does not run tasks, and the
	// lead is then told to start it.
	Watchers SpawnWatcher
	// Workers stops the workers of a paused wish, and wakes the scheduler for an active one; nil where djinn up does
	// not run tasks.
	Workers WishWorkers
	// Home is Djinn's data folder, which holds the tilasms' files; empty: a wish with tilasms neither exports nor
	// imports.
	Home string
	// Load reports Djinn's current load notch; nil where djinn up does not run machine monitoring.
	Load func() djinnv1.LoadNotch

	watch watchers // the open Watch streams
}

func (w *Wishes) Make(
	ctx context.Context, req *connect.Request[planv1.WishServiceMakeRequest],
) (*connect.Response[planv1.WishServiceMakeResponse], error) {
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		wish, err = makeWish(ctx, tx, req.Msg)
		return err
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServiceMakeResponse{Wish: wish}), nil
}

// makeWish stores the wish req asks for in tx: last by rank among the active ones, or paused. The caller journals
// the command.
func makeWish(ctx context.Context, tx *store.Tx, req *planv1.WishServiceMakeRequest) (*planv1.Wish, error) {
	wish := &planv1.Wish{
		Id: store.NewID(), Title: req.GetTitle(), CreateTime: timestamppb.Now(),
		PushStrategy: req.GetPushStrategy(),
	}
	for _, id := range req.GetProjectIds() {
		project, err := store.Get[*planv1.Project](ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(wish.GetProjectIds(), project.GetId()) {
			wish.ProjectIds = append(wish.ProjectIds, project.GetId())
		}
	}
	if err := integrationBranches(ctx, tx, wish); err != nil {
		return nil, err
	}
	if req.GetPaused() {
		wish.State = planv1.WishState_WISH_STATE_PAUSED
		return wish, tx.Put(wish)
	}
	actives, err := ActiveWishes(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(actives) >= MaxActive {
		// Three wishes are active: the new one waits, paused, until you make it active.
		wish.State = planv1.WishState_WISH_STATE_PAUSED
		return wish, tx.Put(wish)
	}
	if err := renumber(tx, actives); err != nil {
		return nil, err
	}
	wish.State, wish.Rank = planv1.WishState_WISH_STATE_ACTIVE, int32(len(actives)+1)
	return wish, tx.Put(wish)
}

func (w *Wishes) List(
	ctx context.Context, _ *connect.Request[planv1.WishServiceListRequest],
) (*connect.Response[planv1.WishServiceListResponse], error) {
	wishes, err := store.List[*planv1.Wish](ctx, w.Store, nil)
	if err != nil {
		return nil, Status(err)
	}
	wishes = sorted(wishes)
	if err := fill(ctx, w.Store, wishes...); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceListResponse{Wishes: wishes}), nil
}

// Allow gives the wish's workers a right in one of its projects, or takes it back with ALLOWANCE_NONE.
func (w *Wishes) Allow(
	ctx context.Context, req *connect.Request[planv1.WishServiceAllowRequest],
) (*connect.Response[planv1.WishServiceAllowResponse], error) {
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		id := req.Msg.GetProjectId()
		ids := wish.GetProjectIds()
		switch {
		case id == "" && len(ids) != 1:
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"the wish has %d projects: name one with --project-id", len(ids)))
		case id == "":
			id = ids[0]
		}
		i := slices.IndexFunc(ids, func(p string) bool { return strings.EqualFold(p, id) })
		if i < 0 {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("project %s is not one of the wish's projects", id))
		}
		id = ids[i]
		wish.Allowances = slices.DeleteFunc(wish.Allowances, func(a *planv1.ProjectAllowance) bool { return a.GetProjectId() == id })
		if mode := req.Msg.GetMode(); mode != planv1.Allowance_ALLOWANCE_NONE {
			wish.Allowances = append(wish.Allowances, &planv1.ProjectAllowance{ProjectId: id, Allowance: mode})
		}
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceAllowResponse{Wish: wish}), nil
}

// Describe sets the wish's description, in place of what it had; empty takes it back to the title.
func (w *Wishes) Describe(
	ctx context.Context, req *connect.Request[planv1.WishServiceDescribeRequest],
) (*connect.Response[planv1.WishServiceDescribeResponse], error) {
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		wish.Description = strings.TrimSpace(req.Msg.GetText())
		if wish.Description == strings.TrimSpace(wish.GetTitle()) {
			wish.Description = "" // The title stands for it already.
		}
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceDescribeResponse{Wish: wish}), nil
}

// Rename sets the wish's title, in place of what it had.
func (w *Wishes) Rename(
	ctx context.Context, req *connect.Request[planv1.WishServiceRenameRequest],
) (*connect.Response[planv1.WishServiceRenameResponse], error) {
	title := strings.TrimSpace(req.Msg.GetTitle())
	if title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("title is required"))
	}
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		wish.Title = title
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceRenameResponse{Wish: wish}), nil
}

// AllowanceOf is the right wish allows its workers in the project projectID: ALLOWANCE_NONE when it allows none.
func AllowanceOf(wish *planv1.Wish, projectID string) planv1.Allowance {
	for _, a := range wish.GetAllowances() {
		if strings.EqualFold(a.GetProjectId(), projectID) && a.GetAllowance() != planv1.Allowance_ALLOWANCE_UNSPECIFIED {
			return a.GetAllowance()
		}
	}
	return planv1.Allowance_ALLOWANCE_NONE
}

// Questions implements QuestionService.
type Questions struct {
	planv1connect.UnimplementedQuestionServiceHandler
	Store *store.Store
	// Answered are called with a question once its answer is stored.
	Answered []AnswerHook
	// Told, when set, is called next, with what they did.
	Told Told
	// Enlightened are called with a question once a request to investigate it is stored, with its note.
	Enlightened []func(ctx context.Context, q *planv1.Question, note string)
	// Settle, when set, acts on an answer in the transaction that stores it, and returns what follows once it is
	// stored, if anything: a question that routes a request files it, or makes its wish.
	Settle Settle
	// Workers tells whether a task is running.
	Workers WishWorkers
}

// AnswerHook is called with a question once its answer is stored. It says what it did with the answer, for the
// wish's lead: "Djinn started W12: …"; "" when the answer is the lead's to act on.
type AnswerHook func(ctx context.Context, q *planv1.Question) string

// Told tells the wish's lead that q was answered, did saying what the hooks did with the answer.
type Told func(ctx context.Context, q *planv1.Question, did string)

// afterAnswer calls hooks with q, its answer stored, then told, when set, with what they did.
func afterAnswer(ctx context.Context, hooks []AnswerHook, told Told, q *planv1.Question) {
	var did []string
	for _, f := range hooks {
		if d := f(ctx, proto.CloneOf(q)); d != "" {
			did = append(did, d)
		}
	}
	if told != nil {
		told(ctx, proto.CloneOf(q), strings.Join(did, "; "))
	}
}

// Settle acts on the answer of q in tx, the transaction that stores it, and may change q before it is stored. It
// returns what follows once the answer is stored, if anything.
type Settle func(ctx context.Context, tx *store.Tx, q *planv1.Question) (func(context.Context), error)

// settled runs settle on q when there is one.
func settled(ctx context.Context, settle Settle, tx *store.Tx, q *planv1.Question) (func(context.Context), error) {
	if settle == nil {
		return nil, nil
	}
	return settle(ctx, tx, q)
}

// maxCode is the last code a wish can give: the codes follow ^Q[0-9]{2,3}$.
const maxCode = 999

func (q *Questions) Ask(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceAskRequest],
) (*connect.Response[planv1.QuestionServiceAskResponse], error) {
	question := &planv1.Question{
		Id: store.NewID(), WishId: req.Msg.GetWishId(), Text: req.Msg.GetText(), Options: req.Msg.GetOptions(),
		Context: req.Msg.GetContext(), Recommendation: req.Msg.GetRecommendation(), CreateTime: timestamppb.Now(),
		Icon: req.Msg.GetIcon(), TaskId: req.Msg.GetTaskId(), Before: strings.TrimSpace(req.Msg.GetBefore()),
		Move: req.Msg.GetMove(),
	}
	if err := checkIcon(question.GetIcon()); err != nil {
		return nil, err
	}
	err := write(ctx, q.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		if _, err := store.Get[*planv1.Wish](ctx, tx, question.GetWishId()); err != nil {
			return err
		}
		if err := taskOfWish(ctx, tx, question.GetWishId(), question.GetTaskId()); err != nil {
			return err
		}
		return Ask(ctx, tx, question)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.QuestionServiceAskResponse{Question: question}), nil
}

func (q *Questions) taskRuns(t *planv1.Task) bool {
	if working(t) {
		return true
	}
	if q.Workers != nil && q.Workers.HasRun(t.GetId()) {
		return true
	}
	return false
}

// NextQuestionCode is the code of a new question of the wish wishID: Q01, Q02…
// The number after the highest one its questions have or its retired codes had (Wish.retired_codes),
// so that a code is never given twice, deleted or moved.
func NextQuestionCode(ctx context.Context, r store.Reader, wishID string) (string, error) {
	wish, err := store.Get[*planv1.Wish](ctx, r, wishID)
	if err != nil {
		return "", err
	}
	asked, err := store.List[*planv1.Question](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return "", err
	}
	last := 0
	for _, a := range asked {
		var n int
		if _, err := fmt.Sscanf(a.GetCode(), "Q%d", &n); err == nil && n > last {
			last = n
		}
	}
	for _, code := range wish.GetRetiredCodes() {
		var n int
		if _, err := fmt.Sscanf(code, "Q%d", &n); err == nil && n > last {
			last = n
		}
	}
	if last >= maxCode {
		return "", connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("this wish has used its %d question codes", maxCode))
	}
	return fmt.Sprintf("Q%02d", last+1), nil
}

// Ask stores question, new, in tx with the next code of its wish. The caller journals the command that asks it.
func Ask(ctx context.Context, tx *store.Tx, question *planv1.Question) error {
	code, err := NextQuestionCode(ctx, tx, question.GetWishId())
	if err != nil {
		return err
	}
	question.Code = code
	return tx.Put(question)
}

func (q *Questions) Answer(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceAnswerRequest],
) (*connect.Response[planv1.QuestionServiceAnswerResponse], error) {
	var question *planv1.Question
	var then func(context.Context)
	err := write(ctx, q.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if question, err = find(ctx, tx, req.Msg.GetQuestion(), req.Msg.GetWishId()); err != nil {
			return err
		}
		if question.GetWithdrawal() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s was withdrawn", question.GetCode()))
		}
		if question.GetRoute() != nil && question.GetAnswer() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"question %s has routed its request already: route it again (djinn wish route) to send it elsewhere",
				question.GetCode()))
		}
		choice, err := resolve(question, req.Msg.GetChoice())
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		question.Answer = &planv1.Answer{Choice: choice, Note: req.Msg.GetNote(), CreateTime: timestamppb.Now()}
		if then, err = settled(ctx, q.Settle, tx, question); err != nil {
			return err
		}
		return tx.Put(question)
	})
	if err != nil {
		return nil, err
	}
	if then != nil {
		then(ctx)
	}
	afterAnswer(ctx, q.Answered, q.Told, question)
	return connect.NewResponse(&planv1.QuestionServiceAnswerResponse{Question: question}), nil
}

// find resolves a reference to a question: its id, or its code within wishID, or within every wish when the code
// is used by one only.
func find(ctx context.Context, tx *store.Tx, ref *planv1.QuestionRef, wishID string) (*planv1.Question, error) {
	if id := ref.GetId(); id != "" {
		return store.Get[*planv1.Question](ctx, tx, id)
	}
	where := store.Where{"code": ref.GetCode()}
	if wishID != "" {
		where["wish_id"] = wishID
	}
	found, err := store.List[*planv1.Question](ctx, tx, where)
	if err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no question %s", ref.GetCode()))
	case 1:
		return found[0], nil
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"%d wishes have a question %s: give its identifier, or the wish with --wish-id", len(found), ref.GetCode()))
}

// resolve checks that choice answers question, and returns the choice to keep: yes without options, or the letter
// of one of its options. On options that say yes and no, yes and no pick them, and the answer keeps the letter.
func resolve(question *planv1.Question, choice planv1.Choice) (planv1.Choice, error) {
	options := question.GetOptions()
	n := len(options)
	if n == 0 {
		if choice != planv1.Choice_CHOICE_YES {
			return 0, fmt.Errorf("question %s has no options: answer yes", question.GetCode())
		}
		return choice, nil
	}
	letters := fmt.Sprintf("answer with a letter from a to %c", 'a'+n-1)
	if choice == planv1.Choice_CHOICE_YES || choice == planv1.Choice_CHOICE_NO {
		key, word := "answer.yes", "yes"
		if choice == planv1.Choice_CHOICE_NO {
			key, word = "answer.no", "no"
		}
		if letter, ok := saying(options, key); ok {
			return letter, nil
		}
		return 0, fmt.Errorf("no option of question %s says %s: %s", question.GetCode(), word, letters)
	}
	if choice < planv1.Choice_CHOICE_A || choice > planv1.Choice_CHOICE_D || int(choice-planv1.Choice_CHOICE_A) >= n {
		return 0, fmt.Errorf("question %s has %d options: %s", question.GetCode(), n, letters)
	}
	return choice, nil
}

// saying is the letter of the one option whose first word means key in a language of Djinn: "Yes: it starts
// again" says yes, in English or in another catalog's word. None, or two, and there is no such option.
func saying(options []string, key string) (planv1.Choice, bool) {
	found := -1
	for i, option := range options {
		option = strings.TrimSpace(option)
		word := option
		if end := strings.IndexFunc(option, func(r rune) bool { return !unicode.IsLetter(r) }); end >= 0 {
			word = option[:end]
		}
		if !locales.Means(word, key) {
			continue
		}
		if found >= 0 {
			return 0, false
		}
		found = i
	}
	if found < 0 {
		return 0, false
	}
	return planv1.Choice_CHOICE_A + planv1.Choice(found), true
}

func (q *Questions) List(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceListRequest],
) (*connect.Response[planv1.QuestionServiceListResponse], error) {
	where := store.Where{}
	if id := req.Msg.GetWishId(); id != "" {
		where["wish_id"] = id
	}
	all, err := store.List[*planv1.Question](ctx, q.Store, where)
	if err != nil {
		return nil, Status(err)
	}
	since := req.Msg.GetSince()
	var matching []*planv1.Question
	for _, question := range all {
		if req.Msg.GetOpen() && (question.GetAnswer() != nil || question.GetWithdrawal() != nil) {
			continue
		}
		if since != nil && question.GetCreateTime().AsTime().Before(since.AsTime()) {
			continue
		}
		matching = append(matching, question)
	}
	page, nextToken, total, err := Paginate(matching, req.Msg.GetPageSize(), req.Msg.GetPageToken())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.QuestionServiceListResponse{
		Questions:     page,
		NextPageToken: nextToken,
		Total:         total,
	}), nil
}

// Move moves an open question to another wish, optionally along with tasks that name it as their decision.
func (q *Questions) Move(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceMoveRequest],
) (*connect.Response[planv1.QuestionServiceMoveResponse], error) {
	var movedQuestion *planv1.Question
	var movedTasks []*planv1.Task
	err := write(ctx, q.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		sourceQuestion, err := find(ctx, tx, req.Msg.GetQuestion(), req.Msg.GetWishId())
		if err != nil {
			return err
		}
		if sourceQuestion.GetAnswer() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s is decided: ask a new one", sourceQuestion.GetCode()))
		}
		if sourceQuestion.GetWithdrawal() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s was withdrawn", sourceQuestion.GetCode()))
		}
		targetWish, err := ResolveWish(ctx, tx, req.Msg.GetWish())
		if err != nil {
			return err
		}
		if targetWish.GetId() == sourceQuestion.GetWishId() {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("question %s is already in %s", sourceQuestion.GetCode(), targetWish.GetTitle()))
		}
		sourceWish, err := store.Get[*planv1.Wish](ctx, tx, sourceQuestion.GetWishId())
		if err != nil {
			return err
		}

		oldCode := sourceQuestion.GetCode()
		newCode, err := NextQuestionCode(ctx, tx, targetWish.GetId())
		if err != nil {
			return err
		}

		// Tasks naming this question with --decision
		var following []*planv1.Task
		if req.Msg.GetFollow() {
			tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": sourceWish.GetId()})
			if err != nil {
				return err
			}
			for _, t := range tasks {
				if strings.EqualFold(t.GetDecision(), oldCode) || t.GetDecision() == sourceQuestion.GetId() {
					if q.taskRuns(t) {
						return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s runs: wait for it to end", t.GetCode()))
					}
					following = append(following, t)
				}
			}
		}

		// Retire old question code in source wish
		if !slices.Contains(sourceWish.GetRetiredCodes(), oldCode) {
			sourceWish.RetiredCodes = append(sourceWish.RetiredCodes, oldCode)
		}

		// Prepare target tasks and taken codes for following tasks
		targetTasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": targetWish.GetId()})
		if err != nil {
			return err
		}
		takenCodes := make(map[string]bool)
		for _, c := range targetWish.GetRetiredCodes() {
			takenCodes[strings.ToUpper(c)] = true
		}
		for _, tt := range targetTasks {
			takenCodes[strings.ToUpper(tt.GetCode())] = true
		}

		validTargetTaskIDs := make(map[string]bool, len(targetTasks)+len(following))
		for _, tt := range targetTasks {
			validTargetTaskIDs[tt.GetId()] = true
		}
		for _, ft := range following {
			validTargetTaskIDs[ft.GetId()] = true
		}

		nextNum := func(letter string) string {
			last := 0
			for c := range takenCodes {
				var n int
				if _, err := fmt.Sscanf(c, letter+"%d", &n); err == nil && n > last {
					last = n
				}
			}
			code := fmt.Sprintf("%s%d", letter, last+1)
			takenCodes[strings.ToUpper(code)] = true
			return code
		}

		for _, t := range following {
			oldTaskCode := t.GetCode()
			if !slices.Contains(sourceWish.GetRetiredCodes(), oldTaskCode) {
				sourceWish.RetiredCodes = append(sourceWish.RetiredCodes, oldTaskCode)
			}
			if takenCodes[strings.ToUpper(t.GetCode())] {
				letter := "W"
				if IsAzima(t) {
					letter = "T"
				}
				if len(t.GetCode()) > 0 {
					r := rune(t.GetCode()[0])
					if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
						letter = strings.ToUpper(string(r))
					}
				}
				t.Code = nextNum(letter)
			} else {
				takenCodes[strings.ToUpper(t.GetCode())] = true
			}
			t.WishId = targetWish.GetId()
			t.Decision = newCode

			var validDeps []string
			for _, depID := range t.GetDependsOn() {
				if validTargetTaskIDs[depID] {
					validDeps = append(validDeps, depID)
				}
			}
			t.DependsOn = validDeps

			if t.GetPartOf() != "" && !validTargetTaskIDs[t.GetPartOf()] {
				t.PartOf = ""
			}
		}

		// Cycle check in target wish
		allTargetTasks := append(slices.Clone(targetTasks), following...)
		for _, t := range following {
			if cycle := ClosesCycle(t, t.GetDependsOn(), t.GetPartOf(), allTargetTasks); cycle != "" {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s closes a cycle: %s", t.GetCode(), cycle))
			}
		}

		// Save source wish (with retired codes)
		if err := tx.Put(sourceWish); err != nil {
			return err
		}

		// Save question in target wish
		sourceQuestion.WishId = targetWish.GetId()
		sourceQuestion.Code = newCode
		if err := tx.Put(sourceQuestion); err != nil {
			return err
		}

		// Save following tasks
		for _, t := range following {
			if err := tx.Put(t); err != nil {
				return err
			}
		}

		// Trace decision block in source wish's decision log
		if _, err := PutBlock(ctx, tx, &planv1.BlockServicePutRequest{
			WishId:  sourceWish.GetId(),
			Kind:    "decision",
			Title:   fmt.Sprintf("moved to %s as %s", targetWish.GetTitle(), newCode),
			Content: sourceQuestion.GetText(),
			Icon:    sourceQuestion.GetIcon(),
		}); err != nil {
			return err
		}

		movedQuestion = sourceQuestion
		movedTasks = following
		return nil
	})
	if err != nil {
		return nil, err
	}
	if q.Workers != nil && len(movedTasks) > 0 {
		q.Workers.Wake()
	}
	return connect.NewResponse(&planv1.QuestionServiceMoveResponse{
		Question: movedQuestion,
		Tasks:    movedTasks,
	}), nil
}

// Withdraw closes an open question without an answer.
func (q *Questions) Withdraw(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceWithdrawRequest],
) (*connect.Response[planv1.QuestionServiceWithdrawResponse], error) {
	var question *planv1.Question
	err := write(ctx, q.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if question, err = find(ctx, tx, req.Msg.GetQuestion(), req.Msg.GetWishId()); err != nil {
			return err
		}
		if question.GetAnswer() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s is decided: ask a new one", question.GetCode()))
		}
		if question.GetWithdrawal() != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s was withdrawn", question.GetCode()))
		}
		question.Withdrawal = &planv1.Withdrawal{
			Note:       req.Msg.GetNote(),
			CreateTime: timestamppb.Now(),
		}
		return tx.Put(question)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.QuestionServiceWithdrawResponse{Question: question}), nil
}
