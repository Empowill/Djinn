package plan

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/store"
)

// formatVersion is the version of WishExport this Djinn writes, and the only one it reads.
const formatVersion = 1

// maxExport bounds the file an import reads: tilasms make it large, up to 50 MiB each, every version carried.
const maxExport = 512 << 20

// near is how close in time a journal entry and the entity it made must be, to tie a command that names no
// identifier (a wish made, a question answered by its code) to its wish.
const near = 5 * time.Second

var questionCode = regexp.MustCompile(`^Q[0-9]{2,3}$`)

// Export writes the wish to a file.
func (w *Wishes) Export(
	ctx context.Context, req *connect.Request[planv1.WishServiceExportRequest],
) (*connect.Response[planv1.WishServiceExportResponse], error) {
	exp, _, err := collect(ctx, w.Store, req.Msg.GetWishId(), allEvents)
	if err != nil {
		return nil, Status(err)
	}
	all, err := store.List[*planv1.Project](ctx, w.Store, nil)
	if err != nil {
		return nil, Status(err)
	}
	exp = portable(exp, newScrubber(all))
	// The files of the tilasms travel as they are: a scrubber does not rewrite what they hold.
	for _, t := range exp.GetTilasms() {
		if w.Home == "" {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("this server keeps no tilasms: run djinn up"))
		}
		if t.Files, err = tilasmFiles(w.Home, t.GetTilasm()); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}
	file := req.Msg.GetFile()
	if file == "" {
		dir, err := Downloads()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("find the Downloads folder: %w", err))
		}
		file = freePath(dir, fileName(exp.GetWish().GetTitle()), ".djinn")
	}
	if !filepath.IsAbs(file) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file: %q is not an absolute path", file))
	}
	data, err := encode(exp, strings.EqualFold(filepath.Ext(file), ".json"))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := writeFile(file, data); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&planv1.WishServiceExportResponse{File: file, Size: int64(len(data))}), nil
}

// Import reads a wish from a file.
func (w *Wishes) Import(
	ctx context.Context, req *connect.Request[planv1.WishServiceImportRequest],
) (*connect.Response[planv1.WishServiceImportResponse], error) {
	file := req.Msg.GetFile()
	if !filepath.IsAbs(file) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file: %q is not an absolute path", file))
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if info.Size() > maxExport {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is larger than %d MB", file, maxExport>>20))
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	res, err := w.load(ctx, data, req.Msg.GetReplace())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.WishServiceImportResponse{
		Wish: res.GetWish(), Projects: res.GetProjects(), Note: res.GetNote(),
	}), nil
}

// ImportData reads a wish from the content of a file.
func (w *Wishes) ImportData(
	ctx context.Context, req *connect.Request[planv1.WishServiceImportDataRequest],
) (*connect.Response[planv1.WishServiceImportDataResponse], error) {
	res, err := w.load(ctx, req.Msg.GetData(), req.Msg.GetReplace())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// Snapshot returns everything the wish holds, with the identifiers and folders of this machine.
func (w *Wishes) Snapshot(
	ctx context.Context, req *connect.Request[planv1.WishServiceSnapshotRequest],
) (*connect.Response[planv1.WishServiceSnapshotResponse], error) {
	exp, projects, err := collect(ctx, w.Store, req.Msg.GetWishId(), allEvents)
	if err != nil {
		return nil, Status(err)
	}
	wish := exp.GetWish()
	wish.Ready = wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions())
	return connect.NewResponse(&planv1.WishServiceSnapshotResponse{Export: exp, Projects: projects}), nil
}

// eventsOf reads the events of a wish's tasks that a reader of collect needs, in the order of an export: by task,
// then by position.
type eventsOf func(ctx context.Context, r store.Reader, tasks []*planv1.Task) ([]*planv1.TaskEvent, error)

// allEvents is every event of the tasks: an export carries them all.
func allEvents(ctx context.Context, r store.Reader, tasks []*planv1.Task) ([]*planv1.TaskEvent, error) {
	var out []*planv1.TaskEvent
	for _, t := range tasks {
		events, err := store.List[*planv1.TaskEvent](ctx, r, store.Where{"task_id": t.GetId()})
		if err != nil {
			return nil, err
		}
		slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return int(a.GetSeq() - b.GetSeq()) })
		out = append(out, events...)
	}
	return out, nil
}

// noEvents is none: the brief shows none, and a wish's events are most of the store.
func noEvents(context.Context, store.Reader, []*planv1.Task) ([]*planv1.TaskEvent, error) {
	return nil, nil
}

// collect gathers what the wish holds, as stored, with the events that events reads: its project references carry
// the identifiers of this machine.
func collect(ctx context.Context, r store.Reader, wishID string, events eventsOf) (*planv1.WishExport, []*planv1.Project, error) {
	wish, err := store.Get[*planv1.Wish](ctx, r, wishID)
	if err != nil {
		return nil, nil, err
	}
	exp := &planv1.WishExport{Version: formatVersion, CreateTime: timestamppb.Now(), Wish: wish}
	where := store.Where{"wish_id": wishID}
	if exp.Tasks, err = store.List[*planv1.Task](ctx, r, where); err != nil {
		return nil, nil, err
	}
	if exp.Events, err = events(ctx, r, exp.GetTasks()); err != nil {
		return nil, nil, err
	}
	if exp.Questions, err = store.List[*planv1.Question](ctx, r, where); err != nil {
		return nil, nil, err
	}
	if exp.Blocks, err = store.List[*planv1.Block](ctx, r, where); err != nil {
		return nil, nil, err
	}
	sortBlocks(exp.Blocks)
	if exp.Tilasms, err = tilasmsOf(ctx, r, wishID); err != nil {
		return nil, nil, err
	}

	ids := slices.Clone(wish.GetProjectIds())
	for _, t := range exp.GetTasks() {
		if id := t.GetProjectId(); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var projects []*planv1.Project
	for _, id := range ids {
		p, err := store.Get[*planv1.Project](ctx, r, id)
		if err != nil {
			return nil, nil, err
		}
		projects = append(projects, p)
		exp.Projects = append(exp.Projects, &planv1.ProjectRef{
			Id: p.GetId(), Name: p.GetName(), Remote: cleanRemote(p.GetRemote()), Git: p.GetGit(),
		})
	}
	if exp.Commands, err = commands(ctx, r, exp); err != nil {
		return nil, nil, err
	}
	return exp, projects, nil
}

// commands returns the journal entries that changed the wish: those whose request names the wish or one of its
// entities, the one that made it, and the answers given by code. An entry that imported the wish stands for the
// commands it carried, and supersedes those before it: the history follows the wish from machine to machine, without
// nesting.
func commands(ctx context.Context, r store.Reader, exp *planv1.WishExport) ([]*planv1.Command, error) {
	wish := exp.GetWish()
	ids := map[string]bool{strings.ToLower(wish.GetId()): true}
	for _, t := range exp.GetTasks() {
		ids[strings.ToLower(t.GetId())] = true
	}
	answered := map[string]time.Time{}
	for _, q := range exp.GetQuestions() {
		ids[strings.ToLower(q.GetId())] = true
		if a := q.GetAnswer(); a != nil {
			answered[strings.ToUpper(q.GetCode())] = a.GetCreateTime().AsTime()
		}
	}
	for _, b := range exp.GetBlocks() {
		ids[strings.ToLower(b.GetId())] = true
	}
	for _, t := range exp.GetTilasms() {
		ids[strings.ToLower(t.GetTilasm().GetId())] = true
	}
	var out []*planv1.Command
	var failed error
	// Only the methods of the API make a wish's history: "/plan.v1.WishService/Make"… The harness's entries, a
	// worker's events most of all, are not even read.
	_, err := store.CommandsOf(ctx, r, "/", func(c store.Command) bool {
		req, err := request(c)
		if err != nil || req == nil {
			// An entry of the harness or of an older Djinn: not a request of the API, left out.
			return false
		}
		switch m := req.(type) {
		case *planv1.WishServiceImportDataRequest:
			imported, err := decode(m.GetData())
			if err == nil && strings.EqualFold(imported.GetWish().GetId(), wish.GetId()) {
				// The import set the wish as the file held it: what came before is in the file, or was replaced.
				out = slices.Clone(imported.GetCommands())
			}
			return false
		case *planv1.WishServiceMakeRequest:
			if m.GetTitle() != wish.GetTitle() || !within(c.At, wish.GetCreateTime().AsTime()) {
				return false
			}
		case *planv1.QuestionServiceAnswerRequest:
			at, ok := answered[strings.ToUpper(m.GetQuestion().GetCode())]
			byCode := m.GetWishId() == "" && ok && within(c.At, at)
			if !byCode && !names(req.ProtoReflect(), ids) {
				return false
			}
		default:
			if !names(req.ProtoReflect(), ids) {
				return false
			}
		}
		packed, err := anypb.New(req)
		if err != nil {
			failed = err
			return false
		}
		out = append(out, &planv1.Command{
			Id: c.ID, Actor: c.Actor, At: timestamppb.New(c.At), Method: c.Method, Request: packed,
		})
		return false
	})
	return out, errors.Join(err, failed)
}

func within(a, b time.Time) bool { return a.Sub(b).Abs() <= near }

// request decodes the request of a journal entry by its method; nil when the method is not one of the API.
func request(c store.Command) (proto.Message, error) {
	service, method, ok := strings.Cut(strings.TrimPrefix(c.Method, "/"), "/")
	if !ok {
		return nil, nil
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, nil //nolint:nilerr // Not a method of the API: see above.
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil
	}
	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, nil
	}
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		return nil, nil //nolint:nilerr // Not a method of the API: see above.
	}
	m := mt.New().Interface()
	return m, proto.Unmarshal(c.Request, m)
}

// names tells whether a string field of m, at any depth, holds one of ids.
func names(m protoreflect.Message, ids map[string]bool) bool {
	found := false
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
		case fd.IsList():
			list := v.List()
			for i := range list.Len() {
				if fd.Kind() == protoreflect.StringKind && ids[strings.ToLower(list.Get(i).String())] ||
					fd.Kind() == protoreflect.MessageKind && names(list.Get(i).Message(), ids) {
					found = true
				}
			}
		case fd.Kind() == protoreflect.StringKind:
			found = ids[strings.ToLower(v.String())]
		case fd.Kind() == protoreflect.MessageKind:
			found = names(v.Message(), ids)
		}
		return !found
	})
	return found
}

// portable strips from an export what only makes sense on this machine, and the local paths in its text.
func portable(exp *planv1.WishExport, scrub *scrubber) *planv1.WishExport {
	exp = proto.Clone(exp).(*planv1.WishExport)
	for _, t := range exp.GetTasks() {
		// The worktree is a folder here, and the agent session lives in this machine's provider.
		t.Worktree, t.SessionId, t.ForkSession = "", "", ""
	}
	for _, e := range exp.GetEvents() {
		e.Raw = ""
		switch e.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, planv1.TaskEventKind_TASK_EVENT_KIND_LOG,
			planv1.TaskEventKind_TASK_EVENT_KIND_OTHER:
			// What a tool returned is often code, or a file of the project: it travels through Git, not here.
			e.Text = ""
		case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
			// The tool's name says what the worker did; its input may be the code it wrote.
			name, _, _ := strings.Cut(strings.TrimSpace(e.GetText()), " ")
			e.Text = name
		}
	}
	for _, c := range exp.GetCommands() {
		req, err := c.GetRequest().UnmarshalNew()
		if err != nil {
			continue
		}
		scrub.message(req.ProtoReflect())
		if packed, err := anypb.New(req); err == nil {
			c.Request = packed
		}
	}
	scrub.message(exp.ProtoReflect())
	// The rank is this machine's order of its wishes; the readiness is computed where the wish is read.
	exp.Wish.Rank, exp.Wish.Ready = 0, false
	if lead := exp.GetWish().GetLead(); lead != nil {
		// The lead's session travels, so that the wish can be taken back where the agent's sessions are; its folder
		// only as a project's name or ~.
		lead.Directory = portableFolder(lead.GetDirectory())
	}
	return exp
}

// scrubber replaces the local paths found in text: a project's folder by its name, the home folder by ~.
type scrubber struct{ r *strings.Replacer }

// newScrubber replaces the folders of projects and the home folder, and each extra folder by the name that follows
// it.
func newScrubber(projects []*planv1.Project, extra ...string) *scrubber {
	type pair struct{ from, to string }
	var pairs []pair
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i] != "" {
			pairs = append(pairs, pair{extra[i], extra[i+1]}, pair{filepath.ToSlash(extra[i]), extra[i+1]})
		}
	}
	for _, p := range projects {
		if dir := p.GetDirectory(); dir != "" {
			pairs = append(pairs, pair{dir, p.GetName()}, pair{filepath.ToSlash(dir), p.GetName()})
		}
	}
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		pairs = append(pairs, pair{home, "~"}, pair{filepath.ToSlash(home), "~"})
	}
	// The longest first: a project inside the home folder keeps its name.
	slices.SortStableFunc(pairs, func(a, b pair) int { return len(b.from) - len(a.from) })
	var args []string
	for _, p := range pairs {
		args = append(args, p.from, p.to)
	}
	return &scrubber{strings.NewReplacer(args...)}
}

func (s *scrubber) message(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
		case fd.IsList():
			list := v.List()
			for i := range list.Len() {
				switch fd.Kind() {
				case protoreflect.StringKind:
					list.Set(i, protoreflect.ValueOfString(s.r.Replace(list.Get(i).String())))
				case protoreflect.MessageKind:
					s.message(list.Get(i).Message())
				}
			}
		case fd.Kind() == protoreflect.StringKind:
			m.Set(fd, protoreflect.ValueOfString(s.r.Replace(v.String())))
		case fd.Kind() == protoreflect.MessageKind:
			s.message(v.Message())
		}
		return true
	})
}

// encode writes an export as binary protobuf, or as JSON for a reader.
func encode(exp *planv1.WishExport, json bool) ([]byte, error) {
	if json {
		b, err := protojson.MarshalOptions{Multiline: true, Indent: "  ", UseProtoNames: true}.Marshal(exp)
		return append(b, '\n'), err
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(exp)
}

// decode reads an export written as binary protobuf or as JSON: JSON starts with a brace, which no field of
// WishExport encodes to.
func decode(data []byte) (*planv1.WishExport, error) {
	exp := &planv1.WishExport{}
	text := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	var err error
	if len(text) > 0 && text[0] == '{' {
		err = protojson.Unmarshal(text, exp)
	} else {
		err = proto.Unmarshal(data, exp)
	}
	if err != nil {
		return nil, fmt.Errorf("not a wish export: %w", err)
	}
	return exp, nil
}

// renamer puts in place what the plan writes: pages, exports and tilasms' folders. It tries again a moment on
// Windows, where a browser reading a page or an antivirus scanning a file refuses the rename. A test swaps it.
var renamer = fsx.OS()

// writeFile writes data to file through a temporary file, so that a failed export leaves no half file and a reader
// sees the old content or the new one.
func writeFile(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".djinn-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renamer.Rename(tmp.Name(), file)
}

// load imports an export in one transaction, journaled as ImportData with the content itself: the journal then
// holds what was imported, whether it came by a file or by the window.
func (w *Wishes) load(ctx context.Context, data []byte, replace bool) (*planv1.WishServiceImportDataResponse, error) {
	exp, err := decode(data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := check(exp); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid export: %w", err))
	}
	// The tilasms' files go to the data folder; the journal keeps the rest of the export.
	placed, err := stageTilasms(w.Home, exp)
	if err != nil {
		return nil, Status(err)
	}
	committed := false
	defer func() { placed.done(committed) }()
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(exp)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	res := &planv1.WishServiceImportDataResponse{}
	err = w.Store.Tx(ctx, func(tx *store.Tx) error {
		req := &planv1.WishServiceImportDataRequest{Data: canonical, Replace: replace}
		if err := tx.Journal(actor, planv1connect.WishServiceImportDataProcedure, req); err != nil {
			return err
		}
		wish := exp.GetWish()
		// A wish replaced keeps its place among the active ones; another goes last.
		place := MaxActive
		if old, err := store.Get[*planv1.Wish](ctx, tx, wish.GetId()); err == nil {
			if !replace {
				return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf(
					"wish %q (%s) is already here: import it with --replace to overwrite it", old.GetTitle(), old.GetId()))
			}
			if Active(old) && old.GetRank() > 0 {
				place = int(old.GetRank()) - 1
			}
			tilasms, err := store.List[*planv1.Tilasm](ctx, tx, store.Where{"wish_id": old.GetId()})
			if err != nil {
				return err
			}
			if err := placed.replace(tilasms); err != nil {
				return err
			}
			if err := forget(ctx, tx, old.GetId()); err != nil {
				return err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := free(ctx, tx, exp); err != nil {
			return err
		}
		local, folders := map[string]string{}, map[string]string{}
		for _, ref := range exp.GetProjects() {
			m, err := match(ctx, tx, ref)
			if err != nil {
				return err
			}
			local[ref.GetId()] = m.GetProject().GetId()
			folders[strings.ToLower(ref.GetName())] = m.GetProject().GetDirectory()
			res.Projects = append(res.Projects, m)
		}
		var projectIDs []string
		for _, id := range wish.GetProjectIds() {
			if !slices.Contains(projectIDs, local[id]) {
				projectIDs = append(projectIDs, local[id])
			}
		}
		wish.ProjectIds = projectIDs
		if t := wish.GetTemplate(); t != nil {
			t.ProjectId = local[t.GetProjectId()]
		}
		if lead := wish.GetLead(); lead != nil {
			lead.Directory = localFolder(lead.GetDirectory(), folders)
		}
		wish.Rank, wish.Ready = 0, false
		if Active(wish) {
			actives, err := ActiveWishes(ctx, tx)
			if err != nil {
				return err
			}
			if len(actives) >= MaxActive {
				wish.State = planv1.WishState_WISH_STATE_PAUSED
				res.Note = fmt.Sprintf("imported paused: %d wishes are active here, and a djinn grants %d at a time. "+
					"Pause or grant one, then djinn wish activate %s", len(actives), MaxActive, wish.GetId())
			} else {
				wish.State = planv1.WishState_WISH_STATE_ACTIVE
				if err := renumber(tx, slices.Insert(actives, min(place, len(actives)), wish)); err != nil {
					return err
				}
			}
		}
		res.Wish = wish
		if err := tx.Put(wish); err != nil {
			return err
		}
		for _, t := range exp.GetTasks() {
			t.ProjectId = local[t.GetProjectId()]
			t.Worktree, t.SessionId, t.ForkSession = "", "", ""
			// Planned on the other machine: this one's scheduler never starts it by itself.
			t.Scheduled, t.Continuing = false, false
			if s := t.GetStatus(); s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED ||
				s == planv1.TaskStatus_TASK_STATUS_RESUMING {
				// Its worker runs on the machine that exported it, not here.
				t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "running when its wish was exported: no worker of this Djinn runs it"
				t.WaitReason, t.ResumeAfter = "", nil
			}
			if err := tx.Put(t); err != nil {
				return err
			}
		}
		for _, m := range entities(exp) {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return placed.place(exp.GetTilasms())
	})
	if err != nil {
		return nil, Status(err)
	}
	committed = true
	return res, nil
}

// entities are the events, questions, blocks and tilasms of an export, which an import writes as they are. A task's
// events go in the order of their positions, as Djinn writes them: its last written is its last.
func entities(exp *planv1.WishExport) []proto.Message {
	var out []proto.Message
	events := slices.Clone(exp.GetEvents())
	slices.SortStableFunc(events, func(a, b *planv1.TaskEvent) int { return cmp.Compare(a.GetSeq(), b.GetSeq()) })
	for _, e := range events {
		out = append(out, e)
	}
	for _, q := range exp.GetQuestions() {
		out = append(out, q)
	}
	for _, b := range exp.GetBlocks() {
		out = append(out, b)
	}
	for _, t := range exp.GetTilasms() {
		out = append(out, t.GetTilasm())
	}
	return out
}

// check refuses an export that does not hold together: an unknown version, an entity without a valid id or of
// another wish, a link to something the export does not hold, a code given twice.
func check(exp *planv1.WishExport) error {
	if exp.GetVersion() != formatVersion {
		return fmt.Errorf("format version %d; this Djinn reads version %d", exp.GetVersion(), formatVersion)
	}
	wish := exp.GetWish()
	if wish == nil {
		return errors.New("no wish")
	}
	seen := map[string]bool{}
	valid := func(what string, m proto.Message, id string) error {
		if err := protovalidate.Validate(m); err != nil {
			return fmt.Errorf("%s %s: %w", what, id, err)
		}
		if seen[strings.ToLower(id)] {
			return fmt.Errorf("%s %s: the identifier is given twice", what, id)
		}
		seen[strings.ToLower(id)] = true
		return nil
	}
	if err := valid("wish", wish, wish.GetId()); err != nil {
		return err
	}
	if wish.GetId() == "" || strings.TrimSpace(wish.GetTitle()) == "" {
		return errors.New("the wish needs an id and a title")
	}
	refs := map[string]bool{}
	for _, p := range exp.GetProjects() {
		if p.GetId() == "" || strings.TrimSpace(p.GetName()) == "" || refs[p.GetId()] {
			return fmt.Errorf("project %q: each project needs its own id and a name", p.GetName())
		}
		refs[p.GetId()] = true
	}
	for _, id := range wish.GetProjectIds() {
		if !refs[id] {
			return fmt.Errorf("the wish names project %s, which the export does not hold", id)
		}
	}
	ofWish := func(what, id, wishID string) error {
		if !strings.EqualFold(wishID, wish.GetId()) {
			return fmt.Errorf("%s %s belongs to another wish", what, id)
		}
		return nil
	}
	tasks, codes := map[string]bool{}, map[string]bool{}
	for _, t := range exp.GetTasks() {
		if err := errors.Join(valid("task", t, t.GetId()), ofWish("task", t.GetId(), t.GetWishId())); err != nil {
			return err
		}
		if t.GetId() == "" || t.GetCode() == "" || codes[strings.ToUpper(t.GetCode())] {
			return fmt.Errorf("task %s: each task needs an id and a code of its own", t.GetCode())
		}
		if p := t.GetProjectId(); p != "" && !refs[p] {
			return fmt.Errorf("task %s works in project %s, which the export does not hold", t.GetCode(), p)
		}
		tasks[t.GetId()], codes[strings.ToUpper(t.GetCode())] = true, true
	}
	seqs := map[string]bool{}
	for _, e := range exp.GetEvents() {
		if err := valid("event", e, e.GetId()); err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%d", e.GetTaskId(), e.GetSeq())
		if e.GetId() == "" || !tasks[e.GetTaskId()] || e.GetSeq() < 1 || seqs[key] {
			return fmt.Errorf("event %s: it needs an id, a task of the export and a position of its own", e.GetId())
		}
		seqs[key] = true
	}
	clear(codes)
	for _, q := range exp.GetQuestions() {
		if err := errors.Join(valid("question", q, q.GetId()), ofWish("question", q.GetId(), q.GetWishId())); err != nil {
			return err
		}
		code := strings.ToUpper(q.GetCode())
		if q.GetId() == "" || !questionCode.MatchString(code) || codes[code] {
			return fmt.Errorf("question %q: it needs an id and a code like Q03 of its own", q.GetCode())
		}
		codes[code] = true
	}
	for _, b := range exp.GetBlocks() {
		if err := errors.Join(valid("block", b, b.GetId()), ofWish("block", b.GetId(), b.GetWishId())); err != nil {
			return err
		}
		if b.GetId() == "" || b.GetTaskId() != "" && !tasks[b.GetTaskId()] {
			return fmt.Errorf("block %s: it needs an id, and its task must be in the export", b.GetId())
		}
	}
	return checkTilasms(exp, tasks, valid)
}

// free refuses an export whose identifiers are already taken here by something else than its wish.
func free(ctx context.Context, tx *store.Tx, exp *planv1.WishExport) error {
	check := func(what, id string, get func() error) error {
		err := get()
		if err == nil {
			return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%s %s is already here, in another wish", what, id))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	var errs []error
	for _, t := range exp.GetTasks() {
		errs = append(errs, check("task", t.GetId(), func() error { _, err := store.Get[*planv1.Task](ctx, tx, t.GetId()); return err }))
	}
	for _, e := range exp.GetEvents() {
		errs = append(errs, check("event", e.GetId(), func() error { _, err := store.Get[*planv1.TaskEvent](ctx, tx, e.GetId()); return err }))
	}
	for _, q := range exp.GetQuestions() {
		errs = append(errs, check("question", q.GetId(), func() error { _, err := store.Get[*planv1.Question](ctx, tx, q.GetId()); return err }))
	}
	for _, b := range exp.GetBlocks() {
		errs = append(errs, check("block", b.GetId(), func() error { _, err := store.Get[*planv1.Block](ctx, tx, b.GetId()); return err }))
	}
	for _, t := range exp.GetTilasms() {
		id := t.GetTilasm().GetId()
		errs = append(errs, check("tilasm", id, func() error { _, err := store.Get[*planv1.Tilasm](ctx, tx, id); return err }))
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// forget deletes a wish and everything it holds, before an import replaces it; the caller moves its tilasms' folders.
// Its projects stay.
func forget(ctx context.Context, tx *store.Tx, wishID string) error {
	where := store.Where{"wish_id": wishID}
	tasks, err := store.List[*planv1.Task](ctx, tx, where)
	if err != nil {
		return err
	}
	var all []proto.Message
	for _, t := range tasks {
		events, err := store.List[*planv1.TaskEvent](ctx, tx, store.Where{"task_id": t.GetId()})
		if err != nil {
			return err
		}
		for _, e := range events {
			all = append(all, e)
		}
		all = append(all, t)
	}
	questions, err := store.List[*planv1.Question](ctx, tx, where)
	if err != nil {
		return err
	}
	for _, q := range questions {
		all = append(all, q)
	}
	blocks, err := store.List[*planv1.Block](ctx, tx, where)
	if err != nil {
		return err
	}
	for _, b := range blocks {
		all = append(all, b)
	}
	tilasms, err := store.List[*planv1.Tilasm](ctx, tx, where)
	if err != nil {
		return err
	}
	for _, t := range tilasms {
		all = append(all, t)
	}
	all = append(all, &planv1.Wish{Id: wishID})
	for _, m := range all {
		if err := tx.Delete(m); err != nil {
			return err
		}
	}
	return nil
}

// match finds the project of this machine a reference names: by its remote, then by its name, case ignored. When
// there is none, it adds the project without a folder, for djinn project add <folder> to attach.
func match(ctx context.Context, tx *store.Tx, ref *planv1.ProjectRef) (*planv1.ProjectMatch, error) {
	all, err := store.List[*planv1.Project](ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	var byName *planv1.Project
	for _, p := range all {
		if sameRemote(p.GetRemote(), ref.GetRemote()) {
			return &planv1.ProjectMatch{Project: p, Match: planv1.ProjectMatchKind_PROJECT_MATCH_KIND_REMOTE, Note: attach(p)}, nil
		}
		if strings.EqualFold(p.GetName(), ref.GetName()) {
			byName = p
		}
	}
	if byName != nil {
		note := attach(byName)
		if byName.GetRemote() != "" && ref.GetRemote() != "" {
			note = strings.TrimSpace(fmt.Sprintf("Same name, other remote: %s here, %s in the file; check it is the same project. %s",
				byName.GetRemote(), ref.GetRemote(), note))
		}
		return &planv1.ProjectMatch{Project: byName, Match: planv1.ProjectMatchKind_PROJECT_MATCH_KIND_NAME, Note: note}, nil
	}
	p := &planv1.Project{
		Id: store.NewID(), Name: ref.GetName(), Remote: cleanRemote(ref.GetRemote()), Git: ref.GetGit(),
		CreateTime: timestamppb.Now(),
	}
	if err := tx.Put(p); err != nil {
		return nil, err
	}
	return &planv1.ProjectMatch{Project: p, Match: planv1.ProjectMatchKind_PROJECT_MATCH_KIND_NEW, Note: attach(p)}, nil
}

// attach says how to give a folder to a project that has none.
func attach(p *planv1.Project) string {
	if p.GetDirectory() != "" {
		return ""
	}
	how := "djinn project add <folder>"
	if p.GetRemote() != "" {
		how = fmt.Sprintf("git clone %s, then djinn project add <folder>", p.GetRemote())
	}
	return fmt.Sprintf("Not on this machine yet: %s attaches it.", how)
}
