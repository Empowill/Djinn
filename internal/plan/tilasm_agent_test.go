package plan

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestAwaitsAgent: a discussion waits for the document agent while it is open and nothing answered the developer's
// last message: neither a message by someone else after it, nor a proposal linked to it made after it, whatever its
// status.
func TestAwaitsAgent(t *testing.T) {
	t.Parallel()
	at := func(s int) *timestamppb.Timestamp { return timestamppb.New(time.Unix(1_700_000_000+int64(s), 0)) }
	const id = "0199e5a0-0000-7000-8000-000000000001"
	msg := func(author string, s int) *planv1.TilasmMessage {
		return &planv1.TilasmMessage{Author: author, Text: "…", CreateTime: at(s)}
	}
	proposal := func(thread string, s int, status planv1.TilasmProposalStatus) *planv1.TilasmProposal {
		return &planv1.TilasmProposal{ThreadId: thread, CreateTime: at(s), Status: status}
	}
	accepted := planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_ACCEPTED
	for name, c := range map[string]struct {
		thread    *planv1.TilasmThread
		proposals []*planv1.TilasmProposal
		want      bool
	}{
		"developer last": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{msg("developer", 1)}}, nil, true},
		"replied": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{
			msg("developer", 1), msg(DocumentAuthor, 2)}}, nil, false},
		"resolved": {&planv1.TilasmThread{Id: id, Resolved: true, Messages: []*planv1.TilasmMessage{msg("developer", 1)}}, nil, false},
		"proposal after": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{msg("developer", 1)}},
			[]*planv1.TilasmProposal{proposal(id, 2, accepted)}, false},
		"pending proposal after": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{msg("developer", 1)}},
			[]*planv1.TilasmProposal{proposal(id, 2, planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING)}, false},
		"proposal before the last message": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{
			msg("developer", 1), msg("developer", 3)}}, []*planv1.TilasmProposal{proposal(id, 2, accepted)}, true},
		"another discussion's proposal": {&planv1.TilasmThread{Id: id, Messages: []*planv1.TilasmMessage{msg("developer", 1)}},
			[]*planv1.TilasmProposal{proposal("0199e5a0-0000-7000-8000-000000000002", 2, accepted)}, true},
	} {
		if got := AwaitsAgent(c.thread, c.proposals); got != c.want {
			t.Errorf("%s: AwaitsAgent = %v, want %v", name, got, c.want)
		}
	}
}
