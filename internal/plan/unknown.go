package plan

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var warnedUnknownFields sync.Map // key: filepath.Clean(path) + ":" + field

// WarnUnknownField formats a warning for an unknown field, logs it once per file and field, and returns the warning string.
func WarnUnknownField(path, field string) string {
	warning := fmt.Sprintf("unknown field %s: a newer Djinn knows it", field)
	key := filepath.Clean(path) + ":" + field
	if _, loaded := warnedUnknownFields.LoadOrStore(key, true); !loaded {
		log.Printf("djinn: %s: %s", path, warning)
	}
	return warning
}

// ResetWarnedUnknownFields clears the deduplication map, for tests.
func ResetWarnedUnknownFields() {
	warnedUnknownFields = sync.Map{}
}

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokNumber
	tokSymbol
)

type token struct {
	text string
	kind tokenKind
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isSymbol(c byte) bool {
	return c == ':' || c == '{' || c == '}' || c == '<' || c == '>' || c == '[' || c == ']' || c == ',' || c == ';'
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}

func tokenizeTextProto(b []byte) []token {
	var tokens []token
	n := len(b)
	i := 0
	for i < n {
		c := b[i]
		if isWhitespace(c) {
			i++
			continue
		}
		if c == '#' {
			for i < n && b[i] != '\n' {
				i++
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote := c
			start := i
			i++
			for i < n {
				if b[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if b[i] == quote {
					i++
					break
				}
				i++
			}
			tokens = append(tokens, token{text: string(b[start:i]), kind: tokString})
			continue
		}
		if isSymbol(c) {
			tokens = append(tokens, token{text: string(c), kind: tokSymbol})
			i++
			continue
		}
		if isIdentStart(c) {
			start := i
			for i < n && isIdentChar(b[i]) {
				i++
			}
			tokens = append(tokens, token{text: string(b[start:i]), kind: tokIdent})
			continue
		}
		start := i
		for i < n && !isWhitespace(b[i]) && b[i] != '#' && !isSymbol(b[i]) {
			i++
		}
		tokens = append(tokens, token{text: string(b[start:i]), kind: tokNumber})
	}
	return tokens
}

// FindUnknownFields returns all unknown field names in text protobuf b when unmarshaled into m.
func FindUnknownFields(b []byte, m proto.Message) []string {
	if m == nil {
		return nil
	}
	testMsg := m.ProtoReflect().New().Interface()
	strictErr := prototext.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(b, testMsg)
	if strictErr == nil {
		return nil
	}
	if err := (prototext.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(b, testMsg)); err != nil {
		// Syntax error or invalid proto data: let standard unmarshaler return the error.
		return nil
	}

	tokens := tokenizeTextProto(b)
	md := m.ProtoReflect().Descriptor()
	stack := []protoreflect.MessageDescriptor{md}
	var fieldStack []protoreflect.FieldDescriptor

	var unknowns []string
	addUnknown := func(name string) {
		if !slices.Contains(unknowns, name) {
			unknowns = append(unknowns, name)
		}
	}

	skipBlock := func(open, closing string, idx int) int {
		depth := 1
		for idx < len(tokens) && depth > 0 {
			switch tokens[idx].text {
			case open:
				depth++
			case closing:
				depth--
			}
			idx++
		}
		return idx
	}

	i := 0
	for i < len(tokens) {
		tok := tokens[i]
		if tok.text == "," || tok.text == ";" {
			i++
			continue
		}
		if tok.text == "}" || tok.text == ">" {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			if len(fieldStack) > 0 {
				fieldStack = fieldStack[:len(fieldStack)-1]
			}
			i++
			continue
		}
		if tok.text == "]" {
			if len(fieldStack) > 0 {
				fieldStack = fieldStack[:len(fieldStack)-1]
			}
			i++
			continue
		}
		if tok.text == "{" || tok.text == "<" {
			if len(fieldStack) > 0 && fieldStack[len(fieldStack)-1].Message() != nil {
				stack = append(stack, fieldStack[len(fieldStack)-1].Message())
			}
			i++
			continue
		}
		if tok.kind != tokIdent {
			i++
			continue
		}

		fieldName := tok.text
		i++
		curMD := stack[len(stack)-1]
		fd := curMD.Fields().ByTextName(fieldName)
		if fd == nil {
			fd = curMD.Fields().ByName(protoreflect.Name(fieldName))
		}
		if fd == nil {
			fd = curMD.Fields().ByJSONName(fieldName)
		}

		if fd == nil {
			addUnknown(fieldName)
			if i < len(tokens) && tokens[i].text == ":" {
				i++
			}
			if i < len(tokens) {
				switch tokens[i].text {
				case "{":
					i = skipBlock("{", "}", i+1)
				case "<":
					i = skipBlock("<", ">", i+1)
				case "[":
					i = skipBlock("[", "]", i+1)
				default:
					i++
				}
			}
			continue
		}

		if i < len(tokens) && tokens[i].text == ":" {
			i++
		}
		if i < len(tokens) {
			switch tokens[i].text {
			case "{", "<":
				if fd.Message() != nil {
					stack = append(stack, fd.Message())
					fieldStack = append(fieldStack, fd)
				}
				i++
			case "[":
				if fd.Message() != nil {
					fieldStack = append(fieldStack, fd)
				}
				i++
			default:
				i++
			}
		}
	}

	if len(unknowns) == 0 {
		re := regexp.MustCompile(`unknown field:?\s*["']?([A-Za-z0-9_.]+)`)
		if match := re.FindStringSubmatch(strictErr.Error()); len(match) > 1 {
			addUnknown(match[1])
		}
	}

	return unknowns
}
