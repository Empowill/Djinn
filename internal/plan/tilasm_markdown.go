package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// stageMarkdown writes text as the index.md of a new temporary folder, on its way to a version.
func (t *Tilasms) stageMarkdown(text string) (*staged, error) {
	dir, err := tempDir(t.Home, "write")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := os.WriteFile(filepath.Join(dir, tilasmMarkdown), []byte(text), 0o600); err != nil {
		removeIf(dir)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &staged{
		dir: dir, size: int64(len(text)), count: 1, title: markdownTitle(text),
		format: planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN,
	}, nil
}

// Create makes a Markdown tilasm of the wish from its title and text. The journal keeps the request without its text,
// which its version's folder holds, as PutData leaves out the files.
func (t *Tilasms) Create(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceCreateRequest],
) (*connect.Response[planv1.TilasmServiceCreateResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	m := req.Msg
	files, err := t.stageMarkdown(m.GetMarkdown())
	if err != nil {
		return nil, err
	}
	defer files.clean()
	title := cmp.Or(strings.TrimSpace(m.GetTitle()), files.title)
	if title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"title: give the document a title, or start its text with a # heading"))
	}
	journaled := proto.CloneOf(m)
	journaled.Markdown = ""
	author := cmp.Or(m.GetAuthor(), defaultAuthor)
	var tilasm *planv1.Tilasm
	var placed string
	err = write(ctx, t.Store, req.Spec(), journaled, func(tx *store.Tx) error {
		if _, err := store.Get[*planv1.Wish](ctx, tx, m.GetWish()); err != nil {
			return err
		}
		now := t.now()
		tilasm = &planv1.Tilasm{
			Id: store.NewID(), WishId: m.GetWish(), Title: title, Author: author, CreateTime: now,
			MaxBytes: t.ceiling(nil), Format: planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN,
		}
		var err error
		if tilasm.Code, err = nextTilasmCode(ctx, tx, m.GetWish()); err != nil {
			return err
		}
		if err := files.fits(tilasm, t.ceiling(tilasm)); err != nil {
			return err
		}
		if tilasm.Cites, err = resolveCites(ctx, tx, m.GetWish(), m.GetCites()); err != nil {
			return err
		}
		if placed, err = t.addVersion(tilasm, files, author, 0, planv1.TilasmOrigin_TILASM_ORIGIN_CREATE, now); err != nil {
			return err
		}
		return tx.Put(tilasm)
	})
	if err != nil {
		removeIf(placed)
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceCreateResponse{Tilasm: tilasm, Directory: placed}), nil
}

// stale refuses a save from a revision the tilasm has moved on from.
func stale(tilasm *planv1.Tilasm, base int64) error {
	if tilasm.GetRevision() == base {
		return nil
	}
	return connect.NewError(connect.CodeAborted, fmt.Errorf(
		"tilasm %s changed since revision %d: it is at revision %d; read it again, then save",
		tilasm.GetCode(), base, tilasm.GetRevision()))
}

// rewrites tells whether a save by author at now rewrites the tilasm's latest version rather than adding one: its
// author made it, by a put, a creation or a save, less than saveSitting before.
func rewrites(latest *planv1.TilasmVersion, author string, t *Tilasms) bool {
	switch latest.GetOrigin() {
	case planv1.TilasmOrigin_TILASM_ORIGIN_RESTORE, planv1.TilasmOrigin_TILASM_ORIGIN_IMPORT:
		return false
	}
	if latest.GetAuthor() != author || latest.GetCreateTime() == nil {
		return false
	}
	age := t.now().AsTime().Sub(latest.GetCreateTime().AsTime())
	return age >= 0 && age < saveSitting
}

// Save writes the text of a Markdown tilasm from the revision it was edited from. A save that rewrites the latest
// version (rewrites) replaces its index.md: the new text is written beside it, and renamed over it in the transaction
// that records it, the old one kept aside to put back if that transaction fails. Any other save adds a version, a copy
// of the latest with the new index.md. The journal keeps the request without its text.
func (t *Tilasms) Save(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceSaveRequest],
) (*connect.Response[planv1.TilasmServiceSaveResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	m := req.Msg
	found, err := store.Get[*planv1.Tilasm](ctx, t.Store, m.GetId())
	if err != nil {
		return nil, Status(err)
	}
	if !markdown(found) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"tilasm %s is an HTML page, read only: only a Markdown tilasm is saved", found.GetCode()))
	}
	if err := stale(found, m.GetBaseRevision()); err != nil {
		return nil, err
	}
	n, err := pickVersion(found, 0)
	if err != nil {
		return nil, err
	}
	latest := found.GetVersions()[len(found.GetVersions())-1]
	author := cmp.Or(m.GetAuthor(), developerAuthor)
	text := m.GetMarkdown()
	inPlace := rewrites(latest, author, t)
	var files *staged
	if inPlace {
		if files, err = t.stageMarkdown(text); err != nil {
			return nil, err
		}
	} else {
		if files, err = t.stageVersion(found, n); err != nil {
			return nil, err
		}
		old, _ := os.Stat(filepath.Join(files.dir, tilasmMarkdown))
		if err := os.WriteFile(filepath.Join(files.dir, tilasmMarkdown), []byte(text), 0o600); err != nil {
			files.clean()
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if old != nil {
			files.size -= old.Size()
		} else {
			files.count++
		}
		files.size += int64(len(text))
	}
	defer files.clean()
	journaled := proto.CloneOf(m)
	journaled.Markdown = ""
	dir := versionDir(t.Home, found.GetId(), n)
	aside := ""  // the index.md a rewrite replaced, until the transaction commits
	placed := "" // the folder of a version added
	swapped := false
	var tilasm *planv1.Tilasm
	err = write(ctx, t.Store, req.Spec(), journaled, func(tx *store.Tx) error {
		var err error
		if tilasm, err = store.Get[*planv1.Tilasm](ctx, tx, found.GetId()); err != nil {
			return err
		}
		if err := stale(tilasm, m.GetBaseRevision()); err != nil {
			return err
		}
		now := t.now()
		if m.GetTitle() != "" {
			tilasm.Title = m.GetTitle()
		}
		if !inPlace {
			if err := files.fits(tilasm, t.ceiling(tilasm)); err != nil {
				return err
			}
			placed, err = t.addVersion(tilasm, files, author, 0, planv1.TilasmOrigin_TILASM_ORIGIN_SAVE, now)
			if err != nil {
				return err
			}
			return tx.Put(tilasm)
		}
		v := tilasm.GetVersions()[len(tilasm.GetVersions())-1]
		index := filepath.Join(dir, tilasmMarkdown)
		old, _ := os.Stat(index)
		total := &staged{size: v.GetSize() + files.size}
		if old != nil {
			total.size -= old.Size()
		}
		if err := total.fits(tilasm, t.ceiling(tilasm)); err != nil {
			return err
		}
		if old != nil {
			aside = filepath.Join(files.dir, ".replaced.md")
			if err := renamer.Rename(index, aside); err != nil {
				aside = ""
				return err
			}
		} else {
			v.Files++
		}
		if err := renamer.Rename(filepath.Join(files.dir, tilasmMarkdown), index); err != nil {
			return err
		}
		swapped = true
		tilasm.Revision++
		v.Size, v.Revision, v.UpdateTime = total.size, tilasm.GetRevision(), now
		tilasm.UpdateTime = now
		return tx.Put(tilasm)
	})
	if err != nil {
		removeIf(placed)
		if swapped {
			os.Remove(filepath.Join(dir, tilasmMarkdown))
		}
		if aside != "" {
			renamer.Rename(aside, filepath.Join(dir, tilasmMarkdown)) //nolint:errcheck // Best effort: the transaction's own error is the one reported.
		}
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceSaveResponse{Tilasm: tilasm}), nil
}
