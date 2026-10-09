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
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// developerAuthor is who puts a tilasm dropped on the window's Tilasms tab.
const developerAuthor = "developer"

// PutData puts the files the window read: they are written to a temporary folder of the tilasms' folder, then put as
// a folder, or as a .zip, or imported when the .zip is a tilasm's export. The journal keeps the request without the
// files' contents, as a wish's import does.
func (t *Tilasms) PutData(
	ctx context.Context, req *connect.Request[planv1.TilasmServicePutDataRequest],
) (*connect.Response[planv1.TilasmServicePutDataResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	m := req.Msg
	journaled := proto.Clone(m).(*planv1.TilasmServicePutDataRequest)
	for _, f := range journaled.GetFiles() {
		f.Content = nil
	}
	dir, err := tempDir(t.Home, "drop")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer removeIf(dir)
	name := dropName(m.GetName())
	author := cmp.Or(m.GetAuthor(), developerAuthor)
	if files := m.GetFiles(); len(files) == 1 && bytes.HasPrefix(files[0].GetContent(), []byte("PK\x03\x04")) {
		file := filepath.Join(dir, strings.TrimSuffix(name, ".zip")+".zip")
		if err := os.WriteFile(file, files[0].GetContent(), 0o600); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(files[0].GetContent()), int64(len(files[0].GetContent())))
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is not a .zip: %w", name, err))
		}
		if _, err := readManifest(zr); err == nil {
			tilasm, err := t.importZip(ctx, req.Spec(), journaled, &planv1.TilasmServiceImportRequest{
				File: file, Wish: m.GetWish(), Author: author,
			})
			if err != nil {
				return nil, err
			}
			return connect.NewResponse(&planv1.TilasmServicePutDataResponse{Tilasm: tilasm, Imported: true}), nil
		}
		tilasm, _, err := t.put(ctx, req.Spec(), journaled, &planv1.TilasmServicePutRequest{
			Path: file, Wish: m.GetWish(), Author: author,
		})
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(&planv1.TilasmServicePutDataResponse{Tilasm: tilasm}), nil
	}
	folder := filepath.Join(dir, name)
	var size int64
	for _, f := range m.GetFiles() {
		rel := filepath.FromSlash(f.GetPath())
		if !filepath.IsLocal(rel) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s leaves the folder", f.GetPath()))
		}
		// The ceiling counts again as the files are put; this only spares writing what it would refuse.
		if size += int64(len(f.GetContent())); size > t.ceiling(nil)*2 {
			return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(
				"this folder holds more than a tilasm may, %s", bytesText(t.ceiling(nil))))
		}
		dst := filepath.Join(folder, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if err := os.WriteFile(dst, f.GetContent(), 0o600); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", f.GetPath(), err))
		}
	}
	tilasm, _, err := t.put(ctx, req.Spec(), journaled, &planv1.TilasmServicePutRequest{
		Path: folder, Wish: m.GetWish(), Author: author,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServicePutDataResponse{Tilasm: tilasm}), nil
}

// dropName is the name of what was dropped, as a file name: the title of a tilasm whose index.html has none.
func dropName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:*?"<>|`, r) || r < ' ' {
			return '-'
		}
		return r
	}, name)
	if name == "." || name == "/" || name == ".." || strings.Trim(name, ".- ") == "" {
		return "tilasm"
	}
	return name
}

// TilasmPolicy is the content security policy of a tilasm's files, sent with each. Its own scripts run, inline or
// from its files, and its styles, images, fonts and media load from its files or data: and blob: addresses. It reaches
// no network: connect-src 'none' refuses fetch, XMLHttpRequest, WebSocket and EventSource, default-src 'none' every
// other load from elsewhere, form-action 'none' a form. sandbox gives it an opaque origin even opened alone in a
// browser: it never shares Djinn's, so it reads neither Djinn's cookie nor its storage, and calls none of its
// services. Only Djinn's own pages may frame it.
//
// A link the tilasm follows can still navigate its own frame away: a policy cannot forbid that.
const TilasmPolicy = "default-src 'none'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"media-src 'self' data: blob:; " +
	"worker-src 'self' blob:; " +
	"frame-src 'self'; " +
	"connect-src 'none'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'none'; " +
	"frame-ancestors 'self'; " +
	"sandbox allow-scripts"

// tilasmTypes are the content types of a tilasm's usual files, set here: on Windows Go reads them from the registry,
// where another program may have changed them, and a browser refuses a module script that is not JavaScript.
var tilasmTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".wasm":  "application/wasm",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/plain; charset=utf-8",
}

// Files serves the latest version of each tilasm at server.TilasmPrefix: /tilasm/<id>/ is its index.html, and its
// other files are under it, by their paths. Every answer carries TilasmPolicy. A folder serves its index.html, never a
// listing.
func (t *Tilasms) Files() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", TilasmPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		// The frame's origin is opaque: its module scripts and fonts are cross-origin requests, which this allows. They
		// carry no credential, and the files need one (server.Guard).
		h.Set("Access-Control-Allow-Origin", "*")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "a tilasm's files are read only", http.StatusMethodNotAllowed)
			return
		}
		id, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, server.TilasmPrefix), "/")
		if !uuidLike(id) {
			http.NotFound(w, r)
			return
		}
		if !ok {
			// Its files are relative to its folder: the address ends with a slash.
			http.Redirect(w, r, server.TilasmPrefix+id+"/", http.StatusMovedPermanently) //nolint:gosec // Same origin: a path, its id a UUID.
			return
		}
		if t.Home == "" {
			http.Error(w, "this server keeps no tilasms: run djinn up", http.StatusServiceUnavailable)
			return
		}
		tilasm, err := store.Get[*planv1.Tilasm](r.Context(), t.Store, strings.ToLower(id))
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := pickVersion(tilasm, 0)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		root := os.DirFS(versionDir(t.Home, tilasm.GetId(), n))
		name := path.Clean("/" + rest)[1:]
		if name == "" {
			name = tilasmIndex
		}
		if info, err := fs.Stat(root, name); err == nil && info.IsDir() {
			name = path.Join(name, tilasmIndex)
		}
		f, err := root.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		// A version never changes: its number tells a file the browser kept from the same file of another version, put
		// within the same second as Last-Modified counts.
		h.Set("Etag", fmt.Sprintf(`"v%d"`, n))
		ext := strings.ToLower(path.Ext(name))
		if typ := cmp.Or(tilasmTypes[ext], mime.TypeByExtension(ext)); typ != "" {
			h.Set("Content-Type", typ)
		}
		http.ServeContent(w, r, name, info.ModTime(), f.(io.ReadSeeker)) // os.DirFS opens an *os.File.
	})
}
