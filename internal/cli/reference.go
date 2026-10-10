package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// Command is one command of djinn, as its --help and the documentation's Command line tab show it: a public method
// of the protos, read by the convention, or a command written by hand in cmd/djinn (Builtins).
type Command struct {
	Name    string   // the words after djinn: "wish make", "gate run", "up"
	Usage   string   // the usage line, after "Usage: "
	Summary string   // one sentence, for the lists of commands
	Help    string   // what it does: paragraphs apart by a blank line
	Args    []Param  // the positional arguments, in order
	Flags   []Param  // the flags
	Tags    []string // short facts: "reads only", "streams", "MCP tool wish_make"…
}

// Param is an argument or a flag of a command.
type Param struct {
	Name     string   // <question> when positional, --kind when a flag
	Type     string   // string, bool, int32, enum, time, folder, file, path, pair…
	Values   []string // the values an enum takes
	Expect   string   // what it accepts beyond its type: "a UUID", "a match of ^Q[0-9]{2,3}$"
	Default  string   // the value it takes when not given, when that is worth saying
	Env      string   // the environment variable it defaults to
	Repeated bool     // a flag given once per value
	Help     string
}

// Group is a command and its subcommands: a service, or djinn's own commands.
type Group struct {
	Name     string   // the command word: "wish"; "djinn" for djinn's own commands
	Aliases  []string // other words for it: talisman
	Help     string
	Commands []Command
}

// Own is the name of the group of djinn's own commands, those that are no service.
const Own = "djinn"

// Reference is the whole command line, for its documentation: djinn's own commands first, then one group per
// service, by name, each with its methods then the commands written by hand in it (djinn gate run). It reads the
// same command tree as Run, and the same Builtins as the --help of the commands written by hand.
func Reference() []Group {
	groups := []Group{{
		Name: Own,
		Help: "Run Djinn itself: its window and server, its updates and backups, and the commands every other one " +
			"shares.",
	}}
	for _, b := range Builtins {
		if group(b) == Own {
			groups[0].Commands = append(groups[0].Commands, b)
		}
	}
	for _, sd := range commands() {
		g := Group{Name: command(sd), Aliases: aliases(sd), Help: comment(sd)}
		for _, md := range public(sd) {
			g.Commands = append(g.Commands, methodCommand(md))
		}
		for _, b := range Builtins {
			if group(b) == g.Name {
				g.Commands = append(g.Commands, b)
			}
		}
		groups = append(groups, g)
	}
	return groups
}

// group is the group of a command written by hand: the service its first word names, else djinn's own.
func group(b Command) string {
	first, _, nested := strings.Cut(b.Name, " ")
	if nested && slices.ContainsFunc(commands(), func(sd protoreflect.ServiceDescriptor) bool { return command(sd) == first }) {
		return first
	}
	return Own
}

// methodCommand is the command of a public method, by the convention.
func methodCommand(md protoreflect.MethodDescriptor) Command {
	in := md.Input()
	c := Command{
		Name:    command(md.Parent().(protoreflect.ServiceDescriptor)) + " " + kebab(string(md.Name())),
		Summary: comment(md),
		Help:    comment(md),
	}
	c.Usage = "djinn " + c.Name
	for _, fd := range positionals(in) {
		c.Usage += " " + label(fd)
		c.Args = append(c.Args, param(fd))
	}
	for _, fd := range byNumber(in) {
		if !required(fd) {
			c.Flags = append(c.Flags, param(fd))
		}
	}
	if len(c.Flags) > 0 {
		c.Usage += " [flags]"
	}
	switch {
	case md.IsStreamingServer() || md.IsStreamingClient():
		c.Tags = append(c.Tags, "streams")
	case readOnly(md):
		c.Tags = append(c.Tags, "reads only")
	case writes(md) == djinnv1.Writes_WRITES_DELETE:
		c.Tags = append(c.Tags, "may delete")
	default:
		c.Tags = append(c.Tags, "changes")
	}
	if proto.GetExtension(md.Options(), djinnv1.E_Autostart) == true {
		c.Tags = append(c.Tags, "starts djinn up when none runs")
	}
	if !md.IsStreamingServer() && !md.IsStreamingClient() {
		c.Tags = append(c.Tags, "MCP tool "+toolName(md))
	}
	return c
}

// param is the argument or the flag of a field of a request.
func param(fd protoreflect.FieldDescriptor) Param {
	p := Param{Name: label(fd), Type: kindName(fd), Expect: expect(fd), Env: envName(fd), Repeated: fd.IsList(), Help: comment(fd)}
	if fd.Kind() == protoreflect.EnumKind {
		for _, ev := range values(fd.Enum()) {
			p.Values = append(p.Values, short(ev))
		}
		p.Expect = ""
	}
	return p
}

// kindName is the type of a field, as the command line reads it.
func kindName(fd protoreflect.FieldDescriptor) string {
	switch {
	case ref(fd) != nil:
		return "string"
	case isPair(fd):
		return "pair"
	case fd.Kind() == protoreflect.MessageKind:
		return "time"
	case onMachine(fd):
		switch name := string(fd.Name()); {
		case name == "directory" || strings.HasSuffix(name, "_directory"):
			return "folder"
		case name == "file" || strings.HasSuffix(name, "_file"):
			return "file"
		}
		return "path"
	}
	return fd.Kind().String()
}

// WriteHelp writes what --help prints for c.
func (c Command) WriteHelp(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s\n\n%s\n", c.Usage, c.Help)
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if len(c.Args) > 0 {
		fmt.Fprintln(tw, "\nArguments:")
	}
	for _, p := range c.Args {
		fmt.Fprintf(tw, "  %s\t%s\n", p.Name, p.help())
	}
	if len(c.Flags) > 0 {
		fmt.Fprintln(tw, "\nFlags:")
	}
	for _, p := range c.Flags {
		name := p.Name
		if p.Type != "bool" || p.Repeated {
			name += " value"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, p.help())
	}
	tw.Flush()
}

// help is the line --help gives p: its help, then what it expects, and whether it repeats.
func (p Param) help() string {
	help := p.Help
	switch {
	case len(p.Values) > 0:
		help += " Expects one of " + strings.Join(p.Values, ", ") + "."
	case p.Expect != "":
		help += " Expects " + p.Expect + "."
	}
	if p.Repeated {
		help += " Repeatable."
	}
	if p.Default != "" {
		help += " Default: " + p.Default + "."
	}
	return help
}

// GlobalFlags are the flags every generated command takes, anywhere before "--".
var GlobalFlags = []Param{
	{Name: "--json", Type: "bool", Help: "Print the result as JSON."},
	{
		Name: "--addr", Type: "string", Env: "DJINN_ADDR",
		Help: "Address of the djinn server: unix:///path/to/djinn.sock, or http://127.0.0.1:PORT/?token=… " +
			"(default $DJINN_ADDR, then the address djinn up writes in the data directory).",
	},
	{Name: "-h, --help", Type: "bool", Help: "Show help."},
}
