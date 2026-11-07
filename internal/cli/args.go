package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	"github.com/empowill/djinn/locales"
)

const (
	timestampName = "google.protobuf.Timestamp"
	durationName  = "google.protobuf.Duration"
)

// parse builds the request md from the arguments of a command line: the required fields are positional, in
// field-number order, and the other fields are --kebab-case flags.
func parse(md protoreflect.MessageDescriptor, args []string) (*dynamicpb.Message, error) {
	l, err := parseLine(md, args)
	return l.req, err
}

// parseLine reads a command line as parse does, the command line's own --wish-id of a scoped request, and its own
// file flags (fileFlags) of a request that takes their text (fileField).
func parseLine(md protoreflect.MessageDescriptor, args []string) (line, error) {
	msg := dynamicpb.NewMessage(md)
	l := line{req: msg}
	pos := positionals(md)
	next, onlyPositional := 0, false
	files := map[string][]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !onlyPositional && arg == "--" {
			onlyPositional = true
			continue
		}
		if onlyPositional || !strings.HasPrefix(arg, "--") {
			for next < len(pos) && msg.Has(pos[next]) {
				next++ // Given by its flag already (an optional positional).
			}
			if next == len(pos) {
				return line{}, fmt.Errorf("unexpected argument %q", arg)
			}
			if err := set(msg, pos[next], arg); err != nil {
				return line{}, fmt.Errorf("%s: %w", label(pos[next]), err)
			}
			next++
			continue
		}
		name, value, hasValue := strings.Cut(arg[2:], "=")
		fd := flag(md, name)
		own := (name == "wish-id" || name == "wish") && scoped(md) || ownFile(md, name) != nil
		if fd == nil && own {
			// The command line's own flags: the wish a task given by code is looked up in (its UUID or its title,
			// resolve reads it), the file a prompt, a context or a plan is read from.
			if !hasValue {
				if i+1 == len(args) {
					return line{}, fmt.Errorf("--%s needs a value", name)
				}
				i++
				value = args[i]
			}
			if ownFile(md, name) != nil {
				files[name] = append(files[name], value)
			} else {
				l.wish = value
			}
			continue
		}
		if fd == nil {
			return line{}, fmt.Errorf("unknown flag --%s", name)
		}
		if fd.Kind() == protoreflect.BoolKind && !fd.IsList() && !hasValue {
			value, hasValue = "true", true
		}
		if !hasValue {
			if i+1 == len(args) {
				return line{}, fmt.Errorf("%s needs a value", label(fd))
			}
			i++
			value = args[i]
		}
		if err := set(msg, fd, value); err != nil {
			return line{}, fmt.Errorf("%s: %w", label(fd), err)
		}
	}
	for _, f := range fileFlags {
		if paths, ok := files[f.flag]; ok {
			if err := readFiles(msg, fileField(md, f), f, paths); err != nil {
				return line{}, err
			}
		}
	}
	return l, nil
}

// fileFlag is a flag of the command line's own, which reads a file into a text field of the request: the server
// never sees the file, only its text, so what Djinn stores depends on no file and no path leaves this machine
// (W621, W627).
type fileFlag struct {
	field, flag, help string
}

// fileFlags are the command line's own file flags.
var fileFlags = []fileFlag{
	{"prompt", "prompt-file", "File holding what to ask the worker, in Markdown, read in place of --prompt by the " +
		"command line; a relative path starts from the current directory."},
	{"context", "context-file", "File holding what is at stake, in Markdown, read in place of --context by the " +
		"command line; a relative path starts from the current directory."},
	{"plan", "plan-file", "File holding the task an option launches, read by the command line and sent after the " +
		"--plan texts, one per option; a relative path starts from the current directory."},
}

// fileField is the field of md the file flag f fills: a string field of its name, singular or repeated (a plan per
// option). nil otherwise.
func fileField(md protoreflect.MessageDescriptor, f fileFlag) protoreflect.FieldDescriptor {
	fd := md.Fields().ByName(protoreflect.Name(f.field))
	if fd == nil || fd.Kind() != protoreflect.StringKind {
		return nil
	}
	return fd
}

// ownFile is the file flag of md named flag, when the command line reads it; nil otherwise.
func ownFile(md protoreflect.MessageDescriptor, flag string) *fileFlag {
	for i, f := range fileFlags {
		if f.flag == flag && fileField(md, f) != nil {
			return &fileFlags[i]
		}
	}
	return nil
}

// readFiles sets the field fd of msg from the files at paths. A singular field takes the last file, its last line
// breaks cut, and is refused beside the field's own flag; a repeated one takes each file whole, after the texts its
// own flag gave.
func readFiles(msg *dynamicpb.Message, fd protoreflect.FieldDescriptor, f fileFlag, paths []string) error {
	if !fd.IsList() && msg.Has(fd) {
		return fmt.Errorf("give --%s or --%s, not both", kebab(f.field), f.flag)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("--%s: %w", f.flag, err)
		}
		if fd.IsList() {
			msg.Mutable(fd).List().Append(protoreflect.ValueOfString(string(data)))
		} else {
			msg.Set(fd, protoreflect.ValueOfString(strings.TrimRight(string(data), "\r\n")))
		}
	}
	return nil
}

// format is the inverse of parse: the arguments that build msg.
func format(msg protoreflect.Message) []string {
	md := msg.Descriptor()
	var args []string
	for _, fd := range positionals(md) {
		if msg.Has(fd) {
			args = append(args, text(fd, msg.Get(fd)))
		}
	}
	for _, fd := range byNumber(md) {
		if positional(fd) || !msg.Has(fd) {
			continue
		}
		name := "--" + kebab(string(fd.Name()))
		switch {
		case fd.IsList():
			list := msg.Get(fd).List()
			for i := range list.Len() {
				args = append(args, name, text(fd, list.Get(i)))
			}
		case fd.Kind() == protoreflect.BoolKind && !msg.Get(fd).Bool():
			args = append(args, name+"=false") // An optional bool set false: --urgent=false.
		case fd.Kind() == protoreflect.BoolKind:
			args = append(args, name)
		default:
			args = append(args, name, text(fd, msg.Get(fd)))
		}
	}
	return args
}

// check validates the request before it is sent, and names each faulty argument with name: label on the command line.
func check(msg proto.Message, name func(protoreflect.FieldDescriptor) string) error {
	err := protovalidate.Validate(msg)
	var verr *protovalidate.ValidationError
	if !errors.As(err, &verr) {
		return err
	}
	md := msg.ProtoReflect().Descriptor()
	lines := make([]string, 0, len(verr.Violations))
	for _, v := range verr.Violations {
		line := v.Proto.GetMessage()
		if path := v.Proto.GetField().GetElements(); len(path) > 0 {
			if fd := md.Fields().ByNumber(protoreflect.FieldNumber(path[0].GetFieldNumber())); fd != nil {
				line = name(fd) + ": " + line
				if want := expect(fd); want != "" {
					line += "; expected " + want
				}
			}
		}
		lines = append(lines, line)
	}
	return errors.New(strings.Join(lines, "\n"))
}

// positionals are required singular fields and fields marked positional, in field-number order.
func positionals(md protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
	var out []protoreflect.FieldDescriptor
	for _, fd := range byNumber(md) {
		if positional(fd) {
			out = append(out, fd)
		}
	}
	return out
}

func byNumber(md protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
	out := make([]protoreflect.FieldDescriptor, md.Fields().Len())
	for i := range out {
		out[i] = md.Fields().Get(i)
	}
	slices.SortFunc(out, func(a, b protoreflect.FieldDescriptor) int { return int(a.Number() - b.Number()) })
	return out
}

func required(fd protoreflect.FieldDescriptor) bool {
	return !fd.IsList() && !fd.IsMap() && rules(fd).GetRequired()
}

// positional preserves an argument's spelling without requiring its value in the API.
func positional(fd protoreflect.FieldDescriptor) bool {
	return required(fd) || (!fd.IsList() && !fd.IsMap() && proto.GetExtension(fd.Options(), djinnv1.E_Positional) == true)
}

func rules(fd protoreflect.FieldDescriptor) *validate.FieldRules {
	r, _ := proto.GetExtension(fd.Options(), validate.E_Field).(*validate.FieldRules)
	return r
}

// flag is the field of md the flag --name sets: a field not positional, or one only marked so (an optional
// positional, which answers to its flag too); a wish's flag answers to its other spelling too (wishAlias), when it
// names the wish by UUID (isWishField).
func flag(md protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {
	for _, fd := range byNumber(md) {
		if !required(fd) && kebab(string(fd.Name())) == name {
			return fd
		}
	}
	if alias := wishAlias(name); alias != "" {
		for _, fd := range byNumber(md) {
			if !positional(fd) && kebab(string(fd.Name())) == alias && isWishField(fd) {
				return fd
			}
		}
	}
	return nil
}

// label is how the command line names a field: <name> when positional, --name otherwise.
func label(fd protoreflect.FieldDescriptor) string {
	if positional(fd) {
		return "<" + kebab(string(fd.Name())) + ">"
	}
	return "--" + kebab(string(fd.Name()))
}

// supported says whether the command line can fill fd.
func supported(fd protoreflect.FieldDescriptor) error {
	switch {
	case fd.IsMap():
		return errors.New("maps are not supported")
	case fd.Kind() == protoreflect.BytesKind || fd.Kind() == protoreflect.GroupKind:
		return fmt.Errorf("%s fields are not supported", fd.Kind())
	case fd.Kind() == protoreflect.MessageKind && fd.Message().FullName() != timestampName && fd.Message().FullName() != durationName && ref(fd) == nil && !isPair(fd):
		return errors.New("only a Timestamp, a Duration, a message holding a single oneof of scalars, or a string and a list of strings is supported")
	}
	return nil
}

// pair returns the two fields of fd when fd is a message of a required string, then a repeated string, which the
// command line fills from a single input: key=a,b.
func pair(fd protoreflect.FieldDescriptor) (key, list protoreflect.FieldDescriptor) {
	if fd.Kind() != protoreflect.MessageKind || fd.Message().Fields().Len() != 2 || fd.Message().Oneofs().Len() != 0 {
		return nil, nil
	}
	fields := byNumber(fd.Message())
	key, list = fields[0], fields[1]
	if !required(key) || key.Kind() != protoreflect.StringKind || !list.IsList() || list.Kind() != protoreflect.StringKind {
		return nil, nil
	}
	return key, list
}

func isPair(fd protoreflect.FieldDescriptor) bool {
	key, _ := pair(fd)
	return key != nil
}

// ref returns the oneof of fd when fd is a message that holds nothing but a oneof of scalars, which the command
// line fills from a single input.
func ref(fd protoreflect.FieldDescriptor) protoreflect.OneofDescriptor {
	if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.Message().Oneofs().Len() != 1 {
		return nil
	}
	od := fd.Message().Oneofs().Get(0)
	if od.IsSynthetic() || od.Fields().Len() != fd.Message().Fields().Len() {
		return nil
	}
	for i := range od.Fields().Len() {
		if k := od.Fields().Get(i).Kind(); k == protoreflect.MessageKind || k == protoreflect.GroupKind {
			return nil
		}
	}
	return od
}

// set stores one input into the field fd of msg.
func set(msg protoreflect.Message, fd protoreflect.FieldDescriptor, s string) error {
	if err := supported(fd); err != nil {
		return err
	}
	if od := ref(fd); od != nil {
		// The input goes into the first member whose rules accept it.
		var reasons []string
		for i := range od.Fields().Len() {
			member := od.Fields().Get(i)
			sub := msg.NewField(fd).Message()
			v, err := scalar(member, s)
			if err == nil {
				sub.Set(member, v)
				err = protovalidate.Validate(sub.Interface())
			}
			if err == nil {
				msg.Set(fd, protoreflect.ValueOfMessage(sub))
				return nil
			}
			var verr *protovalidate.ValidationError
			if errors.As(err, &verr) && len(verr.Violations) > 0 {
				err = errors.New(verr.Violations[0].Proto.GetMessage())
			}
			reasons = append(reasons, fmt.Sprintf("%s (%v)", member.Name(), err))
		}
		return fmt.Errorf("%q fits none of: %s", s, strings.Join(reasons, ", "))
	}
	if key, list := pair(fd); key != nil {
		k, values, ok := strings.Cut(s, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return fmt.Errorf("%q is not %s", s, expect(fd))
		}
		sub := dynamicpb.NewMessage(fd.Message())
		sub.Set(key, protoreflect.ValueOfString(k))
		for _, v := range strings.Split(values, ",") {
			if v = strings.TrimSpace(v); v != "" {
				sub.Mutable(list).List().Append(protoreflect.ValueOfString(v))
			}
		}
		if fd.IsList() {
			msg.Mutable(fd).List().Append(protoreflect.ValueOfMessage(sub))
		} else {
			msg.Set(fd, protoreflect.ValueOfMessage(sub))
		}
		return nil
	}
	if fd.Kind() == protoreflect.MessageKind {
		if fd.Message().FullName() == durationName {
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("%q is not a duration, like 10m or 1h30m", s)
			}
			dur := msg.NewField(fd).Message()
			dur.Set(dur.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(int64(d/time.Second)))
			dur.Set(dur.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(int32((d%time.Second)/time.Nanosecond)))
			msg.Set(fd, protoreflect.ValueOfMessage(dur))
			return nil
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return fmt.Errorf("%q is not an RFC 3339 time, like 2026-10-07T09:00:00Z", s)
		}
		ts := msg.NewField(fd).Message()
		ts.Set(ts.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(t.Unix()))
		ts.Set(ts.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(int32(t.Nanosecond())))
		msg.Set(fd, protoreflect.ValueOfMessage(ts))
		return nil
	}
	v, err := scalar(fd, s)
	if err != nil {
		return err
	}
	if onMachine(fd) && s != "" {
		// The server runs elsewhere: a relative path means one under the current directory of the caller.
		abs, err := filepath.Abs(s)
		if err != nil {
			return err
		}
		v = protoreflect.ValueOfString(abs)
	}
	if fd.IsList() {
		msg.Mutable(fd).List().Append(v)
		return nil
	}
	msg.Set(fd, v)
	return nil
}

// onMachine tells whether fd holds a path of this machine: a string field named directory, file, path, or ending in
// _directory, _file or _path.
func onMachine(fd protoreflect.FieldDescriptor) bool {
	name := string(fd.Name())
	if fd.Kind() != protoreflect.StringKind {
		return false
	}
	for _, word := range []string{"directory", "file", "path"} {
		if name == word || strings.HasSuffix(name, "_"+word) {
			return true
		}
	}
	return false
}

func scalar(fd protoreflect.FieldDescriptor, s string) (protoreflect.Value, error) {
	bad := func() (protoreflect.Value, error) {
		return protoreflect.Value{}, fmt.Errorf("%q is not a valid %s", s, fd.Kind())
	}
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(s), nil
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.EnumKind:
		for _, ev := range values(fd.Enum()) {
			if strings.EqualFold(s, short(ev)) || strings.EqualFold(s, string(ev.Name())) {
				return protoreflect.ValueOfEnum(ev.Number()), nil
			}
		}
		for _, ev := range values(fd.Enum()) {
			if key, ok := words[short(ev)]; ok && locales.Means(s, key) {
				return protoreflect.ValueOfEnum(ev.Number()), nil
			}
		}
		return protoreflect.Value{}, fmt.Errorf("%q is not %s", s, expect(fd))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfInt32(int32(n)), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfInt64(n), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfUint32(uint32(n)), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfUint64(n), nil
	case protoreflect.FloatKind:
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfFloat32(float32(f)), nil
	case protoreflect.DoubleKind:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return bad()
		}
		return protoreflect.ValueOfFloat64(f), nil
	}
	return protoreflect.Value{}, fmt.Errorf("%s fields are not supported", fd.Kind())
}

// text writes one value as the command line reads it.
func text(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch {
	case ref(fd) != nil:
		sub := v.Message()
		if member := sub.WhichOneof(ref(fd)); member != nil {
			return text(member, sub.Get(member))
		}
		return ""
	case isPair(fd):
		key, list := pair(fd)
		sub := v.Message()
		values := make([]string, sub.Get(list).List().Len())
		for i := range values {
			values[i] = sub.Get(list).List().Get(i).String()
		}
		return sub.Get(key).String() + "=" + strings.Join(values, ",")
	case fd.Kind() == protoreflect.MessageKind:
		if fd.Message().FullName() == durationName {
			dur := v.Message()
			fields := dur.Descriptor().Fields()
			sec, nanos := dur.Get(fields.ByName("seconds")).Int(), dur.Get(fields.ByName("nanos")).Int()
			return (time.Duration(sec)*time.Second + time.Duration(nanos)*time.Nanosecond).String()
		}
		ts := v.Message()
		fields := ts.Descriptor().Fields()
		sec, nanos := ts.Get(fields.ByName("seconds")).Int(), ts.Get(fields.ByName("nanos")).Int()
		return time.Unix(sec, nanos).UTC().Format(time.RFC3339Nano)
	case fd.Kind() == protoreflect.EnumKind:
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return short(ev)
		}
		return strconv.Itoa(int(v.Enum()))
	}
	return fmt.Sprint(v.Interface())
}

// expect says what a field accepts, for help and errors.
func expect(fd protoreflect.FieldDescriptor) string {
	switch {
	case projectField(fd):
		return "a project's name or UUID"
	case taskField(fd):
		return "a task's code (W12, T07) or UUID"
	case wishRef(fd):
		return wishExpect
	}
	if od := ref(fd); od != nil {
		var alts []string
		for i := range od.Fields().Len() {
			alts = append(alts, expect(od.Fields().Get(i)))
		}
		return strings.Join(alts, " or ")
	}
	if key, list := pair(fd); key != nil {
		return fmt.Sprintf("%s=%s,…", kebab(string(key.Name())), kebab(string(list.Name())))
	}
	switch fd.Kind() {
	case protoreflect.EnumKind:
		var names []string
		for _, ev := range values(fd.Enum()) {
			names = append(names, short(ev))
		}
		return "one of " + strings.Join(names, ", ")
	case protoreflect.MessageKind:
		if fd.Message().FullName() == durationName {
			return "a duration, like 10m or 1h30m"
		}
		return "an RFC 3339 time, like 2026-10-07T09:00:00Z"
	case protoreflect.StringKind:
		r := rules(fd).GetString()
		switch {
		case r.GetUuid():
			return "a UUID"
		case r.HasPattern():
			return "a match of " + r.GetPattern()
		}
	}
	return ""
}

// words are the enum values a person may also type in their own language, by their key in locales/: yes and no.
var words = map[string]string{"yes": "answer.yes", "no": "answer.no"}

// values are the values of an enum a user can type: all but the zero one.
func values(ed protoreflect.EnumDescriptor) []protoreflect.EnumValueDescriptor {
	var out []protoreflect.EnumValueDescriptor
	for i := range ed.Values().Len() {
		if ev := ed.Values().Get(i); ev.Number() != 0 {
			out = append(out, ev)
		}
	}
	return out
}

// short is an enum value without the prefix of its type, in lower case: CHOICE_B is b.
func short(ev protoreflect.EnumValueDescriptor) string {
	prefix := strings.ToUpper(split(string(ev.Parent().Name()), "_")) + "_"
	return strings.ToLower(strings.TrimPrefix(string(ev.Name()), prefix))
}

// kebab turns QuestionService, GetEnvironment or wish_id into question-service, get-environment, wish-id.
func kebab(name string) string {
	return strings.ToLower(strings.ReplaceAll(split(name, "-"), "_", "-"))
}

// split puts sep between the words of a CamelCase name.
func split(name, sep string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := name[i-1]
			if prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9' {
				b.WriteString(sep)
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// envName is the environment variable the field defaults to ((djinn.v1.env)); "" when it has none. It may name
// several, separated by spaces: the first one set wins.
func envName(fd protoreflect.FieldDescriptor) string {
	name, _ := proto.GetExtension(fd.Options(), djinnv1.E_Env).(string)
	return name
}

// fromEnv fills each field of msg left empty from the environment variable it defaults to, read with getenv: a
// worker's djinn block put names its task by $DJINN_TASK_ID.
func fromEnv(msg *dynamicpb.Message, getenv func(string) string) {
	for _, fd := range byNumber(msg.Descriptor()) {
		names := strings.Fields(envName(fd))
		if len(names) == 0 || fd.IsList() || fd.Kind() != protoreflect.StringKind || msg.Has(fd) {
			continue
		}
		for _, name := range names {
			if v := getenv(name); v != "" {
				msg.Set(fd, protoreflect.ValueOfString(v))
				break
			}
		}
	}
}
