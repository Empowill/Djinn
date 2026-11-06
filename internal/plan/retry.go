package plan

import (
	"context"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Retry is err refused with code, carrying what the call takes to pass: the request's field and the values that fit
// it (djinn.v1.Retry). The command line prints the command again with each value, ready to run.
func Retry(code connect.Code, err error, field string, values ...*djinnv1.RetryValue) *connect.Error {
	cerr := connect.NewError(code, err)
	if len(values) == 0 {
		return cerr
	}
	if detail, derr := connect.NewErrorDetail(&djinnv1.Retry{Field: field, Values: values}); derr == nil {
		cerr.AddDetail(detail)
	}
	return cerr
}

// WishValues are the wishes wishIDs as values of a Retry, in that order: each one's identifier, labelled by its
// title. A wish gone is left out.
func WishValues(ctx context.Context, r store.Reader, wishIDs []string) []*djinnv1.RetryValue {
	var out []*djinnv1.RetryValue
	seen := map[string]bool{}
	for _, id := range wishIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		wish, err := store.Get[*planv1.Wish](ctx, r, id)
		if err != nil {
			continue
		}
		out = append(out, &djinnv1.RetryValue{Value: id, Label: wish.GetTitle()})
	}
	return out
}

// ProjectValues are the projects projectIDs as values of a Retry, in that order: each one's name, which the command
// line takes for its identifier. A project gone is left out.
func ProjectValues(ctx context.Context, r store.Reader, projectIDs []string) []*djinnv1.RetryValue {
	var out []*djinnv1.RetryValue
	for _, id := range projectIDs {
		project, err := store.Get[*planv1.Project](ctx, r, id)
		if err != nil {
			continue
		}
		out = append(out, &djinnv1.RetryValue{Value: project.GetName()})
	}
	return out
}

// ValueNames are the values of a Retry, as a sentence lists them: "djinn, dolmen or terraform".
func ValueNames(values []*djinnv1.RetryValue) string {
	var names []string
	for _, v := range values {
		names = append(names, v.GetValue())
	}
	return OrList(names)
}

// OrList joins words as a sentence offers a choice: "a", "a or b", "a, b or c".
func OrList(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	out := ""
	for i, w := range words[:len(words)-1] {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out + " or " + words[len(words)-1]
}

// wishesOf are the wishes of items, in their order.
func wishesOf[T interface{ GetWishId() string }](items []T) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.GetWishId())
	}
	return out
}
