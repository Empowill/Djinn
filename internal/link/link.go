// Package link reads Djinn's links, djinn://tilasm/<id> and djinn://wish/<id>, which open the app on what they name
// from anywhere: a browser, a chat, a terminal, a Markdown file. The system hands them to `djinn open`, which the
// scheme is registered with: a desktop entry on Linux (tools/icons), Djinn.app's Info.plist on macOS (tools/macapp),
// the registry on Windows (Register).
package link

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Scheme is the scheme of Djinn's links.
const Scheme = "djinn"

// Kind is what a link names.
type Kind string

const (
	// Tilasm names a tilasm: the window shows its wish's Tilasms tab on it.
	Tilasm Kind = "tilasm"
	// Wish names a wish: the window shows it.
	Wish Kind = "wish"
)

// kinds are the hosts a link may have, by what they name: "talisman" is the developer's word for a tilasm.
var kinds = map[string]Kind{"tilasm": Tilasm, "talisman": Tilasm, "wish": Wish}

// Link is a link read: what it names, and its identifier, in lower case.
type Link struct {
	Kind Kind
	ID   string
}

// String is the link as Djinn writes it.
func (l Link) String() string { return Scheme + "://" + string(l.Kind) + "/" + l.ID }

// Of is the link of a tilasm or a wish by its identifier.
func Of(kind Kind, id string) string { return Link{kind, strings.ToLower(id)}.String() }

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// UnknownError is a link Djinn does not know.
type UnknownError struct{ URL string }

func (e UnknownError) Error() string {
	return fmt.Sprintf("%q is not a link Djinn knows: djinn://tilasm/<id> or djinn://wish/<id>", e.URL)
}

// Parse reads a link: djinn://tilasm/<id> or djinn://wish/<id>, the scheme and the kind in any case, a slash after the
// identifier, a query or a fragment allowed (a browser or a chat may add them). Anything else is an UnknownError.
func Parse(raw string) (Link, error) {
	unknown := UnknownError{raw}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, Scheme) || u.Opaque != "" || u.User != nil || u.Port() != "" {
		return Link{}, unknown
	}
	kind, ok := kinds[strings.ToLower(u.Hostname())]
	id := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"))
	if !ok || !uuid.MatchString(id) {
		return Link{}, unknown
	}
	return Link{kind, id}, nil
}
