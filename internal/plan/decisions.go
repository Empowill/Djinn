package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// checkIcon refuses an icon that is not one emoji: a subject's 🔒 or 🧱, with its variation selector, its skin tone
// or its joiners, but no letter, digit, space or punctuation. Empty is fine: the window picks one by kind.
func checkIcon(icon string) error {
	if icon == "" || oneEmoji(icon) {
		return nil
	}
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("--icon %q is not one emoji, like 🔒", icon))
}

// oneEmoji tells a single emoji: parts joined by a zero-width joiner, each one pictograph or one flag (two regional
// indicators), with the marks, modifiers and tags that dress it.
func oneEmoji(s string) bool {
	for part := range strings.SplitSeq(s, "‍") {
		bases, flags := 0, 0
		for _, r := range part {
			switch {
			case r >= 0x1F1E6 && r <= 0x1F1FF:
				flags++
			case unicode.Is(unicode.So, r):
				bases++
			case unicode.In(r, unicode.Mn, unicode.Me, unicode.Sk), r >= 0xE0020 && r <= 0xE007F:
			default:
				return false
			}
		}
		if flags%2 != 0 || bases+flags/2 != 1 {
			return false
		}
	}
	return true
}

// Decision resolves the decision a task comes from, in wish wishID: an answered question by its code or its id, or a
// block of kind decision by its id. It returns what the task keeps: the question's code, or the block's id.
func Decision(ctx context.Context, r store.Reader, wishID, ref string) (string, error) {
	if questionCode.MatchString(strings.ToUpper(ref)) {
		found, err := store.List[*planv1.Question](ctx, r, store.Where{"wish_id": wishID, "code": strings.ToUpper(ref)})
		if err != nil {
			return "", err
		}
		if len(found) == 0 {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("--decision %s is not a question of the wish", ref))
		}
		return answered(found[0])
	}
	q, err := store.Get[*planv1.Question](ctx, r, ref)
	switch {
	case err == nil && q.GetWishId() == wishID:
		return answered(q)
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	b, err := store.Get[*planv1.Block](ctx, r, ref)
	switch {
	case errors.Is(err, store.ErrNotFound), err == nil && b.GetWishId() != wishID:
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"--decision %s is neither a question nor a block of the wish", ref))
	case err != nil:
		return "", err
	case !render.IsDecision(b):
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"--decision %s is a block of kind %q, not a decision", ref, b.GetKind()))
	}
	return b.GetId(), nil
}

// answered is the code a task keeps of the question it comes from: a decision only once answered.
func answered(q *planv1.Question) (string, error) {
	if q.GetAnswer() == nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s is not answered yet: a task comes from a decision", q.GetCode()))
	}
	return q.GetCode(), nil
}
