package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Every request finds its wish. A lead hands a request that is not about its wish to djinn wish route: Djinn ranks
// the wishes without a model and proposes where the request goes, as a question on the lead's wish. The answer
// files the request in a wish, or makes a new wish and starts its lead on it.

// What each sign of a request adds to a wish's score.
const (
	routeLink  = 4  // the wish has a project the request links to
	routeName  = 3  // the wish has a project the request names
	routeWord  = 3  // each word the request shares with the wish's title
	routeRef   = 8  // the request and the title name the same merge request or issue
	routeOther = -4 // the title names another one: another piece of work
	routeBlock = 1  // each word shared with the wish's latest blocks
)

const (
	routeBlockMax = 3 // the most the latest blocks add
	routeBlocks   = 5 // how many of the latest blocks are read
	routeShown    = 3 // the least score of a wish proposed
	routeFile     = 6 // the least score for Djinn to recommend filing rather than a new wish
	// the least score to recommend filing rather than a template's new wish: the same piece of work, said again
	routeFileTemplate = routeRef + routeWord
	routeTitleMax     = 80
	routeKindBlock    = "request" // the kind of the block that holds a routed request
)

// links are the URLs of a text: http, https, ssh and git ones, and scp-like Git addresses (git@host:group/repo).
var links = regexp.MustCompile(`(?i)\b(?:https?|ssh|git)://[^\s<>"'()\[\]]+|\b[\w.-]+@[\w.-]+:[\w.-]+/[\w./-]+`)

// refs are the merge requests and issues a text names: !41, #12.
var refs = regexp.MustCompile(`[!#]\d+\b`)

// linkRefs read a merge request or an issue in a link's path, after its repository.
var linkRefs = []struct {
	re   *regexp.Regexp
	sign string
}{
	{regexp.MustCompile(`/(?:-/)?merge_requests/(\d+)`), "!"},
	{regexp.MustCompile(`/(?:-/)?(?:issues|work_items)/(\d+)`), "#"},
	{regexp.MustCompile(`/pull/(\d+)`), "#"},
}

// stopWords say nothing about a wish, in every language Djinn speaks: the route.stop_words of each catalog.
var stopWords = func() map[string]bool {
	out := map[string]bool{}
	for _, lang := range locales.Languages() {
		for _, w := range strings.Fields(locales.T(lang, "route.stop_words", nil)) {
			out[w] = true
		}
	}
	return out
}()

// signs are what a request says, read without a model.
type signs struct {
	words  map[string]bool   // its words, normalized, without stop words
	refs   map[string]bool   // the merge requests and issues it names: !41, #12
	linked []*planv1.Project // the projects it links to, in order
	named  []*planv1.Project // the projects it names, beside those it links to
	short  string            // the request, its links shortened: for a title
}

// readRequest reads text against the projects Djinn knows.
func readRequest(text string, projects []*planv1.Project) signs {
	r := signs{refs: map[string]bool{}}
	has := func(list []*planv1.Project, p *planv1.Project) bool {
		return slices.ContainsFunc(list, func(x *planv1.Project) bool { return x.GetId() == p.GetId() })
	}
	r.short = links.ReplaceAllStringFunc(text, func(link string) string {
		link = strings.TrimRight(link, ".,;:!?")
		key := remoteKey(link)
		ref := ""
		for _, lr := range linkRefs {
			if m := lr.re.FindStringSubmatch(key); m != nil {
				ref = lr.sign + m[1]
				break
			}
		}
		if ref != "" {
			r.refs[ref] = true
		}
		repo, _, _ := strings.Cut(key, "/-/")
		name := repo[strings.LastIndexByte(repo, '/')+1:]
		p := projectOf(key, projects)
		if p != nil {
			name = p.GetName()
			if !has(r.linked, p) {
				r.linked = append(r.linked, p)
			}
		}
		if p == nil && ref == "" {
			return link // A link to no repository Djinn knows reads best as it is.
		}
		return name + ref
	})
	lower := strings.ToLower(text)
	for _, p := range projects {
		// A repository named by its path, without a link: acme/shop.
		if _, path, ok := strings.Cut(remoteKey(p.GetRemote()), "/"); ok && strings.Contains(path, "/") &&
			wordIn(lower, path) && !has(r.linked, p) {
			r.linked = append(r.linked, p)
		}
	}
	for _, p := range projects {
		if name := strings.ToLower(p.GetName()); name != "" && wordIn(lower, name) && !has(r.linked, p) {
			r.named = append(r.named, p)
		}
	}
	for _, ref := range refs.FindAllString(text, -1) {
		r.refs[ref] = true
	}
	r.words = words(r.short)
	return r
}

// projectOf is the project whose remote holds the repository key points to, the closest one when remotes nest.
func projectOf(key string, projects []*planv1.Project) *planv1.Project {
	var found *planv1.Project
	best := 0
	for _, p := range projects {
		pk := remoteKey(p.GetRemote())
		if pk != "" && (key == pk || strings.HasPrefix(key, pk+"/")) && len(pk) > best {
			found, best = p, len(pk)
		}
	}
	return found
}

// wordIn tells whether text holds word, between two characters that are not letters nor digits.
func wordIn(text, word string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if !isWordRune(before) && !isWordRune(after) {
			return true
		}
		i = start + 1
	}
}

func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// words are the words of text that may name a piece of work: three letters or more, lowercase, not a stop word,
// not a number, a plural's s dropped.
func words(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isWordRune(r) }) {
		if utf8.RuneCountInString(w) < 3 || stopWords[w] || strings.Trim(w, "0123456789") == "" {
			continue
		}
		if utf8.RuneCountInString(w) > 4 && strings.HasSuffix(w, "s") {
			w = strings.TrimSuffix(w, "s")
		}
		out[w] = true
	}
	return out
}

// candidate is a wish scored for a request, with the reasons of its score.
type candidate struct {
	wish  *planv1.Wish
	score int
	why   []string
}

// score scores wish for req, from its title, its projects and its latest blocks.
func score(lang string, req signs, wish *planv1.Wish, blocks []*planv1.Block) candidate {
	c := candidate{wish: wish}
	var linked, named []string
	counted := map[string]bool{} // the words of the projects counted: a title that names them says no more
	for _, p := range req.linked {
		if slices.Contains(wish.GetProjectIds(), p.GetId()) {
			c.score += routeLink
			linked = append(linked, p.GetName())
			maps.Copy(counted, words(p.GetName()))
		}
	}
	for _, p := range req.named {
		if slices.Contains(wish.GetProjectIds(), p.GetId()) {
			c.score += routeName
			named = append(named, p.GetName())
			maps.Copy(counted, words(p.GetName()))
		}
	}
	if len(linked) > 0 {
		c.why = append(c.why, locales.T(lang, "route.why_link", map[string]string{"projects": strings.Join(linked, ", ")}))
	}
	if len(named) > 0 {
		c.why = append(c.why, locales.T(lang, "route.why_name", map[string]string{"projects": strings.Join(named, ", ")}))
	}
	title := wish.GetTitle()
	if titleRefs := refs.FindAllString(title, -1); len(titleRefs) > 0 && len(req.refs) > 0 {
		same := slices.DeleteFunc(slices.Clone(titleRefs), func(ref string) bool { return !req.refs[ref] })
		if len(same) > 0 {
			c.score += routeRef
			c.why = append(c.why, locales.T(lang, "route.why_ref", map[string]string{"refs": strings.Join(same, ", ")}))
		} else {
			c.score += routeOther
		}
	}
	titleWords := words(title)
	if shared := slices.DeleteFunc(sharedWords(req.words, titleWords), func(w string) bool { return counted[w] }); len(shared) > 0 {
		c.score += routeWord * len(shared)
		c.why = append(c.why, locales.T(lang, "route.why_words", map[string]string{"words": strings.Join(shared, ", ")}))
	}
	latest := slices.Clone(blocks)
	sortBlocks(latest)
	latest = latest[max(0, len(latest)-routeBlocks):]
	seen := map[string]bool{}
	for _, b := range latest {
		for w := range words(b.GetTitle() + " " + clipText(b.GetContent(), briefBlockText)) {
			if !titleWords[w] {
				seen[w] = true
			}
		}
	}
	if shared := sharedWords(req.words, seen); len(shared) > 0 {
		shared = shared[:min(len(shared), routeBlockMax)]
		c.score += routeBlock * len(shared)
		c.why = append(c.why, locales.T(lang, "route.why_blocks", map[string]string{"words": strings.Join(shared, ", ")}))
	}
	return c
}

// sharedWords are the words of a found in b, sorted.
func sharedWords(a, b map[string]bool) []string {
	var out []string
	for w := range a {
		if b[w] {
			out = append(out, w)
		}
	}
	slices.Sort(out)
	return out
}

// rank scores the wishes a request may go to, the closest first, and keeps those close enough to propose: every
// wish but the one it came to and the granted ones. Between equal scores, an active wish comes first, by rank.
func rank(lang string, req signs, wishes []*planv1.Wish, blocks map[string][]*planv1.Block, from string) []candidate {
	var out []candidate
	for _, w := range wishes {
		if w.GetId() == from || w.GetState() == planv1.WishState_WISH_STATE_GRANTED {
			continue
		}
		if c := score(lang, req, w, blocks[w.GetId()]); c.score >= routeShown {
			out = append(out, c)
		}
	}
	order := func(w *planv1.Wish) int32 {
		if !Active(w) || w.GetRank() <= 0 {
			return MaxActive + 1
		}
		return w.GetRank()
	}
	slices.SortStableFunc(out, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(order(a.wish), order(b.wish)))
	})
	return out
}

// proposeTitle makes a title for a new wish from a request, its links shortened: its first sentence, clipped on a
// word, its first letter capital.
func proposeTitle(short string) string {
	s := strings.TrimSpace(short)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	for _, end := range []string{". ", "? ", "! "} {
		if i := strings.Index(s, end); i > 0 {
			s = s[:i]
		}
	}
	s = strings.Join(strings.Fields(strings.TrimRight(s, ".")), " ")
	if r := []rune(s); len(r) > routeTitleMax {
		cut := string(r[:routeTitleMax])
		if i := strings.LastIndexByte(cut, ' '); i > routeTitleMax/2 {
			cut = cut[:i]
		}
		s = cut + "…"
	}
	if s == "" {
		return "?"
	}
	if first, _, _ := strings.Cut(s, " "); strings.Contains(first, "://") {
		return s
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}

// routeInput is what a route is computed from, read from the store.
type routeInput struct {
	lang     string
	text     string
	from     *planv1.Wish // nil without --wish-id
	wishes   []*planv1.Wish
	projects []*planv1.Project
	blocks   map[string][]*planv1.Block
	title    string   // the title asked for a new wish
	newIDs   []string // the projects asked for a new wish
	home     []string // the projects of a new wish when the request points to none, nor comes from a wish
	// templates are the wish templates of the skills the projects use, each with its project
	templates []*Template
}

// propose computes the route of a request: the wishes close enough to file it in, and a new wish. The recommended
// destination comes first. Three wishes being active, a new wish waits paused, or takes the place of the last
// active one; there are four destinations at most, as a question has four options.
func propose(in routeInput) *planv1.Route {
	req := readRequest(in.text, in.projects)
	route := &planv1.Route{Request: in.text, FromWishId: in.from.GetId()}
	candidates := rank(in.lang, req, in.wishes, in.blocks, in.from.GetId())

	title := in.title
	if title == "" {
		title = proposeTitle(req.short)
	}
	ids := in.newIDs
	if len(ids) == 0 {
		for _, p := range append(slices.Clone(req.linked), req.named...) {
			ids = append(ids, p.GetId())
		}
	}
	if len(ids) == 0 {
		ids = in.from.GetProjectIds()
	}
	if len(ids) == 0 {
		ids = in.home
	}
	why := locales.T(in.lang, "route.why_new_none", nil)
	if len(candidates) > 0 {
		why = locales.T(in.lang, "route.why_new_far", map[string]string{"wish": candidates[0].wish.GetTitle()})
	}
	// The templates of the new wish's projects, in their order: the first one the request matches makes the wish.
	var usable []*Template
	for _, id := range ids {
		for _, t := range in.templates {
			if t.ProjectID == id {
				usable = append(usable, t)
			}
		}
	}
	fileAt := routeFile
	_, template, filled := matchTemplate(usable, in.text)
	if template != nil {
		if in.title == "" {
			title = filled
		}
		why = locales.T(in.lang, "route.why_template", map[string]string{"skill": template.GetSkill()})
		fileAt = routeFileTemplate
	}
	var made []*planv1.RouteOption
	actives := Ranked(in.wishes)
	if len(actives) < MaxActive {
		made = append(made, &planv1.RouteOption{
			Kind: planv1.RouteKind_ROUTE_KIND_NEW, Title: title, ProjectIds: ids, Reason: why, Template: template,
		})
	} else {
		made = append(made, &planv1.RouteOption{
			Kind: planv1.RouteKind_ROUTE_KIND_QUEUE, Title: title, ProjectIds: ids, Template: template,
			Reason: why + "; " + locales.T(in.lang, "route.why_queue", nil),
		})
		// The last active wish by rank makes room, unless the request came to it: the lead works there.
		for _, w := range slices.Backward(actives) {
			if w.GetId() != in.from.GetId() {
				made = append(made, &planv1.RouteOption{
					Kind: planv1.RouteKind_ROUTE_KIND_SWAP, Title: title, ProjectIds: ids, PauseWishId: w.GetId(),
					Template: template,
					Reason:   why + "; " + locales.T(in.lang, "route.why_swap", map[string]string{"wish": w.GetTitle()}),
				})
				break
			}
		}
	}
	var filed []*planv1.RouteOption
	for _, c := range candidates[:min(len(candidates), 4-len(made))] {
		filed = append(filed, &planv1.RouteOption{
			Kind: planv1.RouteKind_ROUTE_KIND_FILE, WishId: c.wish.GetId(), Title: c.wish.GetTitle(),
			Score: int32(c.score), Reason: strings.Join(c.why, ", "),
		})
	}
	if len(candidates) > 0 && candidates[0].score >= fileAt {
		route.Options = append(filed, made...)
	} else {
		route.Options = append(made, filed...)
	}
	return route
}

// routeQuestion is the question that asks the developer where a request goes, on the wish it came to: one option
// per destination, in the route's order, and the first one recommended, so that rubbing the lamp takes it.
func routeQuestion(lang string, route *planv1.Route, names map[string]string) *planv1.Question {
	q := &planv1.Question{
		Id: store.NewID(), WishId: route.GetFromWishId(), CreateTime: timestamppb.Now(), Route: route, Icon: "🧭",
		Text: locales.T(lang, "route.question", map[string]string{"request": clipRunes(oneLine(route.GetRequest()), 300)}),
	}
	var ctx strings.Builder
	ctx.WriteString(locales.T(lang, "route.context", nil) + "\n\n")
	for _, line := range strings.Split(strings.TrimSpace(route.GetRequest()), "\n") {
		ctx.WriteString("> " + line + "\n")
	}
	ctx.WriteString("\n")
	for i, opt := range route.GetOptions() {
		q.Options = append(q.Options, optionText(lang, opt, names))
		fmt.Fprintf(&ctx, "- **%c** · %s\n", 'A'+i, opt.GetReason())
	}
	q.Context = ctx.String()
	if len(route.GetOptions()) > 0 {
		q.Recommendation = "A: " + route.GetOptions()[0].GetReason()
	}
	return q
}

// optionText is how a destination reads on the question; names are the projects' names by identifier.
func optionText(lang string, opt *planv1.RouteOption, names map[string]string) string {
	params := map[string]string{"wish": opt.GetTitle(), "title": opt.GetTitle(), "pause": names[opt.GetPauseWishId()]}
	switch opt.GetKind() {
	case planv1.RouteKind_ROUTE_KIND_FILE:
		return locales.T(lang, "route.file", params)
	case planv1.RouteKind_ROUTE_KIND_QUEUE:
		return locales.T(lang, "route.queue", params) + skillText(lang, opt)
	case planv1.RouteKind_ROUTE_KIND_SWAP:
		return locales.T(lang, "route.swap", params) + skillText(lang, opt)
	}
	var projects []string
	for _, id := range opt.GetProjectIds() {
		projects = append(projects, names[id])
	}
	if len(projects) == 0 {
		return locales.T(lang, "route.new_bare", params) + skillText(lang, opt)
	}
	params["projects"] = strings.Join(projects, ", ")
	return locales.T(lang, "route.new", params) + skillText(lang, opt)
}

// skillText names the skill a new wish is made from, after its option; empty without a template.
func skillText(lang string, opt *planv1.RouteOption) string {
	if t := opt.GetTemplate(); t != nil {
		return locales.T(lang, "route.with_skill", map[string]string{"skill": t.GetSkill()})
	}
	return ""
}

// readRoute reads what a route is computed from into in: the wishes, the projects, the blocks and the templates. It
// returns the names of the projects and the titles of the wishes, by identifier.
func readRoute(ctx context.Context, r store.Reader, in *routeInput) (map[string]string, error) {
	in.blocks = map[string][]*planv1.Block{}
	var err error
	if in.wishes, err = store.List[*planv1.Wish](ctx, r, nil); err != nil {
		return nil, err
	}
	if in.projects, err = store.List[*planv1.Project](ctx, r, nil); err != nil {
		return nil, err
	}
	blocks, err := store.List[*planv1.Block](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	for _, b := range blocks {
		in.blocks[b.GetWishId()] = append(in.blocks[b.GetWishId()], b)
	}
	names := map[string]string{}
	for _, p := range in.projects {
		names[p.GetId()] = p.GetName()
	}
	for _, wish := range in.wishes {
		names[wish.GetId()] = wish.GetTitle()
	}
	if in.templates, err = Templates(ctx, r, in.projects); err != nil {
		return nil, err
	}
	return names, nil
}

// Route proposes where a request goes; with ask, it asks it as a question on the wish the request came to.
func (w *Wishes) Route(
	ctx context.Context, req *connect.Request[planv1.WishServiceRouteRequest],
) (*connect.Response[planv1.WishServiceRouteResponse], error) {
	m := req.Msg
	if m.GetAsk() && m.GetWishId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"--ask asks the question on the wish the request came to: give it with --wish-id"))
	}
	lang := cmp.Or(w.Language, locales.Source)
	compute := func(r store.Reader) (*planv1.Route, map[string]string, error) {
		in := routeInput{lang: lang, text: m.GetRequest(), title: m.GetTitle()}
		var err error
		if id := m.GetWishId(); id != "" {
			if in.from, err = store.Get[*planv1.Wish](ctx, r, id); err != nil {
				return nil, nil, err
			}
		}
		for _, id := range m.GetProjectIds() {
			p, err := store.Get[*planv1.Project](ctx, r, id)
			if err != nil {
				return nil, nil, err
			}
			if !slices.Contains(in.newIDs, p.GetId()) {
				in.newIDs = append(in.newIDs, p.GetId())
			}
		}
		names, err := readRoute(ctx, r, &in)
		if err != nil {
			return nil, nil, err
		}
		return propose(in), names, nil
	}
	res := &planv1.WishServiceRouteResponse{}
	if !m.GetAsk() {
		route, _, err := compute(w.Store)
		if err != nil {
			return nil, Status(err)
		}
		res.Route = route
		return connect.NewResponse(res), nil
	}
	err := write(ctx, w.Store, req.Spec(), m, func(tx *store.Tx) error {
		route, names, err := compute(tx)
		if err != nil {
			return err
		}
		res.Route, res.Question = route, routeQuestion(lang, route, names)
		return Ask(ctx, tx, res.Question)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// settle acts on the answer of a route question, in the transaction that stores the answer: it files the request
// in the chosen wish, or makes the new wish, pausing the one that makes room. Each change is journaled as the
// command that would make it. It records the new wish in the question's option, and returns what follows once the
// answer is stored: tell the lead of the wish, or start the new wish's lead. A question without a route is left.
func (w *Wishes) settle(ctx context.Context, tx *store.Tx, q *planv1.Question) (func(context.Context), error) {
	if q.GetGrant() && q.GetAnswer() != nil {
		return nil, settleGrant(ctx, tx, q)
	}
	route := q.GetRoute()
	if route == nil || q.GetAnswer() == nil {
		return nil, nil
	}
	i := int(q.GetAnswer().GetChoice() - planv1.Choice_CHOICE_A)
	if i < 0 || i >= len(route.GetOptions()) || !routable(route.GetOptions()[i]) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"question %s routes a request: answer with the letter of a destination", q.GetCode()))
	}
	lang := cmp.Or(w.Language, locales.Source)
	from, err := store.Get[*planv1.Wish](ctx, tx, route.GetFromWishId())
	if err != nil {
		return nil, err
	}
	return w.routeTo(ctx, tx, route.GetOptions()[i], route.GetRequest(), origin{
		block:    locales.T(lang, "route.block_title", map[string]string{"wish": from.GetTitle()}),
		first:    FirstLine(from.GetTitle(), route.GetRequest()),
		filed:    func(wishID string) string { return FiledLine(wishID, from.GetTitle(), route.GetRequest()) },
		provider: from.GetLead().GetProvider(),
	})
}

// origin is where a routed request comes from, as the wish it goes to says it: a wish whose lead handed it over, or
// the inbox.
type origin struct {
	block    string                     // the title of the block that holds the request
	first    string                     // the first line of a new wish's lead
	filed    func(wishID string) string // the line that tells the lead of the wish wishID it was filed there
	provider planv1.Provider            // the provider of a new wish's lead; claude when unset
}

// routeTo sends request where opt says, in tx: it files it in the wish, or makes the new wish, pausing the one that
// makes room. Each change is journaled as the command that would make it. It records the new wish in opt, and
// returns what follows once tx is committed: tell the lead of the wish, or start the new wish's watcher and lead.
func (w *Wishes) routeTo(
	ctx context.Context, tx *store.Tx, opt *planv1.RouteOption, request string, from origin,
) (func(context.Context), error) {
	put := func(wishID string) error {
		block := &planv1.BlockServicePutRequest{WishId: wishID, Kind: routeKindBlock, Content: request, Title: from.block}
		if err := tx.Journal(actor, planv1connect.BlockServicePutProcedure, block); err != nil {
			return err
		}
		_, err := putBlock(ctx, tx, block)
		return err
	}
	switch opt.GetKind() {
	case planv1.RouteKind_ROUTE_KIND_FILE:
		wish, err := store.Get[*planv1.Wish](ctx, tx, opt.GetWishId())
		if err != nil {
			return nil, err
		}
		if wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"wish %q was granted since: route the request again", wish.GetTitle()))
		}
		if err := put(wish.GetId()); err != nil {
			return nil, err
		}
		line := from.filed(wish.GetId())
		return func(ctx context.Context) {
			if err := w.Tell(ctx, wish.GetId(), line); err != nil && !errors.Is(err, ErrNoLead) {
				log.Printf("djinn: wish %s: tell the lead about a request filed: %v", wish.GetId(), err)
			}
		}, nil
	case planv1.RouteKind_ROUTE_KIND_NEW, planv1.RouteKind_ROUTE_KIND_QUEUE, planv1.RouteKind_ROUTE_KIND_SWAP:
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no such destination"))
	}
	if opt.GetKind() == planv1.RouteKind_ROUTE_KIND_SWAP {
		pause := &planv1.WishServicePauseRequest{WishId: opt.GetPauseWishId()}
		if err := tx.Journal(actor, planv1connect.WishServicePauseProcedure, pause); err != nil {
			return nil, err
		}
		wish, err := store.Get[*planv1.Wish](ctx, tx, pause.GetWishId())
		if err != nil {
			return nil, err
		}
		if err := pauseWish(ctx, tx, wish); err != nil {
			return nil, err
		}
	}
	makeReq := &planv1.WishServiceMakeRequest{
		Title: opt.GetTitle(), ProjectIds: opt.GetProjectIds(), Paused: opt.GetKind() == planv1.RouteKind_ROUTE_KIND_QUEUE,
	}
	if err := tx.Journal(actor, planv1connect.WishServiceMakeProcedure, makeReq); err != nil {
		return nil, err
	}
	wish, err := makeWish(ctx, tx, makeReq)
	if err != nil {
		return nil, err
	}
	if opt.GetTemplate() != nil {
		wish.Template = opt.GetTemplate()
		if err := tx.Put(wish); err != nil {
			return nil, err
		}
	}
	if err := put(wish.GetId()); err != nil {
		return nil, err
	}
	opt.WishId = wish.GetId()
	if makeReq.GetPaused() {
		// Its watcher waits with it; its lead starts when the wish is resumed.
		return func(ctx context.Context) { w.startTemplate(ctx, wish) }, nil
	}
	return func(ctx context.Context) {
		// The watcher first: the lead's first line says what it runs, or why it did not start.
		w.startLead(ctx, wish.GetId(), from.provider, from.first+w.startTemplate(ctx, wish))
	}, nil
}

// routable tells whether opt is a destination routeTo knows.
func routable(opt *planv1.RouteOption) bool {
	return opt.GetKind() >= planv1.RouteKind_ROUTE_KIND_FILE && opt.GetKind() <= planv1.RouteKind_ROUTE_KIND_SWAP
}

// startLead starts the lead of a wish made for a request: a new session from the wish's brief, whose first line is
// the request, in the wish's own terminal, shown in the window. Without terminals, the wish waits for djinn wish
// resume, the request in its blocks.
func (w *Wishes) startLead(ctx context.Context, wishID string, provider planv1.Provider, first string) {
	if w.Leads == nil {
		return
	}
	err := func() error {
		wish, err := store.Get[*planv1.Wish](ctx, w.Store, wishID)
		if err != nil {
			return err
		}
		dir, err := startFolder(ctx, w.Store, wish)
		if err != nil {
			return err
		}
		line, dir, started, _, err := w.newLead(ctx, wish, provider, dir, first)
		if err != nil {
			return err
		}
		name := LeadTerminal(wishID)
		_, _, attached, err := w.Leads.Open(name, line, dir, started.GetSessionId())
		if err != nil {
			return err
		}
		if !attached && started.GetSessionId() != "" {
			if _, err := w.recordLead(ctx, wishID, started); err != nil {
				return err
			}
		}
		w.Leads.Show(wishID, name)
		return nil
	}()
	if err != nil {
		log.Printf("djinn: wish %s: start its lead: %v", wishID, err)
	}
}

// FirstLine is the first line of the lead of a wish made for a request: the request, and where it comes from.
func FirstLine(from, text string) string {
	return fmt.Sprintf("The developer's request, handed over from the wish %q; this wish was made for it: %s",
		from, strings.TrimSpace(text))
}

// FiledLine is the line that tells the lead of the wish wishID that a request was filed in it, from the wish from.
func FiledLine(wishID, from, text string) string {
	return fmt.Sprintf("Djinn: a request was filed in this wish, from the wish %q: %q. Take it on: djinn wish brief "+
		"%s has it, in its blocks.", from, clipLine(text), wishID)
}

// RoutedLine is the line that tells the lead of a route question's wish where the request went: it hands it over.
func RoutedLine(q *planv1.Question) string {
	opt := routeOption(q)
	where := fmt.Sprintf("the wish %q (%s)", opt.GetTitle(), opt.GetWishId())
	switch opt.GetKind() {
	case planv1.RouteKind_ROUTE_KIND_NEW, planv1.RouteKind_ROUTE_KIND_SWAP:
		where = fmt.Sprintf("the new wish %q (%s), whose lead starts on it", opt.GetTitle(), opt.GetWishId())
	case planv1.RouteKind_ROUTE_KIND_QUEUE:
		where = fmt.Sprintf("the new wish %q (%s), paused until a place frees up", opt.GetTitle(), opt.GetWishId())
	}
	return fmt.Sprintf("Djinn: %s answered: the request went to %s. Hand it over, do not do its work here, and go on "+
		"with this wish.", q.GetCode(), where)
}

// routeOption is the destination the answer of a route question chose; nil when it chose none.
func routeOption(q *planv1.Question) *planv1.RouteOption {
	i := int(q.GetAnswer().GetChoice() - planv1.Choice_CHOICE_A)
	if q.GetAnswer() == nil || i < 0 || i >= len(q.GetRoute().GetOptions()) {
		return nil
	}
	return q.GetRoute().GetOptions()[i]
}
