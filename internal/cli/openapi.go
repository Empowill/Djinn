package cli

import (
	"bytes"
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// OpenAPI describes the public unary methods as an OpenAPI 3.1 document, in JSON: each one is a POST of the Connect
// protocol, whose body is the request in protobuf JSON. `go tool task gen` writes it to docs/openapi.json.
func OpenAPI() ([]byte, error) {
	schemas := map[string]any{}
	paths := map[string]any{}
	var tags []any
	var streams []string
	for _, sd := range commands() {
		tags = append(tags, map[string]any{"name": string(sd.Name()), "description": comment(sd)})
		for _, md := range public(sd) {
			path := "/" + string(sd.FullName()) + "/" + string(md.Name())
			if md.IsStreamingClient() || md.IsStreamingServer() {
				streams = append(streams, "`"+path+"`")
				continue
			}
			addSchema(schemas, md.Input())
			addSchema(schemas, md.Output())
			op := map[string]any{
				"operationId": string(sd.Name()) + "_" + string(md.Name()),
				"summary":     "djinn " + command(sd) + " " + kebab(string(md.Name())),
				"description": comment(md),
				"tags":        []string{string(sd.Name())},
				"requestBody": map[string]any{
					"required": true,
					"content":  map[string]any{"application/json": map[string]any{"schema": schemaRef(md.Input())}},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "The response.",
						"content":     map[string]any{"application/json": map[string]any{"schema": schemaRef(md.Output())}},
					},
					"4XX": map[string]any{"$ref": "#/components/responses/Error"},
					"5XX": map[string]any{"$ref": "#/components/responses/Error"},
				},
			}
			if readOnly(md) {
				op["x-read-only"] = true
			}
			paths[path] = map[string]any{"post": op}
		}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Djinn",
			"version": "1",
			"license": map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
			"description": "The public methods of Djinn, generated from the protos in api/ (go tool task gen). " +
				"They are the commands of the djinn command line and the tools of djinn mcp.\n\n" +
				"Each method is a POST of the [Connect protocol](https://connectrpc.com/docs/protocol): " +
				"`Content-Type: application/json`, the request in protobuf JSON as the body. " +
				"djinn up writes its address in `server.addr` of the data directory: on macOS and Linux a Unix socket, " +
				"`curl --unix-socket <data>/djinn.sock http://localhost/plan.v1.ProjectService/List " +
				"-H 'Content-Type: application/json' -d '{}'`; on Windows or with `djinn up --browser`, " +
				"`http://127.0.0.1:PORT/?token=…`, whose token goes in an `Authorization: Bearer` header.\n\n" +
				"A method marked `x-read-only` changes nothing. Connect serves it as a GET too, the request in the query: " +
				"`GET /plan.v1.ProjectService/List?encoding=json&message=%7B%7D`.\n\n" +
				"The streaming methods use Connect's streaming protocol and are not described here: " +
				strings.Join(streams, ", ") + ".",
		},
		"servers": []any{map[string]any{
			"url":         "http://localhost",
			"description": "The address in server.addr: the Unix socket, or the loopback port.",
		}},
		"security": []any{map[string]any{}, map[string]any{"bearer": []string{}}},
		"tags":     tags,
		"paths":    paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{"bearer": map[string]any{
				"type": "http", "scheme": "bearer",
				"description": "The token of an http address in server.addr. The Unix socket needs none.",
			}},
			"responses": map[string]any{"Error": map[string]any{
				"description": "A Connect error.",
				"content":     map[string]any{"application/json": map[string]any{"schema": connectError}},
			}},
			"schemas": schemas,
		},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(doc)
	return b.Bytes(), err
}

var connectError = map[string]any{
	"type":     "object",
	"required": []string{"code"},
	"properties": map[string]any{
		"code":    map[string]any{"type": "string", "description": "The Connect code, like not_found or invalid_argument."},
		"message": map[string]any{"type": "string"},
		"details": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
	},
}

func schemaRef(d protoreflect.Descriptor) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + string(d.FullName())}
}

// wellKnown are the types protobuf JSON writes as a scalar or in a shape of their own.
var wellKnown = map[protoreflect.FullName]map[string]any{
	timestampName: {"type": "string", "format": "date-time"},
	"google.protobuf.Any": {
		"type":                 "object",
		"description":          "A message of any type, named by @type.",
		"properties":           map[string]any{"@type": map[string]any{"type": "string"}},
		"additionalProperties": true,
	},
}

// addSchema adds the schema of md, and of every message and enum it reaches, to schemas.
func addSchema(schemas map[string]any, md protoreflect.MessageDescriptor) {
	name := string(md.FullName())
	if _, ok := schemas[name]; ok {
		return
	}
	if s, ok := wellKnown[md.FullName()]; ok {
		schemas[name] = s
		return
	}
	props := map[string]any{}
	needed := []string{}
	s := map[string]any{"type": "object", "properties": props}
	schemas[name] = s // before the fields: a message may reach itself
	if c := comment(md); c != "" {
		s["description"] = c
	}
	for _, fd := range byNumber(md) {
		props[fd.JSONName()] = protoField(schemas, fd)
		if required(fd) {
			needed = append(needed, fd.JSONName())
		}
	}
	if len(needed) > 0 {
		s["required"] = needed
	}
	for i := range md.Oneofs().Len() {
		od := md.Oneofs().Get(i)
		if od.IsSynthetic() {
			continue
		}
		var names []string
		for j := range od.Fields().Len() {
			names = append(names, od.Fields().Get(j).JSONName())
		}
		s["description"] = strings.TrimSpace(comment(md) + " Set one of: " + strings.Join(names, ", ") + ".")
	}
}

func protoField(schemas map[string]any, fd protoreflect.FieldDescriptor) map[string]any {
	var s map[string]any
	switch {
	case fd.IsMap():
		s = map[string]any{"type": "object", "additionalProperties": protoValue(schemas, fd.MapValue())}
	case fd.IsList():
		s = map[string]any{"type": "array", "items": protoValue(schemas, fd)}
		if r := rules(fd).GetRepeated(); r != nil {
			if r.HasMinItems() {
				s["minItems"] = r.GetMinItems()
			}
			if r.HasMaxItems() {
				s["maxItems"] = r.GetMaxItems()
			}
		}
	default:
		s = protoValue(schemas, fd)
	}
	if c := comment(fd); c != "" {
		if _, ref := s["$ref"]; ref {
			// A $ref keeps its siblings in OpenAPI 3.1; the description is the field's.
			s = map[string]any{"$ref": s["$ref"], "description": c}
		} else {
			s["description"] = c
		}
	}
	return s
}

// protoValue is the schema of one value of fd in protobuf JSON.
func protoValue(schemas map[string]any, fd protoreflect.FieldDescriptor) map[string]any {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		addSchema(schemas, fd.Message())
		return schemaRef(fd.Message())
	case protoreflect.EnumKind:
		name := string(fd.Enum().FullName())
		if _, ok := schemas[name]; !ok {
			var values []string
			for i := range fd.Enum().Values().Len() {
				values = append(values, string(fd.Enum().Values().Get(i).Name()))
			}
			e := map[string]any{"type": "string", "enum": values}
			if c := comment(fd.Enum()); c != "" {
				e["description"] = c
			}
			schemas[name] = e
		}
		return schemaRef(fd.Enum())
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.StringKind:
		s := map[string]any{"type": "string"}
		r := rules(fd).GetString()
		if r.GetUuid() {
			s["format"] = "uuid"
		}
		if r.HasPattern() {
			s["pattern"] = r.GetPattern()
		}
		if r.HasMinLen() {
			s["minLength"] = r.GetMinLen()
		}
		if r.HasMaxLen() {
			s["maxLength"] = r.GetMaxLen()
		}
		return s
	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		// Protobuf JSON writes 64-bit integers as strings, and reads either.
		return map[string]any{"type": []string{"string", "integer"}, "format": "int64"}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return map[string]any{"type": []string{"string", "integer"}, "format": "uint64"}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]any{"type": "integer", "format": "uint32", "minimum": 0}
	}
	return map[string]any{"type": "integer", "format": "int32"}
}
