// Package cli turns the public methods of the protos into commands, by convention and at runtime: the command is
// the service then the method, the arguments are the fields of the request, and the help is the proto comments.
// See docs/cli-convention.md.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/empowill/djinn/gen"
	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	"github.com/empowill/djinn/internal/server"
)

// Config is what Run needs from its caller.
type Config struct {
	Version string
	// Addr is the server: unix:///path/to/djinn.sock, or an http URL whose token parameter, if any, is sent as a
	// bearer token. When empty, the address djinn up wrote in Home.
	Addr string
	Home string             // data directory
	HTTP connect.HTTPClient // built from Addr when nil
	// Start starts djinn up in the background and returns its address once it answers, for the methods marked
	// autostart when no server answers. Nil leaves them failing as the others do.
	Start  func(ctx context.Context) (addr string, err error)
	Stdin  io.Reader // djinn mcp reads its requests here
	Stdout io.Writer
	Stderr io.Writer
}

// usageError is an error in the command line itself: exit code 2.
type usageError struct{ error }

// Run runs one command line, without the program name, and returns the exit code: 0 on success, 1 when the
// call fails, 2 when the command line is wrong.
func Run(ctx context.Context, args []string, cfg Config) int {
	err := run(ctx, args, cfg)
	if err == nil {
		return 0
	}
	fmt.Fprintln(cfg.Stderr, "error:", strings.ReplaceAll(err.Error(), "\n", "\n  "))
	if errors.As(err, new(usageError)) {
		return 2
	}
	return 1
}

func run(ctx context.Context, args []string, cfg Config) error {
	// Global flags may come anywhere before "--".
	var rest []string
	asJSON, help := false, false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--":
			rest = append(rest, args[i:]...)
			i = len(args)
		case arg == "--json":
			asJSON = true
		case arg == "-h" || arg == "--help":
			help = true
		case arg == "--addr" && i+1 < len(args):
			i++
			cfg.Addr = args[i]
		case strings.HasPrefix(arg, "--addr="):
			cfg.Addr = strings.TrimPrefix(arg, "--addr=")
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) > 0 && rest[0] == "help" {
		rest, help = rest[1:], true
	}
	if len(rest) == 0 {
		writeHelp(cfg.Stdout)
		return nil
	}
	// djinn mcp serves the public methods to an agent that speaks MCP, on stdin and stdout.
	if rest[0] == "mcp" {
		switch {
		case help:
			fmt.Fprint(cfg.Stdout, mcpHelp)
			return nil
		case len(rest) > 1:
			return usageError{fmt.Errorf("djinn mcp takes no argument, got %q", rest[1])}
		}
		return serveMCP(ctx, cfg)
	}

	services := commands()
	names := []string{"help", "version"}
	for _, sd := range services {
		names = append(names, command(sd))
	}
	name, err := pick(rest[0], names, "command")
	if err != nil {
		var ok bool
		if name, ok = aliased(rest[0], names, services); !ok {
			return err
		}
	}
	if name == "version" {
		fmt.Fprintln(cfg.Stdout, "djinn", cfg.Version)
		return nil
	}
	sd := services[slices.IndexFunc(services, func(sd protoreflect.ServiceDescriptor) bool { return command(sd) == name })]
	if len(rest) == 1 {
		writeServiceHelp(cfg.Stdout, sd)
		return nil
	}
	methods := public(sd)
	names = names[:0]
	for _, m := range methods {
		names = append(names, kebab(string(m.Name())))
	}
	name, err = pick(rest[1], names, command(sd)+" method")
	if err != nil {
		return err
	}
	md := methods[slices.Index(names, name)]
	if help {
		writeMethodHelp(cfg.Stdout, md)
		return nil
	}

	req, err := parse(md.Input(), rest[2:])
	if err == nil {
		err = check(req, label)
	}
	if err != nil {
		return usageError{fmt.Errorf("%w\nrun djinn %s %s --help for the arguments", err, command(sd), name)}
	}
	if cfg, err = autostart(ctx, cfg, md); err != nil {
		return err
	}
	if md.IsStreamingServer() && !md.IsStreamingClient() {
		// A stream prints each message as it comes: a JSON object per line with --json, a blank line between
		// messages otherwise.
		first := true
		return stream(ctx, cfg, md, req, func(res *dynamicpb.Message) error {
			if asJSON {
				b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(res)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cfg.Stdout, string(b))
				return err
			}
			if !first {
				fmt.Fprintln(cfg.Stdout)
			}
			first = false
			writeText(cfg.Stdout, res)
			return nil
		})
	}
	res, err := call(ctx, cfg, md, req)
	if err != nil {
		return err
	}
	if asJSON {
		b, err := protojson.MarshalOptions{Multiline: true, UseProtoNames: true}.Marshal(res)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cfg.Stdout, string(b))
		return err
	}
	writeText(cfg.Stdout, res)
	return nil
}

// autostart gives cfg the address of a running djinn, started if needed, when md is marked autostart and no
// address was given.
func autostart(ctx context.Context, cfg Config, md protoreflect.MethodDescriptor) (Config, error) {
	if cfg.Addr == "" && cfg.Start != nil && proto.GetExtension(md.Options(), djinnv1.E_Autostart) == true {
		var err error
		cfg.Addr, err = running(ctx, cfg)
		return cfg, err
	}
	return cfg, nil
}

// running returns the address of the djinn that answers, after starting one when none does.
func running(ctx context.Context, cfg Config) (string, error) {
	if addr, err := server.ReadAddr(cfg.Home); err == nil && Alive(addr) {
		return addr, nil
	}
	return cfg.Start(ctx)
}

// call sends one unary request over Connect, in binary protobuf.
func call(ctx context.Context, cfg Config, md protoreflect.MethodDescriptor, req *dynamicpb.Message) (*dynamicpb.Message, error) {
	if md.IsStreamingClient() || md.IsStreamingServer() {
		return nil, usageError{errors.New("streaming methods are not available on the command line")}
	}
	client, err := newClient(cfg, md)
	if err != nil {
		return nil, err
	}
	res, err := client.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// stream sends one request to a server-streaming method and hands each message to each, until the server ends
// the stream or ctx ends.
func stream(
	ctx context.Context, cfg Config, md protoreflect.MethodDescriptor, req *dynamicpb.Message,
	each func(*dynamicpb.Message) error,
) error {
	client, err := newClient(cfg, md)
	if err != nil {
		return err
	}
	s, err := client.CallServerStream(ctx, connect.NewRequest(req))
	if err != nil {
		return err
	}
	defer s.Close()
	for s.Receive() {
		if err := each(s.Msg()); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// newClient is a generic Connect client of md on the server of cfg.
func newClient(cfg Config, md protoreflect.MethodDescriptor) (*connect.Client[dynamicpb.Message, dynamicpb.Message], error) {
	addr := cfg.Addr
	if addr == "" {
		var err error
		if addr, err = server.ReadAddr(cfg.Home); err != nil {
			return nil, err
		}
	}
	httpClient, base, err := dial(addr)
	if err != nil {
		return nil, err
	}
	if cfg.HTTP != nil {
		httpClient = cfg.HTTP
	}
	url := base + "/" + string(md.Parent().FullName()) + "/" + string(md.Name())
	return connect.NewClient[dynamicpb.Message, dynamicpb.Message](httpClient, url,
		connect.WithSchema(md),
		connect.WithResponseInitializer(func(_ connect.Spec, msg any) error {
			*msg.(*dynamicpb.Message) = *dynamicpb.NewMessage(md.Output())
			return nil
		}),
	), nil
}

// pick resolves a word to one of names: an exact name, or the only name it is a prefix of.
func pick(word string, names []string, what string) (string, error) {
	if slices.Contains(names, word) {
		return word, nil
	}
	var found []string
	for _, n := range names {
		if strings.HasPrefix(n, word) {
			found = append(found, n)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", usageError{fmt.Errorf("unknown %s %q, expected one of: %s", what, word, strings.Join(names, ", "))}
	}
	return "", usageError{fmt.Errorf("%s %q is ambiguous: %s", what, word, strings.Join(found, ", "))}
}

// aliased resolves a word no command's own name takes to the command of a service it is an alias of, in full or by
// a unique prefix: djinn talisman is djinn tilasm.
func aliased(word string, names []string, services []protoreflect.ServiceDescriptor) (string, bool) {
	if slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, word) }) {
		return "", false
	}
	var found []string
	for _, sd := range services {
		for _, alias := range aliases(sd) {
			if (alias == word || strings.HasPrefix(alias, word)) && !slices.Contains(found, command(sd)) {
				found = append(found, command(sd))
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// aliases are the other names of a service's command.
func aliases(sd protoreflect.ServiceDescriptor) []string {
	out, _ := proto.GetExtension(sd.Options(), djinnv1.E_Alias).([]string)
	return out
}

// files are the protos of api/, read from the embedded descriptors so that they keep their comments.
var files = sync.OnceValue(func() *protoregistry.Files {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(gen.Descriptors, &set); err != nil {
		panic(err)
	}
	local := new(protoregistry.Files)
	for _, fdp := range set.GetFile() {
		fd, err := protodesc.NewFile(fdp, resolver{local})
		if err == nil {
			err = local.RegisterFile(fd)
		}
		if err != nil {
			panic(err)
		}
	}
	return local
})

// resolver finds the imports the embedded descriptors leave out among the ones compiled in.
type resolver struct{ local *protoregistry.Files }

func (r resolver) FindFileByPath(path string) (protoreflect.FileDescriptor, error) {
	if fd, err := r.local.FindFileByPath(path); err == nil {
		return fd, nil
	}
	return protoregistry.GlobalFiles.FindFileByPath(path)
}

func (r resolver) FindDescriptorByName(name protoreflect.FullName) (protoreflect.Descriptor, error) {
	if d, err := r.local.FindDescriptorByName(name); err == nil {
		return d, nil
	}
	return protoregistry.GlobalFiles.FindDescriptorByName(name)
}

// commands are the services that have at least one public method, sorted by command name.
func commands() []protoreflect.ServiceDescriptor {
	var out []protoreflect.ServiceDescriptor
	files().RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			if sd := fd.Services().Get(i); len(public(sd)) > 0 {
				out = append(out, sd)
			}
		}
		return true
	})
	slices.SortFunc(out, func(a, b protoreflect.ServiceDescriptor) int { return strings.Compare(command(a), command(b)) })
	return out
}

// public are the methods of sd marked VISIBILITY_PUBLIC, in declaration order.
func public(sd protoreflect.ServiceDescriptor) []protoreflect.MethodDescriptor {
	var out []protoreflect.MethodDescriptor
	for i := range sd.Methods().Len() {
		md := sd.Methods().Get(i)
		if proto.GetExtension(md.Options(), djinnv1.E_Visibility) == djinnv1.Visibility_VISIBILITY_PUBLIC {
			out = append(out, md)
		}
	}
	return out
}

// readOnly tells whether md only reads: it says option idempotency_level = NO_SIDE_EFFECTS.
func readOnly(md protoreflect.MethodDescriptor) bool {
	opts, _ := md.Options().(*descriptorpb.MethodOptions)
	return opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

// writes is what md changes, when it does not only read.
func writes(md protoreflect.MethodDescriptor) djinnv1.Writes {
	return proto.GetExtension(md.Options(), djinnv1.E_Writes).(djinnv1.Writes)
}

// command is the name of the command of a service: QuestionService is question.
func command(sd protoreflect.ServiceDescriptor) string {
	return kebab(strings.TrimSuffix(string(sd.Name()), "Service"))
}

func comment(d protoreflect.Descriptor) string {
	c := d.ParentFile().SourceLocations().ByDescriptor(d).LeadingComments
	return strings.Join(strings.Fields(c), " ")
}

func writeHelp(w io.Writer) {
	fmt.Fprint(w, "Usage: djinn <command> <method> [arguments] [flags]\n\nCommands:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, sd := range commands() {
		name := command(sd)
		if a := aliases(sd); len(a) > 0 {
			name += ", " + strings.Join(a, ", ")
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, comment(sd))
	}
	fmt.Fprintf(tw, "  version\tPrint the version of djinn.\n")
	fmt.Fprintf(tw, "  mcp\tServe these commands as MCP tools on stdin and stdout, for an agent that speaks MCP.\n")
	fmt.Fprintf(tw, "  update\tRestart the running djinn on the newer one installed at its path (go tool task install).\n")
	tw.Flush()
	fmt.Fprint(w, `
Global flags, anywhere on the line:
  --json        Print the result as JSON.
  --addr URL    Address of the djinn server: unix:///path/to/djinn.sock, or http://127.0.0.1:PORT/?token=…
                (default $DJINN_ADDR, then the address djinn up writes in the data directory).
  -h, --help    Show help.

A unique prefix of a command or a method is enough: djinn q answer.
`)
}

const mcpHelp = `Usage: djinn mcp [--addr URL]

Serve the commands of djinn as MCP tools, on stdin and stdout (the stdio transport of the Model Context
Protocol). Each public method that answers once is a tool: djinn wish set-lead is wish_set_lead. Its arguments
are the fields of the request, by their proto names, read and checked as the command line does. A relative path
starts from the folder djinn mcp runs in. The streaming methods (watch, gate hold) stay on the command line.

Add it to an agent, for example: claude mcp add djinn -- djinn mcp
`

func writeServiceHelp(w io.Writer, sd protoreflect.ServiceDescriptor) {
	fmt.Fprintf(w, "%s\n\nUsage: djinn %s <method> [arguments] [flags]\n\nMethods:\n", comment(sd), command(sd))
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, md := range public(sd) {
		fmt.Fprintf(tw, "  %s\t%s\n", kebab(string(md.Name())), comment(md))
	}
	tw.Flush()
}

func writeMethodHelp(w io.Writer, md protoreflect.MethodDescriptor) {
	in := md.Input()
	pos := positionals(in)
	usage := "djinn " + command(md.Parent().(protoreflect.ServiceDescriptor)) + " " + kebab(string(md.Name()))
	for _, fd := range pos {
		usage += " " + label(fd)
	}
	if len(pos) < in.Fields().Len() {
		usage += " [flags]"
	}
	fmt.Fprintf(w, "Usage: %s\n\n%s\n", usage, comment(md))
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if len(pos) > 0 {
		fmt.Fprintln(tw, "\nArguments:")
	}
	for _, fd := range pos {
		fmt.Fprintf(tw, "  %s\t%s\n", label(fd), fieldHelp(fd))
	}
	if len(pos) < in.Fields().Len() {
		fmt.Fprintln(tw, "\nFlags:")
	}
	for _, fd := range byNumber(in) {
		if required(fd) {
			continue
		}
		name := label(fd)
		if fd.Kind() != protoreflect.BoolKind || fd.IsList() {
			name += " value"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, fieldHelp(fd))
	}
	tw.Flush()
}

func fieldHelp(fd protoreflect.FieldDescriptor) string {
	help := comment(fd)
	if want := expect(fd); want != "" {
		help += " Expects " + want + "."
	}
	if fd.IsList() {
		help += " Repeatable."
	}
	return help
}

// writeText prints a response for a human: one "name: value" line per field set, nested messages indented, and
// the content of the response directly when it holds a single field.
func writeText(w io.Writer, m protoreflect.Message) {
	if fields := m.Descriptor().Fields(); fields.Len() == 1 && fields.Get(0).Kind() == protoreflect.MessageKind {
		fd := fields.Get(0)
		switch {
		case fd.IsList():
			writeList(w, fd, m.Get(fd).List(), "")
			return
		case !fd.IsMap() && fd.Message().FullName() != timestampName:
			m = m.Get(fd).Message()
		}
	}
	writeFields(w, m, "")
}

func writeFields(w io.Writer, m protoreflect.Message, indent string) {
	for _, fd := range byNumber(m.Descriptor()) {
		if !m.Has(fd) || fd.IsMap() {
			continue
		}
		v := m.Get(fd)
		switch {
		case fd.IsList():
			fmt.Fprintf(w, "%s%s:\n", indent, fd.Name())
			writeList(w, fd, v.List(), indent+"  ")
		case fd.Kind() == protoreflect.MessageKind && fd.Message().FullName() != timestampName:
			fmt.Fprintf(w, "%s%s:\n", indent, fd.Name())
			writeFields(w, v.Message(), indent+"  ")
		default:
			fmt.Fprintf(w, "%s%s: %s\n", indent, fd.Name(), text(fd, v))
		}
	}
}

func writeList(w io.Writer, fd protoreflect.FieldDescriptor, list protoreflect.List, indent string) {
	for i := range list.Len() {
		if fd.Kind() != protoreflect.MessageKind || fd.Message().FullName() == timestampName {
			fmt.Fprintf(w, "%s- %s\n", indent, text(fd, list.Get(i)))
			continue
		}
		var b strings.Builder
		writeFields(&b, list.Get(i).Message(), indent+"  ")
		fmt.Fprint(w, indent+"- "+strings.TrimPrefix(b.String(), indent+"  "))
	}
}
