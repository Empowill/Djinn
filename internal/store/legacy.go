package store

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// unmarshal accepts payloads written by the harness-friendly branch before it was merged with main. Those payloads
// used field 11 of Wish for the provider and field 27 of Task for machine_wait; main uses those numbers for its
// durable template and resume_after fields. Keep the main numbers stable and move the old values to the merged
// fields while the payload is decoded.
func unmarshal(payload []byte, m proto.Message) error {
	payload = migrateLegacyPayload(payload, m.ProtoReflect().Descriptor().FullName())
	return proto.Unmarshal(payload, m)
}

func migrateLegacyPayload(payload []byte, name protoreflect.FullName) []byte {
	switch name {
	case "plan.v1.Wish":
		return migrateLegacyWish(payload)
	case "plan.v1.Task":
		return migrateLegacyTask(payload)
	default:
		return payload
	}
}

type wireField struct {
	num  protowire.Number
	typ  protowire.Type
	full []byte
}

func fields(payload []byte) ([]wireField, bool) {
	var out []wireField
	for len(payload) > 0 {
		num, typ, n := protowire.ConsumeField(payload)
		if n < 0 {
			return nil, false
		}
		out = append(out, wireField{num: num, typ: typ, full: payload[:n]})
		payload = payload[n:]
	}
	return out, true
}

func appendAs(out []byte, f wireField, num protowire.Number) []byte {
	_, _, tag := protowire.ConsumeTag(f.full)
	out = protowire.AppendTag(out, num, f.typ)
	return append(out, f.full[tag:]...)
}

func migrateLegacyWish(payload []byte) []byte {
	fs, ok := fields(payload)
	if !ok {
		return payload
	}
	legacyProvider := false
	legacyRunning := false
	legacyPrompt := false
	legacyExit := false
	for _, f := range fs {
		switch {
		case f.num == 11 && f.typ == protowire.VarintType:
			legacyProvider = true
		case f.num == 12 && f.typ == protowire.VarintType:
			legacyRunning = true
		case f.num == 13 && f.typ == protowire.BytesType:
			value, n := fieldBytes(f)
			legacyPrompt = n >= 0 && isLegacyLeadPrompt(value)
		case f.num == 14 && f.typ == protowire.BytesType:
			value, n := fieldBytes(f)
			legacyExit = n >= 0 && isLegacyLeadExit(value)
		}
	}
	if !legacyProvider && !legacyRunning && !legacyPrompt && !legacyExit {
		return payload
	}

	out := make([]byte, 0, len(payload)+32)
	for _, f := range fs {
		switch {
		case f.num == 11 && f.typ == protowire.VarintType:
			out = appendAs(out, f, 20)
		case f.num == 12 && f.typ == protowire.VarintType:
			out = appendAs(out, f, 21)
		case f.num == 13 && f.typ == protowire.BytesType && legacyPrompt:
			out = appendAs(out, f, 22)
		case f.num == 14 && f.typ == protowire.BytesType && legacyExit:
			out = appendAs(out, f, 23)
		default:
			out = append(out, f.full...)
		}
	}
	return out
}

// MigrateLegacyWish normalizes a Wish payload from the pre-merge wire layout before an export envelope is decoded.
func MigrateLegacyWish(payload []byte) []byte { return migrateLegacyWish(payload) }

func fieldBytes(f wireField) ([]byte, int) {
	_, _, tag := protowire.ConsumeTag(f.full)
	return protowire.ConsumeBytes(f.full[tag:])
}

func isLegacyLeadPrompt(payload []byte) bool {
	var prompt planv1.LeadPrompt
	if err := proto.Unmarshal(payload, &prompt); err != nil {
		return false
	}
	return prompt.GetTitle() != "" || len(prompt.GetLines()) > 0 || len(prompt.GetOptions()) > 0
}

func isLegacyLeadExit(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	for len(payload) > 0 {
		num, typ, n := protowire.ConsumeField(payload)
		if n < 0 || num != 1 || typ != protowire.VarintType {
			return false
		}
		payload = payload[n:]
	}
	return true
}

func migrateLegacyTask(payload []byte) []byte {
	fs, ok := fields(payload)
	if !ok {
		return payload
	}
	legacy := false
	for _, f := range fs {
		if f.num == 27 && f.typ == protowire.VarintType {
			legacy = true
			break
		}
	}
	if !legacy {
		return payload
	}
	out := make([]byte, 0, len(payload)+8)
	for _, f := range fs {
		if f.num == 27 && f.typ == protowire.VarintType {
			out = appendAs(out, f, 47)
			continue
		}
		out = append(out, f.full...)
	}
	return out
}

// MigrateLegacyTask normalizes a Task payload from the pre-merge wire layout before an export envelope is decoded.
func MigrateLegacyTask(payload []byte) []byte { return migrateLegacyTask(payload) }
