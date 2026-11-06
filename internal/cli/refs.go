package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// The command line takes a project by its name, a wish by its title and a task by its code where a request wants an
// identifier: it looks them up on the server before it sends the request (resolve). A wish is --wish-id or --wish,
// whichever the request spells. A refusal that carries a djinn.v1.Retry prints the command again with each value
// that fits (retried); the command line's own refusals carry one too (refusal).

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	taskCodePattern = regexp.MustCompile(`^[WwTt][0-9]{1,4}$`)
)

// scopeHelp is the help of --wish-id on a command whose request has no wish: the command line's own flag.
const scopeHelp = "Wish the task belongs to, when given by code and the code is in several wishes: the command line " +
	"looks the task up in it."

// wishExpect is what a field naming a wish accepts (wishRef).
const wishExpect = "a wish's UUID, its title, or the start of only one title"

// projectField tells whether fd names projects by identifier (project_id, project_ids): the command line takes a
// project's name there too.
func projectField(fd protoreflect.FieldDescriptor) bool {
	name := fd.Name()
	return fd.Kind() == protoreflect.StringKind && (name == "project_id" || name == "project_ids")
}

// taskField tells whether fd is a task the request wants by identifier only (task_id, a UUID): the command line takes
// its code (W12, T07) too. A task that defaults to $DJINN_TASK_ID is the calling worker's own, never typed by code.
func taskField(fd protoreflect.FieldDescriptor) bool {
	return fd.Name() == "task_id" && fd.Kind() == protoreflect.StringKind && !fd.IsList() &&
		rules(fd).GetString().GetUuid() && envName(fd) == ""
}

// wishField is the field of md that names the wish what it acts on belongs to, wish_id or wish, a UUID; nil when it
// has none. A wish named otherwise, such as where task move takes an azima, is not one.
func wishField(md protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	for _, name := range []protoreflect.Name{"wish_id", "wish"} {
		if fd := md.Fields().ByName(name); isWishField(fd) {
			return fd
		}
	}
	return nil
}

// isWishField tells whether fd names a wish by its UUID, as wishField means it.
func isWishField(fd protoreflect.FieldDescriptor) bool {
	return fd != nil && (fd.Name() == "wish_id" || fd.Name() == "wish") && fd.Kind() == protoreflect.StringKind &&
		!fd.IsList() && rules(fd).GetString().GetUuid()
}

// wishRef tells whether fd names wishes by UUID, as wish, wish_id or wish_ids: the command line takes a wish's title
// there too.
func wishRef(fd protoreflect.FieldDescriptor) bool {
	switch fd.Name() {
	case "wish", "wish_id", "wish_ids":
	default:
		return false
	}
	if fd.Kind() != protoreflect.StringKind || fd.IsMap() {
		return false
	}
	if fd.IsList() {
		return rules(fd).GetRepeated().GetItems().GetString().GetUuid()
	}
	return rules(fd).GetString().GetUuid()
}

// scoped tells whether the command of md takes --wish-id of its own: its request has a task the command line looks
// up by code, and no wish to look it up in.
func scoped(md protoreflect.MessageDescriptor) bool {
	return wishField(md) == nil && slices.ContainsFunc(byNumber(md), taskField)
}

// wishAlias is the other spelling of a wish's flag: a request names it wish_id or wish, the lead types either.
func wishAlias(name string) string {
	switch name {
	case "wish-id":
		return "wish"
	case "wish":
		return "wish-id"
	}
	return ""
}

// line is a command line read: its request, and the wish its own --wish-id gives (scoped).
type line struct {
	req  *dynamicpb.Message
	wish string
}

// resolve turns, in req, a project's name, a wish's title and a task's code into their identifiers, asking the server
// of cfg; wish is the wish of the command line's own --wish-id, a UUID or a title. It calls the server only when a
// value needs it: the wishes first, a task by code is looked up in its wish.
func resolve(ctx context.Context, cfg Config, req *dynamicpb.Message, wish string) error {
	md := req.Descriptor()
	var titles []wishTitle
	titled := func(fd protoreflect.FieldDescriptor, flag, s string) (string, error) {
		if s == "" || uuidPattern.MatchString(s) {
			return s, nil
		}
		if titles == nil {
			var err error
			if titles, err = wishTitles(ctx, cfg); err != nil {
				return "", fmt.Errorf("%s %s: read the wishes to find it: %w", flag, s, err)
			}
		}
		return wishTitled(titles, fd, flag, s)
	}
	for _, fd := range byNumber(md) {
		if !wishRef(fd) {
			continue
		}
		if !fd.IsList() {
			id, err := titled(fd, label(fd), req.Get(fd).String())
			if err != nil {
				return err
			}
			if id != "" {
				req.Set(fd, protoreflect.ValueOfString(id))
			}
			continue
		}
		if !req.Has(fd) {
			continue
		}
		list := req.Mutable(fd).List()
		for i := range list.Len() {
			id, err := titled(fd, label(fd), list.Get(i).String())
			if err != nil {
				return err
			}
			list.Set(i, protoreflect.ValueOfString(id))
		}
	}
	if wish != "" {
		id, err := titled(nil, "--wish-id", wish)
		if err != nil {
			return err
		}
		wish = id
	}
	if fd := wishField(md); fd != nil && wish == "" && uuidPattern.MatchString(req.Get(fd).String()) {
		wish = req.Get(fd).String()
	}
	var projects map[string]string
	var names []string
	for _, fd := range byNumber(md) {
		switch {
		case projectField(fd):
			values := []protoreflect.Value{req.Get(fd)}
			if fd.IsList() {
				list := req.Get(fd).List()
				values = values[:0]
				for i := range list.Len() {
					values = append(values, list.Get(i))
				}
			}
			for i, v := range values {
				s := v.String()
				if s == "" || uuidPattern.MatchString(s) {
					continue
				}
				if projects == nil {
					var err error
					if projects, names, err = projectNames(ctx, cfg); err != nil {
						return fmt.Errorf("%s %s: read the projects to find it: %w", label(fd), s, err)
					}
				}
				id, err := projectNamed(projects, names, s)
				if err != nil {
					return fmt.Errorf("%s: %w", label(fd), err)
				}
				if fd.IsList() {
					req.Mutable(fd).List().Set(i, protoreflect.ValueOfString(id))
				} else {
					req.Set(fd, protoreflect.ValueOfString(id))
				}
			}
		case taskField(fd) && taskCodePattern.MatchString(req.Get(fd).String()):
			id, err := taskCoded(ctx, cfg, req.Get(fd).String(), wish)
			if err != nil {
				return err
			}
			req.Set(fd, protoreflect.ValueOfString(id))
		}
	}
	return nil
}

// wishTitle is a wish as resolve looks it up by title.
type wishTitle struct {
	id, title string
	granted   bool
}

// wishTitles are the wishes of the server of cfg, in the server's order.
func wishTitles(ctx context.Context, cfg Config) ([]wishTitle, error) {
	res, err := callMethod(ctx, cfg, "plan.v1.WishService.List", nil)
	if err != nil {
		return nil, err
	}
	var out []wishTitle
	list := res.Get(res.Descriptor().Fields().ByName("wishes")).List()
	for i := range list.Len() {
		w := list.Get(i).Message()
		fields := w.Descriptor().Fields()
		state := w.Get(fields.ByName("state")).Enum()
		out = append(out, wishTitle{
			id:      w.Get(fields.ByName("id")).String(),
			title:   w.Get(fields.ByName("title")).String(),
			granted: fields.ByName("state").Enum().Values().ByNumber(state).Name() == "WISH_STATE_GRANTED",
		})
	}
	return out, nil
}

// wishTitled is the identifier of the wish titled s, case and outer spaces ignored, else of the only one whose title
// starts so; among several, the only one not granted. Several left is a refusal that gives each one (refusal), for
// the field fd of the request, or the command line's own --wish-id when fd is nil; none, one that says so.
func wishTitled(wishes []wishTitle, fd protoreflect.FieldDescriptor, flag, s string) (string, error) {
	want := strings.ToLower(strings.TrimSpace(s))
	pick := func(match func(title string) bool) []wishTitle {
		var found, open []wishTitle
		for _, w := range wishes {
			if match(strings.ToLower(strings.TrimSpace(w.title))) {
				found = append(found, w)
				if !w.granted {
					open = append(open, w)
				}
			}
		}
		if len(found) > 1 && len(open) > 0 {
			return open
		}
		return found
	}
	found := pick(func(title string) bool { return title == want })
	if len(found) == 0 {
		found = pick(func(title string) bool { return want != "" && strings.HasPrefix(title, want) })
	}
	switch len(found) {
	case 1:
		return found[0].id, nil
	case 0:
		return "", fmt.Errorf("%s: no wish is titled %q, nor starts so; expected %s (djinn wish list shows them)",
			flag, s, wishExpect)
	}
	field := "wish_id"
	if fd != nil {
		field = string(fd.Name())
	}
	r := &djinnv1.Retry{Field: field}
	for _, w := range found {
		r.Values = append(r.Values, &djinnv1.RetryValue{Value: w.id, Label: w.title})
	}
	return "", &refusal{fmt.Errorf("%s: %d wishes are titled %q or start so: give one by its UUID", flag, len(found), s), r}
}

// refusal is a refusal of the command line itself that says what fits, as a server's djinn.v1.Retry does.
type refusal struct {
	err   error
	retry *djinnv1.Retry
}

func (r *refusal) Error() string { return r.err.Error() }

func (r *refusal) Unwrap() error { return r.err }

// projectNames are the projects of the server of cfg: identifier by lowercase name, and the names in their order.
func projectNames(ctx context.Context, cfg Config) (map[string]string, []string, error) {
	res, err := callMethod(ctx, cfg, "plan.v1.ProjectService.List", nil)
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]string{}
	var names []string
	list := res.Get(res.Descriptor().Fields().ByName("projects")).List()
	for i := range list.Len() {
		p := list.Get(i).Message()
		name := p.Get(p.Descriptor().Fields().ByName("name")).String()
		byName[strings.ToLower(name)] = p.Get(p.Descriptor().Fields().ByName("id")).String()
		names = append(names, name)
	}
	return byName, names, nil
}

// projectNamed is the identifier of the project named name, case ignored, or of the only one whose name starts so.
func projectNamed(projects map[string]string, names []string, name string) (string, error) {
	if id, ok := projects[strings.ToLower(name)]; ok {
		return id, nil
	}
	var found []string
	for _, n := range names {
		if strings.HasPrefix(strings.ToLower(n), strings.ToLower(name)) {
			found = append(found, n)
		}
	}
	if len(found) == 1 {
		return projects[strings.ToLower(found[0])], nil
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no project is named %s: Djinn has no project yet (djinn project add)", name)
	}
	return "", fmt.Errorf("no project is named %s; expected one of %s, or a project's UUID", name, strings.Join(names, ", "))
}

// taskCoded is the identifier of the task with this code, in wish when given: the server's TaskService.Get finds it,
// and refuses a code in several wishes with what fits.
func taskCoded(ctx context.Context, cfg Config, code, wish string) (string, error) {
	res, err := callMethod(ctx, cfg, "plan.v1.TaskService.Get", map[string]string{"task_id": code, "wish_id": wish})
	if err != nil {
		return "", err
	}
	task := res.Get(res.Descriptor().Fields().ByName("task")).Message()
	return task.Get(task.Descriptor().Fields().ByName("id")).String(), nil
}

// callMethod calls the method named so with a request of these string fields, those empty left out.
func callMethod(ctx context.Context, cfg Config, name string, fields map[string]string) (*dynamicpb.Message, error) {
	d, err := files().FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil, err
	}
	md := d.(protoreflect.MethodDescriptor)
	req := dynamicpb.NewMessage(md.Input())
	for k, v := range fields {
		if v != "" {
			req.Set(md.Input().Fields().ByName(protoreflect.Name(k)), protoreflect.ValueOfString(v))
		}
	}
	return call(ctx, cfg, md, req)
}

// retryOf is the djinn.v1.Retry an error of the server, or a refusal of the command line, carries; nil when it has
// none.
func retryOf(err error) *djinnv1.Retry {
	if rf := (*refusal)(nil); errors.As(err, &rf) {
		return rf.retry
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return nil
	}
	for _, d := range cerr.Details() {
		if v, derr := d.Value(); derr == nil {
			if r, ok := v.(*djinnv1.Retry); ok && len(r.GetValues()) > 0 {
				return r
			}
		}
	}
	return nil
}

// retried is err followed by the command run again with each value its Retry offers, ready to copy; err when it
// offers none, or when the request cannot take them. command is the command's words: "task spawn".
func retried(err error, command string, l line) error {
	r := retryOf(err)
	if r == nil {
		return err
	}
	md := l.req.Descriptor()
	fd := md.Fields().ByName(protoreflect.Name(r.GetField()))
	flag := ""
	if fd == nil && (r.GetField() == "wish_id" || r.GetField() == "wish") && scoped(md) {
		flag = "--wish-id" // The command line's own flag.
	} else if fd == nil || fd.IsList() || fd.Kind() != protoreflect.StringKind {
		return err
	}
	var commands []string
	elided := false
	for _, v := range r.GetValues() {
		req := proto.Clone(l.req).(*dynamicpb.Message)
		var args []string
		if flag == "" {
			req.Set(fd, protoreflect.ValueOfString(v.GetValue()))
			args = format(req)
		} else {
			args = append(format(req), flag, v.GetValue())
		}
		words := []string{"djinn", command}
		for _, a := range args {
			q, cut := shellWord(a)
			elided = elided || cut
			words = append(words, q)
		}
		c := strings.Join(words, " ")
		if label := strings.Join(strings.Fields(v.GetLabel()), " "); label != "" {
			c += "  # " + clip(label, 60)
		}
		commands = append(commands, c)
	}
	head := "run it again with what fits:"
	if elided {
		head = "run it again with what fits (… stands for the long text you gave):"
	}
	return fmt.Errorf("%w\n%s\n%s", err, head, strings.Join(commands, "\n"))
}

// mcpRetried is err followed by the field and the values its Retry offers, for an agent's client.
func mcpRetried(err error) error {
	r := retryOf(err)
	if r == nil {
		return err
	}
	var values []string
	for _, v := range r.GetValues() {
		s := v.GetValue()
		if label := strings.Join(strings.Fields(v.GetLabel()), " "); label != "" {
			s += " (" + clip(label, 60) + ")"
		}
		values = append(values, s)
	}
	return fmt.Errorf("%w\ncall it again with %s set to one of: %s", err, r.GetField(), strings.Join(values, "; "))
}

// shellWord is s as a shell reads it back, quoted when it must be; a long or multi-line text is cut to "…", which
// cut says.
func shellWord(s string) (word string, cut bool) {
	if strings.ContainsAny(s, "\n\r") || utf8.RuneCountInString(s) > 200 {
		return "…", true
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+%", r))
	}) < 0 {
		return s, false
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", false
}

// clip is s cut to n runes, an ellipsis ending it when cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
