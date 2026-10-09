package plan

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"connectrpc.com/connect"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/store"
)

// DefaultTilasmBytes is the ceiling of a new tilasm, its files summed: material to read, not a store of videos.
const DefaultTilasmBytes = 50 << 20

// tilasmManifest is the manifest of a tilasm exported alone, beside its files. A put leaves out a file of that name at
// the root: the manifest is Djinn's.
const tilasmManifest = "tilasm.json"

// maxTilasmCode is the last code a wish can give: the codes follow ^L[0-9]{2,3}$.
const maxTilasmCode = 999

// tilasmIndex is the page a tilasm opens on.
const tilasmIndex = "index.html"

var tilasmCode = regexp.MustCompile(`^L[0-9]{2,3}$`)

// defaultAuthor is who makes a tilasm when the request names nobody: the lead, through the command line or MCP.
const defaultAuthor = "lead"

// Tilasms implements TilasmService.
type Tilasms struct {
	planv1connect.UnimplementedTilasmServiceHandler
	Store *store.Store
	// Home is Djinn's data folder: the tilasms' files are in its folder tilasms. Empty: the service is unavailable.
	Home string
	// MaxBytes is the ceiling of a new tilasm, in bytes; 0: DefaultTilasmBytes.
	MaxBytes int64
	// Show shows a tilasm in the window, in its wish's Tilasms tab, and tells whether a native window came to the
	// front. Nil: Open is unavailable.
	Show func(wishID, tilasmID string) bool
	// URL is the local http address of a tilasm's latest version, by its identifier; empty, or nil, while Djinn serves
	// no http.
	URL func(id string) string
}

// TilasmDir is the folder of a tilasm in the data folder home: version n is in its folder v<n>.
func TilasmDir(home, id string) string { return filepath.Join(home, "tilasms", strings.ToLower(id)) }

func versionDir(home, id string, n int32) string {
	return filepath.Join(TilasmDir(home, id), fmt.Sprintf("v%d", n))
}

// tempDir is a new folder for files on their way into the tilasms' folder: on the same file system, so that a rename
// puts them in place at once.
func tempDir(home, prefix string) (string, error) {
	root := filepath.Join(home, "tilasms")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "."+prefix+"-*")
}

func (t *Tilasms) ready() error {
	if t.Home == "" {
		return connect.NewError(connect.CodeUnavailable, errors.New("this server keeps no tilasms: run djinn up"))
	}
	return nil
}

func (t *Tilasms) ceiling(existing *planv1.Tilasm) int64 {
	if n := existing.GetMaxBytes(); n > 0 {
		return n
	}
	return cmp.Or(t.MaxBytes, DefaultTilasmBytes)
}

func (t *Tilasms) Put(
	ctx context.Context, req *connect.Request[planv1.TilasmServicePutRequest],
) (*connect.Response[planv1.TilasmServicePutResponse], error) {
	tilasm, placed, err := t.put(ctx, req.Spec(), req.Msg, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServicePutResponse{Tilasm: tilasm, Directory: placed}), nil
}

// put puts m's folder or .zip, journaling the request journaled under spec. It returns the tilasm and the folder of the
// version put.
func (t *Tilasms) put(
	ctx context.Context, spec connect.Spec, journaled proto.Message, m *planv1.TilasmServicePutRequest,
) (*planv1.Tilasm, string, error) {
	if err := t.ready(); err != nil {
		return nil, "", err
	}
	src := m.GetPath()
	if !filepath.IsAbs(src) {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path: %q is not an absolute path", src))
	}
	var existing *planv1.Tilasm
	if m.GetCode() != "" {
		var err error
		if existing, err = tilasmByCode(ctx, t.Store, m.GetWish(), m.GetCode()); err != nil {
			return nil, "", Status(err)
		}
	}
	files, err := t.stagePath(src, t.ceiling(existing))
	if err != nil {
		return nil, "", err
	}
	defer files.clean()
	var tilasm *planv1.Tilasm
	var placed string
	err = write(ctx, t.Store, spec, journaled, func(tx *store.Tx) error {
		if _, err := store.Get[*planv1.Wish](ctx, tx, m.GetWish()); err != nil {
			return err
		}
		now := timestamppb.Now()
		var err error
		if m.GetCode() != "" {
			if tilasm, err = tilasmByCode(ctx, tx, m.GetWish(), m.GetCode()); err != nil {
				return err
			}
		}
		if tilasm == nil {
			tilasm = &planv1.Tilasm{
				Id: store.NewID(), WishId: m.GetWish(), Code: strings.ToUpper(m.GetCode()), CreateTime: now,
				Author: cmp.Or(m.GetAuthor(), defaultAuthor), MaxBytes: t.ceiling(nil),
			}
			if tilasm.Code == "" {
				if tilasm.Code, err = nextTilasmCode(ctx, tx, m.GetWish()); err != nil {
					return err
				}
			}
			tilasm.Title = cmp.Or(files.title, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
		}
		if err := files.fits(tilasm, t.ceiling(tilasm)); err != nil {
			return err
		}
		if m.GetTitle() != "" {
			tilasm.Title = m.GetTitle()
		}
		if len(m.GetCites()) > 0 {
			if tilasm.Cites, err = resolveCites(ctx, tx, m.GetWish(), m.GetCites()); err != nil {
				return err
			}
		}
		placed, err = t.addVersion(tilasm, files, cmp.Or(m.GetAuthor(), defaultAuthor), 0, now)
		if err != nil {
			return err
		}
		return tx.Put(tilasm)
	})
	if err != nil {
		removeIf(placed)
		return nil, "", err
	}
	return tilasm, placed, nil
}

// addVersion moves the staged files into the tilasm's next version, and records it. It returns the version's folder.
func (t *Tilasms) addVersion(
	tilasm *planv1.Tilasm, files *staged, author string, from int32, now *timestamppb.Timestamp,
) (string, error) {
	var n int32 = 1
	if vs := tilasm.GetVersions(); len(vs) > 0 {
		n = vs[len(vs)-1].GetNumber() + 1
	}
	dir := versionDir(t.Home, tilasm.GetId(), n)
	// A folder left by a put that never committed holds nothing the store knows.
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(files.dir, dir); err != nil {
		return "", err
	}
	files.dir = ""
	tilasm.Versions = append(tilasm.Versions, &planv1.TilasmVersion{
		Number: n, CreateTime: now, Author: author, Size: files.size, Files: files.count, RestoredFrom: from,
	})
	tilasm.UpdateTime = now
	return dir, nil
}

func removeIf(dir string) {
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// staged are files copied into a temporary folder of the tilasms' folder, on their way to a version.
type staged struct {
	dir   string
	size  int64
	count int32
	title string // the <title> of its index.html
	over  string // the file at which the ceiling was passed; empty when it was not
}

func (s *staged) clean() { removeIf(s.dir) }

// fits refuses files beyond the tilasm's ceiling, saying so.
func (s *staged) fits(tilasm *planv1.Tilasm, ceiling int64) error {
	if s.over == "" && s.size <= ceiling {
		return nil
	}
	what := "this tilasm"
	if tilasm.GetCode() != "" {
		what = "tilasm " + tilasm.GetCode()
	}
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(
		"%s would hold more than its ceiling, %s (passed at %s): leave out what it does not need, large videos first",
		what, bytesText(ceiling), cmp.Or(s.over, "the last file")))
}

// bytesText says a size the way a person reads it: 50 MiB, 300 KiB, 12 bytes.
func bytesText(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// stagePath copies a folder, or the content of a .zip, into a temporary folder, up to limit bytes: past it, it stops
// and says at which file. The folder must hold an index.html at its root.
func (t *Tilasms) stagePath(src string, limit int64) (*staged, error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	dir, err := tempDir(t.Home, "put")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s := &staged{dir: dir}
	if info.IsDir() {
		err = s.copyFolder(src, limit)
	} else {
		var zr *zip.ReadCloser
		if zr, err = zip.OpenReader(src); err != nil {
			err = connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is neither a folder nor a .zip: %w", src, err))
		} else {
			err = s.copyZip(&zr.Reader, limit)
			zr.Close()
		}
	}
	if err == nil {
		err = s.index()
	}
	if err != nil {
		s.clean()
		return nil, err
	}
	return s, nil
}

// index checks that the files hold an index.html, and reads its title.
func (s *staged) index() error {
	if s.over != "" {
		return nil // The ceiling refuses it, saying so.
	}
	f, err := os.Open(filepath.Join(s.dir, tilasmIndex))
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New(
			"a tilasm is a folder with an index.html at its root, and this one has none"))
	}
	defer f.Close()
	s.title, _ = htmlText(f)
	return nil
}

func (s *staged) copyFolder(src string, limit int64) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if s.over != "" {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"%s is a symbolic link: a tilasm holds its files themselves", name))
		case d.IsDir() || !d.Type().IsRegular() || name == tilasmManifest:
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return s.add(name, f, limit)
	})
}

// copyZip copies the files of a .zip. A .zip that holds one folder, with the index.html in it, is that folder.
func (s *staged) copyZip(zr *zip.Reader, limit int64) error {
	var files []*zip.File
	names := map[*zip.File]string{}
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s leaves the .zip's folder", f.Name))
		}
		files = append(files, f)
		names[f] = path.Clean(name)
	}
	prefix := ""
	if top, _, ok := strings.Cut(names[firstOr(files)], "/"); ok {
		prefix = top + "/"
		for _, f := range files {
			if !strings.HasPrefix(names[f], prefix) {
				prefix = ""
				break
			}
		}
	}
	for _, f := range files {
		name := strings.TrimPrefix(names[f], prefix)
		if name == tilasmManifest {
			continue
		}
		if err := s.addZipped(f, name, limit); err != nil || s.over != "" {
			return err
		}
	}
	return nil
}

func firstOr(files []*zip.File) *zip.File {
	if len(files) == 0 {
		return nil
	}
	return files[0]
}

func (s *staged) addZipped(f *zip.File, name string, limit int64) error {
	rc, err := f.Open()
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", f.Name, err))
	}
	defer rc.Close()
	return s.add(name, rc, limit)
}

// add copies r to the file name, counting what it copies against limit; the size a .zip announces is not trusted.
func (s *staged) add(name string, r io.Reader, limit int64) error {
	dst := filepath.Join(s.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", name, err))
	}
	n, err := io.Copy(out, io.LimitReader(r, limit-s.size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", name, err))
	}
	s.size += n
	s.count++
	if s.size > limit {
		s.over = name
	}
	return nil
}

// stageVersion copies a version's folder, to restore it as a new one.
func (t *Tilasms) stageVersion(tilasm *planv1.Tilasm, n int32) (*staged, error) {
	dir, err := tempDir(t.Home, "restore")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s := &staged{dir: dir}
	if err := s.copyFolder(versionDir(t.Home, tilasm.GetId(), n), 1<<62); err != nil {
		s.clean()
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("read version %d of %s: %w", n, tilasm.GetCode(), err))
	}
	return s, nil
}

// tilasmByCode is the tilasm of the wish with this code, case ignored; nil when there is none.
func tilasmByCode(ctx context.Context, r store.Reader, wishID, code string) (*planv1.Tilasm, error) {
	found, err := store.List[*planv1.Tilasm](ctx, r, store.Where{"wish_id": wishID, "code": code})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return found[0], nil
}

// findTilasm resolves a reference to a tilasm: its id, or its code within wishID, or within every wish when the code
// is used by one only.
func findTilasm(ctx context.Context, r store.Reader, ref *planv1.TilasmRef, wishID string) (*planv1.Tilasm, error) {
	if id := ref.GetId(); id != "" {
		return store.Get[*planv1.Tilasm](ctx, r, id)
	}
	where := store.Where{"code": ref.GetCode()}
	if wishID != "" {
		where["wish_id"] = wishID
	}
	found, err := store.List[*planv1.Tilasm](ctx, r, where)
	if err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no tilasm %s", strings.ToUpper(ref.GetCode())))
	case 1:
		return found[0], nil
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"%d wishes have a tilasm %s: give its identifier, or the wish with --wish", len(found), strings.ToUpper(ref.GetCode())))
}

// nextTilasmCode follows the highest code of the wish's tilasms.
func nextTilasmCode(ctx context.Context, r store.Reader, wishID string) (string, error) {
	all, err := store.List[*planv1.Tilasm](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return "", err
	}
	last := 0
	for _, t := range all {
		var n int
		if _, err := fmt.Sscanf(t.GetCode(), "L%d", &n); err == nil && n > last {
			last = n
		}
	}
	if last >= maxTilasmCode {
		return "", connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("this wish has used its %d tilasm codes", maxTilasmCode))
	}
	return fmt.Sprintf("L%02d", last+1), nil
}

// resolveCites turns codes (T29, W12) or identifiers into the identifiers of tasks of the wish, each once.
func resolveCites(ctx context.Context, r store.Reader, wishID string, names []string) ([]string, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range names {
		i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
			return strings.EqualFold(t.GetId(), name) || strings.EqualFold(t.GetCode(), name)
		})
		if i < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is not an azima nor a task of the wish", name))
		}
		if !slices.Contains(out, tasks[i].GetId()) {
			out = append(out, tasks[i].GetId())
		}
	}
	return out, nil
}

// citeCodes are the codes of the tasks a tilasm cites, in its order; a task gone leaves its identifier.
func citeCodes(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm) ([]string, error) {
	var out []string
	for _, id := range tilasm.GetCites() {
		task, err := store.Get[*planv1.Task](ctx, r, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			out = append(out, id)
		case err != nil:
			return nil, err
		default:
			out = append(out, task.GetCode())
		}
	}
	return out, nil
}

func (t *Tilasms) List(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceListRequest],
) (*connect.Response[planv1.TilasmServiceListResponse], error) {
	where := store.Where{}
	if id := req.Msg.GetWish(); id != "" {
		where["wish_id"] = id
	}
	all, err := store.List[*planv1.Tilasm](ctx, t.Store, where)
	if err != nil {
		return nil, Status(err)
	}
	words := searchWords(req.Msg.GetSearch())
	res := &planv1.TilasmServiceListResponse{}
	for _, tilasm := range all {
		if len(words) == 0 || t.matches(tilasm, words) {
			res.Tilasms = append(res.Tilasms, tilasm)
		}
	}
	slices.SortStableFunc(res.Tilasms, func(a, b *planv1.Tilasm) int {
		return cmp.Or(strings.Compare(a.GetWishId(), b.GetWishId()), CompareCodes(a.GetCode(), b.GetCode()))
	})
	return connect.NewResponse(res), nil
}

// searchWords are the words of a search, in lower case. The name of the kind itself, tilasm or talisman, in English or
// French, names every tilasm: it finds them all.
func searchWords(search string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(search)) {
		switch strings.TrimSuffix(w, "s") {
		case "tilasm", "talisman", "talismán":
			continue
		}
		out = append(out, w)
	}
	return out
}

// matches tells whether the code, the title or the text of the tilasm's latest version holds every word.
func (t *Tilasms) matches(tilasm *planv1.Tilasm, words []string) bool {
	hay := strings.ToLower(tilasm.GetCode() + " " + tilasm.GetTitle())
	text := ""
	if vs := tilasm.GetVersions(); len(vs) > 0 && t.Home != "" {
		if f, err := os.Open(filepath.Join(versionDir(t.Home, tilasm.GetId(), vs[len(vs)-1].GetNumber()), tilasmIndex)); err == nil {
			_, text = htmlText(f)
			f.Close()
		}
	}
	hay += " " + strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func (t *Tilasms) Get(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceGetRequest],
) (*connect.Response[planv1.TilasmServiceGetResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	tilasm, err := findTilasm(ctx, t.Store, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	n, err := pickVersion(tilasm, req.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	dir := versionDir(t.Home, tilasm.GetId(), n)
	res := &planv1.TilasmServiceGetResponse{
		Tilasm: tilasm, Version: n, Directory: dir, Link: link.Of(link.Tilasm, tilasm.GetId()),
	}
	if t.URL != nil {
		res.Url = t.URL(tilasm.GetId())
	}
	if f, err := os.Open(filepath.Join(dir, tilasmIndex)); err == nil {
		_, res.Text = htmlText(f)
		f.Close()
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		res.Files = append(res.Files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("read the files of %s: %w", tilasm.GetCode(), err))
	}
	return connect.NewResponse(res), nil
}

func (t *Tilasms) Open(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceOpenRequest],
) (*connect.Response[planv1.TilasmServiceOpenResponse], error) {
	if t.Show == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("this server has no window: run djinn up"))
	}
	tilasm, err := findTilasm(ctx, t.Store, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	window := t.Show(tilasm.GetWishId(), tilasm.GetId())
	return connect.NewResponse(&planv1.TilasmServiceOpenResponse{
		Tilasm: tilasm, Link: link.Of(link.Tilasm, tilasm.GetId()), Window: window,
	}), nil
}

// Linked is the wish a link shows: the tilasm's, or the wish itself. One not on this machine is an error of code
// NotFound.
func Linked(ctx context.Context, r store.Reader, l link.Link) (string, error) {
	var wishID string
	var err error
	switch l.Kind {
	case link.Tilasm:
		var tilasm *planv1.Tilasm
		if tilasm, err = store.Get[*planv1.Tilasm](ctx, r, l.ID); err == nil {
			wishID = tilasm.GetWishId()
		}
	case link.Wish:
		var wish *planv1.Wish
		if wish, err = store.Get[*planv1.Wish](ctx, r, l.ID); err == nil {
			wishID = wish.GetId()
		}
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, link.UnknownError{URL: l.String()})
	}
	if errors.Is(err, store.ErrNotFound) {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("no %s %s on this machine", l.Kind, l.ID))
	}
	return wishID, err
}

// pickVersion is version n of the tilasm, or its latest for 0.
func pickVersion(tilasm *planv1.Tilasm, n int32) (int32, error) {
	vs := tilasm.GetVersions()
	if len(vs) == 0 {
		return 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("tilasm %s has no version", tilasm.GetCode()))
	}
	if n == 0 {
		return vs[len(vs)-1].GetNumber(), nil
	}
	if !slices.ContainsFunc(vs, func(v *planv1.TilasmVersion) bool { return v.GetNumber() == n }) {
		return 0, connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"tilasm %s has no version %d: it has %d to %d", tilasm.GetCode(), n, vs[0].GetNumber(), vs[len(vs)-1].GetNumber()))
	}
	return n, nil
}

func (t *Tilasms) History(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceHistoryRequest],
) (*connect.Response[planv1.TilasmServiceHistoryResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	tilasm, err := findTilasm(ctx, t.Store, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.TilasmServiceHistoryResponse{
		Versions: tilasm.GetVersions(), Directory: TilasmDir(t.Home, tilasm.GetId()),
	}), nil
}

func (t *Tilasms) Restore(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceRestoreRequest],
) (*connect.Response[planv1.TilasmServiceRestoreResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	m := req.Msg
	found, err := findTilasm(ctx, t.Store, m.GetTilasm(), m.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	n, err := pickVersion(found, m.GetVersion())
	if err != nil {
		return nil, err
	}
	files, err := t.stageVersion(found, n)
	if err != nil {
		return nil, err
	}
	defer files.clean()
	var tilasm *planv1.Tilasm
	var placed string
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if tilasm, err = store.Get[*planv1.Tilasm](ctx, tx, found.GetId()); err != nil {
			return err
		}
		placed, err = t.addVersion(tilasm, files, cmp.Or(m.GetAuthor(), defaultAuthor), n, timestamppb.Now())
		if err != nil {
			return err
		}
		return tx.Put(tilasm)
	})
	if err != nil {
		removeIf(placed)
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceRestoreResponse{Tilasm: tilasm}), nil
}

func (t *Tilasms) Export(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceExportRequest],
) (*connect.Response[planv1.TilasmServiceExportResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	tilasm, err := findTilasm(ctx, t.Store, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	n, err := pickVersion(tilasm, 0)
	if err != nil {
		return nil, err
	}
	codes, err := citeCodes(ctx, t.Store, tilasm)
	if err != nil {
		return nil, Status(err)
	}
	manifest, err := protojson.MarshalOptions{Multiline: true, Indent: "  ", UseProtoNames: true}.Marshal(
		&planv1.TilasmExport{Tilasm: tilasm, CiteCodes: codes})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err = errors.Join(zipFolder(zw, versionDir(t.Home, tilasm.GetId(), n)), zipBytes(zw, tilasmManifest, append(manifest, '\n')), zw.Close())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("zip %s: %w", tilasm.GetCode(), err))
	}
	file := req.Msg.GetFile()
	if file == "" {
		dir, err := Downloads()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("find the Downloads folder: %w", err))
		}
		file = freePath(dir, cmp.Or(fileName(tilasm.GetTitle()), strings.ToLower(tilasm.GetCode())), ".zip")
	}
	if !filepath.IsAbs(file) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file: %q is not an absolute path", file))
	}
	if err := writeFile(file, buf.Bytes()); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&planv1.TilasmServiceExportResponse{File: file, Size: int64(buf.Len())}), nil
}

// zipFolder adds the files of dir to zw, by their paths in it, in a stable order.
func zipFolder(zw *zip.Writer, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return zipBytes(zw, filepath.ToSlash(rel), data)
	})
}

func zipBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func (t *Tilasms) Import(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceImportRequest],
) (*connect.Response[planv1.TilasmServiceImportResponse], error) {
	tilasm, err := t.importZip(ctx, req.Spec(), req.Msg, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceImportResponse{Tilasm: tilasm}), nil
}

// importZip imports m's .zip, journaling the request journaled under spec.
func (t *Tilasms) importZip(
	ctx context.Context, spec connect.Spec, journaled proto.Message, m *planv1.TilasmServiceImportRequest,
) (*planv1.Tilasm, error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	file := m.GetFile()
	if !filepath.IsAbs(file) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file: %q is not an absolute path", file))
	}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is not a .zip: %w", file, err))
	}
	defer zr.Close()
	exp, err := readManifest(&zr.Reader)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", file, err))
	}
	from := exp.GetTilasm()
	same, err := store.Get[*planv1.Tilasm](ctx, t.Store, from.GetId())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, Status(err)
	}
	if !strings.EqualFold(same.GetWishId(), m.GetWish()) {
		same = nil
	}
	dir, err := tempDir(t.Home, "import")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	files := &staged{dir: dir}
	defer files.clean()
	if err := files.copyZip(&zr.Reader, t.ceiling(same)); err != nil {
		return nil, err
	}
	if err := files.index(); err != nil {
		return nil, err
	}
	var tilasm *planv1.Tilasm
	var placed string
	err = write(ctx, t.Store, spec, journaled, func(tx *store.Tx) error {
		if _, err := store.Get[*planv1.Wish](ctx, tx, m.GetWish()); err != nil {
			return err
		}
		now := timestamppb.Now()
		var err error
		tilasm, err = store.Get[*planv1.Tilasm](ctx, tx, from.GetId())
		switch {
		case err == nil && strings.EqualFold(tilasm.GetWishId(), m.GetWish()):
			// The wish has it: the file is its new version.
		case err == nil || errors.Is(err, store.ErrNotFound):
			// Another wish has it, or none: a tilasm of its own, under the file's identifier when it is free.
			id := from.GetId()
			if err == nil || !uuidLike(id) {
				id = store.NewID()
			}
			tilasm = &planv1.Tilasm{
				Id: id, WishId: m.GetWish(), Author: cmp.Or(from.GetAuthor(), defaultAuthor), CreateTime: now,
				MaxBytes: t.ceiling(nil),
			}
			if code := strings.ToUpper(from.GetCode()); tilasmCode.MatchString(code) {
				if taken, err := tilasmByCode(ctx, tx, m.GetWish(), code); err != nil {
					return err
				} else if taken == nil {
					tilasm.Code = code
				}
			}
			if tilasm.Code == "" {
				if tilasm.Code, err = nextTilasmCode(ctx, tx, m.GetWish()); err != nil {
					return err
				}
			}
		default:
			return err
		}
		if err := files.fits(tilasm, t.ceiling(tilasm)); err != nil {
			return err
		}
		tilasm.Title = cmp.Or(from.GetTitle(), tilasm.GetTitle(), files.title, tilasm.GetCode())
		if tilasm.Cites, err = matchCites(ctx, tx, m.GetWish(), from.GetCites(), exp.GetCiteCodes()); err != nil {
			return err
		}
		placed, err = t.addVersion(tilasm, files, cmp.Or(m.GetAuthor(), from.GetAuthor(), defaultAuthor), 0, now)
		if err != nil {
			return err
		}
		return tx.Put(tilasm)
	})
	if err != nil {
		removeIf(placed)
		return nil, err
	}
	return tilasm, nil
}

// readManifest reads tilasm.json at the root of a tilasm's .zip, or in its one folder.
func readManifest(zr *zip.Reader) (*planv1.TilasmExport, error) {
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if name != tilasmManifest && !(strings.Count(name, "/") == 1 && path.Base(name) == tilasmManifest) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		exp := &planv1.TilasmExport{}
		if err := protojson.Unmarshal(data, exp); err != nil {
			return nil, fmt.Errorf("%s: %w", tilasmManifest, err)
		}
		if exp.GetTilasm() == nil {
			return nil, fmt.Errorf("%s holds no tilasm", tilasmManifest)
		}
		return exp, nil
	}
	return nil, fmt.Errorf("no %s: it is not a tilasm exported by djinn tilasm export (djinn tilasm put takes a .zip of a folder)", tilasmManifest)
}

// matchCites finds, among the wish's tasks, what an imported tilasm cites: by identifier, or else by code.
func matchCites(ctx context.Context, r store.Reader, wishID string, ids, codes []string) ([]string, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	var out []string
	for i, id := range ids {
		code := ""
		if i < len(codes) {
			code = codes[i]
		}
		j := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return strings.EqualFold(t.GetId(), id) })
		if j < 0 && code != "" {
			j = slices.IndexFunc(tasks, func(t *planv1.Task) bool { return strings.EqualFold(t.GetCode(), code) })
		}
		if j >= 0 && !slices.Contains(out, tasks[j].GetId()) {
			out = append(out, tasks[j].GetId())
		}
	}
	return out, nil
}

func uuidLike(id string) bool {
	return len(id) == 36 && strings.Count(id, "-") == 4
}

// htmlText reads an HTML page: its <title>, and its text without tags, scripts nor styles, a line per block.
func htmlText(r io.Reader) (title, text string) {
	z := html.NewTokenizer(io.LimitReader(r, 10<<20))
	var b strings.Builder
	skip, inTitle, titled := 0, false, false
	newline := func() {
		if s := b.String(); s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(title), tidy(b.String())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			switch a {
			case atom.Script, atom.Style, atom.Noscript, atom.Template:
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case atom.Title:
				// The page's title is the first; an SVG's own titles are text.
				if tt == html.StartTagToken && !titled {
					inTitle = true
				} else if tt == html.EndTagToken && inTitle {
					inTitle, titled = false, true
				}
			case atom.P, atom.Div, atom.Br, atom.Li, atom.Tr, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
				atom.Section, atom.Article, atom.Header, atom.Footer, atom.Pre, atom.Table, atom.Ul, atom.Ol, atom.Dt,
				atom.Dd, atom.Blockquote, atom.Figcaption, atom.Main, atom.Nav, atom.Aside:
				newline()
			case atom.Td, atom.Th:
				b.WriteByte(' ')
			}
		case html.TextToken:
			switch {
			case inTitle:
				title += string(z.Text())
			case skip == 0:
				// In HTML a line break is a blank like another: the blocks make the lines.
				b.WriteString(strings.Map(func(r rune) rune {
					if unicode.IsSpace(r) {
						return ' '
					}
					return r
				}, string(z.Text())))
			}
		}
	}
}

// tidy collapses the blanks of each line, and drops the empty lines.
func tidy(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// tilasmFiles reads the files of every version of a tilasm, for a wish's export.
func tilasmFiles(home string, tilasm *planv1.Tilasm) ([]*planv1.TilasmFile, error) {
	var out []*planv1.TilasmFile
	for _, v := range tilasm.GetVersions() {
		dir := versionDir(home, tilasm.GetId(), v.GetNumber())
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out = append(out, &planv1.TilasmFile{Version: v.GetNumber(), Path: filepath.ToSlash(rel), Content: data})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("tilasm %s, version %d: %w", tilasm.GetCode(), v.GetNumber(), err)
		}
	}
	return out, nil
}

// tilasmFolders places the folders of a wish's imported tilasms, in the transaction that stores them, and undoes it
// when that transaction fails. The files are written before the transaction, in a temporary folder; the folders they
// replace go to another, deleted once it commits.
type tilasmFolders struct {
	home   string
	staged string // the new folders, by tilasm id
	trash  string // the folders replaced
	placed []string
	moved  []string // ids whose folder went to trash
}

// stageTilasms writes the files of an export's tilasms to a temporary folder, and empties them from the export: the
// journal keeps the manifests, not the files.
func stageTilasms(home string, exp *planv1.WishExport) (*tilasmFolders, error) {
	f := &tilasmFolders{home: home}
	if len(exp.GetTilasms()) == 0 {
		return f, nil
	}
	if home == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("this server keeps no tilasms: run djinn up"))
	}
	var err error
	if f.staged, err = tempDir(home, "import"); err != nil {
		return nil, err
	}
	for _, t := range exp.GetTilasms() {
		for _, file := range t.GetFiles() {
			dst := filepath.Join(f.staged, strings.ToLower(t.GetTilasm().GetId()), fmt.Sprintf("v%d", file.GetVersion()),
				filepath.FromSlash(file.GetPath()))
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				f.done(false)
				return nil, err
			}
			if err := os.WriteFile(dst, file.GetContent(), 0o600); err != nil {
				f.done(false)
				return nil, err
			}
			file.Content = nil
		}
	}
	return f, nil
}

// replace moves the folder of each tilasm of old aside, in a transaction.
func (f *tilasmFolders) replace(old []*planv1.Tilasm) error {
	for _, t := range old {
		if err := f.aside(t.GetId()); err != nil {
			return err
		}
	}
	return nil
}

func (f *tilasmFolders) aside(id string) error {
	dir := TilasmDir(f.home, id)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if f.trash == "" {
		var err error
		if f.trash, err = tempDir(f.home, "replaced"); err != nil {
			return err
		}
	}
	if err := os.Rename(dir, filepath.Join(f.trash, strings.ToLower(id))); err != nil {
		return err
	}
	f.moved = append(f.moved, id)
	return nil
}

// place puts the staged folder of each tilasm in its place, a folder there before going aside.
func (f *tilasmFolders) place(tilasms []*planv1.TilasmExport) error {
	for _, t := range tilasms {
		id := strings.ToLower(t.GetTilasm().GetId())
		src := filepath.Join(f.staged, id)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if !slices.Contains(f.moved, id) {
			if err := f.aside(id); err != nil {
				return err
			}
		}
		if err := os.Rename(src, TilasmDir(f.home, id)); err != nil {
			return err
		}
		f.placed = append(f.placed, id)
	}
	return nil
}

// done deletes what was replaced once the transaction committed, or puts it back when it failed.
func (f *tilasmFolders) done(committed bool) {
	if !committed {
		for _, id := range f.placed {
			os.RemoveAll(TilasmDir(f.home, id))
		}
		for _, id := range f.moved {
			os.Rename(filepath.Join(f.trash, strings.ToLower(id)), TilasmDir(f.home, id))
		}
	}
	removeIf(f.trash)
	removeIf(f.staged)
}

// tilasmsOf are the manifests of a wish's tilasms, with the codes of what they cite.
func tilasmsOf(ctx context.Context, r store.Reader, wishID string) ([]*planv1.TilasmExport, error) {
	all, err := store.List[*planv1.Tilasm](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(all, func(a, b *planv1.Tilasm) int { return CompareCodes(a.GetCode(), b.GetCode()) })
	var out []*planv1.TilasmExport
	for _, t := range all {
		codes, err := citeCodes(ctx, r, t)
		if err != nil {
			return nil, err
		}
		out = append(out, &planv1.TilasmExport{Tilasm: t, CiteCodes: codes})
	}
	return out, nil
}

// checkTilasms refuses tilasms of an export that do not hold together: of another wish, a code given twice, a cite
// outside the export's tasks, a file of no version or outside its folder.
func checkTilasms(exp *planv1.WishExport, tasks map[string]bool, valid func(string, proto.Message, string) error) error {
	codes := map[string]bool{}
	for _, te := range exp.GetTilasms() {
		t := te.GetTilasm()
		if t == nil {
			return errors.New("a tilasm without its manifest")
		}
		if err := valid("tilasm", t, t.GetId()); err != nil {
			return err
		}
		if !strings.EqualFold(t.GetWishId(), exp.GetWish().GetId()) {
			return fmt.Errorf("tilasm %s belongs to another wish", t.GetId())
		}
		code := strings.ToUpper(t.GetCode())
		if t.GetId() == "" || !tilasmCode.MatchString(code) || codes[code] {
			return fmt.Errorf("tilasm %q: it needs an id and a code like L01 of its own", t.GetCode())
		}
		codes[code] = true
		for _, id := range t.GetCites() {
			if !tasks[id] {
				return fmt.Errorf("tilasm %s cites %s, which the export does not hold", t.GetCode(), id)
			}
		}
		versions := map[int32]bool{}
		for _, v := range t.GetVersions() {
			versions[v.GetNumber()] = true
		}
		for _, f := range te.GetFiles() {
			if !versions[f.GetVersion()] || !filepath.IsLocal(filepath.FromSlash(f.GetPath())) || strings.Contains(f.GetPath(), `\`) {
				return fmt.Errorf("tilasm %s: file %q of version %d has no place in it", t.GetCode(), f.GetPath(), f.GetVersion())
			}
		}
	}
	return nil
}
