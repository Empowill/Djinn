package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// The Model Context Protocol, served on stdio by `djinn mcp`: newline-delimited JSON-RPC 2.0, with only the tools
// capability. Each tool is a public unary method, named and checked as the command line does. See
// docs/cli-convention.md.

// mcpVersions are the protocol versions the server speaks, newest first. Tools have not changed between them.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations mcpAnnotations `json:"annotations"`
	method      protoreflect.MethodDescriptor
}

// mcpAnnotations tell the client what a tool changes, so that it may run a read without asking. Both are said:
// MCP takes a tool that says nothing for one that may delete.
type mcpAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

// tools are the public unary methods as MCP tools, in the order of the command line. A stream does not fit a tool
// call, which answers once: the streaming methods stay on the command line. A method that only reads is a read-only
// tool; one that deletes, a destructive one.
func tools() []mcpTool {
	var out []mcpTool
	for _, sd := range commands() {
		for _, md := range public(sd) {
			if md.IsStreamingClient() || md.IsStreamingServer() {
				continue
			}
			out = append(out, mcpTool{
				Name:        toolName(md),
				Description: comment(md),
				InputSchema: inputSchema(md.Input()),
				Annotations: mcpAnnotations{
					ReadOnlyHint:    readOnly(md),
					DestructiveHint: writes(md) == djinnv1.Writes_WRITES_DELETE,
				},
				method: md,
			})
		}
	}
	return out
}

// toolName is the command line of a method in snake_case: djinn wish set-lead is wish_set_lead.
func toolName(md protoreflect.MethodDescriptor) string {
	name := command(md.Parent().(protoreflect.ServiceDescriptor)) + "_" + kebab(string(md.Name()))
	return strings.ReplaceAll(name, "-", "_")
}

// inputSchema is the JSON Schema of the arguments of a tool: one property per field of the request, by its proto
// name, taking what the command line takes.
func inputSchema(md protoreflect.MessageDescriptor) map[string]any {
	props, needed := map[string]any{}, []string{}
	for _, fd := range byNumber(md) {
		props[string(fd.Name())] = fieldSchema(fd)
		if required(fd) {
			needed = append(needed, string(fd.Name()))
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": needed, "additionalProperties": false}
}

func fieldSchema(fd protoreflect.FieldDescriptor) map[string]any {
	desc := comment(fd)
	if want := expect(fd); want != "" {
		desc += " Expects " + want + "."
	}
	if onMachine(fd) {
		desc += " A relative path starts from the folder djinn mcp runs in."
	}
	s := valueSchema(fd)
	if fd.IsList() {
		s = map[string]any{"type": "array", "items": s}
	}
	s["description"] = desc
	return s
}

// valueSchema is the schema of one value of fd. Patterns stay in the description: protovalidate reads RE2, JSON
// Schema ECMAScript.
func valueSchema(fd protoreflect.FieldDescriptor) map[string]any {
	switch {
	case ref(fd) != nil, isPair(fd):
		return map[string]any{"type": "string"}
	case fd.Kind() == protoreflect.MessageKind:
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch fd.Kind() {
	case protoreflect.EnumKind:
		var names []string
		for _, ev := range values(fd.Enum()) {
			names = append(names, short(ev))
		}
		return map[string]any{"type": "string", "enum": names}
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}
	case protoreflect.StringKind:
		if rules(fd).GetString().GetUuid() {
			return map[string]any{"type": "string", "format": "uuid"}
		}
		return map[string]any{"type": "string"}
	}
	return map[string]any{"type": "integer"}
}

// request builds the request of md from the arguments of a tool call, each value read as the command line reads
// its text.
func request(md protoreflect.MessageDescriptor, args map[string]json.RawMessage) (*dynamicpb.Message, error) {
	msg := dynamicpb.NewMessage(md)
	for name := range args {
		if md.Fields().ByName(protoreflect.Name(name)) == nil {
			return nil, fmt.Errorf("unknown argument %q", name)
		}
	}
	for _, fd := range byNumber(md) {
		raw, ok := args[string(fd.Name())]
		if !ok {
			continue
		}
		// A list takes an array, or a single value.
		items := []json.RawMessage{raw}
		var list []json.RawMessage
		if fd.IsList() && json.Unmarshal(raw, &list) == nil {
			items = list
		}
		for _, item := range items {
			s, null, err := argument(item)
			if err == nil && !null {
				err = set(msg, fd, s)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fd.Name(), err)
			}
		}
	}
	return msg, nil
}

// argument is a JSON value as the text the command line would read. A null is no value.
func argument(raw json.RawMessage) (s string, null bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", false, err
	}
	switch v := v.(type) {
	case nil:
		return "", true, nil
	case string:
		return v, false, nil
	case json.Number:
		return v.String(), false, nil
	case bool:
		return strconv.FormatBool(v), false, nil
	}
	return "", false, fmt.Errorf("%s is not a string, a number or a boolean", raw)
}

// serveMCP serves the tools on cfg.Stdin and cfg.Stdout until the input ends. Requests run side by side: a long
// call does not hold the others.
func serveMCP(ctx context.Context, cfg Config) error {
	all := tools()
	s := &mcpServer{cfg: cfg, tools: all, out: json.NewEncoder(cfg.Stdout), running: map[string]context.CancelFunc{}}
	s.out.SetEscapeHTML(false)
	in := bufio.NewReader(cfg.Stdin)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		line, err := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr != nil {
				s.reply(json.RawMessage("null"), nil, &rpcError{rpcParseError, jerr.Error()})
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.handle(ctx, msg)
				}()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type mcpServer struct {
	cfg   Config
	tools []mcpTool
	mu    sync.Mutex // guards out and running
	out   *json.Encoder
	// running cancels the calls in flight, by request id, for notifications/cancelled.
	running map[string]context.CancelFunc
}

func (s *mcpServer) reply(id json.RawMessage, result any, rerr *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.out.Encode(rpcMessage{JSONRPC: "2.0", ID: id, Result: result, Error: rerr})
}

func (s *mcpServer) handle(ctx context.Context, msg rpcMessage) {
	if msg.ID == nil {
		// A notification: nothing to answer. Only a cancellation asks for something.
		if msg.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(msg.Params, &p) == nil {
				s.mu.Lock()
				if cancel := s.running[string(p.RequestID)]; cancel != nil {
					cancel()
				}
				s.mu.Unlock()
			}
		}
		return
	}
	if msg.JSONRPC != "2.0" || msg.Method == "" {
		s.reply(msg.ID, nil, &rpcError{rpcInvalidRequest, "not a JSON-RPC 2.0 request"})
		return
	}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		s.reply(msg.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "djinn", "version": s.cfg.Version},
			"instructions": "Each tool is a command of the djinn command line: djinn wish set-lead is wish_set_lead, " +
				"with the same arguments by their proto names and the same checks.",
		}, nil)
	case "ping":
		s.reply(msg.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(msg.ID, map[string]any{"tools": s.tools}, nil)
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			s.reply(msg.ID, nil, &rpcError{rpcInvalidParams, err.Error()})
			return
		}
		i := slices.IndexFunc(s.tools, func(t mcpTool) bool { return t.Name == p.Name })
		if i < 0 {
			s.reply(msg.ID, nil, &rpcError{rpcInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)})
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		key := string(msg.ID)
		s.mu.Lock()
		s.running[key] = cancel
		s.mu.Unlock()
		text, err := s.call(ctx, s.tools[i].method, p.Arguments)
		s.mu.Lock()
		delete(s.running, key)
		s.mu.Unlock()
		cancel()
		if err != nil {
			text = err.Error()
		}
		s.reply(msg.ID, mcpCallResult{Content: []mcpContent{{Type: "text", Text: text}}, IsError: err != nil}, nil)
	default:
		s.reply(msg.ID, nil, &rpcError{rpcMethodNotFound, "unknown method " + msg.Method})
	}
}

// call checks the arguments as the command line does, calls the method and returns its response as JSON.
func (s *mcpServer) call(ctx context.Context, md protoreflect.MethodDescriptor, args map[string]json.RawMessage) (string, error) {
	req, err := request(md.Input(), args)
	if err == nil {
		err = check(req, func(fd protoreflect.FieldDescriptor) string { return string(fd.Name()) })
	}
	if err != nil {
		return "", err
	}
	cfg, err := autostart(ctx, s.cfg, md)
	if err != nil {
		return "", err
	}
	res, err := call(ctx, cfg, md, req)
	if err != nil {
		return "", err
	}
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(res)
	if err != nil {
		return "", err
	}
	// protojson varies its own spacing on purpose: indent it here, so the same answer reads the same.
	var out bytes.Buffer
	err = json.Indent(&out, b, "", "  ")
	return out.String(), err
}
