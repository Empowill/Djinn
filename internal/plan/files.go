package plan

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

const (
	// MaxTellFiles is how many files one message to the lead hands it at most (LeadServiceTellRequest.files).
	MaxTellFiles = 10
	// MaxTellFileBytes is the largest file a message to the lead hands it.
	MaxTellFileBytes = 20 << 20
	// maxTellBytes is the largest request LeadService reads: its files whole, and room for the rest.
	maxTellBytes = MaxTellFiles*MaxTellFileBytes + 1<<20
	// fileKind is the kind of the block a file handed to the lead puts on its wish.
	fileKind = "file"
	// filesLine starts the line of a message to the lead that names a file it was handed (FileDirs).
	filesLine = "Files: "
)

// keptFile is a file handed to the lead, as written in Djinn's data folder.
type keptFile struct {
	path, name, kind string
	size             int
}

// line is how the lead's message names the file: its path, its type and its size.
func (f keptFile) line() string {
	return fmt.Sprintf("%s%s (%s, %s)", filesLine, f.path, f.kind, sizeText(f.size))
}

// filesFolder is where the files handed to the lead about wishID are kept: wishes/<wish>/files in Djinn's data folder
// home, lead/files about no wish.
func filesFolder(home, wishID string) string {
	if wishID == "" {
		return filepath.Join(home, "lead", "files")
	}
	return filepath.Join(home, "wishes", wishID, "files")
}

// keepFiles writes the files of a message to the lead about wishID in its folder, each under the time it came and its
// safe name, and puts a block of kind "file" on the wish for each. It returns the lines that name them, one per file.
func (w *Wishes) keepFiles(ctx context.Context, wishID string, files []*planv1.LeadFile) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	home := w.home()
	if home == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("this server keeps no files: it has no data folder"))
	}
	dir := filesFolder(home, wishID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}
	stamp := w.now().Format("2006-01-02-150405")
	kept := make([]keptFile, 0, len(files))
	for _, f := range files {
		name := safeFileName(f.GetName())
		path, err := writeNew(dir, stamp+"-"+name, f.GetData())
		if err != nil {
			return "", connect.NewError(connect.CodeInternal, err)
		}
		kept = append(kept, keptFile{path: path, name: name, kind: fileType(f), size: len(f.GetData())})
	}
	if wishID != "" {
		err := Status(w.Store.Tx(ctx, func(tx *store.Tx) error {
			for _, f := range kept {
				req := &planv1.BlockServicePutRequest{
					WishId: wishID, Kind: fileKind, Title: f.name, Icon: "📎", MediaType: "text/markdown",
					Content: fmt.Sprintf("`%s` (%s, %s)", f.path, f.kind, sizeText(f.size)),
				}
				if err := tx.Journal(CallerActor(ctx), planv1connect.BlockServicePutProcedure, req); err != nil {
					return err
				}
				if _, err := PutBlock(ctx, tx, req); err != nil {
					return err
				}
			}
			return nil
		}))
		if err != nil {
			return "", err
		}
	}
	lines := make([]string, len(kept))
	for i, f := range kept {
		lines[i] = f.line()
	}
	return strings.Join(lines, "\n"), nil
}

// writeNew writes data in a new file of dir named name, or name-2, name-3… when it is taken, and returns its path.
func writeNew(dir, name string, data []byte) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		path := filepath.Join(dir, name)
		if n > 1 {
			path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, n, ext))
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) && n < 1000 {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return "", err
		}
		return path, f.Close()
	}
}

// unsafeRun is a run of the characters a kept file's name leaves out: anything but letters, digits, dot, dash and
// underscore of ASCII, so that its path is one word in a prompt and on every file system.
var unsafeRun = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFileName is name made safe for a file of the data folder: no folder, no space, at most 80 characters, its
// extension kept; "file" when nothing is left.
func safeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimRight(strings.TrimLeft(unsafeRun.ReplaceAllString(name, "-"), "-"), "-.")
	if ext := filepath.Ext(name); len(name) > 80 {
		if len(ext) > 16 {
			ext = ""
		}
		name = strings.TrimRight(name[:80-len(ext)], "-.") + ext
	}
	if name == "" || strings.HasPrefix(name, ".") {
		return "file" + name
	}
	return name
}

// fileType is the media type of f: the one given, else the one of its extension, else the one its bytes tell.
func fileType(f *planv1.LeadFile) string {
	t := f.GetType()
	if t == "" {
		t = mime.TypeByExtension(filepath.Ext(f.GetName()))
	}
	if t == "" {
		t = http.DetectContentType(f.GetData())
	}
	if base, _, err := mime.ParseMediaType(t); err == nil {
		return base
	}
	return "application/octet-stream"
}

// sizeText is a size in bytes as a person reads it: 512 B, 240 KB, 3.4 MB.
func sizeText(n int) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", (n+1<<9)>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// FileDirs are the folders of the files handed to the lead that a worker's prompt names (a "Files: <path>" line, or
// the path anywhere), in Djinn's data folder home: the folders the worker reads besides its own
// (harness.Spec.ReadDirs).
func FileDirs(home, prompt string) []string {
	if home == "" {
		return nil
	}
	sep := regexp.QuoteMeta(string(filepath.Separator))
	root := regexp.QuoteMeta(filepath.Clean(home))
	re := regexp.MustCompile(root + sep + `(?:wishes` + sep + `[0-9a-fA-F-]{36}|lead)` + sep + `files(?:` + sep + `|\b)`)
	var dirs []string
	for _, m := range re.FindAllString(prompt, -1) {
		dir := filepath.Clean(m)
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// ReadDirs are the folders of Djinn's data folder home a worker's prompt gives it to read besides its own: its
// tilasms' versions (TilasmDirs) and the folders of the files handed to the lead it names (FileDirs).
func ReadDirs(home, prompt string) []string {
	dirs := TilasmDirs(home, prompt)
	for _, d := range FileDirs(home, prompt) {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
