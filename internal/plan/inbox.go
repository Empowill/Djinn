package plan

// The inbox. What comes from outside (a merge request assigned to you, a mention in a thread) becomes an item,
// proposed as a request: Djinn routes it as djinn wish route does, without a model, and waits for your answer. A
// source is a command a skill declares in its SKILL.md, under metadata.djinn.source, run like a watcher; each
// paragraph it prints is an item. A source runs only once the developer plugs it in, on this machine: a clone of a
// project polls nothing by itself. Djinn reads a source and never sends anything back: no comment, no reaction,
// nothing marked as read. The command logs in by its own tool: Djinn holds no token.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// How often a source runs.
const (
	// SourceEvery is the least time between two starts of a source's command, by default.
	SourceEvery = 5 * time.Minute
	// sourceEveryMin is the least a source may ask for: Djinn polls nobody's server faster.
	sourceEveryMin = time.Minute //nolint:revive // Min is the least, not minutes.
)

// What an item holds.
const (
	itemMax    = 4000 // the most characters of an item's text: a request's
	itemKeyMax = 300  // the most characters of an item's key
)

// Who journals an item that came, and how.
const (
	actorSource   = "source"
	methodReceive = "inbox/receive" // a source printed a new item; the request is the item
)

// Source is the inbox source of a skill, read from its SKILL.md.
type Source struct {
	// Skill is the skill's name; Dir its folder.
	Skill, Dir string
	// HolderID is the project that holds the skill, Holder its name: the source is named <Holder>/<Skill>.
	HolderID, Holder string
	// ProjectID is the project that holds the skill or summons it: the command runs in its folder.
	ProjectID string
	// Watch is the command line, run without a shell.
	Watch string
	// Every is the least time between two starts of the command.
	Every time.Duration
	// Plugged tells whether the source is plugged in on this machine: only then does its command run.
	Plugged bool
	// Err says why the source cannot be used; nil when it can.
	Err error
}

// Name is the source as the developer names it: <project>/<skill>, the project that holds the skill.
func (s *Source) Name() string { return s.Holder + "/" + s.Skill }

// sourceYAML is an inbox source as SKILL.md writes it.
type sourceYAML struct {
	Watch string `yaml:"watch"`
	Every string `yaml:"every"`
}

// ReadSource reads the inbox source of the skill in dir: nil without one, an error when it cannot be used.
func ReadSource(dir string) (*Source, error) {
	djinn, err := readDjinn(dir)
	if err != nil || djinn.Source == nil {
		return nil, err
	}
	s := &Source{Skill: filepath.Base(dir), Dir: dir, Watch: strings.TrimSpace(djinn.Source.Watch), Every: SourceEvery}
	if s.Watch == "" {
		return nil, errors.New("metadata.djinn.source has no watch: the command that prints the items")
	}
	if placeholders.MatchString(s.Watch) {
		return nil, errors.New("metadata.djinn.source.watch: a source has no placeholder, no request fills it")
	}
	if every := strings.TrimSpace(djinn.Source.Every); every != "" {
		if s.Every, err = time.ParseDuration(every); err != nil {
			return nil, fmt.Errorf("metadata.djinn.source.every: %w", err)
		}
		if s.Every < sourceEveryMin {
			return nil, fmt.Errorf("metadata.djinn.source.every: %s is too often, a source runs at most every %s",
				every, sourceEveryMin)
		}
	}
	return s, nil
}

// heldSkill is a skill a project uses, and the project that holds it.
type heldSkill struct {
	SkillDir
	// HolderID is the project that holds the skill, Holder its name.
	HolderID, Holder string
}

// usedSkills are the skills project uses, its own then those it summons that are found.
func usedSkills(ctx context.Context, r store.Reader, project *planv1.Project) ([]heldSkill, error) {
	var out []heldSkill
	for _, d := range ProjectSkills(project.GetDirectory()) {
		out = append(out, heldSkill{SkillDir: d, HolderID: project.GetId(), Holder: project.GetName()})
	}
	summoned, err := SummonedSkills(ctx, r, project)
	if err != nil {
		return nil, err
	}
	for _, s := range summoned {
		if s.Missing == "" {
			holder, _, _ := strings.Cut(s.Source, "/")
			out = append(out, heldSkill{SkillDir: s.SkillDir, HolderID: s.ProjectID, Holder: holder})
		}
	}
	return out, nil
}

// declaredSources are the inbox sources of the skills the projects use, their own then those they summon, in the
// projects' order then by name, each said plugged in or not on this machine; a source that cannot be used says
// why. A skill used by several projects is one source, run in the first one's folder. A source plugged in whose
// skill no longer declares it comes last, with why: it runs nothing, and can be unplugged.
func declaredSources(ctx context.Context, r store.Reader, projects []*planv1.Project) ([]*Source, error) {
	plugged, err := store.List[*planv1.PluggedSource](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	isPlugged := func(s *Source) bool {
		return slices.ContainsFunc(plugged, func(p *planv1.PluggedSource) bool {
			return strings.EqualFold(p.GetProjectId(), s.HolderID) && strings.EqualFold(p.GetSkill(), s.Skill)
		})
	}
	var out []*Source
	for _, p := range projects {
		if p.GetDirectory() == "" {
			continue
		}
		used, err := usedSkills(ctx, r, p)
		if err != nil {
			return nil, err
		}
		for _, d := range used {
			if slices.ContainsFunc(out, func(s *Source) bool { return s.Dir == d.Dir }) {
				continue
			}
			s, err := ReadSource(d.Dir)
			switch {
			case err != nil:
				s = &Source{Skill: d.Name, Dir: d.Dir, Err: err}
			case s == nil:
				continue
			}
			s.HolderID, s.Holder, s.ProjectID = d.HolderID, d.Holder, p.GetId()
			s.Plugged = isPlugged(s)
			out = append(out, s)
		}
	}
	for _, p := range plugged {
		if slices.ContainsFunc(out, func(s *Source) bool {
			return strings.EqualFold(p.GetProjectId(), s.HolderID) && strings.EqualFold(p.GetSkill(), s.Skill)
		}) {
			continue
		}
		holder := p.GetProjectId()
		if project, err := store.Get[*planv1.Project](ctx, r, holder); err == nil {
			holder = project.GetName()
		}
		out = append(out, &Source{
			Skill: p.GetSkill(), HolderID: p.GetProjectId(), Holder: holder, Plugged: true,
			Err: errors.New("no skill of the projects declares this source on this machine any more: it runs nothing"),
		})
	}
	return out, nil
}

// Sources are the inbox sources plugged in on this machine that can run, from the skills the projects use, in the
// projects' order then by name. A skill used by several projects runs once, in the first one's folder. A source
// that cannot be used is left out: djinn inbox sources says why. One not plugged in never runs.
func Sources(ctx context.Context, r store.Reader, projects []*planv1.Project) ([]*Source, error) {
	all, err := declaredSources(ctx, r, projects)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(s *Source) bool { return s.Err != nil || !s.Plugged }), nil
}

// sourceMessage is the source s as InboxService shows it.
func sourceMessage(s *Source) *planv1.InboxSource {
	out := &planv1.InboxSource{Name: s.Name(), Skill: s.Skill, Project: s.Holder, Watch: s.Watch, Plugged: s.Plugged}
	if s.Every > 0 {
		out.Every = s.Every.String()
	}
	if s.Err != nil {
		out.Error = s.Err.Error()
	}
	return out
}

// findSource is the source named name among srcs: <project>/<skill>, or the skill alone when one project holds it.
func findSource(srcs []*Source, name string) (*Source, error) {
	project, skill, scoped := strings.Cut(name, "/")
	if !scoped {
		skill = project
	}
	var found []*Source
	for _, s := range srcs {
		if strings.EqualFold(s.Skill, skill) && (!scoped || strings.EqualFold(s.Holder, project)) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("no inbox source %s: djinn inbox sources lists them, %w", name, store.ErrNotFound))
	case 1:
		return found[0], nil
	}
	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.Name()
	}
	return nil, connect.NewError(connect.CodeInvalidArgument,
		fmt.Errorf("several projects hold a source %s: name one, as %s", name, strings.Join(names, " or ")))
}

// sourcesIn are the sources the projects' skills declare, read in r.
func sourcesIn(ctx context.Context, r store.Reader) ([]*Source, error) {
	projects, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	return declaredSources(ctx, r, projects)
}

// itemKey is what makes an item the same when a source prints it again: its first link, else its first line.
func itemKey(text string) string {
	if link := links.FindString(text); link != "" {
		return clipRunes(strings.TrimRight(link, ".,;:!?"), itemKeyMax)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return clipRunes(strings.Join(strings.Fields(first), " "), itemKeyMax)
}

// held tells whether a wish holds the link key already, in its title or its blocks: its request was routed, or a
// lead wrote it down.
func held(key string, wishes []*planv1.Wish, blocks map[string][]*planv1.Block) bool {
	if !links.MatchString(key) {
		return false
	}
	for _, w := range wishes {
		if strings.Contains(w.GetTitle(), key) {
			return true
		}
		for _, b := range blocks[w.GetId()] {
			if strings.Contains(b.GetContent(), key) {
				return true
			}
		}
	}
	return false
}

// Inbox implements InboxService, and receives what the sources print.
type Inbox struct {
	planv1connect.UnimplementedInboxServiceHandler
	// Wishes routes an item: it files it, or makes its wish and starts its watcher and lead.
	Wishes *Wishes
}

// Receive reads a paragraph a source printed: a new item, its route proposed without a model, unless the same item
// came already (new, routed or dismissed) or a wish holds its link. It returns the item made, nil when none. Nothing
// is made nor sent: the item waits for the developer's answer.
func (in *Inbox) Receive(ctx context.Context, src *Source, text string) (*planv1.InboxItem, error) {
	text = clipRunes(strings.TrimSpace(text), itemMax)
	if text == "" {
		return nil, nil
	}
	key := itemKey(text)
	var made *planv1.InboxItem
	err := in.Wishes.Store.Tx(ctx, func(tx *store.Tx) error {
		items, err := store.List[*planv1.InboxItem](ctx, tx, nil)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(items, func(i *planv1.InboxItem) bool { return strings.EqualFold(i.GetKey(), key) }) {
			return nil
		}
		input := routeInput{lang: cmp.Or(in.Wishes.Language, locales.Source), text: text, home: []string{src.ProjectID}}
		if _, err := readRoute(ctx, tx, &input); err != nil {
			return err
		}
		if held(key, input.wishes, input.blocks) {
			return nil
		}
		item := &planv1.InboxItem{
			Id: store.NewID(), Source: src.Skill, ProjectId: src.ProjectID, Key: key, Text: text,
			State: planv1.InboxState_INBOX_STATE_NEW, CreateTime: timestamppb.Now(), Route: propose(input),
		}
		if err := tx.Journal(actorSource, methodReceive, item); err != nil {
			return err
		}
		made = item
		return tx.Put(item)
	})
	if err != nil {
		return nil, Status(err)
	}
	return made, nil
}

func (in *Inbox) List(
	ctx context.Context, req *connect.Request[planv1.InboxServiceListRequest],
) (*connect.Response[planv1.InboxServiceListResponse], error) {
	items, err := store.List[*planv1.InboxItem](ctx, in.Wishes.Store, nil)
	if err != nil {
		return nil, Status(err)
	}
	if !req.Msg.GetAll() {
		items = slices.DeleteFunc(items, func(i *planv1.InboxItem) bool {
			return i.GetState() != planv1.InboxState_INBOX_STATE_NEW
		})
	}
	// Identifiers are UUIDv7: the newest first.
	slices.SortFunc(items, func(a, b *planv1.InboxItem) int { return strings.Compare(b.GetId(), a.GetId()) })
	return connect.NewResponse(&planv1.InboxServiceListResponse{Items: items}), nil
}

// pending is the item id that still waits for an answer, read in tx.
func pending(ctx context.Context, tx *store.Tx, id string) (*planv1.InboxItem, error) {
	item, err := store.Get[*planv1.InboxItem](ctx, tx, id)
	if err != nil {
		return nil, err
	}
	switch item.GetState() {
	case planv1.InboxState_INBOX_STATE_ROUTED:
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this item was routed already"))
	case planv1.InboxState_INBOX_STATE_DISMISSED:
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this item was dismissed"))
	}
	return item, nil
}

func (in *Inbox) Dismiss(
	ctx context.Context, req *connect.Request[planv1.InboxServiceDismissRequest],
) (*connect.Response[planv1.InboxServiceDismissResponse], error) {
	var item *planv1.InboxItem
	err := write(ctx, in.Wishes.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if item, err = pending(ctx, tx, req.Msg.GetItemId()); err != nil {
			return err
		}
		item.State, item.SettleTime = planv1.InboxState_INBOX_STATE_DISMISSED, timestamppb.Now()
		return tx.Put(item)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InboxServiceDismissResponse{Item: item}), nil
}

func (in *Inbox) Route(
	ctx context.Context, req *connect.Request[planv1.InboxServiceRouteRequest],
) (*connect.Response[planv1.InboxServiceRouteResponse], error) {
	var item *planv1.InboxItem
	var then func(context.Context)
	lang := cmp.Or(in.Wishes.Language, locales.Source)
	err := write(ctx, in.Wishes.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if item, err = pending(ctx, tx, req.Msg.GetItemId()); err != nil {
			return err
		}
		opts := item.GetRoute().GetOptions()
		i := int(req.Msg.GetChoice() - planv1.Choice_CHOICE_A)
		if i < 0 || i >= len(opts) || !routable(opts[i]) {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"this item has %d destinations: choose the letter of one", len(opts)))
		}
		source, text := item.GetSource(), item.GetText()
		then, err = in.Wishes.routeTo(ctx, tx, opts[i], text, origin{
			block: locales.T(lang, "inbox.block_title", map[string]string{"source": source}),
			first: InboxFirstLine(source, text),
			filed: func(wishID string) string { return InboxFiledLine(wishID, source, text) },
		})
		if err != nil {
			return err
		}
		item.State, item.WishId, item.SettleTime = planv1.InboxState_INBOX_STATE_ROUTED, opts[i].GetWishId(), timestamppb.Now()
		return tx.Put(item)
	})
	if err != nil {
		return nil, err
	}
	if then != nil {
		then(ctx)
	}
	return connect.NewResponse(&planv1.InboxServiceRouteResponse{Item: item}), nil
}

func (in *Inbox) Sources(
	ctx context.Context, _ *connect.Request[planv1.InboxServiceSourcesRequest],
) (*connect.Response[planv1.InboxServiceSourcesResponse], error) {
	srcs, err := sourcesIn(ctx, in.Wishes.Store)
	if err != nil {
		return nil, Status(err)
	}
	out := make([]*planv1.InboxSource, len(srcs))
	for i, s := range srcs {
		out[i] = sourceMessage(s)
	}
	slices.SortStableFunc(out, func(a, b *planv1.InboxSource) int {
		return strings.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName()))
	})
	return connect.NewResponse(&planv1.InboxServiceSourcesResponse{Sources: out}), nil
}

func (in *Inbox) Plug(
	ctx context.Context, req *connect.Request[planv1.InboxServicePlugRequest],
) (*connect.Response[planv1.InboxServicePlugResponse], error) {
	var out *planv1.InboxSource
	err := write(ctx, in.Wishes.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		srcs, err := sourcesIn(ctx, tx)
		if err != nil {
			return err
		}
		s, err := findSource(srcs, req.Msg.GetSource())
		if err != nil {
			return err
		}
		if s.Err != nil {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the source %s cannot run: %w", s.Name(), s.Err))
		}
		if !s.Plugged {
			s.Plugged = true
			err := tx.Put(&planv1.PluggedSource{
				Id: store.NewID(), ProjectId: s.HolderID, Skill: s.Skill, CreateTime: timestamppb.Now(),
			})
			if err != nil {
				return err
			}
		}
		out = sourceMessage(s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InboxServicePlugResponse{Source: out}), nil
}

func (in *Inbox) Unplug(
	ctx context.Context, req *connect.Request[planv1.InboxServiceUnplugRequest],
) (*connect.Response[planv1.InboxServiceUnplugResponse], error) {
	var out *planv1.InboxSource
	err := write(ctx, in.Wishes.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		srcs, err := sourcesIn(ctx, tx)
		if err != nil {
			return err
		}
		s, err := findSource(srcs, req.Msg.GetSource())
		if err != nil {
			return err
		}
		plugged, err := store.List[*planv1.PluggedSource](ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, p := range plugged {
			if strings.EqualFold(p.GetProjectId(), s.HolderID) && strings.EqualFold(p.GetSkill(), s.Skill) {
				if err := tx.Delete(p); err != nil {
					return err
				}
			}
		}
		s.Plugged = false
		out = sourceMessage(s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InboxServiceUnplugResponse{Source: out}), nil
}

// InboxFirstLine is the first line of the lead of a wish made for an inbox item: the item, and its source.
func InboxFirstLine(source, text string) string {
	return fmt.Sprintf("The developer made this wish for an item of their inbox, printed by the source of the skill %s. "+
		"Djinn only reads that source: answer nobody on the developer's behalf unless they ask. The item: %s",
		source, strings.TrimSpace(text))
}

// InboxFiledLine is the line that tells the lead of the wish wishID that an inbox item was filed in it.
func InboxFiledLine(wishID, source, text string) string {
	return fmt.Sprintf("Djinn: the developer filed an item of their inbox in this wish, from the source of the skill "+
		"%s: %q. Take it on: djinn wish brief %s has it, in its blocks.", source, clipLine(text), wishID)
}
