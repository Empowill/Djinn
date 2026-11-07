package plan

import (
	"context"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// A refusal names, in its text, what makes the call pass: the flag to add and each value that fits it, so that the
// one who typed the command runs it again right. Djinn retries nothing itself.

// WishFlags are the wishes wishIDs as a refusal offers them, in that order: flag and each one's identifier, its title
// beside it, "--wish-id 01a1… (Dew) or --wish-id 01a2… (Docs)". A wish gone is left out.
func WishFlags(ctx context.Context, r store.Reader, flag string, wishIDs []string) string {
	var out []string
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
		out = append(out, flag+" "+id+" ("+wish.GetTitle()+")")
	}
	return OrList(out)
}

// ProjectFlags are the projects projectIDs as a refusal offers them, in that order: --project-id and each one's name,
// which the command line takes for its identifier, "--project-id api or --project-id web". A project gone is left
// out.
func ProjectFlags(ctx context.Context, r store.Reader, projectIDs []string) string {
	var out []string
	for _, id := range projectIDs {
		project, err := store.Get[*planv1.Project](ctx, r, id)
		if err != nil {
			continue
		}
		out = append(out, "--project-id "+project.GetName())
	}
	return OrList(out)
}

// ProjectNames are the names of the projects projectIDs, as a sentence lists them: "djinn, dolmen or terraform".
func ProjectNames(ctx context.Context, r store.Reader, projectIDs []string) string {
	var out []string
	for _, id := range projectIDs {
		if project, err := store.Get[*planv1.Project](ctx, r, id); err == nil {
			out = append(out, project.GetName())
		}
	}
	return OrList(out)
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
