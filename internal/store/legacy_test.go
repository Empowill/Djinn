package store

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestGetMigratesLegacyWishProvider(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.Wish{}, &planv1.Task{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	wishID := NewID()
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, wishID)
	payload = protowire.AppendTag(payload, 2, protowire.BytesType)
	payload = protowire.AppendString(payload, "Old provider")
	// Before the merge, Wish.provider was the varint field 11. Main owns 11
	// for the durable template, so this must be moved to merged field 20.
	payload = protowire.AppendTag(payload, 11, protowire.VarintType)
	payload = protowire.AppendVarint(payload, uint64(planv1.Provider_PROVIDER_CODEX))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO wish (id, payload) VALUES (?, ?)`, wishID, payload); err != nil {
		t.Fatal(err)
	}

	wish, err := Get[*planv1.Wish](ctx, s, wishID)
	if err != nil {
		t.Fatal(err)
	}
	if wish.GetProvider() != planv1.Provider_PROVIDER_CODEX || wish.GetTemplate() != nil {
		t.Fatalf("migrated wish = %v", wish)
	}
	canonical, err := proto.Marshal(wish)
	if err != nil {
		t.Fatal(err)
	}
	if hasWireField(canonical, 11, protowire.VarintType) || !hasWireField(canonical, 20, protowire.VarintType) {
		t.Fatalf("canonical wish payload did not move provider: %x", canonical)
	}
}

func TestGetMigratesLegacyWishWithoutDroppingMainFields(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.Wish{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	wishID := NewID()
	template, err := proto.Marshal(&planv1.WishTemplate{Skill: "release"})
	if err != nil {
		t.Fatal(err)
	}
	integration, err := proto.Marshal(&planv1.IntegrationBranch{ProjectId: NewID(), Branch: "feat/release"})
	if err != nil {
		t.Fatal(err)
	}
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, wishID)
	payload = protowire.AppendTag(payload, 2, protowire.BytesType)
	payload = protowire.AppendString(payload, "Mixed")
	// Legacy provider and main fields deliberately coexist in one payload.
	payload = protowire.AppendTag(payload, 11, protowire.BytesType)
	payload = protowire.AppendBytes(payload, template)
	payload = protowire.AppendTag(payload, 11, protowire.VarintType)
	payload = protowire.AppendVarint(payload, uint64(planv1.Provider_PROVIDER_CODEX))
	payload = protowire.AppendTag(payload, 12, protowire.BytesType)
	payload = protowire.AppendBytes(payload, integration)
	payload = protowire.AppendTag(payload, 13, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 7)
	// Main owns field 14 as the scalar push-after-tasks setting. Keep a valid
	// canonical value alongside the old branch lead-exit bytes below.
	payload = protowire.AppendTag(payload, 14, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 8)
	payload = protowire.AppendTag(payload, 14, protowire.BytesType)
	payload = protowire.AppendString(payload, "W1")
	payload = protowire.AppendTag(payload, 15, protowire.BytesType)
	payload = protowire.AppendString(payload, "Description survives")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO wish (id, payload) VALUES (?, ?)`, wishID, payload); err != nil {
		t.Fatal(err)
	}

	wish, err := Get[*planv1.Wish](ctx, s, wishID)
	if err != nil {
		t.Fatal(err)
	}
	if wish.GetProvider() != planv1.Provider_PROVIDER_CODEX || wish.GetTemplate().GetSkill() != "release" ||
		len(wish.GetIntegrationBranches()) != 1 || wish.GetIntegrationBranches()[0].GetBranch() != "feat/release" ||
		wish.GetPushAfterMinutes() != 7 || wish.GetPushAfterTasks() != 8 ||
		wish.GetDescription() != "Description survives" {
		t.Fatalf("mixed migrated wish = %v", wish)
	}
}

func TestGetMigratesLegacyTaskMachineWait(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.Task{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	taskID := NewID()
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, taskID)
	payload = protowire.AppendTag(payload, 27, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 1)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO task (id, payload) VALUES (?, ?)`, taskID, payload); err != nil {
		t.Fatal(err)
	}

	task, err := Get[*planv1.Task](ctx, s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.GetMachineWait() || task.GetResumeAfter() != nil {
		t.Fatalf("migrated task = %v", task)
	}
}

func hasWireField(payload []byte, want protowire.Number, typ protowire.Type) bool {
	for len(payload) > 0 {
		num, got, n := protowire.ConsumeField(payload)
		if n < 0 {
			return false
		}
		if num == want && got == typ {
			return true
		}
		payload = payload[n:]
	}
	return false
}
